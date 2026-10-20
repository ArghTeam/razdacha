package restorestate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	r, e := ReadRecoveryMetadata(dir)
	if e != nil {
		return r, e
	}
	if !r.Complete {
		return r, errors.New("current-destination recovery set is incomplete")
	}
	return r, nil
}

// ReadRecoveryMetadata also admits the initial incomplete metadata set. Its caller
// must not treat unknown database presence as verified absence until Complete=true.
func ReadRecoveryMetadata(dir string) (Recovery, error) {
	var r Recovery
	b, e := ReadPrivate(filepath.Join(dir, "recovery.json"), 4<<20)
	if e != nil {
		return r, e
	}
	var wire struct {
		Version         int               `json:"version"`
		Complete        bool              `json:"complete"`
		Files           []json.RawMessage `json:"files"`
		DatabasePresent bool              `json:"database_present"`
		Services        json.RawMessage   `json:"services"`
	}
	if e = decodeRequired(b, []string{"version", "complete", "files", "database_present", "services"}, &wire); e != nil {
		return r, e
	}
	if wire.Version != Protocol || len(wire.Files) == 0 || len(wire.Files) > 8192 {
		return r, errors.New("invalid current-destination recovery manifest")
	}
	var services map[string]json.RawMessage
	if e = decodeRequired(wire.Services, []string{"razdachad.service", "sing-box.service"}, &services); e != nil {
		return r, e
	}
	for _, raw := range services {
		var unit struct{ Active, Enabled string }
		if e = decodeRequired(raw, []string{"Active", "Enabled"}, &unit); e != nil {
			return r, e
		}
		if unit.Active != "active" && unit.Active != "inactive" && unit.Active != "failed" {
			return r, errors.New("invalid recovery service state")
		}
		if unit.Enabled != "enabled" && unit.Enabled != "disabled" {
			return r, errors.New("invalid recovery enable intent")
		}
	}
	r = Recovery{Version: wire.Version, Complete: wire.Complete, DatabasePresent: wire.DatabasePresent, Services: wire.Services}
	seen := make(map[string]bool)
	for _, raw := range wire.Files {
		var f SavedFile
		if e = decodeRequired(raw, []string{"target", "name", "sha256", "missing"}, &f); e != nil {
			return r, e
		}
		if !filepath.IsAbs(f.Target) || filepath.Clean(f.Target) != f.Target || seen[f.Target] || len(f.Name) != 69 || !strings.HasPrefix(f.Name, "file-") {
			return r, errors.New("invalid or duplicate recovery path")
		}
		seen[f.Target] = true
		if _, e = hex.DecodeString(strings.TrimPrefix(f.Name, "file-")); e != nil {
			return r, e
		}
		if f.Missing {
			if f.Hash != "" {
				return r, errors.New("missing recovery source has content hash")
			}
		} else {
			if len(f.Hash) != 64 {
				return r, errors.New("invalid recovery hash")
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
		r.Files = append(r.Files, f)
	}
	return r, nil
}

// RecoveryState permits a serialized local recovery to inspect interrupted marker
// transitions; it never grants startup/callback authority or ignores record corruption.
func RecoveryState(dir string) (*State, error) { return privateRecord(dir) }

func PreviousHold(stateDir, operationDir string, r Recovery) (bool, error) {
	for _, f := range r.Files {
		if f.Target == filepath.Join(stateDir, File) {
			if f.Missing {
				return false, nil
			}
			b, e := ReadPrivate(filepath.Join(operationDir, f.Name), 64<<10)
			if e != nil {
				return false, e
			}
			var prior State
			if e = decodeState(b, &prior); e != nil {
				return false, e
			}
			if e = prior.Validate(); e != nil {
				return false, e
			}
			return prior.Held, nil
		}
	}
	return false, errors.New("recovery set lacks prior hold metadata")
}
