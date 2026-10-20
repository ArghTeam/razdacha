//go:build linux

package restorestate

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

func processStart(pid int) (string, error) {
	b, e := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if e != nil {
		return "", e
	}
	end := strings.LastIndexByte(string(b), ')')
	if end < 0 {
		return "", fmt.Errorf("invalid process identity")
	}
	fields := strings.Fields(string(b[end+1:]))
	if len(fields) < 20 {
		return "", fmt.Errorf("incomplete process identity")
	}
	if _, e = strconv.ParseUint(fields[19], 10, 64); e != nil {
		return "", e
	}
	return fields[19], nil
}

func CurrentKernelFacts() (KernelFacts, error) {
	var f KernelFacts
	b, e := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if e != nil {
		return f, e
	}
	f.BootID = strings.TrimSpace(string(b))
	f.PID = os.Getpid()
	f.ProcessStart, e = processStart(f.PID)
	if e != nil {
		return f, e
	}
	var clock unix.Timespec
	if e = unix.ClockGettime(unix.CLOCK_BOOTTIME, &clock); e != nil {
		return f, e
	}
	f.NowNS = clock.Nano()
	return f, nil
}

func ProcessIdentityAlive(pid int, start string) bool {
	actual, e := processStart(pid)
	return e == nil && actual == start
}
