package restorestate

import (
	"errors"
	"path/filepath"
	"sync"
)

// Controller serializes deliberate resume and revalidates trusted state on each action.
// Nil controllers preserve the ordinary installation contract.
type Controller struct {
	Dir string
	// StopRuntime positively stops/verifies the candidate during Abort.
	StopRuntime func() error
	mu          sync.Mutex
	start       func() error
	started     bool
	starting    chan struct{}
	startErr    error
}

func (c *Controller) Status() (*State, error) {
	if c == nil {
		return nil, nil
	}
	return Read(c.Dir)
}
func (c *Controller) Allowed() error {
	if c == nil {
		return nil
	}
	s, e := c.Status()
	if e != nil {
		return e
	}
	if s != nil && (s.Phase != "complete" || s.Held) {
		return ErrHeld
	}
	return nil
}
func (c *Controller) StartWhenAllowed(start func() error) error {
	if c == nil {
		return start()
	}
	c.mu.Lock()
	c.start = start
	c.mu.Unlock()
	if e := c.Allowed(); e != nil {
		if errors.Is(e, ErrHeld) {
			return nil
		}
		return e
	}
	return c.startExternal()
}
func (c *Controller) Resume() error {
	if c == nil {
		return errors.New("installation has no restore hold")
	}
	c.mu.Lock()
	s, e := Read(c.Dir)
	if e != nil {
		c.mu.Unlock()
		return e
	}
	if s == nil || s.Phase != "complete" {
		c.mu.Unlock()
		return errors.New("incomplete restore requires local recovery")
	}
	if s.Held {
		s.Held = false
		if e = Save(c.Dir, *s); e != nil {
			c.mu.Unlock()
			return e
		}
	}
	c.mu.Unlock()
	return c.startExternal()
}

// Never invoke acquisition callbacks while holding mu: callbacks may enter the
// publication gate, whose runtime transition takes mu in the opposite direction.
func (c *Controller) startExternal() error {
	c.mu.Lock()
	if c.started || c.start == nil {
		c.mu.Unlock()
		return nil
	}
	if wait := c.starting; wait != nil {
		c.mu.Unlock()
		<-wait
		c.mu.Lock()
		e := c.startErr
		c.mu.Unlock()
		return e
	}
	wait := make(chan struct{})
	c.starting = wait
	start := c.start
	c.mu.Unlock()
	e := start()
	c.mu.Lock()
	c.started = e == nil
	c.startErr = e
	c.starting = nil
	close(wait)
	c.mu.Unlock()
	return e
}

// Acquisition may start after persisted consent, but Apply must wait until current
// destinations/settings have been synchronized and the once-only startup finishes.
func (c *Controller) ApplyAllowed() error {
	if c == nil {
		return nil
	}
	if e := c.Allowed(); e != nil {
		return e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.start != nil && !c.started {
		return errors.New("restore external-activity startup is incomplete; retry resume before Apply")
	}
	return nil
}

// BeginPublishing is called only after explicit Apply installed/synced checked config.
// It never edits systemd drop-ins; the daemon may write only installation markers.
func (c *Controller) BeginPublishing(config string) error { return c.beginPublishing(config) }

// FinishPublication is called under the daemon publication gate and lifecycle lock.
// Raw record decoding is intentional: interrupted marker transitions fail strict Read.
func (c *Controller) FinishPublication(success bool) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := privateRecord(c.Dir)
	if e != nil {
		return e
	}
	if s.Phase != "publishing" {
		return nil
	}
	if !success {
		if e = WritePrivate(filepath.Join(c.Dir, Runtime), []byte("1\n")); e != nil {
			return e
		}
		if e = revokePermit(c.Dir); e != nil {
			return e
		}
		if c.StopRuntime == nil {
			return errors.New("restore Abort requires positive runtime stop verification")
		}
		if e = c.StopRuntime(); e != nil {
			return e
		}
		s.RuntimeInhibited = true
	} else {
		if e = revokePermit(c.Dir); e != nil {
			return e
		}
	}
	s.Phase = "complete"
	s.Publication = ""
	s.ConfigHash = ""
	return Save(c.Dir, *s)
}

func (c *Controller) PublicationLock() (*Lock, error) {
	if c == nil {
		return nil, nil
	}
	return Acquire(c.Dir, -1)
}
