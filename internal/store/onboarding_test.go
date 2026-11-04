package store

import (
	"context"
	"errors"
	"testing"
)

var onboardingPreset = []string{"russia_inside", "meta", "twitter", "discord"}

const onboardingCatalog = "https://vpnkeys.me/api/v1/keys?country=DE"

func onboardingStore(t *testing.T) (*Store, Peer) {
	t.Helper()
	s := open(t)
	ctx := context.Background()
	peer, err := s.CreatePeer(ctx, samplePeer("initial", "10.8.0.2/32"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureBuiltinPool(ctx, "https://raw.githack.com/igareck/vpn-configs-for-russia/main/", TunnelVLESS); err != nil {
		t.Fatal(err)
	}
	if err := s.InitOnboarding(ctx, peer.ID, "", "AUTO"); err != nil {
		t.Fatal(err)
	}
	return s, peer
}

func fingerprint(t *testing.T, s *Store) string {
	t.Helper()
	snap, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	hash, err := PolicyFingerprint(snap)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func TestOnboardingConfigureIsAtomicScopedAndIdempotent(t *testing.T) {
	s, peer := onboardingStore(t)
	ctx := context.Background()
	initial := fingerprint(t, s)
	state, err := s.ConfigureOnboarding(ctx, initial, "free", "DE", onboardingCatalog, nil, onboardingPreset)
	if err != nil {
		t.Fatal(err)
	}
	firstID := state.RuleID
	if firstID == "" || !state.Prepared || state.Applied {
		t.Fatalf("wrong state %+v", state)
	}
	if state.CandidateCount != 0 || !state.PoolEnabled || state.CatalogOutcome != "no_candidates" {
		t.Fatal("known empty pool not reported truthfully")
	}
	state, err = s.ConfigureOnboarding(ctx, state.Expected, "free", "DE", onboardingCatalog, nil, onboardingPreset)
	if err != nil || state.RuleID != firstID {
		t.Fatalf("retry duplicate: %+v %v", state, err)
	}
	snap, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Rules) != 1 || snap.Rules[0].PeerScope != ScopeSelected || len(snap.Rules[0].PeerIDs) != 1 || snap.Rules[0].PeerIDs[0] != peer.ID || !snap.Rules[0].Enabled {
		t.Fatal("starter scope changed")
	}
	if len(snap.Peers) != 1 || snap.Tunnels[0].Raw != onboardingCatalog || !snap.Tunnels[0].Enabled || len(snap.Tunnels[0].Pool) != 0 {
		t.Fatal("wrong pool or duplicate client")
	}
	if err := s.AcceptOnboarding(ctx, state.Expected, true); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("finished without accepted Apply")
	}
	if err := s.AcceptOnboarding(ctx, state.Expected, false); err != nil {
		t.Fatal(err)
	}
	if err := s.AcceptOnboarding(ctx, state.Expected, true); err != nil {
		t.Fatal(err)
	}
	finished, err := s.Onboarding(ctx)
	if err != nil || finished.Pending {
		t.Fatal("completion not persisted")
	}
}

func TestOnboardingConflictsDoNotOverwritePolicy(t *testing.T) {
	s, _ := onboardingStore(t)
	ctx := context.Background()
	expected := fingerprint(t, s)
	settings, _ := s.Settings(ctx)
	settings.ClientMTU = 1320
	if err := s.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	before := fingerprint(t, s)
	if _, err := s.ConfigureOnboarding(ctx, expected, "free", "DE", onboardingCatalog, nil, onboardingPreset); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("unrelated pending edit accepted")
	}
	if before != fingerprint(t, s) {
		t.Fatal("conflict changed policy")
	}
	state, err := s.ConfigureOnboarding(ctx, before, "free", "DE", onboardingCatalog, nil, onboardingPreset)
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.Rule(ctx, state.RuleID)
	if err != nil {
		t.Fatal(err)
	}
	rule.Domains = []string{"custom.example"}
	if err := s.UpdateRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	changed := fingerprint(t, s)
	if _, err := s.ConfigureOnboarding(ctx, changed, "free", "US", onboardingCatalog, nil, onboardingPreset); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("customized owned rule overwritten")
	}
	if changed != fingerprint(t, s) {
		t.Fatal("failed transaction changed policy")
	}
	if err := s.AcceptOnboarding(ctx, state.Expected, false); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("stale acceptance allowed")
	}
}

func TestLegacyOnboardingReopenIsReadOnlyAndDoesNotAdoptByName(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	peer, err := s.CreatePeer(ctx, samplePeer("existing", "10.8.0.2/32"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateRule(ctx, Rule{Name: "Free start", Action: ActionBlock, Enabled: true, PeerScope: ScopeAll, Domains: []string{"private.example"}}); err != nil {
		t.Fatal(err)
	}
	state, err := s.Onboarding(ctx)
	if err != nil || state.Pending {
		t.Fatal("legacy inferred pending from peer count")
	}
	before := fingerprint(t, s)
	state, err = s.ReopenOnboarding(ctx, peer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Pending || !state.ReadOnly || state.RuleID != "" || fingerprint(t, s) != before {
		t.Fatal("reopen adopted or changed policy")
	}
	if _, err := s.ConfigureOnboarding(ctx, before, "free", "DE", onboardingCatalog, nil, onboardingPreset); !errors.Is(err, ErrInvalid) {
		t.Fatal("legacy free policy modified")
	}
	state, err = s.ConfigureOnboarding(ctx, before, "skip", "AUTO", "", nil, onboardingPreset)
	if err != nil || state.Expected != before || fingerprint(t, s) != before {
		t.Fatal("Skip changed legacy routing")
	}
	reopened, err := s.ReopenOnboarding(ctx, peer.ID)
	if err != nil || !reopened.Prepared || reopened.Expected != state.Expected {
		t.Fatal("reopen lost prepared retry state")
	}
}

func TestOnboardingFingerprintPinsPolicyButAllowsPoolRefresh(t *testing.T) {
	s, _ := onboardingStore(t)
	ctx := context.Background()
	before := fingerprint(t, s)
	tunnels, _ := s.Tunnels(ctx)
	pool := tunnels[0]
	pool.Pool = []PoolServer{{URL: "vless://synthetic@edge.example:443?security=tls"}}
	if err := s.UpdateTunnel(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if fingerprint(t, s) != before {
		t.Fatal("pool observation invalidated policy intent")
	}
	pool.Raw = onboardingCatalog
	if err := s.UpdateTunnel(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if fingerprint(t, s) == before {
		t.Fatal("source/country policy change ignored")
	}
}

func TestOnboardingInventoryCountsOnlyPolicyEligibleCandidates(t *testing.T) {
	s, _ := onboardingStore(t)
	ctx := context.Background()
	tunnels, _ := s.Tunnels(ctx)
	pool := tunnels[0]
	pool.Enabled = true
	pool.Pool = []PoolServer{{URL: "vless://synthetic@edge.example:443?security=tls", Country: "RU"}, {URL: "vless://synthetic@edge.example:443?security=none", Country: "DE"}}
	if err := s.UpdateTunnel(ctx, pool); err != nil {
		t.Fatal(err)
	}
	state, err := s.Onboarding(ctx)
	if err != nil || state.CandidateCount != 0 || state.CatalogOutcome != "no_candidates" {
		t.Fatal("all-excluded stored members reported as eligible exits")
	}
}
