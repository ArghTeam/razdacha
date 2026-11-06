package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

type gatedSettingsPublisher struct {
	Applier
	RealityPublisher
	mu          sync.Mutex
	beforeMerge func()
	saved       store.Settings
}

func (p *gatedSettingsPublisher) PatchSettings(ctx context.Context, st *store.Store, merge func(store.Settings) store.Settings) (store.Settings, store.Settings, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.beforeMerge != nil {
		p.beforeMerge()
		p.beforeMerge = nil
	}
	before, after, err := st.UpdateSettings(ctx, merge)
	p.saved = after
	return before, after, err
}

func TestSettingsPatchMergesFreshStateAndReturnsSavedSettings(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	ctx := context.Background()
	auth, err := ts.st.PasswordState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(tokenBytes)
	notify := store.NotifyConfig{Token: token}
	if err := ts.st.SaveNotifyConfig(ctx, notify); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	pub := &gatedSettingsPublisher{beforeMerge: func() { close(entered); <-release }}
	ts.applier = pub
	done := make(chan response, 1)
	go func() { done <- ts.auth(t, cookie, http.MethodPatch, "/api/settings", `{"log_level":"error"}`) }()
	<-entered
	// Change a field after the HTTP handler has decoded the partial body and before
	// the publisher merges it. An eager handler read would now overwrite this edit.
	current, err := ts.st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	current.DNSUpstream = "9.9.9.9"
	current.DNSType = "dot"
	current.WGListenPort = 51821
	current.WGPool = "10.9.0.0/24"
	current.WGServerAddress = "10.9.0.1"
	current.EndpointHost = "gateway.example"
	current.ClientMTU = 1300
	current.WANInterface = "eth9"
	current.ListUpdateInterval = 2 * time.Hour
	current.PoolUpdateInterval = 2 * time.Hour
	current.TunnelCheckInterval = 3 * time.Minute
	current.PoolCountryBlocklist = []string{}
	current.RealityListenPort = 9443
	current.RealityServerName = "cover.example"
	current.RealityHandshakeServer = "127.0.0.2"
	current.RealityHandshakePort = 444
	if err := ts.st.SaveSettings(ctx, current); err != nil {
		t.Fatal(err)
	}
	close(release)
	result := <-done
	requireCode(t, result, http.StatusOK)
	var out settingsResponse
	decodeJSONBody(t, result, &out)
	if out.DNSUpstream != "9.9.9.9" || out.LogLevel != "error" {
		t.Fatal("response lost a concurrent omitted field")
	}
	saved, err := ts.st.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	expected := current
	expected.LogLevel = "error"
	if !reflect.DeepEqual(saved, expected) {
		t.Fatal("partial save changed an omitted settings section")
	}
	if strings.Contains(result.body, token) || strings.Contains(result.body, auth.Hash) {
		t.Fatal("settings response disclosed authentication material")
	}
	if !reflect.DeepEqual(saved, pub.saved) || !reflect.DeepEqual(newSettingsResponse(saved, ""), out) {
		t.Fatal("response differs from actual save")
	}
	roundtrip, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/settings", string(roundtrip)), http.StatusOK)
	after, err := ts.st.PasswordState(ctx)
	if err != nil || !reflect.DeepEqual(after, auth) {
		t.Fatal("settings roundtrip changed authentication", err)
	}
	savedNotify, err := ts.st.NotifyConfig(ctx)
	if err != nil || savedNotify.Token != token {
		t.Fatal("settings roundtrip changed omitted notification secret", err)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodGet, "/api/settings", ""), http.StatusOK)
}
