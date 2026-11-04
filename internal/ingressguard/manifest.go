package ingressguard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

const (
	// Version is independent of the committed network-generation envelope version.
	Version          = 1
	maxManifestBytes = 64 << 10
	maxConfigBytes   = 64 << 20
)

// Manifest is non-secret safety authority. ConfigHashes admits the exact current
// and prepared configurations while Endpoints retains their union. Guarded records
// durable closed intent; the caller also treats a failed release as uncertainty.
type Manifest struct {
	Version      int        `json:"version"`
	Endpoints    []Endpoint `json:"endpoints"`
	ConfigHashes []string   `json:"config_hashes"`
	Guarded      bool       `json:"guarded"`
}

// HashConfig hashes the complete checked bytes, without normalizing their meaning.
func HashConfig(config []byte) string {
	sum := sha256.Sum256(config)
	return hex.EncodeToString(sum[:])
}

func normalizeManifest(manifest Manifest) (Manifest, error) {
	if manifest.Version != Version {
		return Manifest{}, errors.New("unsupported public ingress manifest version")
	}
	var err error
	manifest.Endpoints, err = NormalizeEndpoints(manifest.Endpoints)
	if err != nil {
		return Manifest{}, err
	}
	if len(manifest.ConfigHashes) == 0 || len(manifest.ConfigHashes) > 8 {
		return Manifest{}, errors.New("public ingress manifest requires bounded configuration hashes")
	}
	hashes := make([]string, 0, len(manifest.ConfigHashes))
	seen := make(map[string]bool)
	for _, hash := range manifest.ConfigHashes {
		decoded, err := hex.DecodeString(hash)
		if err != nil || len(decoded) != sha256.Size || len(hash) != sha256.Size*2 {
			return Manifest{}, errors.New("invalid public ingress configuration hash")
		}
		canonical := hex.EncodeToString(decoded)
		if !seen[canonical] {
			seen[canonical] = true
			hashes = append(hashes, canonical)
		}
	}
	sort.Strings(hashes)
	manifest.ConfigHashes = hashes
	return manifest, nil
}

// Read accepts only a private regular owner-controlled manifest, with strict JSON.
func Read(path string) (Manifest, error) {
	data, err := readPrivate(path, maxManifestBytes)
	if err != nil {
		return Manifest{}, fmt.Errorf("read public ingress manifest: %w", err)
	}
	if err := uniqueJSON(data); err != nil {
		return Manifest{}, fmt.Errorf("decode public ingress manifest: %w", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return Manifest{}, errors.New("public ingress manifest must be a JSON object")
	}
	for _, name := range []string{"version", "endpoints", "config_hashes", "guarded"} {
		if _, exists := fields[name]; !exists {
			return Manifest{}, errors.New("public ingress manifest has missing fields")
		}
	}
	guarded := bytes.TrimSpace(fields["guarded"])
	if !bytes.Equal(guarded, []byte("true")) && !bytes.Equal(guarded, []byte("false")) {
		return Manifest{}, errors.New("public ingress guarded marker must be a boolean")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode public ingress manifest: %w", err)
	}
	return normalizeManifest(manifest)
}

// Write atomically publishes and syncs a private manifest. It deliberately does
// not acquire WithLock: callers combine manifest and kernel operations in one
// short transaction and release the lock before starting the runtime.
func Write(path string, manifest Manifest) error {
	manifest, err := normalizeManifest(manifest)
	if err != nil {
		return err
	}
	if err := privateParent(path); err != nil {
		return err
	}
	if _, err := os.Lstat(path); err == nil {
		if _, err := readPrivate(path, maxManifestBytes); err != nil {
			return fmt.Errorf("validate replaced public ingress manifest: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".ingress-manifest-*")
	if err != nil {
		return fmt.Errorf("prepare public ingress manifest: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return fmt.Errorf("publish public ingress manifest: %w", err)
	}
	return syncDir(directory)
}

func privateParent(path string) error {
	return controlledParent(path, true)
}

func controlledParent(path string, private bool) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("public ingress paths must be clean and absolute")
	}
	directory := filepath.Dir(path)
	for current := directory; ; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("public ingress path has a non-directory or symlink parent")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return errors.New("cannot establish public ingress path ownership")
		}
		if current == directory && (owner.Uid != uint32(os.Geteuid()) || (private && info.Mode().Perm() != 0o700)) {
			return errors.New("public ingress directory must be private and owner-controlled")
		}
		if info.Mode().Perm()&0o022 != 0 && !(info.Mode()&os.ModeSticky != 0 && owner.Uid == 0) {
			return errors.New("public ingress path has a writable ancestor")
		}
		if current == string(filepath.Separator) {
			break
		}
	}
	return nil
}

func readPrivate(path string, limit int64) ([]byte, error) {
	return readControlled(path, limit, true)
}

func readControlled(path string, limit int64, private bool) ([]byte, error) {
	if err := controlledParent(path, private); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || owner.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("public ingress file must be a private owner-controlled regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("public ingress file exceeds size limit")
	}
	return data, nil
}

func syncDir(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}

// uniqueJSON prevents ambiguous duplicate fields in either authority or config.
func uniqueJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return errors.New("public ingress JSON nesting exceeds limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, compound := token.(json.Delim)
		if !compound {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]bool)
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate or invalid JSON object field")
				}
				seen[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("public ingress JSON has trailing content")
	}
	return nil
}
