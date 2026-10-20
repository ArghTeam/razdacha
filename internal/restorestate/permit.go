package restorestate

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
)

const (
	PermitFile       = "restore-runtime-permit.json"
	permitLifetimeNS = int64(60 * 1e9)
)

type KernelFacts struct {
	BootID       string
	PID          int
	ProcessStart string
	NowNS        int64
}
type Permit struct {
	Version      int    `json:"version"`
	Operation    string `json:"operation"`
	Publication  string `json:"publication"`
	ConfigHash   string `json:"config_sha256"`
	BootID       string `json:"boot_id"`
	PID          int    `json:"daemon_pid"`
	ProcessStart string `json:"process_start"`
	GrantedNS    int64  `json:"granted_ns"`
	DeadlineNS   int64  `json:"deadline_ns"`
}

func configHash(path string) (string, error) {
	b, e := ReadPrivate(path, 256<<20)
	if e != nil {
		return "", e
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (c *Controller) beginPublishing(config string) error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, e := Read(c.Dir)
	if e != nil {
		return e
	}
	if s == nil {
		return nil
	}
	if s.Phase != "complete" || s.Held {
		return ErrHeld
	}
	if !s.RuntimeInhibited {
		return nil
	}
	hash, e := configHash(config)
	if e != nil {
		return e
	}
	facts, e := CurrentKernelFacts()
	if e != nil {
		return e
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	s.Phase = "publishing"
	s.RuntimeInhibited = false
	s.Publication = hex.EncodeToString(nonce)
	s.ConfigHash = hash
	if e = Save(c.Dir, *s); e != nil {
		return e
	}
	permit := Permit{Version: Protocol, Operation: s.Operation, Publication: s.Publication, ConfigHash: hash, BootID: facts.BootID, PID: facts.PID, ProcessStart: facts.ProcessStart, GrantedNS: facts.NowNS, DeadlineNS: facts.NowNS + permitLifetimeNS}
	b, e := json.Marshal(permit)
	if e != nil {
		return e
	}
	if e = WritePrivate(filepath.Join(c.Dir, PermitFile), b); e != nil {
		return e
	}
	return Remove(filepath.Join(c.Dir, Runtime))
}

// RuntimeStartAllowed is local metadata-only independent service-start enforcement.
func RuntimeStartAllowed(dir, config string) error {
	facts, e := CurrentKernelFacts()
	if e != nil {
		return e
	}
	return runtimeStartAllowed(dir, config, facts, ProcessIdentityAlive)
}

func runtimeStartAllowed(dir, config string, facts KernelFacts, alive func(int, string) bool) error {
	s, e := Read(dir)
	if e != nil {
		return e
	}
	if s == nil {
		return nil
	}
	if s.Held || s.Phase == "preparing" || s.Phase == "recovering" || s.RuntimeInhibited {
		return ErrHeld
	}
	if s.Phase == "complete" {
		return nil
	}
	if s.Phase != "publishing" {
		return errors.New("runtime start denied for restore phase")
	}
	b, e := ReadPrivate(filepath.Join(dir, PermitFile), 64<<10)
	if e != nil {
		return e
	}
	var p Permit
	fields := []string{"version", "operation", "publication", "config_sha256", "boot_id", "daemon_pid", "process_start", "granted_ns", "deadline_ns"}
	if e = decodeRequired(b, fields, &p); e != nil {
		return e
	}
	if p.Version != Protocol || p.Operation != s.Operation || p.Publication != s.Publication || p.ConfigHash != s.ConfigHash || p.BootID == "" || p.BootID != facts.BootID || p.PID <= 0 || p.ProcessStart == "" || p.GrantedNS < 0 || p.DeadlineNS <= p.GrantedNS || p.DeadlineNS-p.GrantedNS > permitLifetimeNS || facts.NowNS < p.GrantedNS || facts.NowNS > p.DeadlineNS || !alive(p.PID, p.ProcessStart) {
		return errors.New("runtime start permit is invalid, stale or expired")
	}
	hash, e := configHash(config)
	if e != nil {
		return e
	}
	if hash != p.ConfigHash {
		return errors.New("runtime start config differs from checked publication")
	}
	return nil
}

// Revoking a permit blocks future starts; callers separately stop/verify live runtime.
func revokePermit(dir string) error { return Remove(filepath.Join(dir, PermitFile)) }

func privateRecord(dir string) (*State, error) {
	b, e := ReadPrivate(filepath.Join(dir, File), 64<<10)
	if e != nil {
		return nil, e
	}
	var s State
	if e = decodeState(b, &s); e != nil {
		return nil, e
	}
	return &s, s.Validate()
}
