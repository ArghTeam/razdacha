package main

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func fixturePlan() plan {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	return plan{Version: 1, RunID: "0123456789abcdef0123456789abcdef", Port: 443, PreparedAt: now, Deadline: now.Add(60 * time.Second), DeadlineMonoUS: 61_000_000, BootID: "test-boot", NetworkNS: "4:11", Phase: "prepared"}
}

type memoryFirewall struct {
	mu         sync.Mutex
	present    bool
	foreign    bool
	installs   int
	removes    int
	installErr error
	onInstall  func()
	onInspect  func()
}

func (f *memoryFirewall) install(plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.installs++
	if f.onInstall != nil {
		f.onInstall()
	}
	if f.installErr != nil {
		return f.installErr
	}
	f.present = true
	return nil
}

func (f *memoryFirewall) inspect(plan) (counters, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.onInspect != nil {
		f.onInspect()
	}
	if f.foreign {
		return counters{}, true, errors.New("foreign table")
	}
	return counters{}, f.present, nil
}

func (f *memoryFirewall) remove(plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.foreign {
		return errors.New("foreign table")
	}
	if f.present {
		f.removes++
	}
	f.present = false
	return nil
}

func TestPlanRejectsBroaderEndpointAndIdentity(t *testing.T) {
	for _, change := range []func(*plan){func(p *plan) { p.Port = 80 }, func(p *plan) { p.RunID = "../not-owned" }, func(p *plan) { p.RunID = "0123456789ABCDEF0123456789ABCDEF" }, func(p *plan) { p.Deadline = p.PreparedAt.Add(91 * time.Second) }, func(p *plan) { p.Phase = "unknown" }} {
		p := fixturePlan()
		change(&p)
		if p.validate() == nil {
			t.Fatal("unsafe fixture plan accepted")
		}
	}
	p := fixturePlan()
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	p.Port = 2443
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRevocationBeforeInstallPreventsLateFault(t *testing.T) {
	p := fixturePlan()
	fw := &memoryFirewall{}
	saved := p
	save := func(v plan) error { saved = v; return nil }
	if err := revoke(&p, save, fw); err != nil {
		t.Fatal(err)
	}
	if saved.Phase != "revoked" {
		t.Fatal("revocation was not durable")
	}
	now := func() (time.Time, uint64) { return p.PreparedAt.Add(time.Second), 2_000_000 }
	if install(&saved, now, save, fw) == nil || fw.installs != 0 {
		t.Fatal("revoked run installed a late fault")
	}
}

func TestDeadlineRecheckedImmediatelyBeforeInstall(t *testing.T) {
	p := fixturePlan()
	calls := 0
	fw := &memoryFirewall{}
	now := func() (time.Time, uint64) {
		calls++
		if calls == 1 {
			return p.PreparedAt.Add(time.Second), 2_000_000
		}
		return p.Deadline, p.DeadlineMonoUS
	}
	if install(&p, now, func(plan) error { return nil }, fw) == nil || fw.installs != 0 {
		t.Fatal("expired prepared run installed")
	}
}

func TestMonotonicDeadlineSurvivesWallClockRollback(t *testing.T) {
	p := fixturePlan()
	fw := &memoryFirewall{}
	now := func() (time.Time, uint64) { return p.PreparedAt.Add(-time.Hour), p.DeadlineMonoUS }
	if install(&p, now, func(plan) error { return nil }, fw) == nil || fw.installs != 0 {
		t.Fatal("wall clock rollback extended the run")
	}
}

func TestInstallThatCrossesDeadlineIsRevokedAndRemoved(t *testing.T) {
	p := fixturePlan()
	fw := &memoryFirewall{}
	expired := false
	fw.onInstall = func() { expired = true }
	now := func() (time.Time, uint64) {
		if expired {
			return p.Deadline, p.DeadlineMonoUS
		}
		return p.PreparedAt.Add(time.Second), 2_000_000
	}
	if install(&p, now, func(plan) error { return nil }, fw) == nil {
		t.Fatal("late install returned success")
	}
	if fw.present || fw.removes != 1 || p.Phase != "revoked" {
		t.Fatal("late fault was retained")
	}
}

func TestInstalledJournalFailureRemovesFault(t *testing.T) {
	p := fixturePlan()
	fw := &memoryFirewall{}
	saves := 0
	save := func(plan) error {
		saves++
		if saves == 1 {
			return errors.New("disk failure")
		}
		return nil
	}
	now := func() (time.Time, uint64) { return p.PreparedAt.Add(time.Second), 2_000_000 }
	if install(&p, now, save, fw) == nil || fw.present || p.Phase != "revoked" {
		t.Fatal("journal failure retained fault")
	}
}

func TestForeignCollisionIsNotAdoptedOrDeleted(t *testing.T) {
	p := fixturePlan()
	fw := &memoryFirewall{foreign: true}
	now := func() (time.Time, uint64) { return p.PreparedAt.Add(time.Second), 2_000_000 }
	if install(&p, now, func(plan) error { return nil }, fw) == nil || fw.installs != 0 || fw.removes != 0 {
		t.Fatal("foreign table was mutated")
	}
	if revoke(&p, func(plan) error { return nil }, fw) == nil || fw.removes != 0 {
		t.Fatal("foreign replacement was deleted")
	}
}

func TestExpiredUnitCanStillBeStoppedAndCleanedByWatchdog(t *testing.T) {
	const original = 60_000_000
	if err := validateRuntimeBound(original, 3_000_000, 0, false); err != nil {
		t.Fatal("expired watchdog rejected original bounded fault unit:", err)
	}
	if validateRuntimeBound(original, 3_000_000, 0, true) == nil {
		t.Fatal("expired holder accepted")
	}
	for _, max := range []uint64{0, 96_000_000, ^uint64(0)} {
		if validateRuntimeBound(max, 3_000_000, 0, false) == nil {
			t.Fatal("unbounded unit accepted")
		}
	}
}

func TestWatchdogMayFireBeforeHolderDeadline(t *testing.T) {
	if err := validateTimerWindow(60_000_000, 30_000_000, 1_000_000, 100_000, 0); err != nil {
		t.Fatal("earlier independent watchdog rejected:", err)
	}
	for _, next := range []uint64{0, 1_000_000, 60_000_001, 66_000_000} {
		if validateTimerWindow(60_000_000, next, 1_000_000, 100_000, 0) == nil {
			t.Fatal("absent/elapsed/late timer accepted")
		}
	}
	if validateTimerWindow(60_000_000, 30_000_000, 1_000_000, 2_000_000, 0) == nil || validateTimerWindow(60_000_000, 30_000_000, 1_000_000, 100_000, 1) == nil {
		t.Fatal("unbounded timer accuracy/randomization accepted")
	}
	if validateRuntimeBound(60_000_000, 0, 0, false) == nil {
		t.Fatal("zero stop budget accepted")
	}
}

func TestForeignReplacementAfterInstallIsRetained(t *testing.T) {
	p := fixturePlan()
	p.Phase = "installed"
	fw := &memoryFirewall{present: true, foreign: true}
	if revoke(&p, func(plan) error { return nil }, fw) == nil || !fw.present || fw.removes != 0 || p.Phase != "revoked" {
		t.Fatal("foreign replacement was deleted or revocation lost")
	}
}

func TestPersistentJournalFailureStillRemovesInjectedOutage(t *testing.T) {
	p := fixturePlan()
	p.Phase = "installed"
	fw := &memoryFirewall{present: true}
	if err := revoke(&p, func(plan) error { return errors.New("persistent disk failure") }, fw); err == nil {
		t.Fatal("lost tombstone reported success")
	}
	if fw.present || fw.removes != 1 {
		t.Fatal("persistent journal failure left the injected outage")
	}
}

func TestWatchdogUnitErrorStillAttemptsOwnedCleanup(t *testing.T) {
	stopErr := errors.New("unit identity differs")
	called := 0
	err := watchdogSequence(func() error { return stopErr }, func() error { called++; return nil }, func() bool { return false })
	if !errors.Is(err, stopErr) || called != 1 {
		t.Fatal("unit error skipped cleanup or was hidden")
	}
	cleanupErr := errors.New("foreign table")
	called = 0
	err = watchdogSequence(func() error { return stopErr }, func() error { called++; return cleanupErr }, func() bool { return false })
	if !errors.Is(err, stopErr) || !errors.Is(err, cleanupErr) || called != 1 {
		t.Fatal("watchdog failures were not retained")
	}
}

func TestRevocationDoesNotRequireLiveSandboxAddress(t *testing.T) {
	for _, mode := range []string{"cleanup", "watchdog", "status"} {
		if requiresSandboxAddress(mode) {
			t.Fatal("address loss disables recovery command", mode)
		}
	}
	for _, mode := range []string{"prepare", "hold", "selftest"} {
		if !requiresSandboxAddress(mode) {
			t.Fatal("activation lost sandbox identity guard", mode)
		}
	}
}

func TestRecoveryRequiresPreparedBootAndNetworkNamespace(t *testing.T) {
	p := fixturePlan()
	if err := validateRunEnvironment(p, p.BootID, p.NetworkNS); err != nil {
		t.Fatal(err)
	}
	if validateRunEnvironment(p, p.BootID, "4:12") == nil || validateRunEnvironment(p, "other-boot", p.NetworkNS) == nil {
		t.Fatal("foreign namespace or boot accepted for table absence")
	}
	if requiresSandboxAddress("cleanup") {
		t.Fatal("address change disables exact same-namespace cleanup")
	}
}
