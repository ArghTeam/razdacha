package restorestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// SavedFile records a fixed destination file in a private current-destination set.
type SavedFile struct {
	Target  string `json:"target"`
	Name    string `json:"name"`
	Hash    string `json:"sha256"`
	Missing bool   `json:"missing"`
}
type Recovery struct {
	Version         int             `json:"version"`
	Complete        bool            `json:"complete"`
	Files           []SavedFile     `json:"files"`
	DatabasePresent bool            `json:"database_present"`
	Services        json.RawMessage `json:"services"`
}

func CaptureFile(dir, target string) (SavedFile, error) {
	result := SavedFile{Target: target, Name: fmt.Sprintf("file-%x", sha256.Sum256([]byte(target)))}
	i, e := os.Lstat(target)
	if errors.Is(e, os.ErrNotExist) {
		result.Missing = true
		return result, nil
	}
	if e != nil {
		return result, e
	}
	if !i.Mode().IsRegular() || i.Mode().Perm()&0o077 != 0 {
		return result, errors.New("recovery source must be a private regular file")
	}
	b, e := ReadPrivate(target, 256<<20)
	if e != nil {
		return result, e
	}
	sum := sha256.Sum256(b)
	result.Hash = hex.EncodeToString(sum[:])
	return result, WritePrivate(filepath.Join(dir, result.Name), b)
}
func SaveRecovery(dir string, r Recovery) error {
	r.Version = Protocol
	b, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return WritePrivate(filepath.Join(dir, "recovery.json"), b)
}
func ReadRecovery(dir string) (Recovery, error) {
	var r Recovery
	b, e := ReadPrivate(filepath.Join(dir, "recovery.json"), 4<<20)
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	if r.Version != Protocol || !r.Complete {
		return r, errors.New("current-destination recovery set is incomplete")
	}
	for _, f := range r.Files {
		if filepath.Base(f.Name) != f.Name || !filepath.IsAbs(f.Target) {
			return r, errors.New("invalid recovery path")
		}
		if f.Missing {
			continue
		}
		b, e := ReadPrivate(filepath.Join(dir, f.Name), 256<<20)
		if e != nil {
			return r, e
		}
		h := sha256.Sum256(b)
		if hex.EncodeToString(h[:]) != f.Hash {
			return r, errors.New("recovery file hash mismatch")
		}
	}
	return r, nil
}
