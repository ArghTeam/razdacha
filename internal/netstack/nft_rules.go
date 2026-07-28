package netstack

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"go4.org/netipx"
)

// Ошибки слоя. Проверяются через errors.Is; тексты доходят до UI как есть.
var (
	// ErrNftNoWAN — не задан внешний интерфейс, masquerade построить не из чего.
	ErrNftNoWAN = errors.New("не задан WAN-интерфейс")
	// ErrNftNoWG — не задан интерфейс клиентов, маркировать нечего.
	ErrNftNoWG = errors.New("не задан интерфейс WireGuard")
	// ErrNftBadResolver — адрес резолвера не разобран или не IPv4.
	ErrNftBadResolver = errors.New("адрес резолвера не разобран")
)

// Имена объектов nft. Заданы в docs/03-networking.md и цитируются
// диагностикой и документацией — менять только вместе с ними.
const (
	// NftTable — единственная таблица, семейство inet.
	NftTable = "razdacha"
	// ChainMangle — маркировка проксируемого трафика.
	ChainMangle = "mangle"
	// ChainProxy — передача помеченного трафика в tproxy sing-box.
	ChainProxy = "proxy"
	// ChainDNSNat — перехват DNS клиентов: адрес назначения переписывается на
	// наш резолвер, каким бы он ни был прописан у клиента.
	ChainDNSNat = "dnsnat"
	// ChainForward — MSS-клампинг и отказ IPv6 клиентов.
	ChainForward = "forward"
	// ChainPostrouting — masquerade прямого трафика.
	ChainPostrouting = "postrouting"
	// SetLocalV4 — приватные и служебные диапазоны, которые не проксируются.
	SetLocalV4 = "local_v4"
	// SetSubnets — подсети из списков (слой lists), для них FakeIP не работает.
	SetSubnets = "razdacha_subnets"
)

// Числа сетевого пути. FwMark и TProxy* совпадают с Podkop и с генератором
// конфига sing-box (internal/singbox): inbound tproxy слушает 127.0.0.1:1602.
const (
	// FwMark — метка проксируемого трафика, она же маска в ip rule.
	FwMark uint32 = 0x00100000
	// TProxyAddr — адрес inbound tproxy sing-box.
	TProxyAddr = "127.0.0.1"
	// TProxyPort — порт inbound tproxy sing-box.
	TProxyPort uint16 = 1602
	// DNSPort — plaintext DNS. Весь такой трафик с wg0 забирает себе наш
	// резолвер, на этом держится селективность (docs/04-dns-fakeip.md).
	DNSPort uint16 = 53
	// DoTPort — DNS поверх TLS и QUIC. Перехватить нельзя, поэтому отказ:
	// молча ушедший в чужой шифрованный резолвер клиент теряет FakeIP.
	DoTPort uint16 = 853
)

// DefaultResolverAddr — адрес, на котором sing-box слушает DNS клиентов. Тот
// же, что store.DefaultSettings().WGServerAddress; вызывающий передаёт живое
// значение из настроек, константа — только запасной вариант.
const DefaultResolverAddr = "10.8.0.1"

// FakeIPRange — диапазон FakeIP sing-box (ADR 0005: только v4). Трафик на эти
// адреса всегда проксируется: реального маршрута к ним не существует.
var FakeIPRange = netip.MustParsePrefix("198.18.0.0/15")

// localV4 — содержимое сета local_v4 из docs/03-networking.md: адреса, до
// которых клиент ходит напрямую, даже если они попали в список подсетей.
var localV4 = []string{
	"0.0.0.0/8",
	"10.0.0.0/8",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.168.0.0/16",
	"224.0.0.0/4",
	"240.0.0.0/4",
}

// NftConfig — состояние, из которого строится набор правил.
type NftConfig struct {
	// WGInterface — интерфейс клиентов, пусто означает wg0.
	WGInterface string
	// WANInterface — внешний интерфейс, на него уходит masquerade.
	WANInterface string
	// ResolverAddr — адрес нашего резолвера (Settings.WGServerAddress), на него
	// переписывается DNS клиентов. Пусто означает [DefaultResolverAddr].
	ResolverAddr string
	// Subnets — подсети из слоя lists (Manager.Subnets), строками CIDR или
	// отдельными адресами. Битые и IPv6-записи пропускаются, а не роняют сборку.
	Subnets []string
}

// NftAction — что правило делает с пакетом.
type NftAction string

// Действия правил.
const (
	// ActionReturn — выйти из цепочки, пакет идёт своим путём.
	ActionReturn NftAction = "return"
	// ActionMark — поставить метку FwMark.
	ActionMark NftAction = "mark"
	// ActionTProxy — отдать соединение sing-box на TProxyAddr:TProxyPort.
	ActionTProxy NftAction = "tproxy"
	// ActionMasquerade — подменить адрес источника на адрес WAN.
	ActionMasquerade NftAction = "masquerade"
	// ActionClampMSS — вписать MSS по MTU маршрута (docs/03-networking.md).
	ActionClampMSS NftAction = "clamp-mss"
	// ActionRejectICMPv6 — отказ IPv6 клиентов, слой 3 ADR 0005. Именно reject:
	// drop даёт Happy Eyeballs четверть секунды задержки на каждое соединение.
	ActionRejectICMPv6 NftAction = "reject-icmpv6"
	// ActionRejectICMP — отказ IPv4 с ICMP «administratively prohibited».
	// Клиент получает отказ сразу и переходит к следующему резолверу, а не
	// ждёт таймаута, как было бы с drop.
	ActionRejectICMP NftAction = "reject-icmp"
	// ActionDNAT — переписать адрес и порт назначения на DNATAddr:DNATPort.
	ActionDNAT NftAction = "dnat"
)

// NftRule — одно правило в терминах предметной области: набор условий и
// действие. Перевод в выражения nftables — дело nft.go, здесь netlink нет,
// поэтому состав набора проверяется тестом без root.
type NftRule struct {
	// IIf, OIf — входящий и исходящий интерфейсы, пусто означает «любой».
	IIf, OIf string
	// IIfNeq инвертирует сравнение по IIf.
	IIfNeq bool
	// NFProto — "ipv4" или "ipv6", пусто означает «любой».
	NFProto string
	// DstPrefix — совпадение по адресу назначения.
	DstPrefix netip.Prefix
	// DstPrefixNeq инвертирует сравнение по DstPrefix.
	DstPrefixNeq bool
	// DstSet — совпадение адреса назначения с сетом по имени.
	DstSet string
	// L4Proto — "tcp" или "udp".
	L4Proto string
	// DstPort — порт назначения, 0 означает «любой». Требует непустого
	// L4Proto: без него заголовок транспортного уровня читать нельзя.
	DstPort uint16
	// MarkMatch — совпадение метки: mark & MarkMatch == MarkMatch.
	MarkMatch uint32
	// TCPSyn — только SYN-пакеты (tcp flags syn / syn,rst).
	TCPSyn bool
	// DNATAddr, DNATPort — куда переписывается назначение для ActionDNAT.
	DNATAddr netip.Addr
	DNATPort uint16
	// Action — что делать с совпавшим пакетом.
	Action NftAction
}

// NftChain — цепочка с точкой подключения к хукам ядра.
type NftChain struct {
	Name     string
	Type     string // filter | nat
	Hook     string // prerouting | forward | postrouting
	Priority int
	Rules    []NftRule
}

// NftSet — сет интервалов IPv4. Элементы уже слиты и отсортированы:
// auto-merge — приём userspace-утилиты nft, через netlink его нет, и
// пересекающиеся интервалы ядро отвергает.
type NftSet struct {
	Name     string
	Elements []netipx.IPRange
}

// NftRuleset — полный набор объектов таблицы. Заливается целиком, частичных
// правок нет (docs/03-networking.md).
type NftRuleset struct {
	Table  string
	Sets   []NftSet
	Chains []NftChain
	// SkippedSubnets — сколько записей из Subnets разобрать не удалось.
	// Идёт в лог: чужой список из-за одной битой строки терять нельзя.
	SkippedSubnets int
}

// Приоритеты хуков nftables (include/uapi/linux/netfilter_ipv4.h). Числа, а не
// имена: имена разбирает userspace-утилита nft, через netlink идут значения.
const (
	priorityMangle = -150
	priorityProxy  = -100
	priorityDstNAT = -100
	prioritySrcNAT = 100
)

// BuildNftRuleset собирает набор правил из состояния. Чистая функция: ни
// netlink, ни root, ни файловой системы — только вычисление.
func BuildNftRuleset(cfg NftConfig) (NftRuleset, error) {
	wg := cfg.WGInterface
	if wg == "" {
		wg = DefaultWGInterface
	}
	if strings.TrimSpace(wg) == "" {
		return NftRuleset{}, ErrNftNoWG
	}
	if strings.TrimSpace(cfg.WANInterface) == "" {
		return NftRuleset{}, fmt.Errorf("сборка nft-правил: %w", ErrNftNoWAN)
	}
	resolver, err := resolverAddr(cfg.ResolverAddr)
	if err != nil {
		return NftRuleset{}, err
	}

	local, _ := MergeSubnets(localV4)
	subnets, skipped := MergeSubnets(cfg.Subnets)

	rs := NftRuleset{
		Table: NftTable,
		Sets: []NftSet{
			{Name: SetLocalV4, Elements: local},
			{Name: SetSubnets, Elements: subnets},
		},
		SkippedSubnets: skipped,
	}

	// mangle: что маркируется. Первое правило отсекает всё, что пришло не от
	// клиентов, включая исходящий трафик самого sing-box — поэтому петли нет
	// и отдельные исключения по адресам эндпоинтов не нужны (ADR 0002).
	//
	// Сразу за ним — выход для DNS-портов. Он обязателен: без него запрос на
	// чужой резолвер, чей адрес случайно попал в razdacha_subnets, получил бы
	// метку и уехал бы в tproxy, где перехватывать его уже нечем — правило
	// hijack-dns в sing-box висит на inbound dns-in, а не на tproxy. С выходом
	// DNS гарантированно доходит до цепочки dnsnat, а DoT — до forward, где
	// его ждёт отказ. Заодно снимается вопрос порядка между цепочками proxy и
	// dnsnat: они обе на приоритете -100, и кто из них раньше — не определено.
	mangleRules := []NftRule{{IIf: wg, IIfNeq: true, Action: ActionReturn}}
	for _, port := range []uint16{DNSPort, DoTPort} {
		mangleRules = append(mangleRules,
			portRules(NftRule{NFProto: "ipv4", DstPort: port, Action: ActionReturn})...)
	}
	mangleRules = append(mangleRules,
		NftRule{NFProto: "ipv4", DstSet: SetLocalV4, Action: ActionReturn},
		NftRule{NFProto: "ipv4", DstPrefix: FakeIPRange, Action: ActionMark},
		NftRule{NFProto: "ipv4", DstSet: SetSubnets, Action: ActionMark},
	)
	mangle := NftChain{
		Name: ChainMangle, Type: "filter", Hook: "prerouting", Priority: priorityMangle,
		Rules: mangleRules,
	}

	// dnsnat: весь plaintext-DNS клиентов обслуживает наш резолвер, что бы
	// клиент себе ни прописал — 8.8.8.8 руками, Android Private DNS, зашитый
	// в приложение адрес. Без этого домен резолвится в настоящий адрес, FakeIP
	// не выдаётся, и правило по домену перестаёт существовать (issue #122).
	//
	// Почему DNAT, а не метка с перехватом в tproxy:
	//   - метка увела бы DNS в таблицу 105 и в tproxy, где его пришлось бы
	//     разбирать sniff-ом и отдельным правилом hijack-dns на tproxy-inbound;
	//     DNAT доводит пакет до inbound dns-in, для которого перехват уже
	//     настроен, — новых сущностей в конфиге sing-box не появляется;
	//   - маркировка остаётся ровно тем, чем была: «адрес назначения совпал со
	//     списком». Порт в неё не входит, драться правилам не за что;
	//   - conntrack сам разворачивает ответ обратно, и клиент видит его от того
	//     адреса, на который спрашивал: перехват прозрачен.
	//
	// Петли нет по двум причинам сразу: цепочка висит на prerouting и смотрит
	// только на iifname wg0, а собственные запросы sing-box к апстриму идут
	// через output; плюс сравнение `ip daddr != резолвер` не даёт переписывать
	// то, что и так адресовано нам.
	dnsnat := NftChain{
		Name: ChainDNSNat, Type: "nat", Hook: "prerouting", Priority: priorityDstNAT,
		Rules: portRules(NftRule{
			IIf: wg, NFProto: "ipv4", DstPort: DNSPort,
			DstPrefix: netip.PrefixFrom(resolver, resolver.BitLen()), DstPrefixNeq: true,
			DNATAddr: resolver, DNATPort: DNSPort, Action: ActionDNAT,
		}),
	}

	// proxy: помеченное уходит в sing-box. Всё остальное сюда не попадает —
	// прямой трафик через sing-box не идёт вовсе (docs/01-architecture.md).
	proxy := NftChain{
		Name: ChainProxy, Type: "filter", Hook: "prerouting", Priority: priorityProxy,
		Rules: []NftRule{
			{NFProto: "ipv4", MarkMatch: FwMark, L4Proto: "tcp", Action: ActionTProxy},
			{NFProto: "ipv4", MarkMatch: FwMark, L4Proto: "udp", Action: ActionTProxy},
		},
	}

	// forward: клампинг, отказ IPv6 и отказ шифрованному DNS.
	//
	// 853 (DoT и DoQ) переписать некуда: внутри TLS запрос не разобрать, а
	// подставить свой сертификат нечем. Остаётся отказ — иначе клиент с
	// Private DNS молча уходит мимо нас и теряет FakeIP, ничего не замечая.
	// Отказ здесь, а не в prerouting: reject ядро принимает только в input,
	// forward и output. DoH на 443 не трогаем — он неотличим от обычного
	// HTTPS, и это записано в docs/04-dns-fakeip.md как граница.
	forwardRules := []NftRule{
		{IIf: wg, NFProto: "ipv4", L4Proto: "tcp", TCPSyn: true, Action: ActionClampMSS},
		{OIf: wg, NFProto: "ipv4", L4Proto: "tcp", TCPSyn: true, Action: ActionClampMSS},
		{IIf: wg, NFProto: "ipv6", Action: ActionRejectICMPv6},
	}
	forwardRules = append(forwardRules, portRules(NftRule{
		IIf: wg, NFProto: "ipv4", DstPort: DoTPort, Action: ActionRejectICMP,
	})...)
	forward := NftChain{
		Name: ChainForward, Type: "filter", Hook: "forward", Priority: priorityMangle,
		Rules: forwardRules,
	}

	postrouting := NftChain{
		Name: ChainPostrouting, Type: "nat", Hook: "postrouting", Priority: prioritySrcNAT,
		Rules: []NftRule{
			{IIf: wg, OIf: cfg.WANInterface, Action: ActionMasquerade},
		},
	}

	rs.Chains = []NftChain{mangle, proxy, dnsnat, forward, postrouting}
	return rs, nil
}

// portRules размножает правило на tcp и udp: пара «одно и то же условие для
// обоих протоколов» встречается везде, где речь о порте.
func portRules(base NftRule) []NftRule {
	out := make([]NftRule, 0, 2)
	for _, proto := range []string{"udp", "tcp"} {
		r := base
		r.L4Proto = proto
		out = append(out, r)
	}
	return out
}

// resolverAddr разбирает адрес нашего резолвера. Только IPv4: DNS клиентов
// ходит внутри туннеля, где v6 выключен тремя слоями (ADR 0005).
func resolverAddr(s string) (netip.Addr, error) {
	if strings.TrimSpace(s) == "" {
		s = DefaultResolverAddr
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil || !addr.Is4() {
		return netip.Addr{}, fmt.Errorf("сборка nft-правил: %w: %q", ErrNftBadResolver, s)
	}
	return addr, nil
}

// MergeSubnets превращает записи списков в непересекающиеся интервалы IPv4 и
// возвращает число пропущенных записей. Пропускаются пустые строки, мусор и
// IPv6: сет объявлен как ipv4_addr, IPv6 в системе выключен (ADR 0005).
//
// Слияние обязательно: списки Cloudflare и Telegram пересекаются между собой,
// а ядро отвергает пересекающиеся интервалы целой транзакцией.
func MergeSubnets(in []string) ([]netipx.IPRange, int) {
	var b netipx.IPSetBuilder
	skipped := 0

	for _, raw := range in {
		s := strings.TrimSpace(raw)
		if s == "" {
			skipped++
			continue
		}
		switch {
		case strings.Contains(s, "/"):
			p, err := netip.ParsePrefix(s)
			if err != nil || !p.Addr().Is4() {
				skipped++
				continue
			}
			b.AddPrefix(p.Masked())
		default:
			a, err := netip.ParseAddr(s)
			if err != nil || !a.Is4() {
				skipped++
				continue
			}
			b.Add(a)
		}
	}

	set, err := b.IPSet()
	if err != nil {
		return nil, len(in)
	}
	return set.Ranges(), skipped
}
