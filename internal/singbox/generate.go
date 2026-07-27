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

// Generate собирает конфиг sing-box целиком из снимка состояния.
//
// Генерация — чистая функция от состояния. Частичного патча конфига нет: правило,
// ссылающееся на туннель, и DNS-правило того же набора обязаны меняться вместе, а
// точечная правка JSON этого не гарантирует.
//
// Выключенные туннели и правила в конфиг не попадают. Правило пропускается, если
// его туннель выключен, если у него не осталось ни одного условия совпадения
// (иначе оно поймало бы весь трафик), или если все выбранные им пиры выключены.
// Ссылка на несуществующий туннель — ошибка: состояние повреждено, и молчаливый
// пропуск правила увёл бы трафик мимо туннеля.
// chainPair — цепь из двух звеньев: firstID принимает трафик с сервера, viaID
// выпускает его наружу. Пара, а не правило: тег клона зависит только от неё, и
// одинаковые пары из разных правил дают один outbound (ADR 0012).
type chainPair struct {
	viaID   string
	firstID string
}

func Generate(snap store.Snapshot) (option.Options, error) {
	tunnels := make(map[string]store.Tunnel, len(snap.Tunnels))
	for _, t := range snap.Tunnels {
		tunnels[t.ID] = t
	}

	log := slog.Default()
	endpoints, outbounds, skipped, err := buildTunnels(snap.Tunnels, log)
	if err != nil {
		return option.Options{}, err
	}

	peers := make(map[string]string, len(snap.Peers))
	for _, p := range snap.Peers {
		if !p.Enabled {
			continue
		}
		addr, err := cidr(p.Address)
		if err != nil {
			return option.Options{}, fmt.Errorf("адрес пира %q: %w", p.Name, err)
		}
		peers[p.ID] = addr
	}

	dns, err := buildDNS(snap.Settings)
	if err != nil {
		return option.Options{}, err
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
	for _, r := range sortedRules(snap.Rules) {
		tunnelTag := ""
		if r.Action == store.ActionTunnel {
			t, ok := tunnels[r.TunnelID]
			if !ok {
				return option.Options{}, fmt.Errorf(
					"правило %q ссылается на несуществующий туннель %s", r.Name, r.TunnelID)
			}
			if !t.Enabled {
				continue
			}
			// Пул без пригодных серверов тега не получил, ссылаться не на что.
			if skipped[t.ID] {
				log.Warn("правило пропущено: у его туннеля-пула нет серверов",
					"правило", r.Name, "туннель", t.Name)
				continue
			}
			tunnelTag = TunnelTag(t.ID)

			if r.ViaTunnelID != "" {
				via, ok := tunnels[r.ViaTunnelID]
				if !ok {
					return option.Options{}, fmt.Errorf(
						"второе звено правила %q — несуществующий туннель %s", r.Name, r.ViaTunnelID)
				}
				if via.Source != store.SourceWARP {
					return option.Options{}, fmt.Errorf(
						"второе звено правила %q — туннель %q, а им бывает только WARP", r.Name, via.Name)
				}
				// Выключенное второе звено обрывает цепь целиком. Отправить
				// трафик одним первым звеном было бы тихой подменой маршрута:
				// ресурс увидел бы не тот адрес, ради которого цепь и заводили.
				if !via.Enabled {
					log.Warn("правило пропущено: второе звено цепи выключено",
						"правило", r.Name, "туннель", via.Name)
					continue
				}
				tunnelTag = ChainTag(via.ID, t.ID)
				if !seenChains[tunnelTag] {
					seenChains[tunnelTag] = true
					chains = append(chains, chainPair{viaID: via.ID, firstID: t.ID})
				}
			}
		}

		sets, tags, err := buildRuleSets(r, snap.Settings)
		if err != nil {
			return option.Options{}, err
		}
		if len(tags) == 0 {
			continue
		}

		sources, ok := sourceCIDRs(r, peers)
		if !ok {
			continue
		}

		for _, set := range sets {
			if seenSets[set.Tag] {
				continue
			}
			seenSets[set.Tag] = true
			ruleSets = append(ruleSets, set)
		}
		routeRules = append(routeRules, routeRule(r, tags, sources, tunnelTag))
		if dnsRule, ok := dnsRuleFor(r, tags, sources); ok {
			dns.Rules = append(dns.Rules, dnsRule)
		}
	}

	for _, c := range chains {
		ep, err := chainEndpoint(tunnels[c.viaID], ChainTag(c.viaID, c.firstID), TunnelTag(c.firstID))
		if err != nil {
			return option.Options{}, err
		}
		endpoints = append(endpoints, ep)
	}

	return option.Options{
		Log: &option.LogOptions{Level: snap.Settings.LogLevel},
		DNS: dns,
		Inbounds: []option.Inbound{
			{
				Type: C.TypeDirect,
				Tag:  TagDNSIn,
				Options: &option.DirectInboundOptions{
					ListenOptions: listen(snap.Settings.WGServerAddress, dnsPort),
				},
			},
			{
				Type: C.TypeTProxy,
				Tag:  TagTProxyIn,
				Options: &option.TProxyInboundOptions{
					ListenOptions: listen(tproxyListen, tproxyPort),
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
	}, nil
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
// значение — false, если правило адресовано выбранным пирам, но все они выключены:
// пустой source_ip_cidr означал бы «для всех», а это противоположность заданному.
func sourceCIDRs(r store.Rule, peers map[string]string) ([]string, bool) {
	if r.PeerScope != store.ScopeSelected {
		return nil, true
	}
	var out []string
	for _, id := range r.PeerIDs {
		if addr, ok := peers[id]; ok {
			out = append(out, addr)
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// routeRule собирает запись route.rules[] для одного правила.
func routeRule(r store.Rule, tags, sources []string, tunnelTag string) option.Rule {
	raw := option.RawDefaultRule{
		RuleSet:      tags,
		SourceIPCIDR: sources,
	}
	var action option.RuleAction
	switch r.Action {
	case store.ActionBlock:
		action = option.RuleAction{
			Action:        C.RuleActionTypeReject,
			RejectOptions: option.RejectActionOptions{Method: C.RuleActionRejectMethodDefault},
		}
	case store.ActionDirect:
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
// подсети, а домены уходили бы напрямую. Правилу direct DNS-запись не нужна: его
// трафик и так идёт мимо sing-box. Не нужна и правилу с resolve_real_ip — там
// клиенту нужен настоящий адрес.
func dnsRuleFor(r store.Rule, tags, sources []string) (option.DNSRule, bool) {
	raw := option.RawDefaultDNSRule{RuleSet: tags, SourceIPCIDR: sources}
	switch {
	case r.Action == store.ActionBlock:
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
func listen(addr string, port uint16) option.ListenOptions {
	a := badoption.Addr(netip.MustParseAddr(addr))
	return option.ListenOptions{Listen: &a, ListenPort: port}
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
