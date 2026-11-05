package api

import (
	"context"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// RealityActivityAuthority is private observation authority, never an API payload.
// Epoch binds checked runtime bytes, publication lifetime and live admission.
type RealityActivityAuthority struct {
	Epoch string
	Users map[string]int
}

type realityActivityPublisher interface {
	RealityActivityAuthority(context.Context, *store.Store) (RealityActivityAuthority, error)
}

type realityActivityResponse struct {
	State      string     `json:"state"`
	OpenFlows  *int       `json:"open_flows"`
	ObservedAt *time.Time `json:"observed_at"`
}

type activitySample struct {
	epoch  string
	at     time.Time
	counts map[string]int
}

type realityActivityCache struct {
	mu      sync.Mutex
	current string
	sample  activitySample
	until   time.Time
	flight  chan struct{}
}

const realityActivityInterval = 5 * time.Second

// observe owns at most one bounded controller read. Waiter cancellation cannot
// leave duplicate reads running, and superseded epochs cannot publish old samples.
func (c *realityActivityCache) observe(ctx context.Context, epoch string, now func() time.Time, load func() (map[string]int, error)) activitySample {
	for {
		c.mu.Lock()
		c.current = epoch
		if c.sample.epoch == epoch && now().Before(c.until) {
			out := c.sample
			c.mu.Unlock()
			return out
		}
		if c.flight != nil {
			done := c.flight
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return activitySample{}
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.flight = done
		c.mu.Unlock()
		counts, err := load()
		at := now()
		c.mu.Lock()
		if c.current == epoch {
			c.sample = activitySample{epoch: epoch, at: at}
			if err == nil {
				c.sample.counts = counts
			}
			c.until = at.Add(realityActivityInterval)
		}
		c.flight = nil
		close(done)
		out := c.sample
		c.mu.Unlock()
		if out.epoch != epoch {
			return activitySample{}
		}
		return out
	}
}

func (s *Server) observeReality(ctx context.Context, peers []store.Peer) map[string]realityActivityResponse {
	out := make(map[string]realityActivityResponse)
	for _, p := range peers {
		if p.RealityEnabled {
			out[p.ID] = realityActivityResponse{State: "unavailable"}
		}
	}
	if len(out) == 0 {
		return out
	}
	pub, ok := s.applier.(realityActivityPublisher)
	if !ok {
		return out
	}
	before, err := pub.RealityActivityAuthority(ctx, s.store)
	if err != nil || before.Epoch == "" || len(before.Users) == 0 {
		return out
	}
	sample := s.realityActivity.observe(ctx, before.Epoch, s.now, func() (map[string]int, error) { return s.clash.RealityConnections(context.WithoutCancel(ctx)) })
	after, err := pub.RealityActivityAuthority(ctx, s.store)
	if err != nil || after.Epoch != before.Epoch || sample.epoch != before.Epoch || sample.counts == nil {
		return out
	}
	age := s.now().Sub(sample.at)
	if age < 0 || age > realityActivityInterval {
		return out
	}
	for _, p := range peers {
		user := store.RealityUserName(p.ID)
		if !p.Enabled || !p.RealityEnabled || after.Users[user] != p.RealityRevision {
			continue
		}
		count := sample.counts[user]
		at := sample.at.UTC()
		out[p.ID] = realityActivityResponse{State: "observed", OpenFlows: &count, ObservedAt: &at}
	}
	return out
}
