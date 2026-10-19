package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
)

func TestPlainListsGenerationIsImmutable(t *testing.T) {
	var next atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if next.Load() {
			_, _ = w.Write([]byte("new.example\n203.0.113.0/24\n"))
			return
		}
		_, _ = w.Write([]byte("old.example\n192.0.2.0/24\n"))
	}))
	defer srv.Close()
	f, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Logger: quietSlog()})
	if err != nil {
		t.Fatal(err)
	}
	m := lists.NewManager(lists.ManagerOptions{Fetcher: f, Logger: quietSlog()})
	m.SetSources([]lists.Source{{URL: srv.URL, Format: lists.FormatPlain}})
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	generation := plainLists(m)
	next.Store(true)
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, ok := generation(srv.URL)
	if !ok || len(got.Domains) != 1 || got.Domains[0] != "old.example" || len(got.Subnets) != 1 || got.Subnets[0] != "192.0.2.0/24" {
		t.Fatalf("a captured generation changed during refresh: %+v", got)
	}
}
