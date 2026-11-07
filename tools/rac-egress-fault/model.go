package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"
)

const (
	journalRoot  = "/run/razdacha-egress-fault-348"
	sandboxIP    = "193.233.112.227"
	targetIP     = "165.22.196.130"
	maxLifetime  = 90 * time.Second
	cleanupSlack = 5 * time.Second
)

var runPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type plan struct {
	Version        int       `json:"version"`
	RunID          string    `json:"run_id"`
	Port           int       `json:"target_port"`
	PreparedAt     time.Time `json:"prepared_at"`
	Deadline       time.Time `json:"deadline"`
	DeadlineMonoUS uint64    `json:"deadline_monotonic_usec"`
	BootID         string    `json:"boot_id"`
	NetworkNS      string    `json:"network_namespace"`
	Phase          string    `json:"phase"`
}

func (p plan) validate() error {
	if p.Version != 1 || !runPattern.MatchString(p.RunID) || (p.Port != 443 && p.Port != 2443) || p.BootID == "" || !namespacePattern.MatchString(p.NetworkNS) {
		return errors.New("invalid fixture journal identity or endpoint")
	}
	if p.PreparedAt.IsZero() || p.Deadline.IsZero() || !p.Deadline.After(p.PreparedAt) || p.Deadline.Sub(p.PreparedAt) > maxLifetime || p.DeadlineMonoUS == 0 {
		return errors.New("invalid fixture deadline")
	}
	if p.Phase != "prepared" && p.Phase != "installed" && p.Phase != "revoked" {
		return errors.New("invalid fixture journal phase")
	}
	return nil
}
func (p plan) table() string           { return "rz_egress348_" + p.RunID }
func (p plan) faultUnit() string       { return "razdacha-egress-fault-348-" + p.RunID + ".service" }
func (p plan) watchdogService() string { return "razdacha-egress-watchdog-348-" + p.RunID + ".service" }
func (p plan) watchdogTimer() string   { return "razdacha-egress-watchdog-348-" + p.RunID + ".timer" }
func (p plan) marker() []byte {
	return []byte(fmt.Sprintf("razdacha-egress-fault-348:%s:%s:%d:%s:%d", p.RunID, targetIP, p.Port, p.Deadline.UTC().Format(time.RFC3339Nano), p.DeadlineMonoUS) + ":" + p.NetworkNS)
}

func (p plan) active(now time.Time, mono uint64) error {
	if p.Phase != "prepared" {
		return errors.New("fixture run is not prepared; revoked and installed runs cannot be reused")
	}
	if !now.Before(p.Deadline) || mono >= p.DeadlineMonoUS {
		return errors.New("fixture deadline expired")
	}
	return nil
}

func validRun(id string) error {
	if !runPattern.MatchString(id) {
		return errors.New("run ID must be exactly 32 lowercase hexadecimal characters")
	}
	_, err := hex.DecodeString(id)
	return err
}

type counters struct {
	Packets uint64 `json:"packets"`
	Bytes   uint64 `json:"bytes"`
}
type observation struct {
	RunID          string    `json:"run_id"`
	Table          string    `json:"table"`
	TargetIP       string    `json:"target_ip"`
	TargetPort     int       `json:"target_port"`
	Deadline       time.Time `json:"deadline"`
	DeadlineMonoUS uint64    `json:"deadline_monotonic_usec"`
	NetworkNS      string    `json:"network_namespace"`
	Journal        string    `json:"journal"`
	Phase          string    `json:"phase"`
	Installed      bool      `json:"installed"`
	TableAbsent    bool      `json:"table_absent"`
	counters
	FaultUnit       string `json:"fault_unit"`
	WatchdogService string `json:"watchdog_service"`
	WatchdogTimer   string `json:"watchdog_timer"`
}

func observe(p plan, path string, c counters, present bool) observation {
	return observation{RunID: p.RunID, Table: p.table(), TargetIP: targetIP, TargetPort: p.Port, Deadline: p.Deadline, DeadlineMonoUS: p.DeadlineMonoUS, NetworkNS: p.NetworkNS, Journal: path, Phase: p.Phase, Installed: present, TableAbsent: !present, counters: c, FaultUnit: p.faultUnit(), WatchdogService: p.watchdogService(), WatchdogTimer: p.watchdogTimer()}
}

type firewall interface {
	install(plan) error
	inspect(plan) (counters, bool, error)
	remove(plan) error
}

// The caller holds the private journal lock through each kernel transaction.
// Revocation is retained even when no table exists, preventing a late installer.
func revoke(p *plan, save func(plan) error, fw firewall) error {
	p.Phase = "revoked"
	var persistence error
	if err := save(*p); err != nil {
		persistence = fmt.Errorf("persist fixture revocation: %w", err)
	}
	// A failed journal write must not preserve an injected outage. Exact owned
	// removal is still attempted; success is never reported without persistence.
	if err := fw.remove(*p); err != nil {
		return errors.Join(persistence, fmt.Errorf("remove owned fault: %w", err))
	}
	_, present, err := fw.inspect(*p)
	if err != nil {
		return errors.Join(persistence, err)
	}
	if present {
		return errors.Join(persistence, errors.New("owned fault table remains after cleanup"))
	}
	return persistence
}

func install(p *plan, now func() (time.Time, uint64), save func(plan) error, fw firewall) error {
	wall, mono := now()
	if err := p.active(wall, mono); err != nil {
		return err
	}
	if _, exists, err := fw.inspect(*p); err != nil {
		return err
	} else if exists {
		return errors.New("fixture table already exists; refusing to adopt it")
	}
	// This check is immediately before the exclusive atomic kernel installation.
	wall, mono = now()
	if err := p.active(wall, mono); err != nil {
		return err
	}
	if err := fw.install(*p); err != nil {
		cleanupErr := revoke(p, save, fw)
		return errors.Join(fmt.Errorf("install exact fault: %w", err), cleanupErr)
	}
	wall, mono = now()
	if !wall.Before(p.Deadline) || mono >= p.DeadlineMonoUS {
		return errors.Join(errors.New("deadline elapsed during fault installation"), revoke(p, save, fw))
	}
	p.Phase = "installed"
	if err := save(*p); err != nil {
		return errors.Join(fmt.Errorf("persist installed fault: %w", err), revoke(p, save, fw))
	}
	return nil
}

// Cleanup validates the original static bound even after the deadline. Applying
// the decreasing remaining-time check to cleanup would disable the watchdog.
func validateRuntimeBound(max, stop, remaining uint64, installing bool) error {
	if max == 0 || max > uint64((maxLifetime+cleanupSlack)/time.Microsecond) || stop == 0 || stop > uint64(cleanupSlack/time.Microsecond) {
		return errors.New("fault unit lifetime or stop timeout is unbounded")
	}
	if installing && max > remaining+uint64(cleanupSlack/time.Microsecond) {
		return errors.New("fault unit runtime exceeds the remaining deadline")
	}
	return nil
}

var namespacePattern = regexp.MustCompile(`^[0-9]+:[0-9]+$`)

func validateIsolatedNamespace(parent, host, current string) error {
	if !namespacePattern.MatchString(parent) || !namespacePattern.MatchString(host) || !namespacePattern.MatchString(current) || current == parent || current == host {
		return errors.New("kernel roundtrip is not in a distinct disposable namespace")
	}
	return nil
}

func validateTimerWindow(deadline, next, now, accuracy, random uint64) error {
	if accuracy > 1_000_000 || random != 0 {
		return errors.New("watchdog requires accuracy at most one second and no randomized delay")
	}
	if next <= now || next > deadline {
		return errors.New("watchdog monotonic elapse is absent, elapsed or too late")
	}
	return nil
}

// A unit identity/read/stop error never authorizes stopping a foreign unit and
// never skips exact owned kernel cleanup. Both failures remain visible.
func watchdogSequence(stop, cleanup func() error, retry func() bool) error {
	stopErr := stop()
	for {
		cleanErr := cleanup()
		if cleanErr == nil {
			return stopErr
		}
		if !retry() {
			return errors.Join(stopErr, cleanErr)
		}
	}
}

func requiresSandboxAddress(mode string) bool {
	return mode != "cleanup" && mode != "watchdog" && mode != "status"
}

func validateRunEnvironment(p plan, boot, namespace string) error {
	if boot != p.BootID {
		return errors.New("fixture journal belongs to a different boot")
	}
	if namespace != p.NetworkNS {
		return errors.New("fixture journal belongs to a different network namespace")
	}
	return nil
}
