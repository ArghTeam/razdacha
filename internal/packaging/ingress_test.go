package packaging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/ingressguard"
)

func TestUninstallIngressPreflightIsReadOnlyAndFailsClosed(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(root, "etc/sing-box/config.json")
	manifestPath := filepath.Join(root, "var/lib/razdacha/public-ingress.json")
	for _, p := range []string{configPath, manifestPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
			t.Fatal(err)
		}
	}
	public := []byte(`{"inbounds":[{"type":"vless","tag":"reality-in","listen":"0.0.0.0","listen_port":8443,"tls":{"enabled":true,"reality":{"enabled":true}}}]}`)
	if err := os.WriteFile(configPath, public, 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngressUninstall(root); err == nil || !strings.Contains(err.Error(), "disable Reality and Apply") {
		t.Fatal("public saved listener allowed uninstall", err)
	}
	unchanged, _ := os.ReadFile(configPath)
	if string(unchanged) != string(public) {
		t.Fatal("preflight changed configuration")
	}
	legacy := []byte(`{"inbounds":[{"type":"direct","tag":"dns-in","listen":"10.8.0.1","listen_port":53}]}`)
	if err := os.WriteFile(configPath, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := ingressguard.Write(manifestPath, ingressguard.Manifest{Version: ingressguard.Version, Endpoints: []ingressguard.Endpoint{{Address: "0.0.0.0", Port: 8443}}, ConfigHashes: []string{ingressguard.HashConfig(legacy)}, Guarded: true}); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngressUninstall(root); err == nil {
		t.Fatal("obsolete public coverage allowed uninstall")
	}
	if err := ingressguard.Write(manifestPath, ingressguard.Manifest{Version: ingressguard.Version, ConfigHashes: []string{ingressguard.HashConfig(legacy)}, Guarded: false}); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngressUninstall(root); err != nil {
		t.Fatal("verified WG-only state blocked uninstall", err)
	}
	if err := os.WriteFile(manifestPath, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngressUninstall(root); err == nil {
		t.Fatal("uncertain authority allowed uninstall")
	}
}
