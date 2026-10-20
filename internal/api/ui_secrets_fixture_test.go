package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// TestUISecretsFixture is opt-in and serves only disposable synthetic state.
// It does not call Server.Run, start schedulers, or initialize networking.
func TestUISecretsFixture(t *testing.T) {
	mode := os.Getenv("RAZDACHA_UI_SECRETS_FIXTURE")
	if mode == "" {
		t.Skip("opt-in synthetic browser fixture")
	}
	if mode != "local" && mode != "isolated" {
		t.Fatal("invalid fixture mode")
	}
	var root string
	if mode == "isolated" {
		if isolateUIFixture(t) {
			return
		}
		verifyUIFixtureIsolation(t)
		root = os.Getenv("RAZDACHA_UI_FIXTURE_ROOT")
		if root == "" || !filepath.IsAbs(root) {
			t.Fatal("isolated fixture needs an absolute private root")
		}
		info, err := os.Lstat(root)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			t.Fatal("fixture root must be an existing private directory")
		}
		entries, err := os.ReadDir(root)
		if err != nil || len(entries) != 0 {
			t.Fatal("isolated fixture root must be empty")
		}
	} else {
		root = t.TempDir()
	}
	defer cleanupUIFixtureFiles(t, root)
	ctx := context.Background()
	dbPath := filepath.Join(root, "synthetic-ui.db")
	f, err := os.OpenFile(dbPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			t.Errorf("close synthetic SQLite: %v", err)
		}
	}()
	if err := SetPassword(ctx, st, "synthetic-ui-password-only"); err != nil {
		t.Fatal(err)
	}
	settings := store.DefaultSettings()
	settings.EndpointHost = "synthetic.example"
	if err := st.SaveSettings(ctx, settings); err != nil {
		t.Fatal(err)
	}
	// Fixed public test strings; never copied from any deployed host.
	key := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	for i, id := range []string{"synthetic-client-a", "synthetic-client-b"} {
		_, err := st.CreatePeer(ctx, store.Peer{ID: id, Name: id, Address: fmt.Sprintf("10.8.0.%d", i+2), Enabled: true, PrivateKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune(i)), 32))), PublicKey: base64.StdEncoding.EncodeToString([]byte(strings.Repeat(string(rune(i+1)), 32))), PresharedKey: key})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, err = st.CreateTunnel(ctx, store.Tunnel{ID: "synthetic-tunnel", Name: "synthetic-tunnel", Type: store.TunnelSOCKS, Source: store.SourceJSON, Raw: `{"type":"socks","server":"192.0.2.1","server_port":1080,"username":"synthetic-user","password":"synthetic-tunnel-secret"}`, Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(ctx, Config{Store: st, ClashAddr: "127.0.0.1:0", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), ServerPublicKey: func(context.Context) (string, error) { return key, nil }, Applier: uiFixtureRejectApply{}})
	if err != nil {
		t.Fatal(err)
	}
	network, address := "tcp", "127.0.0.1:0"
	if mode == "isolated" {
		network, address = "unix", filepath.Join(root, "ui.sock")
	}
	ln, err := net.Listen(network, address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	if mode == "isolated" {
		if err := os.Chmod(address, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	stopToken := hex.EncodeToString(nonce)
	stopRequested := make(chan struct{})
	var stopOnce sync.Once
	control := uiFixtureControl(stopToken, func() { stopOnce.Do(func() { close(stopRequested) }) })
	// An allowlist prevents accidentally invoking any side-effectful product endpoint.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__fixture" || strings.HasPrefix(r.URL.Path, "/__fixture/") {
			control.ServeHTTP(w, r)
			return
		}
		allowed := uiFixtureAllowed(r.Method, r.URL.Path)
		if !allowed {
			http.Error(w, "synthetic fixture endpoint denied", http.StatusForbidden)
			return
		}
		srv.Handler().ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second}
	defer func() { _ = server.Close() }()
	shutdownDone := make(chan error, 1)
	var authorizedStop atomic.Bool
	var timedOut atomic.Bool
	go func() {
		timer := time.NewTimer(8 * time.Minute)
		defer timer.Stop()
		select {
		case <-stopRequested:
			authorizedStop.Store(true)
		case <-timer.C:
			timedOut.Store(true)
		}
		stopCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		err := server.Shutdown(stopCtx)
		if err != nil {
			_ = server.Close()
		}
		shutdownDone <- err
	}()
	ready, _ := json.Marshal(map[string]string{"event": "UI-SECRETS-FIXTURE-READY", "mode": mode, "address": ln.Addr().String(), "stop_token": stopToken})
	fmt.Println(string(ready))
	serveErr := server.Serve(ln)
	if !errors.Is(serveErr, http.ErrServerClosed) {
		t.Fatalf("synthetic fixture serve: %v", serveErr)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("synthetic fixture shutdown: %v", err)
	}
	if timedOut.Load() || !authorizedStop.Load() {
		t.Fatal("fixture deadline expired without authorized stop")
	}
}

type uiFixtureRejectApply struct{}

func (uiFixtureRejectApply) Apply(context.Context, store.Snapshot) (singbox.ApplyResult, error) {
	return singbox.ApplyResult{}, errors.New("synthetic fixture forbids apply")
}

// The test-only Linux bootstrap creates namespaces before its child mutates mounts.
// Validate the resulting isolation before opening SQLite or creating files/listeners.
func verifyUIFixtureIsolation(t *testing.T) {
	t.Helper()
	for _, name := range []string{"net", "mnt"} {
		original := os.Getenv("RAZDACHA_UI_ORIGINAL_" + strings.ToUpper(name) + "_NS")
		current, err := os.Readlink("/proc/self/ns/" + name)
		if err != nil || original == "" || original == current {
			t.Fatal("fresh namespace not proven: " + name)
		}
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		t.Fatal(err)
	}
	private, tmpfs := true, false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}
		for _, field := range fields[6:] {
			if field == "-" {
				break
			}
			if strings.HasPrefix(field, "shared:") || strings.HasPrefix(field, "master:") {
				private = false
			}
		}
		if fields[4] == "/var/lib" && strings.Contains(line, " - tmpfs ") {
			tmpfs = true
		}
	}
	if !private || !tmpfs {
		t.Fatal("private mount propagation and tmpfs /var/lib required")
	}
}

func uiFixtureAllowed(method, path string) bool {
	if method == http.MethodPost {
		return path == "/api/login" || path == "/api/logout" || path == "/api/peers" || path == "/api/tunnels/parse"
	}
	if method != http.MethodGet {
		return false
	}
	if !strings.HasPrefix(path, "/api/") && path != "/api" {
		return true
	}
	switch path {
	case "/api/session", "/api/peers", "/api/rules", "/api/tunnels", "/api/settings", "/api/lists/community", "/api/version", "/api/diag", "/api/apply/status", "/api/tunnels/synthetic-tunnel/raw":
		return true
	}
	if strings.HasPrefix(path, "/api/peers/") && strings.HasSuffix(path, "/config") {
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/peers/"), "/config")
		return id != "" && !strings.Contains(id, "/") && id != "." && id != ".."
	}
	return false
}

func TestUIFixtureRejectsUnrelatedCapabilities(t *testing.T) {
	for _, path := range []string{"/api", "/api/backup/download", "/api/logs", "/api/diag/singbox-config", "/api/unknown", "/api/apply", "/api/tunnels/warp", "/api/route/test", "/api/domain/reachability", "/api/peers/../logs/config", "/api/peers//config"} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			if uiFixtureAllowed(method, path) {
				t.Errorf("unrelated capability admitted: %s %s", method, path)
			}
		}
	}
	for _, path := range []string{"/", "/js/app.js", "/api/peers/synthetic-client-a/config", "/api/tunnels/synthetic-tunnel/raw", "/api/session"} {
		if !uiFixtureAllowed(http.MethodGet, path) {
			t.Errorf("required fixture read refused: %s", path)
		}
	}
}

func uiFixtureControl(token string, stop func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__fixture/stop" || r.URL.RawQuery != "" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "fixture control method denied", http.StatusMethodNotAllowed)
			return
		}
		if len(token) != 64 || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "fixture control authorization denied", http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		stop()
	})
}

func cleanupUIFixtureFiles(t *testing.T, root string) {
	t.Helper()
	for _, name := range []string{"ui.sock", "synthetic-ui.db-wal", "synthetic-ui.db-shm", "synthetic-ui.db"} {
		path := filepath.Join(root, name)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Errorf("inspect owned fixture file: %v", err)
			continue
		}
		expectedType := os.FileMode(0)
		if name == "ui.sock" {
			expectedType = os.ModeSocket
		}
		if info.Mode().Type() != expectedType || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			t.Errorf("unexpected fixture file identity: %s", name)
			continue
		}
		if err := os.Remove(path); err != nil {
			t.Errorf("remove owned fixture file: %v", err)
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Error("fixture root is not empty after owned cleanup")
	}
}

func TestUIFixtureControlRequiresExactAuthorization(t *testing.T) {
	token := strings.Repeat("a", 64)
	calls := 0
	handler := uiFixtureControl(token, func() { calls++ })
	for _, tc := range []struct {
		method, path, auth string
		status             int
	}{
		{http.MethodGet, "/__fixture/stop", "Bearer " + token, http.StatusMethodNotAllowed},
		{http.MethodPost, "/__fixture/unknown", "Bearer " + token, http.StatusNotFound},
		{http.MethodPost, "/__fixture", "Bearer " + token, http.StatusNotFound},
		{http.MethodPost, "/__fixture/stop?token=" + token, "Bearer " + token, http.StatusNotFound},
		{http.MethodPost, "/__fixture/stop", "", http.StatusForbidden},
		{http.MethodPost, "/__fixture/stop", "Bearer " + strings.Repeat("b", 64), http.StatusForbidden},
		{http.MethodPost, "/__fixture/stop", "Bearer " + token, http.StatusNoContent},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", tc.auth)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != tc.status {
			t.Errorf("control %s %s: got %d want %d", tc.method, tc.path, res.Code, tc.status)
		}
	}
	if calls != 1 {
		t.Errorf("stop calls = %d, want 1", calls)
	}
}
