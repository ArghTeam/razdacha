package main

import (
	"errors"
	"fmt"
	"strings"
)

func unitPath(unit string) string {
	var s strings.Builder
	s.WriteString("/org/freedesktop/systemd1/unit/")
	for _, c := range []byte(unit) {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			s.WriteByte(c)
		} else {
			fmt.Fprintf(&s, "_%02x", c)
		}
	}
	return s.String()
}

func exactExec(value, exe, mode, id string) bool {
	if strings.Count(value, "argv[]=") != 1 || strings.Count(value, "path=") != 1 {
		return false
	}
	_, executableTail, ok := strings.Cut(value, "path=")
	if !ok {
		return false
	}
	actual, _, ok := strings.Cut(executableTail, ";")
	if !ok || strings.TrimSpace(actual) != exe {
		return false
	}
	_, tail, ok := strings.Cut(value, "argv[]=")
	if !ok {
		return false
	}
	argv, _, ok := strings.Cut(tail, ";")
	if !ok {
		return false
	}
	expected := []string{exe, mode, "--run-id", id, "--enable-rac-exit-fault"}
	parts := strings.Fields(argv)
	if len(parts) != len(expected) {
		return false
	}
	for i := range parts {
		if parts[i] != expected[i] {
			return false
		}
	}
	return true
}

func validateWatchdogContext(v map[string]string) error {
	if (v["User"] != "" && v["User"] != "root" && v["User"] != "0") || v["DynamicUser"] != "no" || v["PrivateNetwork"] != "no" || v["NetworkNamespacePath"] != "" || v["JoinsNamespaceOf"] != "" {
		return errors.New("watchdog must retain root credentials and the host network namespace")
	}
	for _, capability := range strings.Fields(v["CapabilityBoundingSet"]) {
		if capability == "cap_net_admin" {
			return nil
		}
	}
	return errors.New("watchdog capability bound omits CAP_NET_ADMIN")
}
