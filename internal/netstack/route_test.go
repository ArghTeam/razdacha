package netstack

import (
	"errors"
	"net"
	"testing"

	"github.com/vishvananda/netlink"
)

// fakeRoutes подменяет netlink: хранит правила и маршруты в памяти, поэтому
// маршрутизация проверяется без root и без ядра.
type fakeRoutes struct {
	rules    []netlink.Rule
	routes   []netlink.Route
	defaults []netlink.Route
	links    map[string]int
	names    map[int]string
	linkErr  error
}

func newFakeRoutes() *fakeRoutes {
	return &fakeRoutes{
		links: map[string]int{"lo": 1, "eth0": 2},
		names: map[int]string{1: "lo", 2: "eth0"},
	}
}

func (f *fakeRoutes) RuleAdd(rule *netlink.Rule) error {
	f.rules = append(f.rules, *rule)
	return nil
}

func (f *fakeRoutes) RuleDel(rule *netlink.Rule) error {
	for i := range f.rules {
		if f.rules[i].Table == rule.Table && f.rules[i].Mark == rule.Mark {
			f.rules = append(f.rules[:i], f.rules[i+1:]...)
			return nil
		}
	}
	return errors.New("правила нет")
}

func (f *fakeRoutes) Rules() ([]netlink.Rule, error) { return f.rules, nil }

func (f *fakeRoutes) RouteAdd(route *netlink.Route) error {
	f.routes = append(f.routes, *route)
	return nil
}

func (f *fakeRoutes) RouteDel(route *netlink.Route) error {
	for i := range f.routes {
		if f.routes[i].Table == route.Table && f.routes[i].LinkIndex == route.LinkIndex {
			f.routes = append(f.routes[:i], f.routes[i+1:]...)
			return nil
		}
	}
	return errors.New("маршрута нет")
}

func (f *fakeRoutes) RoutesInTable(table int) ([]netlink.Route, error) {
	var out []netlink.Route
	for _, rt := range f.routes {
		if rt.Table == table {
			out = append(out, rt)
		}
	}
	return out, nil
}

func (f *fakeRoutes) LinkIndex(name string) (int, error) {
	if f.linkErr != nil {
		return 0, f.linkErr
	}
	idx, ok := f.links[name]
	if !ok {
		return 0, errors.New("нет такого интерфейса")
	}
	return idx, nil
}

func (f *fakeRoutes) DefaultRoutes() ([]netlink.Route, error) { return f.defaults, nil }

func (f *fakeRoutes) LinkName(index int) (string, error) {
	name, ok := f.names[index]
	if !ok {
		return "", errors.New("нет такого интерфейса")
	}
	return name, nil
}

func TestRouteApply(t *testing.T) {
	f := newFakeRoutes()
	r := &Route{h: f}

	if err := r.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(f.rules) != 1 {
		t.Fatalf("правил %d, ожидалось одно", len(f.rules))
	}
	rule := f.rules[0]
	if rule.Mark != FwMark || rule.Mask == nil || *rule.Mask != FwMark {
		t.Errorf("правило = %+v, ожидалась метка %#x с той же маской", rule, FwMark)
	}
	if rule.Table != RouteTable || rule.Priority != RulePriority {
		t.Errorf("правило = %+v, ожидались таблица %d и приоритет %d", rule, RouteTable, RulePriority)
	}

	if len(f.routes) != 1 {
		t.Fatalf("маршрутов %d, ожидался один", len(f.routes))
	}
	route := f.routes[0]
	if route.Table != RouteTable || route.Type != rtnLocal || route.LinkIndex != 1 {
		t.Errorf("маршрут = %+v, ожидался local 0.0.0.0/0 dev lo table 105", route)
	}
	if ones, _ := route.Dst.Mask.Size(); ones != 0 {
		t.Errorf("маршрут не по умолчанию: %v", route.Dst)
	}
}

// Перезаливка правил идёт на каждое изменение конфигурации, а дубли в ip rule
// накапливаются молча — повторный вызов не должен добавлять ничего.
func TestRouteApplyIdempotent(t *testing.T) {
	f := newFakeRoutes()
	r := &Route{h: f}

	for i := 0; i < 3; i++ {
		if err := r.Apply(); err != nil {
			t.Fatalf("Apply #%d: %v", i, err)
		}
	}

	if len(f.rules) != 1 || len(f.routes) != 1 {
		t.Errorf("после трёх вызовов правил %d, маршрутов %d — ожидалось по одному",
			len(f.rules), len(f.routes))
	}
}

// Чтение состояния различает три случая: всё на месте, снято правило, снята
// таблица. Диагностике мало «работает / не работает»: снимают их порознь
// (issue #120), и назвать пользователю нужно то, чего действительно нет.
func TestRouteDiagState(t *testing.T) {
	f := newFakeRoutes()
	r := &Route{h: f}

	st, err := r.DiagState()
	if err != nil {
		t.Fatalf("DiagState до заливки: %v", err)
	}
	if st.Rule || st.LocalRoute {
		t.Errorf("на нетронутой системе состояние = %+v, ожидалось пустое", st)
	}
	if st.Table != RouteTable || st.Mark != FwMark {
		t.Errorf("состояние = %+v, ожидались таблица %d и метка %#x", st, RouteTable, FwMark)
	}

	if err := r.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	st, err = r.DiagState()
	if err != nil {
		t.Fatalf("DiagState после заливки: %v", err)
	}
	if !st.Rule || !st.LocalRoute || st.RulePriority != RulePriority || st.Routes != 1 {
		t.Errorf("после заливки состояние = %+v, ожидались правило и маршрут", st)
	}

	// Правило снято чужим `ip rule flush`, маршрут остался.
	f.rules = nil
	st, err = r.DiagState()
	if err != nil {
		t.Fatalf("DiagState без правила: %v", err)
	}
	if st.Rule || !st.LocalRoute {
		t.Errorf("без правила состояние = %+v, ожидалось Rule=false при живом маршруте", st)
	}

	// Таблица очищена, правило осталось.
	if err := r.Apply(); err != nil {
		t.Fatalf("Apply после снятия правила: %v", err)
	}
	f.routes = nil
	st, err = r.DiagState()
	if err != nil {
		t.Fatalf("DiagState без маршрута: %v", err)
	}
	if !st.Rule || st.LocalRoute {
		t.Errorf("без маршрута состояние = %+v, ожидалось LocalRoute=false при живом правиле", st)
	}
}

// Снятие возвращает систему в исходное состояние: ни правила, ни таблицы 105.
func TestRouteClear(t *testing.T) {
	f := newFakeRoutes()
	r := &Route{h: f}

	if err := r.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := r.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(f.rules) != 0 || len(f.routes) != 0 {
		t.Errorf("после снятия остались правила %v и маршруты %v", f.rules, f.routes)
	}

	// Снятие на нетронутой системе — не ошибка: --reset-network зовут и там,
	// где демон не работал.
	if err := r.Clear(); err != nil {
		t.Errorf("повторное снятие: %v", err)
	}
}

// Чужие правила маршрутизации снятие не трогает.
func TestRouteClearKeepsForeignRules(t *testing.T) {
	f := newFakeRoutes()
	foreign := netlink.NewRule()
	foreign.Table = 200
	foreign.Priority = 32000
	f.rules = append(f.rules, *foreign)

	r := &Route{h: f}
	if err := r.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if err := r.Clear(); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if len(f.rules) != 1 || f.rules[0].Table != 200 {
		t.Errorf("чужие правила изменились: %v", f.rules)
	}
}

func TestDetectWAN(t *testing.T) {
	f := newFakeRoutes()
	f.defaults = []netlink.Route{
		{LinkIndex: 1, Dst: &net.IPNet{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)}},
		{LinkIndex: 2, Priority: 100},
		{LinkIndex: 3, Priority: 600},
	}
	f.names[3] = "wwan0"

	r := &Route{h: f}
	got, err := r.DetectWAN()
	if err != nil {
		t.Fatalf("DetectWAN: %v", err)
	}
	if got != "eth0" {
		t.Errorf("WAN = %q, ожидался eth0 (меньшая метрика)", got)
	}

	f.defaults = nil
	if _, err := r.DetectWAN(); !errors.Is(err, ErrRouteNoDefault) {
		t.Errorf("без маршрута по умолчанию ошибка = %v, ожидалось ErrRouteNoDefault", err)
	}
}

func TestRouteApplyNoLoopback(t *testing.T) {
	f := newFakeRoutes()
	f.linkErr = errors.New("нет lo")
	r := &Route{h: f}

	if err := r.Apply(); !errors.Is(err, ErrRouteNoLoopback) {
		t.Errorf("ошибка = %v, ожидалось ErrRouteNoLoopback", err)
	}
}
