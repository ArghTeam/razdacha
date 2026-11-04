package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/ingressguard"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

const ingressManifestFile = "public-ingress.json"

var errPublicPortBusy = errors.New("public ingress port is occupied")

func (g *applyGate) configPath() string {
	if g.inner.ConfigPath != "" {
		return g.inner.ConfigPath
	}
	return singbox.DefaultConfigPath
}

func (g *applyGate) manifestPath() string {
	return filepath.Join(filepath.Dir(g.network.path), ingressManifestFile)
}

// stagePublic approves only the checked candidate and current disk image. The
// cross-process lock is short and is never held across a service restart.
func (g *applyGate) stagePublic(candidate []byte) error {
	return ingressguard.WithLock(g.manifestPath(), func() error {
		m, err := ingressguard.Read(g.manifestPath())
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		endpoints, err := ingressguard.ConfigEndpoints(candidate)
		if err != nil {
			return err
		}
		hashes := []string{ingressguard.HashConfig(candidate)}
		currentEndpoints := []ingressguard.Endpoint{}
		if old, err := os.ReadFile(g.configPath()); err == nil {
			oldEndpoints, e := ingressguard.ConfigEndpoints(old)
			if e != nil {
				return e
			}
			currentEndpoints = oldEndpoints
			endpoints, err = ingressguard.Union(endpoints, oldEndpoints)
			if err != nil {
				return err
			}
			h := ingressguard.HashConfig(old)
			if h != hashes[0] {
				hashes = append(hashes, h)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		for _, endpoint := range endpoints {
			managed := false
			for _, current := range currentEndpoints {
				if current == endpoint {
					managed = true
				}
			}
			if !managed {
				check := g.network.checkPublicPort
				if check == nil {
					check = probePublicPort
				}
				if err := check(endpoint); err != nil {
					return fmt.Errorf("%w: %w", errPublicPortBusy, err)
				}
			}
		}
		endpoints, err = ingressguard.Union(m.Endpoints, endpoints)
		if err != nil {
			return err
		}
		next := ingressguard.Manifest{Version: ingressguard.Version, Endpoints: endpoints, ConfigHashes: hashes, Guarded: true}
		if err := ingressguard.Write(g.manifestPath(), next); err != nil {
			return err
		}
		return g.publicGuard(endpoints, true)
	})
}

func (g *applyGate) publicGuard(e []ingressguard.Endpoint, on bool) error {
	if g.network.publicGuard != nil {
		return g.network.publicGuard(e, on)
	}
	return netstack.SetPublicClientGuard(e, on)
}

func (g *applyGate) releasePublic(candidate []byte) error {
	return ingressguard.WithLock(g.manifestPath(), func() error {
		e, err := ingressguard.ConfigEndpoints(candidate)
		if err != nil {
			return err
		}
		m := ingressguard.Manifest{Version: ingressguard.Version, Endpoints: e, ConfigHashes: []string{ingressguard.HashConfig(candidate)}, Guarded: true}
		if err := ingressguard.Write(g.manifestPath(), m); err != nil {
			return err
		}
		if err := g.publicGuard(e, false); err != nil {
			return err
		}
		m.Guarded = false
		if err := ingressguard.Write(g.manifestPath(), m); err != nil {
			return errors.Join(err, g.publicGuard(e, true))
		}
		return nil
	})
}

func (g *applyGate) publicGuarded() bool {
	m, err := ingressguard.Read(g.manifestPath())
	return err != nil || m.Guarded
}

// closeForRevocation leaves durable guard intent before touching live secrets.
func (g *applyGate) closeForRevocation() error {
	b, err := os.ReadFile(g.configPath())
	if err != nil {
		return err
	}
	if err := g.stagePublic(b); err != nil {
		return err
	}
	g.network.uncertain = true
	return g.network.guard(true)
}

func (g *applyGate) reconcileRevocation(ctx context.Context) error {
	_, err := g.publish(ctx, g.base, true, false)
	if err != nil {
		return fmt.Errorf("%w: authentication revoked; ingress remains guarded: %w", api.ErrNetworkGuarded, err)
	}
	return nil
}

func (g *applyGate) PatchPeer(ctx context.Context, st *store.Store, id string, patch store.PeerPatch) (store.Peer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	old, err := st.Peer(ctx, id)
	if err != nil {
		return store.Peer{}, err
	}
	revoke := old.RealityEnabled && old.Enabled && ((patch.Enabled != nil && !*patch.Enabled) || (patch.RealityEnabled != nil && !*patch.RealityEnabled))
	if revoke {
		if err := g.closeForRevocation(); err != nil {
			return store.Peer{}, err
		}
	}
	p, err := st.PatchPeer(ctx, id, patch)
	if err != nil {
		return store.Peer{}, err
	}
	if revoke {
		err = g.reconcileRevocation(ctx)
	}
	return p, err
}

func (g *applyGate) DeletePeer(ctx context.Context, st *store.Store, id string) (store.PeerDeletion, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := st.Peer(ctx, id)
	if err != nil {
		return store.PeerDeletion{}, err
	}
	revoke := p.RealityEnabled && p.Enabled
	if revoke {
		if err := g.closeForRevocation(); err != nil {
			return store.PeerDeletion{}, err
		}
	}
	effects, err := st.DeletePeerWithEffects(ctx, id)
	if err != nil {
		return effects, err
	}
	if revoke {
		err = g.reconcileRevocation(ctx)
	}
	return effects, err
}

func (g *applyGate) RotateReality(ctx context.Context, st *store.Store, id string, expected int) (store.Peer, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	old, err := st.Peer(ctx, id)
	if err != nil {
		return store.Peer{}, err
	}
	if expected != old.RealityRevision {
		return store.Peer{}, store.ErrConflict
	}
	if !old.RealityEnabled {
		return store.Peer{}, store.ErrInvalid
	}
	if err := g.closeForRevocation(); err != nil {
		return store.Peer{}, err
	}
	p, err := st.RotateReality(ctx, id, expected)
	if err != nil {
		return store.Peer{}, err
	}
	return p, g.reconcileRevocation(ctx)
}

func (g *applyGate) SaveSettings(ctx context.Context, st *store.Store, next store.Settings) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	old, err := st.Settings(ctx)
	if err != nil {
		return err
	}
	revoke := old.RealityEnabled && !next.RealityEnabled
	if revoke {
		if err := g.closeForRevocation(); err != nil {
			return err
		}
	}
	if err := st.SaveSettings(ctx, next); err != nil {
		return err
	}
	if revoke {
		return g.reconcileRevocation(ctx)
	}
	return nil
}

func (g *applyGate) RealityStatus(ctx context.Context, st *store.Store, p store.Peer) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.realityStatus(ctx, st, p)
}

func (g *applyGate) realityStatus(ctx context.Context, st *store.Store, p store.Peer) string {
	if !p.Enabled || !p.RealityEnabled {
		return "disabled"
	}
	if !g.network.initialized || g.network.uncertain || g.publicGuarded() {
		return "guarded"
	}
	if !g.base.Settings.RealityEnabled {
		return "pending"
	}
	live, err := st.RealityRuntime(ctx, g.base)
	if err != nil {
		return "guarded"
	}
	for _, u := range live.Users {
		if u.PeerID == p.ID {
			for _, a := range g.base.RealityAdmissions {
				if a.PeerID == p.ID && a.Revision == p.RealityRevision && a.CredentialID == p.RealityCredentialID {
					return "available"
				}
			}
		}
	}
	return "pending"
}

func (g *applyGate) RealityConfig(ctx context.Context, st *store.Store, id string) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := st.Peer(ctx, id)
	if err != nil {
		return "", err
	}
	if g.realityStatus(ctx, st, p) != "available" {
		return "", fmt.Errorf("%w: profile is not applied and available", api.ErrNetworkGuarded)
	}
	live, err := st.RealityRuntime(ctx, g.base)
	if err != nil {
		return "", err
	}
	if live.Identity == nil {
		return "", api.ErrNetworkGuarded
	}
	for _, u := range live.Users {
		if u.PeerID == id {
			cfg := g.base.Settings
			q := url.Values{"encryption": {"none"}, "security": {"reality"}, "sni": {cfg.RealityServerName}, "fp": {"chrome"}, "pbk": {live.Identity.PublicKey}, "sid": {live.Identity.ShortID}, "type": {"tcp"}, "packetEncoding": {"xudp"}}
			uri := url.URL{Scheme: "vless", User: url.User(u.UUID), Host: net.JoinHostPort(cfg.EndpointHost, strconv.Itoa(cfg.RealityListenPort)), RawQuery: q.Encode(), Fragment: p.Name}
			return uri.String(), nil
		}
	}
	return "", api.ErrNetworkGuarded
}

var _ api.RealityPublisher = (*applyGate)(nil)

func probePublicPort(endpoint ingressguard.Endpoint) error {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", net.JoinHostPort(endpoint.Address, strconv.Itoa(int(endpoint.Port))))
	if err != nil {
		return err
	}
	return listener.Close()
}

// guardSavedPublic runs independently of database state and publication locks.
// A genuinely fresh installation has neither artifact; any orphan fails closed.
func guardSavedPublic(configPath, manifestPath string) error {
	return packaging.GuardSavedIngress(configPath, manifestPath, func(e []ingressguard.Endpoint) error { return netstack.SetPublicClientGuard(e, true) })
}

func (g *applyGate) RealityClientConfig(ctx context.Context, st *store.Store, id string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	p, err := st.Peer(ctx, id)
	if err != nil {
		return nil, err
	}
	if g.realityStatus(ctx, st, p) != "available" {
		return nil, api.ErrNetworkGuarded
	}
	live, err := st.RealityRuntime(ctx, g.base)
	if err != nil {
		return nil, err
	}
	if live.Identity == nil {
		return nil, api.ErrNetworkGuarded
	}
	for _, u := range live.Users {
		if u.PeerID == id {
			return singbox.RealityClientProfile(g.base.Settings, u.UUID, live.Identity.PublicKey, live.Identity.ShortID)
		}
	}
	return nil, api.ErrNetworkGuarded
}

func (g *applyGate) emergencyPublicGuard() error {
	return ingressguard.WithLock(g.manifestPath(), func() error {
		m, err := ingressguard.Read(g.manifestPath())
		if err != nil {
			return err
		}
		m.Guarded = true
		if err := ingressguard.Write(g.manifestPath(), m); err != nil {
			return err
		}
		return g.publicGuard(m.Endpoints, true)
	})
}
