package netstack

import (
	"errors"
	"net/netip"
	"reflect"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
)

func testConfig(subnets ...string) NftConfig {
	return NftConfig{WANInterface: "eth0", Subnets: subnets}
}

func buildOK(t *testing.T, cfg NftConfig) NftRuleset {
	t.Helper()
	rs, err := BuildNftRuleset(cfg)
	if err != nil {
		t.Fatalf("BuildNftRuleset: %v", err)
	}
	return rs
}

func chainByName(t *testing.T, rs NftRuleset, name string) NftChain {
	t.Helper()
	for _, ch := range rs.Chains {
		if ch.Name == name {
			return ch
		}
	}
	t.Fatalf("цепочки %s нет в наборе", name)
	return NftChain{}
}

func setByName(t *testing.T, rs NftRuleset, name string) NftSet {
	t.Helper()
	for _, s := range rs.Sets {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("сета %s нет в наборе", name)
	return NftSet{}
}

func TestBuildNftRulesetChains(t *testing.T) {
	rs := buildOK(t, testConfig())

	if rs.Table != "razdacha" {
		t.Errorf("таблица = %q, ожидалось razdacha", rs.Table)
	}

	want := []struct {
		name, typ, hook string
		priority        int
	}{
		{ChainMangle, "filter", "prerouting", -150},
		{ChainProxy, "filter", "prerouting", -100},
		{ChainForward, "filter", "forward", -150},
		{ChainPostrouting, "nat", "postrouting", 100},
	}
	if len(rs.Chains) != len(want) {
		t.Fatalf("цепочек %d, ожидалось %d", len(rs.Chains), len(want))
	}
	for i, w := range want {
		got := rs.Chains[i]
		if got.Name != w.name || got.Type != w.typ || got.Hook != w.hook || got.Priority != w.priority {
			t.Errorf("цепочка %d = %+v, ожидалось %v", i, got, w)
		}
	}

	if _, err := BuildNftRuleset(NftConfig{}); !errors.Is(err, ErrNftNoWAN) {
		t.Errorf("без WAN-интерфейса ошибка = %v, ожидалось ErrNftNoWAN", err)
	}
}

func TestBuildNftRulesetMarking(t *testing.T) {
	rs := buildOK(t, testConfig())
	mangle := chainByName(t, rs, ChainMangle)

	if len(mangle.Rules) != 4 {
		t.Fatalf("правил в mangle %d, ожидалось 4", len(mangle.Rules))
	}

	// Первое правило отсекает всё, что пришло не от клиентов, — включая
	// исходящий трафик самого sing-box. На нём держится отсутствие петли.
	first := mangle.Rules[0]
	if first.IIf != DefaultWGInterface || !first.IIfNeq || first.Action != ActionReturn {
		t.Errorf("первое правило mangle = %+v, ожидалось iifname != wg0 return", first)
	}

	local := mangle.Rules[1]
	if local.DstSet != SetLocalV4 || local.Action != ActionReturn {
		t.Errorf("второе правило mangle = %+v, ожидался return по local_v4", local)
	}

	fakeip := mangle.Rules[2]
	if fakeip.DstPrefix != netip.MustParsePrefix("198.18.0.0/15") || fakeip.Action != ActionMark {
		t.Errorf("правило FakeIP = %+v", fakeip)
	}

	subnets := mangle.Rules[3]
	if subnets.DstSet != SetSubnets || subnets.Action != ActionMark {
		t.Errorf("правило подсетей = %+v, ожидалась маркировка по %s", subnets, SetSubnets)
	}

	if FwMark != 0x00100000 {
		t.Errorf("метка = %#x, ожидалось 0x00100000", FwMark)
	}
}

func TestBuildNftRulesetTProxy(t *testing.T) {
	rs := buildOK(t, testConfig())
	proxy := chainByName(t, rs, ChainProxy)

	if len(proxy.Rules) != 2 {
		t.Fatalf("правил в proxy %d, ожидалось 2 (tcp и udp)", len(proxy.Rules))
	}
	protos := map[string]bool{}
	for _, r := range proxy.Rules {
		if r.MarkMatch != FwMark {
			t.Errorf("правило proxy без совпадения по метке: %+v", r)
		}
		if r.Action != ActionTProxy {
			t.Errorf("правило proxy = %+v, ожидался tproxy", r)
		}
		protos[r.L4Proto] = true
	}
	if !protos["tcp"] || !protos["udp"] {
		t.Errorf("протоколы proxy = %v, ожидались tcp и udp", protos)
	}
	if TProxyAddr != "127.0.0.1" || TProxyPort != 1602 {
		t.Errorf("tproxy слушает %s:%d, ожидалось 127.0.0.1:1602", TProxyAddr, TProxyPort)
	}
}

func TestBuildNftRulesetMasquerade(t *testing.T) {
	rs := buildOK(t, NftConfig{WGInterface: "wg0", WANInterface: "ens3"})
	post := chainByName(t, rs, ChainPostrouting)

	if len(post.Rules) != 1 {
		t.Fatalf("правил в postrouting %d, ожидалось 1", len(post.Rules))
	}
	r := post.Rules[0]
	if r.IIf != "wg0" || r.OIf != "ens3" || r.Action != ActionMasquerade {
		t.Errorf("правило masquerade = %+v", r)
	}
}

// Прямой трафик в sing-box не заходит вовсе: единственное правило, отдающее
// пакет наружу процессу, требует метки, а метка ставится только совпавшим
// адресам (docs/01-architecture.md, «Ключевое следствие»).
func TestDirectTrafficBypassesSingbox(t *testing.T) {
	rs := buildOK(t, testConfig("203.0.113.0/24"))

	for _, ch := range rs.Chains {
		for _, r := range ch.Rules {
			if r.Action != ActionTProxy {
				continue
			}
			if r.MarkMatch != FwMark {
				t.Errorf("tproxy без обязательной метки: %+v", r)
			}
		}
	}

	for _, r := range chainByName(t, rs, ChainMangle).Rules {
		if r.Action != ActionMark {
			continue
		}
		if !r.DstPrefix.IsValid() && r.DstSet == "" {
			t.Errorf("маркировка без условия по адресу назначения: %+v", r)
		}
	}
}

// IPv6 клиентов только отклоняется — слой 3 ADR 0005.
func TestBuildNftRulesetIPv6(t *testing.T) {
	rs := buildOK(t, testConfig("2606:4700::/32", "104.16.0.0/13"))

	var v6 []NftRule
	for _, ch := range rs.Chains {
		for _, r := range ch.Rules {
			if r.NFProto == "ipv6" {
				v6 = append(v6, r)
			}
		}
	}
	if len(v6) != 1 {
		t.Fatalf("правил по ipv6 %d, ожидалось одно (reject)", len(v6))
	}
	if v6[0].Action != ActionRejectICMPv6 || v6[0].IIf != DefaultWGInterface {
		t.Errorf("правило ipv6 = %+v, ожидался reject на wg0", v6[0])
	}

	// IPv6-подсеть в сет не попадает: он объявлен как ipv4_addr.
	if got := setByName(t, rs, SetSubnets).Elements; len(got) != 1 {
		t.Fatalf("элементов сета %d, ожидался один: %v", len(got), got)
	}
	if rs.SkippedSubnets != 1 {
		t.Errorf("пропущено %d записей, ожидалась одна (IPv6)", rs.SkippedSubnets)
	}
}

func TestBuildNftRulesetClampMSS(t *testing.T) {
	rs := buildOK(t, testConfig())
	fw := chainByName(t, rs, ChainForward)

	var clamps []NftRule
	for _, r := range fw.Rules {
		if r.Action == ActionClampMSS {
			clamps = append(clamps, r)
		}
	}
	if len(clamps) != 2 {
		t.Fatalf("правил MSS-клампинга %d, ожидалось два направления", len(clamps))
	}
	if clamps[0].IIf != DefaultWGInterface || clamps[1].OIf != DefaultWGInterface {
		t.Errorf("клампинг не в обе стороны: %+v", clamps)
	}
	for _, r := range clamps {
		if !r.TCPSyn || r.L4Proto != "tcp" {
			t.Errorf("клампинг вне SYN-пакетов TCP: %+v", r)
		}
	}
}

// Подсети приходят из слоя lists ровно в том виде, в каком их отдаёт разбор
// plain-списка, — проверяем стык, а не выдуманные строки.
func TestSubnetsFromLists(t *testing.T) {
	body := []byte("# telegram\n91.108.4.0/22\n91.108.8.0/22\n149.154.160.0/20\nexample.com\n")
	parsed := lists.ParsePlain(body)
	if len(parsed.Subnets) != 3 {
		t.Fatalf("lists отдал %d подсетей: %v", len(parsed.Subnets), parsed.Subnets)
	}

	rs := buildOK(t, testConfig(parsed.Subnets...))
	set := setByName(t, rs, SetSubnets)

	// Соседние /22 сливаются в один интервал, третья подсеть остаётся своей.
	if len(set.Elements) != 2 {
		t.Fatalf("интервалов в сете %d, ожидалось 2: %v", len(set.Elements), set.Elements)
	}
	if got := set.Elements[0].String(); got != "91.108.4.0-91.108.11.255" {
		t.Errorf("первый интервал = %s", got)
	}
	if got := set.Elements[1].String(); got != "149.154.160.0-149.154.175.255" {
		t.Errorf("второй интервал = %s", got)
	}
	if rs.SkippedSubnets != 0 {
		t.Errorf("пропущено %d записей, ожидалось 0", rs.SkippedSubnets)
	}
}

func TestBuildNftRulesetIdempotent(t *testing.T) {
	cfg := testConfig("10.20.0.0/16", "1.1.1.1", "мусор", "104.16.0.0/13")

	first := buildOK(t, cfg)
	second := buildOK(t, cfg)

	if !reflect.DeepEqual(first, second) {
		t.Errorf("две сборки из одного состояния разошлись:\n%+v\n%+v", first, second)
	}
	if first.SkippedSubnets != 1 {
		t.Errorf("пропущено %d записей, ожидалась одна", first.SkippedSubnets)
	}
}

func TestMergeSubnets(t *testing.T) {
	tests := []struct {
		name    string
		in      []string
		want    []string
		skipped int
	}{
		{
			name: "пересечение сливается",
			in:   []string{"104.16.0.0/13", "104.16.0.0/16", "104.24.0.0/14"},
			want: []string{"104.16.0.0-104.27.255.255"},
		},
		{
			name: "порядок не важен",
			in:   []string{"8.8.8.8", "1.1.1.1"},
			want: []string{"1.1.1.1-1.1.1.1", "8.8.8.8-8.8.8.8"},
		},
		{
			name:    "битые записи считаются, но не отменяют разбор",
			in:      []string{"", "не адрес", "10.0.0.0/8", "10.0.0.0/64", "::1"},
			want:    []string{"10.0.0.0-10.255.255.255"},
			skipped: 4,
		},
		{
			name: "адрес с хостовой частью нормализуется",
			in:   []string{"192.0.2.77/24"},
			want: []string{"192.0.2.0-192.0.2.255"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ranges, skipped := MergeSubnets(tt.in)
			got := make([]string, 0, len(ranges))
			for _, r := range ranges {
				got = append(got, r.String())
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("интервалы = %v, ожидалось %v", got, tt.want)
			}
			if skipped != tt.skipped {
				t.Errorf("пропущено %d, ожидалось %d", skipped, tt.skipped)
			}
		})
	}
}

func TestLocalV4Set(t *testing.T) {
	rs := buildOK(t, testConfig())
	set := setByName(t, rs, SetLocalV4)

	if len(set.Elements) == 0 {
		t.Fatal("сет local_v4 пуст")
	}
	// Адреса самой сети клиентов не проксируются даже при совпадении со списком.
	client := netip.MustParseAddr("10.8.0.1")
	found := false
	for _, r := range set.Elements {
		if r.Contains(client) {
			found = true
		}
	}
	if !found {
		t.Errorf("10.8.0.1 не покрыт сетом local_v4: %v", set.Elements)
	}
}
