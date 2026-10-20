package singbox

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRestoreClassificationIncludesEveryEffectiveWireGuard(t *testing.T) {
	tunnels := []store.Tunnel{
		{ID: "ordinary", Type: store.TunnelWireGuard, Source: store.SourceWGConf, Parsed: json.RawMessage(`{"private_key":"fixture-only","persistent_keepalive_interval":25}`), Enabled: true},
		{ID: "warp", Type: store.TunnelWireGuard, Source: store.SourceWARP, Parsed: json.RawMessage(`{}`), Enabled: true},
		{ID: "raw", Type: store.TunnelRaw, Source: store.SourceJSON, Parsed: json.RawMessage(`{"type":"wireguard","private_key":"fixture-only"}`), Enabled: true},
		{ID: "disabled", Type: store.TunnelWireGuard, Source: store.SourceJSON, Parsed: json.RawMessage(`{}`), Enabled: false},
		{ID: "nonwg", Type: store.TunnelVLESS, Source: store.SourceURL, Parsed: json.RawMessage(`{"type":"wireguard"}`), Enabled: true},
		{ID: "pool", Type: store.TunnelVLESS, Source: store.SourcePool, Enabled: true},
	}
	before, _ := json.Marshal(tunnels)
	ids, e := RestoreWireGuardIDs(tunnels)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(ids, []string{"ordinary", "warp", "raw", "disabled"}) {
		t.Fatal(ids)
	}
	after, _ := json.Marshal(tunnels)
	if string(before) != string(after) {
		t.Fatal("classification modified credential bytes or intent")
	}
}

func TestRestoreClassificationRejectsAmbiguousDisabledMetadata(t *testing.T) {
	for _, raw := range []string{`{"type":"vless","type":"wireguard"}`, `{"type":"wireguard","peers":[{"private_key":"a","private_key":"b"}]}`, `null`, `[]`, `{}`, `{"type":null}`, `{"type":""}`, `{"type":"wireguard"} {}`} {
		t.Run(raw, func(t *testing.T) {
			_, e := RestoreWireGuardIDs([]store.Tunnel{{ID: "disabled", Type: store.TunnelRaw, Source: store.SourceJSON, Parsed: json.RawMessage(raw)}})
			if e == nil {
				t.Fatal("ambiguous disabled raw metadata admitted")
			}
		})
	}
	_, e := RestoreWireGuardIDs([]store.Tunnel{{ID: "badwarp", Type: store.TunnelVLESS, Source: store.SourceWARP, Parsed: json.RawMessage(`{}`)}})
	if e == nil {
		t.Fatal("non-WG WARP admitted")
	}
	_, e = RestoreWireGuardIDs([]store.Tunnel{{ID: "badpool", Type: store.TunnelVLESS, Source: store.SourcePool, Pool: []store.PoolServer{{URL: "wireguard://fixture.invalid"}}}})
	if e == nil {
		t.Fatal("unsupported pool member admitted")
	}
}
