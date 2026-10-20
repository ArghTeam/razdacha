package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/restorestate"
	"github.com/ArghTeam/razdacha/internal/store"
)

func heldAPI(t *testing.T, ts *testServer) *restorestate.Controller {
	t.Helper()
	dir := t.TempDir()
	if e := os.Chmod(dir, 0o700); e != nil {
		t.Fatal(e)
	}
	s := restorestate.State{Version: restorestate.Protocol, Operation: "fixture", Recovery: filepath.Join(dir, "recovery"), Phase: "complete", Held: true, RuntimeInhibited: true}
	if e := restorestate.WritePrivate(filepath.Join(dir, restorestate.Runtime), []byte("1\n")); e != nil {
		t.Fatal(e)
	}
	if e := restorestate.Save(dir, s); e != nil {
		t.Fatal(e)
	}
	ctl := &restorestate.Controller{Dir: dir}
	ts.restore = ctl
	return ctl
}

func TestRestoreHoldGatesActualProbeAndBackupCallbacksUntilExplicitResume(t *testing.T) {
	ctx := context.Background()
	ts := newTestServer(t)
	cookie := ts.login(t)
	setupTelegram(t, ts, cookie)
	sender := &fakeSender{}
	withSender(ts, sender)
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup", `{"enabled":true,"interval_hours":6,"passphrase":"fixture-backup-passphrase"}`), http.StatusOK)
	tunnel, e := ts.st.CreateTunnel(ctx, store.Tunnel{Name: "Fixture local proxy", Type: store.TunnelSOCKS, Source: store.SourceURL, Raw: "socks://127.0.0.1:19080", Parsed: json.RawMessage(`{"server":"127.0.0.1","server_port":19080}`), Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	var probes atomic.Int32
	withClash(t, ts, func(w http.ResponseWriter, r *http.Request) { probes.Add(1); _, _ = w.Write([]byte(`{"delay":7}`)) })
	ctl := heldAPI(t, ts)
	starts := 0
	if e = ctl.StartWhenAllowed(func() error { starts++; return nil }); e != nil {
		t.Fatal(e)
	}
	var lastTry time.Time
	ts.backupRound(ctx, &lastTry)
	if e = ts.refreshChecks(ctx); e == nil {
		t.Fatal("held scheduled check admitted")
	}
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/tunnels/" + tunnel.ID + "/check", ""},
		{http.MethodPost, "/api/tunnels/" + tunnel.ID + "/refresh", ""},
		{http.MethodPost, "/api/tunnels/warp", "{}"},
		{http.MethodPost, "/api/notify/test", "{}"},
		{http.MethodPost, "/api/backup/send", ""},
		{http.MethodPost, "/api/domain/reachability", `{"domain":"fixture.invalid"}`},
		{http.MethodPost, "/api/route/test", `{"domain":"fixture.invalid"}`},
		{http.MethodPost, "/api/apply", ""},
	} {
		requireCode(t, ts.auth(t, cookie, route.method, route.path, route.body), http.StatusConflict)
	}
	for _, path := range []string{"/api/settings", "/api/restore", "/api/lists/community", "/api/backup/download"} {
		requireCode(t, ts.auth(t, cookie, http.MethodGet, path, ""), http.StatusOK)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPatch, "/api/settings", `{}`), http.StatusOK)
	if len(sender.docs) != 0 || probes.Load() != 0 || starts != 0 {
		t.Fatal("held API/scheduler/save contacted a synthetic sink or started acquisition")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/restore/resume", `{"destination_reviewed":true}`), http.StatusBadRequest)
	if e = ctl.Allowed(); e == nil {
		t.Fatal("missing acknowledgment cleared hold")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/restore/resume", `{"destination_reviewed":true,"external_activity_acknowledged":true}`), http.StatusOK)
	if starts != 1 {
		t.Fatal("resume did not start acquisition exactly once")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/restore/resume", `{"destination_reviewed":true,"external_activity_acknowledged":true}`), http.StatusOK)
	if starts != 1 {
		t.Fatal("idempotent resume duplicated workers")
	}
	ts.backupRound(ctx, &lastTry)
	if len(sender.docs) != 1 {
		t.Fatal("positive backup sink control did not observe resumed delivery")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/tunnels/"+tunnel.ID+"/check", ""), http.StatusOK)
	if probes.Load() == 0 {
		t.Fatal("positive probe sink control did not observe resumed check")
	}
	persisted, e := restorestate.Read(ctl.Dir)
	if e != nil || persisted.Held || !persisted.RuntimeInhibited {
		t.Fatal("resume did not persist independently of runtime activation")
	}
}

func TestRestoreHoldResumeRequiresSessionAndPersistence(t *testing.T) {
	ts := newTestServer(t)
	ctl := heldAPI(t, ts)
	cookie := ts.login(t)
	starts := 0
	if e := ctl.StartWhenAllowed(func() error { starts++; return nil }); e != nil {
		t.Fatal(e)
	}
	requireCode(t, ts.do(t, request{method: http.MethodPost, path: "/api/restore/resume", body: `{"destination_reviewed":true,"external_activity_acknowledged":true}`}), http.StatusUnauthorized)
	if e := os.Remove(filepath.Join(ctl.Dir, restorestate.Runtime)); e != nil {
		t.Fatal(e)
	}
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/restore/resume", `{"destination_reviewed":true,"external_activity_acknowledged":true}`), http.StatusConflict)
	if starts != 0 {
		t.Fatal("uncertain persisted safety state started callbacks")
	}
}

func TestRestoreResumeUsesCurrentBackupAndNotificationRecipients(t *testing.T) {
	ctx := context.Background()
	ts := newTestServer(t)
	cookie := ts.login(t)
	requireCode(t, saveNotify(t, ts, cookie, `{"enabled":true,"token":"100:synthetic-old-token","chat_id":"100"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup", `{"enabled":true,"interval_hours":6,"passphrase":"synthetic-old-backup-phrase"}`), http.StatusOK)
	ctl := heldAPI(t, ts)
	oldSink, newSink := &fakeSender{}, &fakeSender{}
	ts.notify = func(config store.NotifyConfig) notifySender {
		if config.Token == "200:synthetic-current-token" && config.ChatID == "200" {
			return newSink
		}
		return oldSink
	}
	requireCode(t, saveNotify(t, ts, cookie, `{"enabled":true,"token":"200:synthetic-current-token","chat_id":"200"}`), http.StatusOK)
	requireCode(t, ts.auth(t, cookie, http.MethodPut, "/api/backup", `{"passphrase":"synthetic-current-backup-phrase"}`), http.StatusOK)
	var last time.Time
	ts.backupRound(ctx, &last)
	if len(oldSink.docs)+len(newSink.docs)+len(oldSink.sent)+len(newSink.sent) != 0 {
		t.Fatal("held saved edit contacted a recipient")
	}
	if e := ctl.Resume(); e != nil {
		t.Fatal(e)
	}
	ts.backupRound(ctx, &last)
	requireCode(t, ts.auth(t, cookie, http.MethodPost, "/api/notify/test", ""), http.StatusOK)
	if len(oldSink.docs) != 0 || len(oldSink.sent) != 0 || len(newSink.docs) != 1 || len(newSink.sent) != 1 {
		t.Fatal("resume used stale recipients or positive current recipient missing")
	}
	if _, e := store.DecryptBackup(newSink.docs[0].data, "synthetic-current-backup-phrase"); e != nil {
		t.Fatalf("current saved backup phrase not used: %v", e)
	}
	if _, e := store.DecryptBackup(newSink.docs[0].data, "synthetic-old-backup-phrase"); e == nil {
		t.Fatal("stale imported backup phrase used")
	}
}
