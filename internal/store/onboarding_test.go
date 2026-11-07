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

func TestOnboardingFingerprintIncludesRealityAuthority(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		name := "identity retained while disabled"
		if enabled {
			name = "listener enabled"
		}
		t.Run(name, func(t *testing.T) {
			s, peer := onboardingStore(t)
			ctx := context.Background()
			settings, err := s.Settings(ctx)
			if err != nil {
				t.Fatal(err)
			}
			settings.RealityEnabled = true
			settings.RealityServerName = "fixture.example"
			settings.RealityHandshakeServer = "127.0.0.1"
			if err := s.SaveSettings(ctx, settings); err != nil {
				t.Fatal(err)
			}
			yes := true
			if _, err := s.PatchPeer(ctx, peer.ID, PeerPatch{RealityEnabled: &yes}); err != nil {
				t.Fatal(err)
			}
			if !enabled {
				settings.RealityEnabled = false
				if err := s.SaveSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
			}
			snap, err := s.Snapshot(ctx)
			if err != nil || snap.RealityIdentityID == "" || len(snap.RealityAdmissions) != 1 {
				t.Fatalf("missing synthetic admission: %+v %v", snap.RealityAdmissions, err)
			}
			state, err := s.ConfigureOnboarding(ctx, fingerprint(t, s), "skip", "AUTO", "", nil, nil)
			if err != nil {
				t.Fatal("coherent Reality policy conflicts with onboarding", err)
			}
			if state.Expected != fingerprint(t, s) {
				t.Fatal("onboarding omitted committed transport authority")
			}
			if err := s.AcceptOnboarding(ctx, state.Expected, false); err != nil {
				t.Fatal(err)
			}
			if err := s.AcceptOnboarding(ctx, state.Expected, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestOnboardingReviewMetadataCompareAndSwap(t *testing.T) {
	st, peer := onboardingStore(t)
	ctx := context.Background()
	prepared, err := st.ConfigureOnboarding(ctx, fingerprint(t, st), "skip", "AUTO", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ := st.Settings(ctx)
	settings.ClientMTU = 1320
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	reviewed := fingerprint(t, st)
	if err := st.ReviewOnboarding(ctx, "obsolete", reviewed); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("stale metadata accepted", err)
	}
	if err := st.ReviewOnboarding(ctx, prepared.Expected, prepared.Expected); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("stale policy accepted", err)
	}
	if err := st.ReviewOnboarding(ctx, prepared.Expected, reviewed); err != nil {
		t.Fatal(err)
	}
	state, _ := st.Onboarding(ctx)
	if state.Expected != reviewed || !state.Pending || !state.Prepared || state.Applied || state.PeerID != peer.ID || fingerprint(t, st) != reviewed {
		t.Fatal("review changed more than metadata")
	}
	if err := st.AcceptOnboarding(ctx, reviewed, false); err != nil {
		t.Fatal(err)
	}
	if err := st.AcceptOnboarding(ctx, reviewed, true); err != nil {
		t.Fatal(err)
	}
	if err := st.ReviewOnboarding(ctx, reviewed, reviewed); !errors.Is(err, ErrOnboardingConflict) {
		t.Fatal("completed setup reviewed", err)
	}
}
