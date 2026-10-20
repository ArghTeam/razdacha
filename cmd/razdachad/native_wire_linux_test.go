//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	SJSON "github.com/sagernet/sing/common/json"
)

// These shared fixture files are also overlaid on the unmodified baseline.
// The header identifies only our daemon HTTP client; sing-box has its own client.
type nativeWireHTTP struct{}

func (nativeWireHTTP) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("X-Synthetic-Consumer", "daemon")
	return http.DefaultClient.Do(req)
}

type nativeWireFixture struct {
	h                                            *wireHarness
	dir, format, skew                            string
	phase                                        atomic.Int32
	runtimeRequests                              atomic.Int64
	runtimeNew, runtimeOld, daemonNew, daemonOld atomic.Int64
}

func newNativeWire(t *testing.T) *nativeWireFixture {
	t.Helper()
	n := &nativeWireFixture{h: newWireHarness(t), dir: t.TempDir(), format: os.Getenv("RAZDACHA_WIRE_NATIVE_FORMAT"), skew: os.Getenv("RAZDACHA_WIRE_NATIVE_CASE")}
	if (n.format != "json" && n.format != "srs") || (n.skew != "daemon-new-runtime-old" && n.skew != "runtime-new-daemon-old") {
		t.Fatal("invalid synthetic native wire case")
	}
	// Prepare trusted fixture bytes before starting handlers (no testing.T in HTTP goroutines).
	encode := func(new bool) []byte {
		extra := ""
		if new {
			extra = `,{"domain":["blocked.example"],"ip_cidr":["203.0.113.10/32"]}`
		}
		body := []byte(`{"version":3,"rules":[{"domain":["old.example"],"ip_cidr":["203.0.113.30/32"]}` + extra + `]}`)
		if n.format == "srs" {
			var compat option.PlainRuleSetCompat
			wireMust(t, SJSON.Unmarshal(body, &compat))
			var b bytes.Buffer
			wireMust(t, srs.Write(&b, compat.Options, 3))
			body = b.Bytes()
		}
		return body
	}
	old, newBody := encode(false), encode(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		daemon := r.Header.Get("X-Synthetic-Consumer") == "daemon"
		if !daemon {
			n.runtimeRequests.Add(1)
		}
		newVersion := false
		if n.phase.Load() > 0 {
			newVersion = (n.skew == "daemon-new-runtime-old" && daemon) || (n.skew == "runtime-new-daemon-old" && !daemon) || n.phase.Load() > 1
		}
		if newVersion {
			if daemon {
				n.daemonNew.Add(1)
			} else {
				n.runtimeNew.Add(1)
			}
			_, _ = w.Write(newBody)
		} else {
			if daemon {
				n.daemonOld.Add(1)
			} else {
				n.runtimeOld.Add(1)
			}
			_, _ = w.Write(old)
		}
	}))
	t.Cleanup(srv.Close)
	n.h.url = srv.URL + "/native." + n.format
	fetcher, err := lists.NewFetcher(lists.Options{Dir: filepath.Join(n.dir, "lists"), Client: nativeWireHTTP{}, Logger: quietSlog()})
	wireMust(t, err)
	n.h.mgr = lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: quietSlog()})
	n.h.mgr.SetSources([]lists.Source{{URL: n.h.url, Format: lists.Format(n.format)}})
	wireMust(t, n.h.mgr.Refresh(n.h.ctx))
	return n
}

func (n *nativeWireFixture) runtime(t *testing.T) (*wireReload, *singbox.Applier, store.Snapshot) {
	t.Helper()
	r := &wireReload{bin: os.Getenv("RAZDACHA_WIRE_BIN"), path: filepath.Join(n.dir, "config.json"), entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(r.stop)
	a := &singbox.Applier{ConfigPath: r.path, Checker: singbox.BinaryChecker{Binary: r.bin}, Reloader: r, Log: quietSlog()}
	snap := store.Snapshot{Settings: n.h.settings, Rules: []store.Rule{{ID: "native", Name: "Synthetic native policy", Enabled: true, Action: store.ActionBlock, PeerScope: store.ScopeAll, RemoteLists: []string{n.h.url}}}}
	return r, a, snap
}

func (n *nativeWireFixture) nft(ctx context.Context, snap store.Snapshot, content map[string]lists.List) (int, error) {
	nf, err := netstack.NewNft()
	if err != nil {
		return 0, err
	}
	sub := generationSubnets(snap, content)
	result, err := nf.Apply(netstack.NftConfig{WANInterface: "wan0", ResolverAddr: snap.Settings.WGServerAddress, Subnets: sub, TransportSubnets: generationTransportSubnets(snap, content)})
	if err == nil && result.SkippedSubnets != 0 {
		err = fmt.Errorf("invalid synthetic native subnets")
	}
	return len(sub), err
}

func (n *nativeWireFixture) preconditions(t *testing.T) {
	t.Helper()
	for _, proto := range []string{"tcp4", "udp4"} {
		n.h.probe(t, proto, "203.0.113.10:12080", "native-precondition-"+proto, true)
		n.h.probe(t, proto, "203.0.113.30:12080", "blocked-final-native-old-"+proto, false)
		n.h.probe(t, proto, "203.0.113.20:12080", "native-control-"+proto, true)
	}
	n.h.assertNoForbidden(t)
}
