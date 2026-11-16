package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

func TestRealityActivityCoalescesAndExpires(t *testing.T) {
	var cache realityActivityCache
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	var reads atomic.Int32
	load := func() (map[string]clash.RealityConnectionObservation, error) {
		if reads.Add(1) == 1 {
			close(started)
		}
		<-release
		return map[string]clash.RealityConnectionObservation{"user": {OpenFlows: 2}}, nil
	}
	var wg sync.WaitGroup
	results := make(chan activitySample, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- cache.observe(context.Background(), "a", func() time.Time { return now }, load)
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	for out := range results {
		if out.counts["user"].OpenFlows != 2 || out.epoch != "a" {
			t.Fatal(out)
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("concurrent reads=%d", reads.Load())
	}
	now = now.Add(6 * time.Second)
	cache.observe(context.Background(), "a", func() time.Time { return now }, load)
	if reads.Load() != 2 {
		t.Fatal("expired observation reused")
	}
}

func TestRealityActivityLateEpochCannotPublish(t *testing.T) {
	var cache realityActivityCache
	now := time.Now
	started := make(chan struct{})
	release := make(chan struct{})
	oldDone := make(chan activitySample)
	go func() {
		oldDone <- cache.observe(context.Background(), "old", now, func() (map[string]clash.RealityConnectionObservation, error) {
			close(started)
			<-release
			return map[string]clash.RealityConnectionObservation{"old": {OpenFlows: 1}}, nil
		})
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cache.observe(ctx, "new", now, func() (map[string]clash.RealityConnectionObservation, error) {
		t.Error("parallel controller read")
		return nil, nil
	})
	close(release)
	if got := <-oldDone; got.counts != nil {
		t.Fatal("superseded epoch published")
	}
	got := cache.observe(context.Background(), "new", now, func() (map[string]clash.RealityConnectionObservation, error) {
		return map[string]clash.RealityConnectionObservation{"new": {OpenFlows: 1}}, nil
	})
	if got.counts["new"].OpenFlows != 1 || got.counts["old"].OpenFlows != 0 {
		t.Fatal(got)
	}
}

type activityAuthorityApplier struct {
	authority func() (RealityActivityAuthority, error)
}

func (a activityAuthorityApplier) Apply(context.Context, store.Snapshot) (singbox.ApplyResult, error) {
	return singbox.ApplyResult{}, nil
}

func (a activityAuthorityApplier) RealityActivityAuthority(context.Context, *store.Store) (RealityActivityAuthority, error) {
	return a.authority()
}

func TestRealityActivityRequiresMatchingAuthorityBeforeAndAfterPoll(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	user := store.RealityUserName(id)
	peers := []store.Peer{{ID: id, Enabled: true, RealityEnabled: true, RealityRevision: 3}}
	for _, change := range []string{"none", "epoch", "revision", "guarded"} {
		t.Run(change, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"connections":[{"id":"flow","metadata":{"type":"vless/reality-in","sourceIP":"192.0.2.4"},"upload":11,"download":29,"rule":"inbound=reality-in auth_user=` + user + ` => route(direct-out)"}]}`))
			}))
			defer srv.Close()
			s := &Server{now: time.Now, clash: clash.New(clash.Options{Addr: srv.URL})}
			s.applier = activityAuthorityApplier{authority: func() (RealityActivityAuthority, error) {
				n := calls.Add(1)
				a := RealityActivityAuthority{Epoch: "one", Users: map[string]int{user: 3}}
				if n > 1 {
					switch change {
					case "epoch":
						a.Epoch = "two"
					case "revision":
						a.Users[user] = 4
					case "guarded":
						return RealityActivityAuthority{}, errors.New("guarded")
					}
				}
				return a, nil
			}}
			got := s.observeReality(context.Background(), peers)[id]
			if change == "none" {
				if got.State != "observed" || got.OpenFlows == nil || *got.OpenFlows != 1 || got.ObservedAt == nil || got.OpenUploadBytes == nil || *got.OpenUploadBytes != 11 || got.OpenDownloadBytes == nil || *got.OpenDownloadBytes != 29 {
					t.Fatal(got)
				}
			} else if got.State != "unavailable" || got.OpenFlows != nil || got.ObservedAt != nil || got.OpenUploadBytes != nil || got.OpenDownloadBytes != nil {
				t.Fatal("stale authority published", got)
			}
		})
	}
}

func TestRealityActivityEmptySnapshotZeroRequiresLiveAdmission(t *testing.T) {
	const id = "00000000-0000-4000-8000-000000000001"
	user := store.RealityUserName(id)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"connections":[]}`))
	}))
	defer srv.Close()
	for _, admitted := range []bool{true, false} {
		s := &Server{now: time.Now, clash: clash.New(clash.Options{Addr: srv.URL})}
		s.applier = activityAuthorityApplier{authority: func() (RealityActivityAuthority, error) {
			users := map[string]int{}
			if admitted {
				users[user] = 3
			}
			return RealityActivityAuthority{Epoch: "one", Users: users}, nil
		}}
		for _, enabled := range []bool{true, false} {
			got := s.observeReality(context.Background(), []store.Peer{{ID: id, Enabled: enabled, RealityEnabled: true, RealityRevision: 3}})[id]
			if admitted && enabled {
				if got.State != "observed" || got.OpenFlows == nil || *got.OpenFlows != 0 || got.OpenUploadBytes == nil || *got.OpenUploadBytes != 0 || got.OpenDownloadBytes == nil || *got.OpenDownloadBytes != 0 {
					t.Fatalf("fresh admitted empty snapshot not a measured zero: %+v", got)
				}
			} else if got.State != "unavailable" || got.OpenFlows != nil || got.OpenUploadBytes != nil || got.OpenDownloadBytes != nil {
				t.Fatalf("unadmitted/disabled absence became a measured zero: %+v", got)
			}
		}
	}
}
