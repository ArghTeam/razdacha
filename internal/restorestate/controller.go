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
	defer c.mu.Unlock()
	c.start = start
	s, e := Read(c.Dir)
	if e != nil {
		return e
	}
	if s == nil || s.Phase == "complete" && !s.Held {
		if e := start(); e != nil {
			return e
		}
		c.started = true
	}
	return nil
}
func (c *Controller) Resume() error {
	if c == nil {
		return errors.New("installation has no restore hold")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := Read(c.Dir)
	if e != nil {
		return e
	}
	if s == nil || s.Phase != "complete" {
		return errors.New("incomplete restore requires local recovery")
	}
	if s.Held {
		s.Held = false
		if e = Save(c.Dir, *s); e != nil {
			return e
		}
	}
	if !c.started && c.start != nil {
		if e := c.start(); e != nil {
			return e
		}
		c.started = true
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
