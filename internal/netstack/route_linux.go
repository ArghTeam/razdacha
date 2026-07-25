//go:build linux

package netstack

import (
	"fmt"

	"github.com/vishvananda/netlink"
)

// NewRoute открывает работу с маршрутизацией через netlink.
func NewRoute() *Route { return &Route{h: netlinkRoutes{}} }

// netlinkRoutes — настоящая реализация routeHandle. Тонкая: вся логика выше,
// здесь только вызовы netlink, которые компилируются лишь под linux.
type netlinkRoutes struct{}

func (netlinkRoutes) RuleAdd(rule *netlink.Rule) error { return netlink.RuleAdd(rule) }

func (netlinkRoutes) RuleDel(rule *netlink.Rule) error { return netlink.RuleDel(rule) }

func (netlinkRoutes) Rules() ([]netlink.Rule, error) { return netlink.RuleList(netlink.FAMILY_V4) }

func (netlinkRoutes) RouteAdd(route *netlink.Route) error { return netlink.RouteAdd(route) }

func (netlinkRoutes) RouteDel(route *netlink.Route) error { return netlink.RouteDel(route) }

func (netlinkRoutes) RoutesInTable(table int) ([]netlink.Route, error) {
	return netlink.RouteListFiltered(netlink.FAMILY_V4,
		&netlink.Route{Table: table}, netlink.RT_FILTER_TABLE)
}

func (netlinkRoutes) LinkIndex(name string) (int, error) {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return 0, err
	}
	return link.Attrs().Index, nil
}

func (netlinkRoutes) DefaultRoutes() ([]netlink.Route, error) {
	return netlink.RouteList(nil, netlink.FAMILY_V4)
}

func (netlinkRoutes) LinkName(index int) (string, error) {
	link, err := netlink.LinkByIndex(index)
	if err != nil {
		return "", fmt.Errorf("интерфейс %d: %w", index, err)
	}
	return link.Attrs().Name, nil
}
