package dnsruntime

import (
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/group"
	"github.com/sagernet/sing/service"
)

type singletonParent struct{ adapter.Outbound }

func (singletonParent) Tag() string { return "parent" }

type singletonManager struct{ adapter.OutboundManager }

func (singletonManager) Outbound(tag string) (adapter.Outbound, bool) {
	return singletonParent{}, tag == "parent"
}

type singletonCache struct {
	adapter.CacheFile
	selected string
	loads    int
}

func (c *singletonCache) LoadSelected(string) string { c.loads++; return c.selected }

// Exercise the pinned runtime itself: stale selector cache must never add an
// alternate exit to the generator's singleton parent alias.
func TestSingletonSelectorRejectsStaleCachedExit(t *testing.T) {
	for _, cached := range []string{"", "parent", "direct-out", "deleted-parent"} {
		t.Run(cached, func(t *testing.T) {
			ctx := service.ContextWith[adapter.OutboundManager](context.Background(), singletonManager{})
			cache := &singletonCache{selected: cached}
			ctx = service.ContextWith[adapter.CacheFile](ctx, cache)
			factory, err := log.New(log.Options{Context: ctx, Options: option.LogOptions{Level: "error"}, DefaultWriter: io.Discard})
			if err != nil {
				t.Fatal(err)
			}
			outbound, err := group.NewSelector(ctx, nil, factory.NewLogger("singleton"), "alias", option.SelectorOutboundOptions{
				Outbounds: []string{"parent"}, Default: "parent", InterruptExistConnections: false,
			})
			if err != nil {
				t.Fatal(err)
			}
			selector := outbound.(*group.Selector)
			if err := selector.Start(); err != nil {
				t.Fatal(err)
			}
			if cache.loads != 1 {
				t.Fatal("runtime did not consult persisted selection")
			}
			if selector.Now() != "parent" || !reflect.DeepEqual(selector.All(), []string{"parent"}) {
				t.Fatal("cached state changed the fixed parent")
			}
			if selector.SelectOutbound("direct-out") || selector.SelectOutbound("deleted-parent") {
				t.Fatal("selector accepted an alternate exit")
			}
		})
	}
}
