package lists

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestManagerPlainRetentionFollowsBothOwners(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = fmt.Fprint(w, "example.com\n192.0.2.0/24\n") }))
	defer srv.Close()
	m := NewManager(ManagerOptions{Fetcher: newFetcher(t, srv), Logger: quietSlog()})
	var committed Source
	for i := range 30 {
		pending := Source{URL: fmt.Sprintf("%s/%d.txt", srv.URL, i), Format: FormatPlain}
		m.SetSources([]Source{pending})
		if err := m.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		if committed.URL != "" {
			// Pending replacement and failed publication leave the old accepted owner.
			if _, ok := m.List(committed.URL); !ok {
				t.Fatal("pending edit evicted committed source")
			}
			if len(m.Snapshot()) != 2 || len(m.States()) != 2 {
				t.Fatal("pending/committed union is not bounded")
			}
		}
		m.SetCommittedSources([]Source{pending})
		// A stale reconciliation can still name the old accepted owner. The new
		// independently pinned owner must survive until reconciliation catches up.
		m.SetSources([]Source{committed})
		if _, ok := m.List(pending.URL); !ok {
			t.Fatal("stale scheduler removed newly committed source")
		}
		m.SetSources([]Source{pending})
		if len(m.Snapshot()) != 1 || len(m.States()) != 1 {
			t.Fatal("retired plain contents/status accumulated")
		}
		// Constant-URL reconciliation must not invalidate a healthy refresh.
		if err := m.Refresh(context.Background()); err != nil {
			t.Fatal(err)
		}
		committed = pending
	}
	// Retiring memory must preserve disk cache and native artifact retention.
	body := m.fetcher.cache.bodyPath(committed.URL)
	nativeURL := srv.URL + "/native.json"
	m.mu.Lock()
	m.lists[nativeURL] = List{Domains: []string{"native.example"}}
	m.states[nativeURL] = SourceState{URL: nativeURL, Cached: true}
	m.mu.Unlock()
	m.SetSources(nil)
	m.SetCommittedSources(nil)
	if _, err := os.Stat(body); err != nil {
		t.Fatal("retirement changed disk cache:", err)
	}
	if len(m.Snapshot()) != 1 || len(m.States()) != 1 {
		t.Fatal("native retention changed")
	}
	if _, ok := m.List(nativeURL); !ok {
		t.Fatal("native authority removed")
	}
}

func TestManagerRetiredInflightIncarnationCannotPublish(t *testing.T) {
	for _, outcome := range []string{"success", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
					if outcome == "failure" {
						http.Error(w, "fixture failure", 500)
						return
					}
					_, _ = fmt.Fprint(w, "old.example\n")
					return
				}
				_, _ = fmt.Fprint(w, "new.example\n")
			}))
			defer srv.Close()
			m := NewManager(ManagerOptions{Fetcher: newFetcher(t, srv), Logger: quietSlog()})
			src := []Source{{URL: srv.URL + "/list.txt", Format: FormatPlain}}
			m.SetSources(src)
			done := make(chan error, 1)
			go func() { done <- m.Refresh(context.Background()) }()
			<-started
			m.SetSources(nil)
			if len(m.Snapshot()) != 0 || len(m.States()) != 0 {
				t.Fatal("removed source retained")
			}
			m.SetSources(src)
			if err := m.Refresh(context.Background()); err != nil {
				t.Fatal(err)
			}
			before := m.States()
			close(release)
			<-done
			got, ok := m.List(src[0].URL)
			if !ok || !reflect.DeepEqual(got.Domains, []string{"new.example"}) || !reflect.DeepEqual(before, m.States()) {
				t.Fatal("retired incarnation overwrote current contents/status")
			}
			// Removal without re-add also rejects a completed old incarnation.
			m.SetSources(nil)
			if len(m.Snapshot()) != 0 || len(m.States()) != 0 {
				t.Fatal("retired source resurrected")
			}
		})
	}
}

func TestManagerInflightOwnershipTransitions(t *testing.T) {
	for _, transition := range []string{"removed", "unchanged", "committed"} {
		t.Run(transition, func(t *testing.T) {
			started, release := make(chan struct{}), make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				close(started)
				<-release
				_, _ = fmt.Fprint(w, "fixture.example\n")
			}))
			defer srv.Close()
			m := NewManager(ManagerOptions{Fetcher: newFetcher(t, srv), Logger: quietSlog()})
			src := []Source{{URL: srv.URL, Format: FormatPlain}}
			m.SetSources(src)
			done := make(chan error, 1)
			go func() { done <- m.Refresh(context.Background()) }()
			<-started
			switch transition {
			case "removed":
				m.SetSources(nil)
			case "unchanged":
				m.SetSources(src)
			case "committed":
				m.SetCommittedSources(src)
				m.SetSources(nil)
			}
			close(release)
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			want := 1
			if transition == "removed" {
				want = 0
			}
			if len(m.Snapshot()) != want || len(m.States()) != want {
				t.Fatalf("in-flight completion violated %s ownership", transition)
			}
		})
	}
}
