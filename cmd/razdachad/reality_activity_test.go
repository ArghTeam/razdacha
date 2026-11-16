package main

import (
	"context"
	"os"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityActivityAuthorityTracksPublicationAndRevocation(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.RealityActivityAuthority(ctx, f.st); err == nil {
		t.Fatal("uninitialized publication exposed observation authority")
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	before, err := f.g.RealityActivityAuthority(ctx, f.st)
	if err != nil || before.Epoch == "" || before.Users[store.RealityUserName(f.p.ID)] != f.p.RealityRevision {
		t.Fatalf("authority=%+v err=%v", before, err)
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	after, err := f.g.RealityActivityAuthority(ctx, f.st)
	if err != nil || before.Epoch == after.Epoch {
		t.Fatal("same-byte Apply reused observation epoch", err)
	}
	if _, err := f.g.RotateReality(ctx, f.st, f.p.ID, f.p.RealityRevision); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.RealityActivityAuthority(ctx, f.st); err == nil {
		t.Fatal("pending replacement retained observation authority")
	}
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	replaced, err := f.g.RealityActivityAuthority(ctx, f.st)
	if err != nil || replaced.Epoch == after.Epoch || replaced.Users[store.RealityUserName(f.p.ID)] != f.p.RealityRevision+1 {
		t.Fatal("credential revision did not replace observation authority", err)
	}
	disabled := false
	if _, err := f.g.PatchPeer(ctx, f.st, f.p.ID, store.PeerPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.RealityActivityAuthority(ctx, f.st); err == nil {
		t.Fatal("disabled admission exposed an activity sample")
	}
}

func TestRealityActivityAuthorityRejectsChangedConfigAndGuard(t *testing.T) {
	f := newRealityGateFixture(t)
	ctx := context.Background()
	if _, err := f.g.ApplyCurrent(ctx, f.st); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(f.g.configPath())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.g.configPath(), append(original, ' '), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.RealityActivityAuthority(ctx, f.st); err == nil {
		t.Fatal("unchecked runtime bytes admitted")
	}
	if err := os.WriteFile(f.g.configPath(), original, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.g.emergencyPublicGuard(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.RealityActivityAuthority(ctx, f.st); err == nil {
		t.Fatal("guarded runtime exposed activity")
	}
}

func TestActivityFallbackCapabilityAcceptsPinnedListableEncoding(t *testing.T) {
	user := "client-00000000-0000-4000-8000-000000000001"
	for _, rule := range []string{
		`{"inbound":"reality-in","auth_user":"` + user + `","outbound":"direct-out"}`,
		`{"inbound":"reality-in","auth_user":"` + user + `","action":"route","outbound":"direct-out"}`,
		`{"inbound":["reality-in"],"auth_user":["` + user + `"],"action":"route","outbound":"direct-out"}`,
	} {
		if !activityFallbacksPresent([]byte(`{"route":{"rules":[`+rule+`]}}`), map[string]int{user: 1}) {
			t.Fatal("pinned listable representation rejected", rule)
		}
	}
	if activityFallbacksPresent([]byte(`{"route":{"rules":[]}}`), map[string]int{user: 1}) {
		t.Fatal("legacy config reported supported observation")
	}
	for _, extra := range []string{`"rule_set":"selected"`, `"port":443`, `"domain":"site.example"`, `"invert":true`, `"unknown":true`} {
		rule := `{"inbound":"reality-in","auth_user":"` + user + `","outbound":"direct-out",` + extra + `}`
		if activityFallbacksPresent([]byte(`{"route":{"rules":[`+rule+`]}}`), map[string]int{user: 1}) {
			t.Fatal("restricted rule proved unconditional fallback", extra)
		}
	}
}
