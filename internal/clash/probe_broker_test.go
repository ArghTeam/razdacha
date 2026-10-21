package clash

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBrokerCoalescesCanceledWaiterAndRetainsCapacity(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{}, 10)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(`{"delay":12000}`))
	}))
	defer server.Close()
	c := New(Options{Addr: server.URL, ProbeTimeout: 100 * time.Millisecond, HTTPClient: &http.Client{Timeout: 200 * time.Millisecond}})
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := c.Delay(ctx, "same"); result <- err }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	secondCtx, secondCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer secondCancel()
	_, secondErr := c.Delay(secondCtx, "same")
	if !errors.Is(secondErr, context.DeadlineExceeded) {
		t.Fatal(secondErr)
	}
	close(release)

	if calls.Load() != 1 {
		t.Fatalf("backend jobs: %d", calls.Load())
	}
}

func TestBrokerHoldsUncertainBackendCapacity(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	c := New(Options{Addr: server.URL, ProbeTimeout: 100 * time.Millisecond, HTTPClient: &http.Client{Timeout: 150 * time.Millisecond}})
	for _, tag := range []string{"a", "b"} {
		_, _ = c.Delay(context.Background(), tag)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, err := c.Delay(ctx, "c")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting cancellation: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("uncertain runtime jobs released capacity early: %d", calls.Load())
	}
}

func TestBrokerBoundsPendingIdentities(t *testing.T) {
	c := New(Options{Addr: "http://127.0.0.1:1", ProbeTimeout: time.Millisecond, HTTPClient: &http.Client{Timeout: 100 * time.Millisecond}})
	c.broker.jobs = make(map[string]*probeJob)
	c.broker.slots = make(chan struct{}, 2)
	for i := 0; i < 32; i++ {
		c.broker.jobs[fmt.Sprint(i)] = &probeJob{done: make(chan struct{})}
	}
	_, err := c.Delay(context.Background(), "overflow")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("queue limit: %v", err)
	}
}

func TestDelayRejectsMissingMeasurement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	c := New(Options{Addr: server.URL, HTTPClient: &http.Client{Timeout: time.Millisecond * 50}})
	_, err := c.Delay(context.Background(), "test")
	if !errors.Is(err, ErrBadResponse) {
		t.Fatal(err)
	}
}

func TestTwelveSecondMeasurementIsAcceptedWithinBudget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("timeout") != "15000" {
			t.Errorf("runtime budget: %s", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(`{"delay":12000}`))
	}))
	defer server.Close()
	c := New(Options{Addr: server.URL})
	delay, err := c.Delay(context.Background(), "slow")
	if err != nil || delay != 12*time.Second {
		t.Fatalf("measurement %s: %v", delay, err)
	}
}
