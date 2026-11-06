//go:build linux

package main

import (
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/vishvananda/netns"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Reuse only the disposable encrypted quality topology. Unlike its two-source
// adapter, this bounded case uses a real stored client key and the production
// Up/live-peer-diff publication callback. No installed key or device is imported.
func wgAdmissionWireExercise(t *testing.T) {
	qualityRequireIsolated(t)
	if !wireQualityEnabled() {
		t.Fatal("WG admission requires the encrypted quality opt-in")
	}
	wireMust(t, os.Setenv("RAZDACHA_WIRE_PEER_CASE", "grant"))
	p := newPeerWire(t)
	control, err := wgctrl.New()
	wireMust(t, err)
	defer control.Close()
	dev, err := control.Device("wg0")
	wireMust(t, err)
	if dev.Type != wgtypes.LinuxKernel {
		t.Fatal("refusing non-kernel WG device")
	}
	wireMust(t, p.st.SetServerPrivateKey(p.h.ctx, dev.PrivateKey.String()))
	settings, err := p.st.Settings(p.h.ctx)
	wireMust(t, err)
	settings.WGListenPort = dev.ListenPort
	wireMust(t, p.st.SaveSettings(p.h.ctx, settings))
	// Replace the throwaway topology client key with this fixture's DB identity.
	// The endpoint and server key stay unchanged; this is not a migration test.
	wgAdmissionClientKey(t, p.h.client, p.a, dev.PublicKey)
	p.a.EgressTunnelID = &p.rule.TunnelID
	wireMust(t, p.st.UpdatePeer(p.h.ctx, p.a))
	p.gate.network.wg = func(ctx context.Context, s store.Settings) error {
		qualityRequireIsolated(t)
		return configureLiveWG(ctx, p.st, s)
	}
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.10:12080", "private", "admission-before")
	}
	before := wgAdmissionPeer(t, control, p.a.PublicKey)
	if before.LastHandshakeTime.IsZero() || before.ReceiveBytes == 0 || before.TransmitBytes == 0 {
		t.Fatal("missing encrypted admission precondition")
	}
	// A healthy periodic no-op must preserve the established kernel handshake.
	manager, err := startWG(p.h.ctx, p.st, settings)
	wireMust(t, err)
	defer manager.Close()
	wireMust(t, p.gate.syncWGPeers(p.h.ctx, p.st, manager))
	after := wgAdmissionPeer(t, control, p.a.PublicKey)
	if !before.LastHandshakeTime.Equal(after.LastHandshakeTime) {
		t.Fatal("unchanged periodic reconciliation reset handshake")
	}
	_, err = p.gate.DeletePeer(p.h.ctx, p.st, p.a.ID)
	wireMust(t, err)
	// Pending deletion still has its old source policy and admitted key.
	for _, proto := range []string{"tcp4", "udp4"} {
		p.probe(t, p.a.Address, proto, "203.0.113.10:12080", "private", "admission-pending")
	}
	guard := p.gate.network.guard
	p.gate.network.guard = func(on bool) error {
		if !on {
			wgAdmissionAbsent(t, control, p.a.PublicKey)
		}
		return guard(on)
	}
	_, err = p.gate.ApplyCurrent(p.h.ctx, p.st)
	wireMust(t, err)
	wgAdmissionAbsent(t, control, p.a.PublicKey)
	for _, target := range []string{"203.0.113.10:12080", "203.0.113.20:12080"} {
		for _, proto := range []string{"tcp4", "udp4"} {
			p.probe(t, p.a.Address, proto, target, "deny", "admission-revoked")
		}
	}
	wireMust(t, p.gate.syncWGPeers(p.h.ctx, p.st, manager))
	wgAdmissionAbsent(t, control, p.a.PublicKey)
	// A separate gateway socket proves sink liveness after the negative probes;
	// forbidden nonce accounting independently rejects direct leaked payloads.
	for _, proto := range []string{"tcp4", "udp4"} {
		conn, err := net.DialTimeout(proto, "203.0.113.20:12080", time.Second)
		wireMust(t, err)
		wireMust(t, conn.SetDeadline(time.Now().Add(time.Second)))
		body := []byte("wg325-positive-control\n")
		_, err = conn.Write(body)
		wireMust(t, err)
		reply := make([]byte, len(body))
		_, err = io.ReadFull(conn, reply)
		wireMust(t, err)
		wireMust(t, conn.Close())
		if string(reply) != string(body) {
			t.Fatal("sink control mismatch")
		}
	}
	p.h.captureBarrier(t)
	p.h.assertNoForbidden(t)
	t.Log("325-ENCRYPTED-WG-ADMISSION-PASS live-key removal before release; old-key TCP/UDP denied; sink controls passed; no-op handshake retained")
}

func wgAdmissionClientKey(t *testing.T, ns netns.NsHandle, peer store.Peer, server wgtypes.Key) {
	t.Helper()
	qualityRequireIsolated(t)
	key, err := wgtypes.ParseKey(peer.PrivateKey)
	wireMust(t, err)
	psk, err := wgtypes.ParseKey(peer.PresharedKey)
	wireMust(t, err)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	previous, err := netns.Get()
	wireMust(t, err)
	defer previous.Close()
	wireMust(t, netns.Set(ns))
	defer func() { wireMust(t, netns.Set(previous)) }()
	control, err := wgctrl.New()
	wireMust(t, err)
	defer control.Close()
	dev, err := control.Device("client0")
	wireMust(t, err)
	if dev.Type != wgtypes.LinuxKernel {
		t.Fatal("refusing non-kernel fixture client")
	}
	wireMust(t, control.ConfigureDevice("client0", wgtypes.Config{PrivateKey: &key, Peers: []wgtypes.PeerConfig{{PublicKey: server, PresharedKey: &psk, UpdateOnly: true}}}))
}

func wgAdmissionPeer(t *testing.T, control *wgctrl.Client, key string) wgtypes.Peer {
	t.Helper()
	dev, err := control.Device("wg0")
	wireMust(t, err)
	for _, peer := range dev.Peers {
		if peer.PublicKey.String() == key {
			return peer
		}
	}
	t.Fatal("expected live fixture peer missing")
	return wgtypes.Peer{}
}

func wgAdmissionAbsent(t *testing.T, control *wgctrl.Client, key string) {
	t.Helper()
	dev, err := control.Device("wg0")
	wireMust(t, err)
	for _, peer := range dev.Peers {
		if peer.PublicKey.String() == key {
			t.Fatal("revoked kernel key still admitted")
		}
	}
}
