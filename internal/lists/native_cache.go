package lists

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// NativeStorageBytes bounds all retained native artifacts, manifests and crash leftovers.
const NativeStorageBytes int64 = 256 << 20

// NativeStorageFiles also bounds inventory scanning and retained file count.
const NativeStorageFiles = 4096

// NativeArtifact pins immutable original and exact DNS bytes in a generation.
type NativeArtifact struct {
	Version int    `json:"projection_version"`
	Format  Format `json:"format"`
	Hash    string `json:"original_hash"`
	Path    string `json:"original_path"`
	DNSHash string `json:"dns_hash,omitempty"`
	DNSPath string `json:"dns_path,omitempty"`
}

type nativeAccepted struct {
	Version int  `json:"version"`
	Meta    meta `json:"meta"`
	List    List `json:"list"`
}

// One daemon owns acquisition. This process-wide lock also serializes independently
// constructed fetchers and accounts crash-orphan/temp files on every reservation.
var nativeStorageMu sync.Mutex

func digest(body []byte) string        { s := sha256.Sum256(body); return hex.EncodeToString(s[:]) }
func nativeDir(cacheDir string) string { return filepath.Join(cacheDir, "native") }
func hashOK(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && strings.ToLower(s) == s
}

func controlled(info os.FileInfo, dir bool) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	mode := os.FileMode(0o600)
	if dir {
		mode = 0o700
	}
	return ok && st.Uid == uint32(os.Geteuid()) && info.Mode().Perm() == mode && ((dir && info.IsDir()) || (!dir && info.Mode().IsRegular()))
}

func ensureNativeDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !controlled(info, true) {
		return errors.New("native artifact directory must be private and owner-controlled")
	}
	return nil
}

func privateRead(path string, max int64) ([]byte, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !controlled(info, false) || info.Size() > max {
		return nil, errors.New("native artifact must be a bounded private owner-controlled regular file")
	}
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, errors.New("native artifact size limit")
	}
	return body, nil
}

func (a NativeArtifact) Verify(dir string, subnets []string, transport ...[]string) error {
	var transportSubnets []string
	if len(transport) > 0 {
		transportSubnets = transport[0]
	}
	if (a.Version != 1 && a.Version != ProjectionVersion) || (a.Format != FormatSRS && a.Format != FormatJSON) || !hashOK(a.Hash) {
		return errors.New("invalid native artifact authority")
	}
	expected := filepath.Join(dir, a.Hash+"."+string(a.Format))
	if !filepath.IsAbs(dir) || a.Path != expected {
		return errors.New("native artifact path outside owned content store")
	}
	if err := ensureNativeDir(dir); err != nil {
		return err
	}
	original, err := privateRead(a.Path, defaultMaxBytes)
	if err != nil {
		return err
	}
	if digest(original) != a.Hash {
		return errors.New("native original hash mismatch")
	}
	projection, err := projectNativeVersion(original, a.Format, a.Version)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(projection.Subnets, subnets) || !reflect.DeepEqual(projection.TransportSubnets, transportSubnets) {
		return errors.New("native subnet projection mismatch")
	}
	if len(projection.DNS) == 0 {
		if a.DNSHash != "" || a.DNSPath != "" {
			return errors.New("unexpected native DNS artifact")
		}
		return nil
	}
	if !hashOK(a.DNSHash) || a.DNSPath != filepath.Join(dir, a.DNSHash+".dns.json") {
		return errors.New("invalid native DNS authority")
	}
	body, err := privateRead(a.DNSPath, defaultMaxBytes)
	if err != nil {
		return err
	}
	if digest(body) != a.DNSHash || !bytes.Equal(body, projection.DNS) {
		return errors.New("native DNS projection mismatch")
	}
	return nil
}

func (f *Fetcher) nativeManifest(rawURL string) string {
	return filepath.Join(nativeDir(f.cache.dir), f.cache.key(rawURL)+".accepted.json")
}

func (f *Fetcher) accepted(rawURL string) (nativeAccepted, error) {
	var out nativeAccepted
	body, err := privateRead(f.nativeManifest(rawURL), defaultMaxBytes)
	if err != nil {
		return out, err
	}
	if err = json.Unmarshal(body, &out); err != nil {
		return out, err
	}
	if (out.Version != 1 && out.Version != 2) || (out.Version == 1 && out.List.Native != nil && out.List.Native.Version != 1) || out.Meta.URL != rawURL || out.List.Native == nil || out.List.Native.Format != FormatOf(rawURL) {
		return out, errors.New("invalid accepted native manifest")
	}
	if err = out.List.Native.Verify(nativeDir(f.cache.dir), out.List.Subnets, out.List.TransportSubnets); err != nil {
		return out, err
	}
	return out, nil
}

// NativeQuota includes accepted manifests, immutable blobs and crash leftovers.
// Reserve the full candidate+temporary manifest footprint before the first write.
func nativeQuota(dir string, addBytes int64, addFiles int) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	entries, err := d.ReadDir(NativeStorageFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if len(entries)+addFiles > NativeStorageFiles {
		return errors.New("native artifact file quota exhausted")
	}
	total := addBytes
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > NativeStorageBytes-total {
			return errors.New("native artifact storage quota exhausted or unsafe entry")
		}
		total += info.Size()
	}
	if total > NativeStorageBytes {
		return errors.New("native artifact storage quota exhausted")
	}
	return nil
}

func syncNativeDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	return f.Sync()
}

func writeNativeFile(path string, body []byte, immutable bool) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".native-tmp-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err = f.Chmod(0o600); err == nil {
		_, err = f.Write(body)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if immutable {
		err = os.Link(f.Name(), path)
	} else {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return err
	}
	return syncNativeDir(dir)
}

func (f *Fetcher) acceptNative(rawURL string, body []byte, m meta) (nativeAccepted, error) {
	projection, err := ProjectNative(body, FormatOf(rawURL))
	if len(projection.DNS) > defaultMaxBytes {
		return nativeAccepted{}, errors.New("native DNS projection size limit")
	}
	if err != nil {
		return nativeAccepted{}, err
	}
	dir, err := filepath.Abs(nativeDir(f.cache.dir))
	if err != nil {
		return nativeAccepted{}, err
	}
	a := NativeArtifact{Version: ProjectionVersion, Format: FormatOf(rawURL), Hash: digest(body)}
	a.Path = filepath.Join(dir, a.Hash+"."+string(a.Format))
	blobs := map[string][]byte{a.Path: body}
	if len(projection.DNS) > 0 {
		a.DNSHash = digest(projection.DNS)
		a.DNSPath = filepath.Join(dir, a.DNSHash+".dns.json")
		blobs[a.DNSPath] = projection.DNS
	}
	accepted := nativeAccepted{Version: 2, Meta: m, List: List{Subnets: projection.Subnets, TransportSubnets: projection.TransportSubnets, Native: &a}}
	manifest, err := json.Marshal(accepted)
	if err != nil {
		return accepted, err
	}
	nativeStorageMu.Lock()
	defer nativeStorageMu.Unlock()
	if err = ensureNativeDir(dir); err != nil {
		return accepted, err
	}
	// Existing hash paths must verify; corruption is never repaired in place.
	newBlobs := map[string][]byte{}
	reserve := int64(len(manifest))
	files := 1
	for path, data := range blobs {
		existing, e := privateRead(path, defaultMaxBytes)
		switch {
		case errors.Is(e, os.ErrNotExist):
			newBlobs[path] = data
			reserve += int64(len(data))
			files++
		case e != nil:
			return accepted, e
		case !bytes.Equal(existing, data):
			return accepted, errors.New("immutable native artifact corruption")
		}
	}
	if err = nativeQuota(dir, reserve, files); err != nil {
		return accepted, err
	}
	for path, data := range newBlobs {
		if err = writeNativeFile(path, data, true); err != nil {
			return accepted, err
		}
	}
	if err = writeNativeFile(f.nativeManifest(rawURL), manifest, false); err != nil {
		return accepted, err
	}
	return accepted, nil
}

func (f *Fetcher) fetchNative(ctx context.Context, rawURL string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()
	prev, prevErr := f.accepted(rawURL)
	res := Result{URL: rawURL, Format: FormatOf(rawURL)}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return res, err
		}
		req.Header.Set("User-Agent", userAgent)
		if attempt == 0 && prevErr == nil {
			req.Header.Set("If-None-Match", prev.Meta.ETag)
			req.Header.Set("If-Modified-Since", prev.Meta.LastModified)
		}
		resp, err := f.client.Do(req)
		if err != nil {
			return res, fmt.Errorf("fetch native source: %w", err)
		}
		if resp.StatusCode == http.StatusNotModified {
			_ = resp.Body.Close()
			verified, e := f.accepted(rawURL)
			if e == nil && attempt == 0 && prevErr == nil && reflect.DeepEqual(prev, verified) {
				return Result{URL: rawURL, Format: res.Format, Status: StatusNotModified, Path: prev.List.Native.Path, Size: prev.Meta.Size, FetchedAt: time.Now().UTC()}, nil
			}
			prevErr = errors.New("304 without verified native body")
			continue
		}
		if resp.StatusCode != http.StatusOK {
			_ = resp.Body.Close()
			return res, fmt.Errorf("%w: native source HTTP %d", ErrBadResponse, resp.StatusCode)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
		_ = resp.Body.Close()
		if readErr != nil {
			return res, fmt.Errorf("%w: native body read: %w", ErrBadResponse, readErr)
		}
		if len(body) == 0 || int64(len(body)) > f.maxBytes || (resp.ContentLength >= 0 && resp.ContentLength != int64(len(body))) {
			return res, fmt.Errorf("%w: empty, oversized or truncated native body", ErrBadResponse)
		}
		m := meta{URL: rawURL, ETag: resp.Header.Get("ETag"), LastModified: resp.Header.Get("Last-Modified"), FetchedAt: time.Now().UTC(), Size: int64(len(body))}
		accepted, err := f.acceptNative(rawURL, body, m)
		if err != nil {
			return res, err
		}
		return Result{URL: rawURL, Format: res.Format, Status: StatusUpdated, Path: accepted.List.Native.Path, Size: m.Size, FetchedAt: m.FetchedAt}, nil
	}
	return res, fmt.Errorf("%w: unconditional native retry did not return verified body", ErrBadResponse)
}

func (f *Fetcher) updateNative(ctx context.Context, rawURL string) (Refreshed, error) {
	res, fetchErr := f.fetchNative(ctx, rawURL)
	accepted, err := f.accepted(rawURL)
	if err != nil {
		if fetchErr != nil {
			return Refreshed{}, fetchErr
		}
		return Refreshed{}, err
	}
	out := Refreshed{List: accepted.List, FetchedAt: res.FetchedAt, Stale: fetchErr}
	return out, nil
}
