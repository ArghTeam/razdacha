package netstack

import (
	"errors"
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

// Ошибки маршрутизации. Проверяются через errors.Is; тексты доходят до UI.
var (
	// ErrRouteNoDefault — в системе нет маршрута по умолчанию, определить
	// внешний интерфейс не из чего.
	ErrRouteNoDefault = errors.New("не найден маршрут по умолчанию")
	// ErrRouteNoLoopback — нет интерфейса lo, некуда направить local-маршрут.
	ErrRouteNoLoopback = errors.New("не найден интерфейс lo")
)

// Параметры policy routing из docs/03-networking.md. Таблица и приоритет
// совпадают с Podkop (bin/podkop:253-268), но имя таблицы в
// /etc/iproute2/rt_tables не пишется: через netlink нужен только номер, а
// правка чужого файла — след, который потом приходится убирать.
const (
	// RouteTable — номер таблицы маршрутизации помеченного трафика.
	RouteTable = 105
	// RulePriority — приоритет правила ip rule.
	RulePriority = 105
	// LoopbackName — интерфейс, на который смотрит local-маршрут.
	LoopbackName = "lo"

	// familyV4 — AF_INET. Константы netlink.FAMILY_V4 и RT_FILTER_* объявлены
	// только для linux, а разбор набора должен собираться везде.
	familyV4 = 2
	// rtnLocal — RTN_LOCAL: адрес считается локальным, условие работы tproxy.
	rtnLocal = 2
	// scopeHost — RT_SCOPE_HOST.
	scopeHost = 254
)

// routeHandle — то, что слой берёт от netlink. Интерфейс существует ради
// тестов: подменённая реализация проверяет состав правила и маршрута,
// идемпотентность и полноту снятия без root.
type routeHandle interface {
	RuleAdd(rule *netlink.Rule) error
	RuleDel(rule *netlink.Rule) error
	Rules() ([]netlink.Rule, error)
	RouteAdd(route *netlink.Route) error
	RouteDel(route *netlink.Route) error
	RoutesInTable(table int) ([]netlink.Route, error)
	LinkIndex(name string) (int, error)
	DefaultRoutes() ([]netlink.Route, error)
	LinkName(index int) (string, error)
}

// Route владеет правилом ip rule и таблицей 105.
type Route struct {
	h routeHandle
}

// MarkRule — правило «помеченное идёт в таблицу 105». Чистая сборка: то же
// значение маски, что ставит nft, и тот же приоритет, что снимается в Clear.
func MarkRule() *netlink.Rule {
	mask := FwMark
	r := netlink.NewRule()
	r.Family = familyV4
	r.Mark = FwMark
	r.Mask = &mask
	r.Table = RouteTable
	r.Priority = RulePriority
	return r
}

// LocalRoute — маршрут «local 0.0.0.0/0 dev lo table 105». Без него ядро
// отбрасывает помеченный пакет как чужой и tproxy не срабатывает.
func LocalRoute(loIndex int) *netlink.Route {
	return &netlink.Route{
		LinkIndex: loIndex,
		Table:     RouteTable,
		Dst:       &net.IPNet{IP: net.IPv4zero, Mask: net.CIDRMask(0, 32)},
		Type:      rtnLocal,
		Scope:     scopeHost,
	}
}

// Apply создаёт правило и маршрут. Идемпотентно: повторный вызов ничего не
// добавляет — перезаливка nft-правил происходит на каждое изменение
// конфигурации, а дубли в ip rule накапливаются молча.
func (r *Route) Apply() error {
	loIndex, err := r.h.LinkIndex(LoopbackName)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrRouteNoLoopback, err)
	}

	routes, err := r.h.RoutesInTable(RouteTable)
	if err != nil {
		return fmt.Errorf("список маршрутов таблицы %d: %w", RouteTable, err)
	}
	if !hasLocalRoute(routes, loIndex) {
		if err := r.h.RouteAdd(LocalRoute(loIndex)); err != nil {
			return fmt.Errorf("маршрут local в таблице %d: %w", RouteTable, err)
		}
	}

	rules, err := r.h.Rules()
	if err != nil {
		return fmt.Errorf("список правил маршрутизации: %w", err)
	}
	if !hasMarkRule(rules) {
		if err := r.h.RuleAdd(MarkRule()); err != nil {
			return fmt.Errorf("правило маршрутизации по метке: %w", err)
		}
	}
	return nil
}

// DiagRouteState — маршрутизация помеченного трафика глазами диагностики.
//
// Два признака, а не один: правило и local-маршрут заводятся порознь и порознь
// же пропадают (чужой `ip rule flush`, `ip route flush table 105`), а диагнозы
// у них разные — и оба нужны, чтобы tproxy работал.
type DiagRouteState struct {
	// Table — номер таблицы, состояние которой прочитано.
	Table int
	// Mark — метка, по которой ищется правило.
	Mark uint32
	// Rule — правило «помеченное идёт в таблицу» на месте.
	Rule bool
	// RulePriority — приоритет найденного правила. Осмыслен только при Rule.
	RulePriority int
	// LocalRoute — маршрут `local 0.0.0.0/0 dev lo` в таблице на месте.
	LocalRoute bool
	// Routes — сколько всего маршрутов лежит в таблице.
	Routes int
}

// DiagState читает состояние правила и таблицы 105, ничего не меняя.
//
// Логика на общем [routeHandle], а не на netlink напрямую: чтение проверяется
// тем же подменённым набором, что и заливка, — без root и не под Linux.
func (r *Route) DiagState() (DiagRouteState, error) {
	out := DiagRouteState{Table: RouteTable, Mark: FwMark}

	rules, err := r.h.Rules()
	if err != nil {
		return DiagRouteState{}, fmt.Errorf("список правил маршрутизации: %w", err)
	}
	for _, rule := range rules {
		if !isMarkRule(rule) {
			continue
		}
		out.Rule = true
		out.RulePriority = rule.Priority
		break
	}

	loIndex, err := r.h.LinkIndex(LoopbackName)
	if err != nil {
		return DiagRouteState{}, fmt.Errorf("%w: %w", ErrRouteNoLoopback, err)
	}
	routes, err := r.h.RoutesInTable(RouteTable)
	if err != nil {
		return DiagRouteState{}, fmt.Errorf("список маршрутов таблицы %d: %w", RouteTable, err)
	}
	out.Routes = len(routes)
	out.LocalRoute = hasLocalRoute(routes, loIndex)
	return out, nil
}

// Clear снимает правило и всё содержимое таблицы 105. Отсутствие того и
// другого ошибкой не считается: снятие вызывается и при остановке демона,
// и по --reset-network на системе, где демон уже не работал.
func (r *Route) Clear() error {
	var errs []error

	rules, err := r.h.Rules()
	if err != nil {
		errs = append(errs, fmt.Errorf("список правил маршрутизации: %w", err))
	}
	for i := range rules {
		if !isMarkRule(rules[i]) {
			continue
		}
		rule := rules[i]
		if err := r.h.RuleDel(&rule); err != nil {
			errs = append(errs, fmt.Errorf("снятие правила маршрутизации: %w", err))
		}
	}

	routes, err := r.h.RoutesInTable(RouteTable)
	if err != nil {
		errs = append(errs, fmt.Errorf("список маршрутов таблицы %d: %w", RouteTable, err))
	}
	for i := range routes {
		route := routes[i]
		if err := r.h.RouteDel(&route); err != nil {
			errs = append(errs, fmt.Errorf("снятие маршрута таблицы %d: %w", RouteTable, err))
		}
	}

	return errors.Join(errs...)
}

// DetectWAN определяет внешний интерфейс по маршруту по умолчанию. Задавать
// его руками пользователю не нужно: на VPS он один и известен ядру.
func (r *Route) DetectWAN() (string, error) {
	routes, err := r.h.DefaultRoutes()
	if err != nil {
		return "", fmt.Errorf("список маршрутов по умолчанию: %w", err)
	}
	idx, err := pickDefaultRoute(routes)
	if err != nil {
		return "", err
	}
	name, err := r.h.LinkName(idx)
	if err != nil {
		return "", fmt.Errorf("имя интерфейса %d: %w", idx, err)
	}
	return name, nil
}

// pickDefaultRoute выбирает индекс интерфейса маршрута по умолчанию: из
// нескольких берётся маршрут с наименьшей метрикой, как это делает ядро.
// Чистая функция — разбор проверяется тестом без netlink.
func pickDefaultRoute(routes []netlink.Route) (int, error) {
	best := -1
	bestPriority := 0
	for _, rt := range routes {
		if !isDefault(rt) || rt.LinkIndex == 0 {
			continue
		}
		if best == -1 || rt.Priority < bestPriority {
			best, bestPriority = rt.LinkIndex, rt.Priority
		}
	}
	if best == -1 {
		return 0, ErrRouteNoDefault
	}
	return best, nil
}

// isDefault — маршрут по умолчанию: без Dst либо с нулевой длиной префикса.
func isDefault(rt netlink.Route) bool {
	if rt.Dst == nil {
		return true
	}
	ones, _ := rt.Dst.Mask.Size()
	return ones == 0
}

// hasLocalRoute — local-маршрут в таблице уже есть.
func hasLocalRoute(routes []netlink.Route, loIndex int) bool {
	for _, rt := range routes {
		if rt.Type == rtnLocal && rt.LinkIndex == loIndex && isDefault(rt) {
			return true
		}
	}
	return false
}

// hasMarkRule — правило по метке уже есть.
func hasMarkRule(rules []netlink.Rule) bool {
	for _, rule := range rules {
		if isMarkRule(rule) {
			return true
		}
	}
	return false
}

// isMarkRule — это наше правило: та же метка и та же таблица. Приоритет не
// сверяется: правило, заведённое руками с другим приоритетом, всё равно наше
// по смыслу, и при снятии его нужно убрать.
func isMarkRule(rule netlink.Rule) bool {
	return rule.Table == RouteTable && rule.Mark == FwMark
}
