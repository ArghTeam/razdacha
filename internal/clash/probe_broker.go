package clash

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type probeJob struct {
	done  chan struct{}
	delay time.Duration
	err   error
}
type probeBroker struct {
	mu    sync.Mutex
	jobs  map[string]*probeJob
	slots chan struct{}
}

// Delay coalesces manual and scheduled callers without allowing cancellation to
// release capacity still occupied by a runtime job.
func (c *Client) Delay(ctx context.Context, tag string) (time.Duration, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	b := &c.broker
	b.mu.Lock()
	if b.jobs == nil {
		b.jobs = make(map[string]*probeJob)
		b.slots = make(chan struct{}, 2)
	}
	job := b.jobs[tag]
	if job == nil {
		if len(b.jobs) >= 32 {
			b.mu.Unlock()
			return 0, fmt.Errorf("%w: diagnostic queue full", ErrUnavailable)
		}
		job = &probeJob{done: make(chan struct{})}
		b.jobs[tag] = job
		go c.runProbe(tag, job)
	}
	b.mu.Unlock()
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case <-job.done:
		return job.delay, job.err
	}
}

func (c *Client) runProbe(tag string, job *probeJob) {
	b := &c.broker
	queueTimer := time.NewTimer(c.deadline)
	select {
	case b.slots <- struct{}{}:
		queueTimer.Stop()
	case <-queueTimer.C:
		job.err = fmt.Errorf("%w: diagnostic queue deadline", ErrUnavailable)
		close(job.done)
		b.mu.Lock()
		delete(b.jobs, tag)
		b.mu.Unlock()
		return
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.deadline)
	job.delay, job.err = c.delay(ctx, tag)
	cancel()
	close(job.done)
	// The pinned runtime's background job is not canceled with its HTTP request.
	if job.err != nil {
		budget := c.deadline
		if c.probe > budget {
			budget = c.probe
		}
		if remaining := budget - time.Since(started); remaining > 0 {
			time.Sleep(remaining)
		}
	}
	b.mu.Lock()
	delete(b.jobs, tag)
	b.mu.Unlock()
	<-b.slots
}
