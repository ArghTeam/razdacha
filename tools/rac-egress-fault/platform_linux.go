//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

func checkCleanupEligibility() error {
	if os.Geteuid() != 0 {
		return errors.New("the RAC fixture requires root")
	}
	return nil
}

func checkHost() error {
	if err := checkCleanupEligibility(); err != nil {
		return err
	}
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return err
	}
	for _, a := range addresses {
		ip, _, e := net.ParseCIDR(a.String())
		if e == nil && ip.String() == sandboxIP {
			return nil
		}
	}
	return errors.New("local RAC IPv4 is absent; no mutation is allowed")
}

func clockNow() (time.Time, uint64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return time.Time{}, 0, err
	}
	return time.Now(), uint64(ts.Sec)*1_000_000 + uint64(ts.Nsec)/1_000, nil
}

func bootID() (string, error) {
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(b))
	if len(s) != 36 {
		return "", errors.New("invalid boot identity")
	}
	return s, nil
}

func command(path string, args ...string) ([]byte, error) {
	return commandTimeout(3*time.Second, path, args...)
}

func commandTimeout(timeout time.Duration, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c := exec.CommandContext(ctx, path, args...)
	b, e := c.Output()
	if len(b) > 65536 {
		return nil, errors.New("systemd response exceeds limit")
	}
	if e != nil {
		return nil, fmt.Errorf("fixture systemd command: %w", e)
	}
	return b, nil
}

func properties(unit string, names ...string) (map[string]string, error) {
	args := []string{"show", unit, "--no-pager"}
	for _, name := range names {
		args = append(args, "--property="+name)
	}
	b, e := command("/usr/bin/systemctl", args...)
	if e != nil {
		return nil, e
	}
	result := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, errors.New("malformed systemd property response")
		}
		result[k] = v
	}
	return result, nil
}

func busProperty(unit, iface, name string) (json.RawMessage, error) {
	b, e := command("/usr/bin/busctl", "--system", "--json=short", "get-property", "org.freedesktop.systemd1", unitPath(unit), "org.freedesktop.systemd1."+iface, name)
	if e != nil {
		return nil, e
	}
	var value struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if e = json.Unmarshal(b, &value); e != nil {
		return nil, e
	}
	return value.Data, nil
}

func busUint(unit, iface, name string) (uint64, error) {
	b, e := busProperty(unit, iface, name)
	if e != nil {
		return 0, e
	}
	var n uint64
	e = json.Unmarshal(b, &n)
	return n, e
}

func executable() (string, error) {
	p, e := os.Executable()
	if e != nil {
		return "", e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return "", e
	}
	info, e := os.Stat(p)
	if e != nil {
		return "", e
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0o022 != 0 || strings.ContainsAny(p, " \t\n;") {
		return "", errors.New("fixture executable must be a root-owned non-writable path without whitespace")
	}
	return p, nil
}

func validateFaultUnit(p plan, exe string, requireSelf bool) error {
	v, e := properties(p.faultUnit(), "LoadState", "ActiveState", "MainPID", "ExecStart", "ExecStopPost", "KillMode", "SendSIGKILL")
	if e != nil {
		return e
	}
	if v["LoadState"] != "loaded" || !exactExec(v["ExecStart"], exe, "hold", p.RunID) || !exactExec(v["ExecStopPost"], exe, "cleanup", p.RunID) || v["KillMode"] != "control-group" || v["SendSIGKILL"] != "yes" {
		return errors.New("fault unit identity or bounded cleanup contract differs")
	}
	if requireSelf && (v["MainPID"] != strconv.Itoa(os.Getpid()) || (v["ActiveState"] != "active" && v["ActiveState"] != "activating")) {
		return errors.New("hold must run in its exact owned fault unit")
	}
	max, e := busUint(p.faultUnit(), "Service", "RuntimeMaxUSec")
	if e != nil {
		return e
	}
	stop, e := busUint(p.faultUnit(), "Service", "TimeoutStopUSec")
	if e != nil {
		return e
	}
	_, mono, e := clockNow()
	if e != nil {
		return e
	}
	remaining := uint64(0)
	if mono < p.DeadlineMonoUS {
		remaining = p.DeadlineMonoUS - mono
	}
	return validateRuntimeBound(max, stop, remaining, requireSelf)
}

func verifyArmedUnits(p plan) error {
	exe, e := executable()
	if e != nil {
		return e
	}
	if e = validateFaultUnit(p, exe, true); e != nil {
		return e
	}
	v, e := properties(p.watchdogTimer(), "LoadState", "ActiveState", "Unit")
	if e != nil {
		return e
	}
	if v["LoadState"] != "loaded" || v["ActiveState"] != "active" || v["Unit"] != p.watchdogService() {
		return errors.New("independent watchdog timer is not armed for this run")
	}
	v, e = properties(p.watchdogService(), "LoadState", "ExecStart", "User", "DynamicUser", "PrivateNetwork", "NetworkNamespacePath", "JoinsNamespaceOf", "CapabilityBoundingSet")
	if e != nil {
		return e
	}
	if v["LoadState"] != "loaded" || !exactExec(v["ExecStart"], exe, "watchdog", p.RunID) {
		return errors.New("watchdog command identity differs")
	}
	if e = validateWatchdogContext(v); e != nil {
		return e
	}
	accuracy, e := busUint(p.watchdogTimer(), "Timer", "AccuracyUSec")
	if e != nil {
		return e
	}
	random, e := busUint(p.watchdogTimer(), "Timer", "RandomizedDelayUSec")
	if e != nil {
		return e
	}
	next, e := busUint(p.watchdogTimer(), "Timer", "NextElapseUSecMonotonic")
	if e != nil {
		return e
	}
	_, now, e := clockNow()
	if e != nil {
		return e
	}
	if e = validateTimerWindow(p.DeadlineMonoUS, next, now, accuracy, random); e != nil {
		return e
	}
	raw, e := busProperty(p.watchdogTimer(), "Timer", "TimersMonotonic")
	if e != nil {
		return e
	}
	var timers [][]json.RawMessage
	if e = json.Unmarshal(raw, &timers); e != nil {
		return e
	}
	if len(timers) != 1 || len(timers[0]) != 3 {
		return errors.New("watchdog must have one non-recurring monotonic timer")
	}
	var base string
	if e = json.Unmarshal(timers[0][0], &base); e != nil {
		return e
	}
	if base != "OnActiveUSec" {
		return errors.New("watchdog must use OnActiveSec")
	}
	raw, e = busProperty(p.watchdogTimer(), "Timer", "TimersCalendar")
	if e != nil {
		return e
	}
	var calendars []json.RawMessage
	if e = json.Unmarshal(raw, &calendars); e != nil {
		return e
	}
	if len(calendars) != 0 {
		return errors.New("calendar watchdog triggers are not allowed")
	}
	return nil
}

func watchdog(j journals, p plan) error {
	stop := func() error {
		exe, e := executable()
		if e != nil {
			return e
		}
		v, e := properties(p.faultUnit(), "LoadState")
		if e != nil {
			return e
		}
		if v["LoadState"] == "not-found" {
			return nil
		}
		if e = validateFaultUnit(p, exe, false); e != nil {
			return e
		}
		_, e = commandTimeout(10*time.Second, "/usr/bin/systemctl", "stop", p.faultUnit())
		return e
	}
	var cleanupDeadline time.Time
	clean := func() error {
		if cleanupDeadline.IsZero() {
			cleanupDeadline = time.Now().Add(cleanupSlack)
		}
		return cleanup(j, p.RunID)
	}
	retry := func() bool {
		if !time.Now().Before(cleanupDeadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
		return true
	}
	return watchdogSequence(stop, clean, retry)
}

// The parent launches a fresh process: every Go thread and netlink socket is
// born inside the disposable namespace, rather than changing one runtime thread.
func kernelSelfTest(id string) error {
	parent, e := namespaceID("/proc/self/ns/net")
	if e != nil {
		return e
	}
	exe, e := executable()
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/unshare", "--net", "--fork", "--kill-child=KILL", "--", exe, "selftest-child", "--run-id", id, "--parent-netns", parent, "--enable-rac-exit-fault")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return fmt.Errorf("isolated kernel roundtrip: %w", e)
	}
	return nil
}

func namespaceID(path string) (string, error) {
	info, e := os.Stat(path)
	if e != nil {
		return "", e
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("namespace identity unavailable")
	}
	return fmt.Sprintf("%d:%d", stat.Dev, stat.Ino), nil
}

func kernelSelfTestChild(id, parent string) error {
	if os.Geteuid() != 0 {
		return errors.New("isolated kernel roundtrip requires root")
	}
	current, e := namespaceID("/proc/self/ns/net")
	if e != nil {
		return e
	}
	host, e := namespaceID("/proc/1/ns/net")
	if e != nil {
		return e
	}
	if e = validateIsolatedNamespace(parent, host, current); e != nil {
		return e
	}
	interfaces, e := net.Interfaces()
	if e != nil {
		return e
	}
	if len(interfaces) != 1 || interfaces[0].Name != "lo" || interfaces[0].Flags&net.FlagLoopback == 0 {
		return errors.New("kernel roundtrip requires only the fresh loopback interface")
	}
	now, mono, e := clockNow()
	if e != nil {
		return e
	}
	boot, e := bootID()
	if e != nil {
		return e
	}
	for _, port := range []int{443, 2443} {
		p := plan{Version: 1, RunID: id, Port: port, PreparedAt: now.UTC(), Deadline: now.Add(10 * time.Second).UTC(), DeadlineMonoUS: mono + 10_000_000, BootID: boot, NetworkNS: current, Phase: "prepared"}
		fw, e := newFirewall()
		if e != nil {
			return e
		}
		if e = fw.install(p); e != nil {
			return e
		}
		count, present, e := fw.inspect(p)
		if e != nil {
			return e
		}
		if !present || count.Packets != 0 || count.Bytes != 0 {
			return errors.New("isolated kernel roundtrip status differs")
		}
		if e = fw.install(p); e == nil {
			return errors.New("isolated exclusive table collision was accepted")
		}
		if e = fw.remove(p); e != nil {
			return e
		}
		_, present, e = fw.inspect(p)
		if e != nil {
			return e
		}
		if present {
			return errors.New("isolated fixture table remains")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"isolated_kernel_roundtrip": true, "ports": []int{443, 2443}, "host_network_mutated": false})
}

func networkNamespace() (string, error) { return namespaceID("/proc/self/ns/net") }
