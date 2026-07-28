package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/netip"
	"sort"
	"strconv"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	singjson "github.com/sagernet/sing/common/json"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// chainPair — цепь из двух звеньев: firstID принимает трафик с сервера, viaID
// выпускает его наружу. Пара, а не правило: тег клона зависит только от неё, и
// одинаковые пары из разных правил дают один outbound (ADR 0012).
type chainPair struct {
	viaID   string
	firstID string
}

// RuleDiag — что генератор сделал с одним правилом. По успеху [Generate] этого
// не видно: и отказ, и пропуск — штатный успех, а для пользователя это разные
// состояния (issue #123).
type RuleDiag struct {
	// ID, Name — правило, о котором речь. Имя идёт в текст проверки: «есть
	// расхождение» без имени не говорит, что чинить.
	ID, Name string
	// Reason — почему у правила нет маршрута: «туннель выключен» и прочие
	// причины из [Generate]. Пусто означает, что маршрут есть. Правило с
	// причиной уходит в конфиг отказом (ADR 0013): ресурс недоступен, но и
	// напрямую не идёт.
	Reason string
	// Skipped — правила в конфиге нет вовсе. После ADR 0013 остался ровно один
	// такой случай: у правила не осталось ни одного условия совпадения. Его
	// трафик разбирают правила ниже, а в конце — прямой выход.
	Skipped bool
}

// Generate собирает конфиг sing-box целиком из снимка состояния, отбрасывая
// диагностику по правилам. Всё остальное — в [GenerateWithDiag].
func Generate(snap store.Snapshot) (option.Options, error) {
	opts, _, err := GenerateWithDiag(snap)
	return opts, err
}

// GenerateWithDiag собирает конфиг sing-box целиком из снимка состояния и
// рассказывает, что стало с каждым включённым правилом ([RuleDiag]).
//
// Генерация — чистая функция от состояния. Частичного патча конфига нет: правило,
// ссылающееся на туннель, и DNS-правило того же набора обязаны меняться вместе, а
// точечная правка JSON этого не гарантирует.
//
// Выключенные туннели в конфиг не попадают, а правило, чей туннель недоступен,
// остаётся в конфиге с действием reject — и в route.rules, и в dns.rules
// (ADR 0013). Убрать его нельзя: route.final — прямой выход, и исчезнувшее
// правило отдало бы свой трафик наружу с адреса сервера. Единственное исключение
// — правило без единого условия совпадения: его не выразить ни маршрутом, ни
// отказом, потому что совпадать оно будет со всем.
// Ссылка на несуществующий туннель — ошибка: состояние повреждено, и молчаливое
// продолжение скрыло бы это.
//
// Диагностика собирается тем же проходом, что и конфиг: отдельная функция,
// повторяющая условия генератора, разъехалась бы с ним молча.
func GenerateWithDiag(snap store.Snapshot) (option.Options, []RuleDiag, error) {
	tunnels := make(map[string]store.Tunnel, len(snap.Tunnels))
	for _, t := range snap.Tunnels {
		tunnels[t.ID] = t
	}

	log := slog.Default()
	endpoints, outbounds, skipped, err := buildTunnels(snap.Tunnels, log)
	if err != nil {
		return option.Options{}, nil, err
	}

	// Адреса всех пиров, не только включённых: правилу, у которого все выбранные
	// пиры выключены, адреса нужны, чтобы отказ достался ровно им, а не всем.
	peers := make(map[string]string, len(snap.Peers))
	live := make(map[string]bool, len(snap.Peers))
	for _, p := range snap.Peers {
		addr, err := cidr(p.Address)
		if err != nil {
			if !p.Enabled {
				// Выключенный пир с битым адресом конфиг не ломает: в wg0 его нет.
				continue
			}
			return option.Options{}, nil, fmt.Errorf("адрес пира %q: %w", p.Name, err)
		}
		peers[p.ID] = addr
		live[p.ID] = p.Enabled
	}

	dns, err := buildDNS(snap.Settings)
	if err != nil {
		return option.Options{}, nil, err
	}

	// Первым делом — перехват DNS: всё, что пришло на dns-in, уходит в резолвер,
	// а не в маршрутизацию.
	routeRules := []option.Rule{{
		Type: C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{
			RawDefaultRule: option.RawDefaultRule{
				Inbound: badoption.Listable[string]{TagDNSIn},
			},
			RuleAction: option.RuleAction{Action: C.RuleActionTypeHijackDNS},
		},
	}}

	var ruleSets []option.RuleSet
	// Один community-список могут использовать несколько правил, а тег набора в
	// конфиге обязан быть уникальным.
	seenSets := make(map[string]bool)
	// Пары «первое звено + второе» из правил с цепью: на каждую различную пару
	// приходится один клон второго звена, сколько бы правил на неё ни ссылалось
	// (ADR 0012). Порядок задаётся первым правилом пары и потому воспроизводим.
	var chains []chainPair
	seenChains := make(map[string]bool)
	// Судьба каждого включённого правила: с маршрутом, отказом или вовсе мимо
	// конфига. Порядок — тот же, в каком правила идут в route.rules.
	diags := make([]RuleDiag, 0, len(snap.Rules))
	for _, r := range sortedRules(snap.Rules) {
		tunnelTag := ""
		// Непустая причина означает: маршрута у правила нет, и оно уходит в
		// конфиг отказом. Причина идёт только в лог, пользователь видит
		// недоступный туннель в панели.
		unavailable := ""
		if r.Action == store.ActionTunnel {
			t, ok := tunnels[r.TunnelID]
			if !ok {
				return option.Options{}, nil, fmt.Errorf(
					"правило %q ссылается на несуществующий туннель %s", r.Name, r.TunnelID)
			}
			switch {
			case !t.Enabled:
				unavailable = "туннель выключен"
			// Пул без пригодных серверов тега не получил, ссылаться не на что.
			case skipped[t.ID]:
				unavailable = "у туннеля-пула нет серверов"
			default:
				tunnelTag = TunnelTag(t.ID)
			}

			if unavailable == "" && r.ViaTunnelID != "" {
				via, ok := tunnels[r.ViaTunnelID]
				if !ok {
					return option.Options{}, nil, fmt.Errorf(
						"второе звено правила %q — несуществующий туннель %s", r.Name, r.ViaTunnelID)
				}
				if via.Source != store.SourceWARP {
					return option.Options{}, nil, fmt.Errorf(
						"второе звено правила %q — туннель %q, а им бывает только WARP", r.Name, via.Name)
				}
				// Выключенное второе звено обрывает цепь целиком. Отправить
				// трафик одним первым звеном было бы тихой подменой маршрута:
				// ресурс увидел бы не тот адрес, ради которого цепь и заводили.
				if !via.Enabled {
					unavailable = "второе звено цепи выключено"
					tunnelTag = ""
				} else {
					tunnelTag = ChainTag(via.ID, t.ID)
					if !seenChains[tunnelTag] {
						seenChains[tunnelTag] = true
						chains = append(chains, chainPair{viaID: via.ID, firstID: t.ID})
					}
				}
			}
		}

		sets, tags, err := buildRuleSets(r, snap.Settings)
		if err != nil {
			return option.Options{}, nil, err
		}
		// Единственный пропуск: правило без условий совпадения нельзя выразить ни
		// маршрутом, ни отказом — совпадать оно будет со всем.
		if len(tags) == 0 {
			diags = append(diags, RuleDiag{ID: r.ID, Name: r.Name, Skipped: true})
			continue
		}

		sources, ok := sourceCIDRs(r, peers, live)
		if !ok {
			unavailable = "выключены все выбранные пиры"
			tunnelTag = ""
		}
		if unavailable != "" {
			log.Warn("правило отказывает: маршрута нет",
				"правило", r.Name, "причина", unavailable)
		}

		for _, set := range sets {
			if seenSets[set.Tag] {
				continue
			}
			seenSets[set.Tag] = true
			ruleSets = append(ruleSets, set)
		}
		denied := unavailable != ""
		diags = append(diags, RuleDiag{ID: r.ID, Name: r.Name, Reason: unavailable})
		routeRules = append(routeRules, routeRule(r, tags, sources, tunnelTag, denied))
		if dnsRule, ok := dnsRuleFor(r, tags, sources, denied); ok {
			dns.Rules = append(dns.Rules, dnsRule)
		}
	}

	for _, c := range chains {
		ep, err := chainEndpoint(tunnels[c.viaID], ChainTag(c.viaID, c.firstID), TunnelTag(c.firstID))
		if err != nil {
			return option.Options{}, nil, err
		}
		endpoints = append(endpoints, ep)
	}

	dnsListen, err := listen(snap.Settings.WGServerAddress, dnsPort)
	if err != nil {
		return option.Options{}, nil, fmt.Errorf("адрес сервера в настройках: %w", err)
	}
	tproxyIn, err := listen(tproxyListen, tproxyPort)
	if err != nil {
		return option.Options{}, nil, err
	}

	return option.Options{
		Log: &option.LogOptions{Level: snap.Settings.LogLevel},
		DNS: dns,
		Inbounds: []option.Inbound{
			{
				Type: C.TypeDirect,
				Tag:  TagDNSIn,
				Options: &option.DirectInboundOptions{
					ListenOptions: dnsListen,
				},
			},
			{
				Type: C.TypeTProxy,
				Tag:  TagTProxyIn,
				Options: &option.TProxyInboundOptions{
					ListenOptions: tproxyIn,
				},
			},
		},
		Endpoints: endpoints,
		Outbounds: append([]option.Outbound{{
			Type:    C.TypeDirect,
			Tag:     TagDirect,
			Options: &option.DirectOutboundOptions{},
		}}, outbounds...),
		Route: &option.RouteOptions{
			Rules:                 routeRules,
			RuleSet:               ruleSets,
			Final:                 TagDirect,
			DefaultDomainResolver: &option.DomainResolveOptions{Server: TagDNSUpstream},
		},
		Experimental: &option.ExperimentalOptions{
			CacheFile: &option.CacheFileOptions{
				Enabled:     true,
				Path:        cacheFile,
				StoreFakeIP: true,
			},
			ClashAPI: &option.ClashAPIOptions{ExternalController: clashListen},
		},
	}, diags, nil
}

// Marshal сериализует конфиг для записи в /etc/sing-box/config.json. Часть типов
// option сериализуется с учётом контекста, поэтому не encoding/json.
func Marshal(opts option.Options) ([]byte, error) {
	b, err := singjson.MarshalContext(context.Background(), opts)
	if err != nil {
		return nil, fmt.Errorf("сериализация конфига sing-box: %w", err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, b, "", "  "); err != nil {
		return nil, fmt.Errorf("форматирование конфига sing-box: %w", err)
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// sortedRules отдаёт включённые правила в порядке приоритета: первое совпадение
// в route.rules выигрывает, поэтому порядок — часть смысла, а не оформление.
func sortedRules(rules []store.Rule) []store.Rule {
	out := make([]store.Rule, 0, len(rules))
	for _, r := range rules {
		if r.Enabled {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out
}

// sourceCIDRs возвращает адреса пиров, на которых действует правило. Второе
// значение — false, если правило адресовано выбранным пирам, но все они
// выключены: маршрута у такого правила нет, оно уходит в конфиг отказом.
//
// Адреса при этом возвращаются всё равно — теперь уже выключенных пиров. Отказ
// без них достался бы всем: пустой source_ip_cidr означает «для всех», а это
// противоположность заданному.
func sourceCIDRs(r store.Rule, peers map[string]string, live map[string]bool) ([]string, bool) {
	if r.PeerScope != store.ScopeSelected {
		return nil, true
	}
	var selected, enabled []string
	for _, id := range r.PeerIDs {
		addr, ok := peers[id]
		if !ok {
			continue
		}
		selected = append(selected, addr)
		if live[id] {
			enabled = append(enabled, addr)
		}
	}
	if len(enabled) == 0 {
		return selected, false
	}
	return enabled, true
}

// routeRule собирает запись route.rules[] для одного правила.
//
// denied — маршрута у правила нет: туннель недоступен. Такое правило остаётся в
// конфиге отказом, потому что route.final — прямой выход и выпавшее правило
// отдало бы свой трафик наружу с адреса сервера (ADR 0013).
func routeRule(r store.Rule, tags, sources []string, tunnelTag string, denied bool) option.Rule {
	raw := option.RawDefaultRule{
		RuleSet:      tags,
		SourceIPCIDR: sources,
	}
	var action option.RuleAction
	switch {
	case denied, r.Action == store.ActionBlock:
		action = option.RuleAction{
			Action:        C.RuleActionTypeReject,
			RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault},
		}
	case r.Action == store.ActionDirect:
		action = option.RuleAction{
			Action:       C.RuleActionTypeRoute,
			RouteOptions: option.RouteActionOptions{Outbound: TagDirect},
		}
	default:
		action = option.RuleAction{
			Action:       C.RuleActionTypeRoute,
			RouteOptions: option.RouteActionOptions{Outbound: tunnelTag},
		}
	}
	return option.Rule{
		Type:           C.RuleTypeDefault,
		DefaultOptions: option.DefaultRule{RawDefaultRule: raw, RuleAction: action},
	}
}

// dnsRuleFor собирает запись dns.rules[] для правила.
//
// Правило в туннель получает FakeIP: без него трафик к домену пошёл бы на
// настоящий адрес мимо nft-метки, то есть мимо туннеля. Правило block получает
// отказ по той же причине — без FakeIP запись в route.rules ловила бы только
// подсети, а домены уходили бы напрямую. Правило с недоступным туннелем (denied)
// отказывает ровно поэтому же (ADR 0013). Правилу direct DNS-запись не нужна: его
// трафик и так идёт мимо sing-box. Не нужна и правилу с resolve_real_ip — там
// клиенту нужен настоящий адрес.
func dnsRuleFor(r store.Rule, tags, sources []string, denied bool) (option.DNSRule, bool) {
	raw := option.RawDefaultDNSRule{RuleSet: tags, SourceIPCIDR: sources}
	switch {
	case denied, r.Action == store.ActionBlock:
		return reject(raw), true
	case r.Action == store.ActionTunnel && !r.ResolveRealIP:
		ttl := uint32(fakeIPTTL)
		return option.DNSRule{
			Type: C.RuleTypeDefault,
			DefaultOptions: option.DefaultDNSRule{
				RawDefaultDNSRule: raw,
				DNSRuleAction: option.DNSRuleAction{
					Action: C.RuleActionTypeRoute,
					RouteOptions: option.DNSRouteActionOptions{
						Server:     TagDNSFakeIP,
						RewriteTTL: &ttl,
					},
				},
			},
		}, true
	default:
		return option.DNSRule{}, false
	}
}

// listen — общая часть inbound: адрес и порт, больше ничего не настраивается.
//
// Ошибка, а не паника: адрес приходит из настроек, то есть от пользователя.
// `MustParseAddr` здесь превращал опечатку в 500 на `POST /api/apply` и в
// невзлетающий демон на следующем старте (аудит от 2026-07, пункт 11). Разбор
// адреса при сохранении настроек сюда битую строку уже не пускает, но
// генератору незачем полагаться на чужую проверку.
func listen(addr string, port uint16) (option.ListenOptions, error) {
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return option.ListenOptions{}, fmt.Errorf("адрес %q не разобран: %w", addr, err)
	}
	la := badoption.Addr(a)
	return option.ListenOptions{Listen: &la, ListenPort: port}, nil
}

// cidr нормализует адрес до префикса: голый адрес пира — это /32.
func cidr(s string) (string, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return "", fmt.Errorf("%q не подсеть: %w", s, err)
		}
		return p.String(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return "", fmt.Errorf("%q не адрес: %w", s, err)
	}
	return netip.PrefixFrom(a, a.BitLen()).String(), nil
}

// hostPort разбирает «хост» или «хост:порт» из настроек DNS.
func hostPort(s string) (string, uint16, error) {
	host, port, found := strings.Cut(s, ":")
	if !found {
		return s, 0, nil
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("%w: порт DNS %q не число", store.ErrInvalid, port)
	}
	return host, uint16(n), nil
}
