//go:build linux

package dnsruntime

import (
	"os"
	"testing"

	"github.com/vishvananda/netlink"
)

func realityNamespace(t *testing.T) {
	t.Helper()
	parent := os.Getenv("RAZDACHA_REALITY_PARENT_NETNS")
	if parent == "" {
		return // Ordinary local loopback-only execution needs no network mutation.
	}
	current, err := os.Readlink("/proc/self/ns/net")
	realityMust(t, err)
	if current == parent {
		t.Fatal("refusing loopback setup in the caller's network namespace")
	}
	links, err := netlink.LinkList()
	realityMust(t, err)
	if len(links) != 1 || links[0].Attrs().Name != "lo" {
		t.Fatal("expected a fresh network namespace containing only loopback")
	}
	realityMust(t, netlink.LinkSetUp(links[0]))
}
