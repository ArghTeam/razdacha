package ingressguard

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	legacyConfig  = `{"inbounds":[{"type":"direct","tag":"dns-in","listen":"10.8.0.1","listen_port":53},{"type":"tproxy","tag":"tproxy-in","listen":"127.0.0.1","listen_port":1602}],"experimental":{"clash_api":{"external_controller":"127.0.0.1:9090"}}}`
	realityConfig = `{"inbounds":[{"type":"vless","tag":"reality-in","listen":"0.0.0.0","listen_port":8443,"tls":{"enabled":true,"reality":{"enabled":true}}}]}`
)

func privateDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func writeTest(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func testManifest(config string) Manifest {
	return Manifest{Version: Version, Endpoints: []Endpoint{{Address: "0.0.0.0", Port: 8443}}, ConfigHashes: []string{HashConfig([]byte(config))}}
}

func TestManifestRoundTripAndRejectedReplacement(t *testing.T) {
	directory := privateDirectory(t)
	path := filepath.Join(directory, "ingress.json")
	manifest := testManifest(realityConfig)
	manifest.Endpoints = append(manifest.Endpoints, manifest.Endpoints[0])
	manifest.ConfigHashes = append(manifest.ConfigHashes, manifest.ConfigHashes[0])
	if err := Write(path, manifest); err != nil {
		t.Fatal(err)
	}
	read, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Endpoints) != 1 || len(read.ConfigHashes) != 1 || read.Guarded {
		t.Fatalf("unexpected normalized manifest: %#v", read)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatal("manifest is not private")
	}
	before, _ := os.ReadFile(path)
	for _, invalid := range []Manifest{{Version: 2, ConfigHashes: read.ConfigHashes}, {Version: 1, ConfigHashes: []string{"not-a-hash"}}, {Version: 1, ConfigHashes: read.ConfigHashes, Endpoints: []Endpoint{{Address: "gateway.example", Port: 8443}}}} {
		if err := Write(path, invalid); err == nil {
			t.Fatal("invalid replacement accepted")
		}
		after, _ := os.ReadFile(path)
		if string(after) != string(before) {
			t.Fatal("invalid replacement changed authority")
		}
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary publication files remain")
	}
}

func TestManifestRejectsAmbiguousAndUnsafeFiles(t *testing.T) {
	directory := privateDirectory(t)
	path := filepath.Join(directory, "ingress.json")
	hash := HashConfig([]byte(realityConfig))
	for _, content := range []string{
		`{"version":1,"version":1,"endpoints":[],"config_hashes":["` + hash + `"],"guarded":false}`,
		`{"version":1,"endpoints":[],"config_hashes":["` + hash + `"],"guarded":null}`,
		`{"version":1,"endpoints":[],"config_hashes":["` + hash + `"],"guarded":false,"extra":true}`,
		`{"version":1,"endpoints":[],"config_hashes":["` + hash + `"]}`,
		`null`,
		strings.Repeat(" ", maxManifestBytes+1),
	} {
		writeTest(t, path, content)
		if _, err := Read(path); err == nil {
			t.Fatal("malformed manifest accepted")
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Write(path, testManifest(realityConfig)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("public-readable manifest accepted")
	}
	if err := Write(path, testManifest(realityConfig)); err == nil {
		t.Fatal("unsafe existing authority overwritten")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(directory, "linked.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(link); err == nil {
		t.Fatal("symlink manifest accepted")
	}
	if err := Write(link, testManifest(realityConfig)); err == nil {
		t.Fatal("symlink manifest replaced")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Read(path); err == nil {
		t.Fatal("nonprivate authority directory accepted")
	}
}

func TestEndpointUnionAndCoverage(t *testing.T) {
	union, err := Union([]Endpoint{{Address: "::", Port: 8443}}, []Endpoint{{Address: "0.0.0.0", Port: 9443}, {Address: "::", Port: 8443}})
	if err != nil || len(union) != 2 {
		t.Fatal("old/candidate union lost coverage", err)
	}
	for _, address := range []string{"0.0.0.0", "127.0.0.1", "::1", "2001:db8::1"} {
		if !covers(Endpoint{Address: "::", Port: 8443}, Endpoint{Address: address, Port: 8443}) {
			t.Fatal("dual-stack wildcard lost family")
		}
	}
	if covers(Endpoint{Address: "0.0.0.0", Port: 8443}, Endpoint{Address: "::1", Port: 8443}) {
		t.Fatal("IPv4 wildcard covered IPv6")
	}
	if covers(Endpoint{Address: "127.0.0.1", Port: 8443}, Endpoint{Address: "127.0.0.2", Port: 8443}) {
		t.Fatal("specific address widened")
	}
	if covers(Endpoint{Address: "::", Port: 8443}, Endpoint{Address: "127.0.0.1", Port: 9443}) {
		t.Fatal("port boundary widened")
	}
	for _, invalid := range []Endpoint{{Address: "gateway.example", Port: 8443}, {Address: "fe80::1%eth0", Port: 8443}, {Address: "127.0.0.1", Port: 0}} {
		if _, err := NormalizeEndpoints([]Endpoint{invalid}); err == nil {
			t.Fatal("invalid endpoint accepted")
		}
	}
}

func TestConfigClosureAndExactAuthority(t *testing.T) {
	endpoints, err := ConfigEndpoints([]byte(legacyConfig))
	if err != nil || len(endpoints) != 0 {
		t.Fatal("safe legacy shape rejected", err)
	}
	manifest := testManifest(realityConfig)
	if err := ValidateConfig([]byte(realityConfig), manifest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateConfig([]byte(realityConfig+" "), manifest); err == nil {
		t.Fatal("unchecked config bytes accepted")
	}
	manifest.Endpoints = []Endpoint{{Address: "127.0.0.1", Port: 8443}}
	if err := ValidateConfig([]byte(realityConfig), manifest); err == nil {
		t.Fatal("wildcard outside guard accepted")
	}
	for _, config := range []string{
		strings.Replace(legacyConfig, "10.8.0.1", "0.0.0.0", 1),
		strings.Replace(legacyConfig, "127.0.0.1", "0.0.0.0", 1),
		strings.Replace(legacyConfig, "127.0.0.1:9090", "0.0.0.0:9090", 1),
		`{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":1080}]}`,
		`{"inbounds":[{"type":"vless","listen":"0.0.0.0","listen_port":8443}]}`,
		`{"inbounds":[],"inbounds":[]}`,
		`{"experimental":{"unknown_api":{"listen":"0.0.0.0:8080"}}}`,
	} {
		if _, err := ConfigEndpoints([]byte(config)); err == nil {
			t.Fatal("unsafe or unknown legacy listener accepted")
		}
	}
}

func TestStartupFailsClosedAndPreservesLegacyBarrier(t *testing.T) {
	directory := privateDirectory(t)
	config, authority := filepath.Join(directory, "config.json"), filepath.Join(directory, "ingress.json")
	writeTest(t, config, legacyConfig)
	called := false
	if err := Startup(config, authority, func([]Endpoint) error { called = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("legacy startup cleared or replaced an existing barrier")
	}
	writeTest(t, config, realityConfig)
	if err := Startup(config, authority, func([]Endpoint) error { called = true; return nil }); err == nil {
		t.Fatal("public listener started without authority")
	}
	if called {
		t.Fatal("missing authority reached installer")
	}
	manifest := testManifest(realityConfig)
	manifest.Endpoints = append(manifest.Endpoints, Endpoint{Address: "0.0.0.0", Port: 9443})
	if err := Write(authority, manifest); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("synthetic kernel failure")
	if err := Startup(config, authority, func(endpoints []Endpoint) error {
		called = true
		current, err := Read(authority)
		if err != nil || !current.Guarded || len(endpoints) != 2 {
			t.Fatal("startup did not persist closed union first")
		}
		return failure
	}); !errors.Is(err, failure) {
		t.Fatal("installer failure hidden", err)
	}
	read, err := Read(authority)
	if err != nil || !read.Guarded {
		t.Fatal("failed startup lost guarded intent")
	}
	writeTest(t, authority, `{"version":1}`)
	called = false
	if err := Startup(config, authority, func([]Endpoint) error { called = true; return nil }); err == nil || called {
		t.Fatal("corrupt authority did not stop startup")
	}
}

func TestStartupAcceptsProtectedConfigDirectoryAndRejectsSymlinkParents(t *testing.T) {
	directory := privateDirectory(t)
	configDir, authorityDir := filepath.Join(directory, "runtime"), filepath.Join(directory, "authority")
	if err := os.Mkdir(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(authorityDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	config, authority := filepath.Join(configDir, "config.json"), filepath.Join(authorityDir, "ingress.json")
	writeTest(t, config, legacyConfig)
	if err := Startup(config, authority, func([]Endpoint) error { return nil }); err != nil {
		t.Fatal("safe existing runtime directory rejected", err)
	}
	link := filepath.Join(directory, "linked")
	if err := os.Symlink(configDir, link); err != nil {
		t.Fatal(err)
	}
	if err := Startup(filepath.Join(link, "config.json"), authority, func([]Endpoint) error { return nil }); err == nil {
		t.Fatal("symlink configuration parent accepted")
	}
	if err := os.Chmod(configDir, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := Startup(config, authority, func([]Endpoint) error { return nil }); err == nil {
		t.Fatal("writable runtime directory accepted")
	}
}

func TestShortLockSerializesAuthorityAndInstaller(t *testing.T) {
	directory := privateDirectory(t)
	config, authority := filepath.Join(directory, "config.json"), filepath.Join(directory, "ingress.json")
	writeTest(t, config, realityConfig)
	if err := Write(authority, testManifest(realityConfig)); err != nil {
		t.Fatal(err)
	}
	entered, release, second := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		if err := Startup(config, authority, func([]Endpoint) error { close(entered); <-release; return nil }); err != nil {
			t.Error(err)
		}
	}()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("startup did not reach installer")
	}
	go func() {
		defer group.Done()
		if err := WithLock(authority, func() error {
			close(second)
			manifest, err := Read(authority)
			if err != nil {
				return err
			}
			manifest.Endpoints, err = Union(manifest.Endpoints, []Endpoint{{Address: "0.0.0.0", Port: 9443}})
			if err != nil {
				return err
			}
			return Write(authority, manifest)
		}); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-second:
		t.Fatal("controller passed startup's read/install transaction")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	group.Wait()
	read, err := Read(authority)
	if err != nil || !reflect.DeepEqual(read.Endpoints, []Endpoint{{Address: "0.0.0.0", Port: 8443}, {Address: "0.0.0.0", Port: 9443}}) {
		t.Fatal("new coverage was overwritten by stale startup", err)
	}
}
