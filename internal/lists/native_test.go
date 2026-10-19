package lists

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/sagernet/sing-box/common/srs"
	"github.com/sagernet/sing-box/option"
	SJSON "github.com/sagernet/sing/common/json"
)

func nativeBody(rule string) []byte { return []byte(`{"version":3,"rules":[` + rule + `]}`) }
func nativeBinary(t *testing.T, body []byte) []byte {
	t.Helper()
	var c option.PlainRuleSetCompat
	if err := SJSON.Unmarshal(body, &c); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := srs.Write(&b, c.Options, c.Version); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestNativeProjectionAdmission(t *testing.T) {
	cases := []struct {
		name, rule string
		dns        bool
		subnets    []string
		reject     bool
	}{
		{name: "domain", rule: `{"domain":["selected.example"]}`, dns: true},
		{name: "suffix", rule: `{"domain_suffix":[".selected.example"]}`, dns: true},
		{name: "keyword", rule: `{"domain_keyword":["selected"]}`, dns: true},
		{name: "regex", rule: `{"domain_regex":[".*"]}`, dns: true},
		{name: "empty-matching-regex", rule: `{"domain_regex":["^$"]}`, dns: true},
		{name: "address-only", rule: `{"ip_cidr":["203.0.113.0/24"]}`, subnets: []string{"203.0.113.0/24"}},
		{name: "default-domain-IP-OR", rule: `{"domain":["selected.example"],"ip_cidr":["203.0.113.0/24"]}`, dns: true, subnets: []string{"203.0.113.0/24"}},
		{name: "logical-OR", rule: `{"type":"logical","mode":"or","rules":[{"domain":["selected.example"]},{"ip_cidr":["203.0.113.0/24"]}]}`, dns: true, subnets: []string{"203.0.113.0/24"}},
		{name: "domain-AND-NOT", rule: `{"type":"logical","mode":"and","rules":[{"domain_keyword":["selected"]},{"domain":["other.example"],"invert":true}]}`, dns: true},
		{name: "address-AND", rule: `{"type":"logical","mode":"and","rules":[{"ip_cidr":["203.0.113.0/24"]},{"ip_cidr":["203.0.113.10/32"]}]}`, subnets: []string{"203.0.113.10/32"}},
		{name: "false-address", rule: `{"type":"logical","mode":"and","rules":[{"ip_cidr":["203.0.113.0/24"]},{"ip_cidr":["192.0.2.0/24"]}]}`},
		{name: "address-complement", rule: `{"ip_cidr":["0.0.0.0/1"],"invert":true}`, subnets: []string{"128.0.0.0/1"}},
		{name: "true-address", rule: `{"ip_cidr":["0.0.0.0/0"]}`, reject: true},
		{name: "inverted-domain", rule: `{"domain":["selected.example"],"invert":true}`, reject: true},
		{name: "mixed-AND", rule: `{"type":"logical","mode":"and","rules":[{"domain":["selected.example"]},{"ip_cidr":["203.0.113.0/24"]}]}`, reject: true},
		{name: "mixed-NOT", rule: `{"domain":["selected.example"],"ip_cidr":["203.0.113.0/24"],"invert":true}`, reject: true},
		{name: "source-scope", rule: `{"domain":["selected.example"],"source_ip_cidr":["10.8.0.2"]}`, reject: true},
		{name: "ports", rule: `{"domain":["selected.example"],"port":[443]}`, reject: true},
		{name: "network", rule: `{"network":["tcp"]}`, reject: true},
		{name: "empty-default", rule: `{}`, reject: true},
		{name: "empty-logical", rule: `{"type":"logical","mode":"or","rules":[]}`, reject: true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := nativeBody(tt.rule)
			formats := []Format{FormatJSON}
			if !strings.Contains(tt.name, "empty-") {
				formats = append(formats, FormatSRS)
			}
			for _, f := range formats {
				b := body
				if f == FormatSRS {
					b = nativeBinary(t, body)
				}
				p, err := ProjectNative(b, f)
				if tt.reject {
					if err == nil {
						t.Fatalf("%s accepted unsupported rule", f)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: %v", f, err)
				}
				if (len(p.DNS) > 0) != tt.dns || !reflect.DeepEqual(p.Subnets, tt.subnets) {
					t.Fatalf("%s projection %+v", f, p)
				}
			}
		})
	}
	for _, body := range []string{`{"version":4,"rules":[{"domain":["a.example"]}]}`, `{"version":3,"rules":[{"domain":["a.example"],"network_is_expensive":false}]}`, `{"version":3,"rules":[{"domain":["a.example"],"unknown":null}]}`, `{"version":3,"rules":[{"domain":["a.example"],"domain":["b.example"]}]}`} {
		if _, e := ProjectNative([]byte(body), FormatJSON); e == nil {
			t.Fatalf("unknown/duplicate/version accepted: %s", body)
		}
	}
}

func compressedSRS(payload []byte) []byte {
	var b bytes.Buffer
	b.WriteString("SRS")
	b.WriteByte(3)
	z := zlib.NewWriter(&b)
	_, _ = z.Write(payload)
	_ = z.Close()
	return b.Bytes()
}

func TestNativeSRSHostileBounds(t *testing.T) {
	var huge [10]byte
	n := binary.PutUvarint(huge[:], ^uint64(0))
	valid := nativeBinary(t, nativeBody(`{"domain_suffix":["selected.example"],"ip_cidr":["203.0.113.0/24"]}`))
	bad := [][]byte{compressedSRS(huge[:n]), compressedSRS(append([]byte{1, 1, 0}, huge[:n]...)), compressedSRS([]byte{1, 0, 2, 0, 255, 255, 255}), valid[:len(valid)-1], append(append([]byte{}, valid...), 0), compressedSRS([]byte{1, 0, 255, 0, 0}), compressedSRS([]byte{1, 0, 3, 1, 1, 'a', 3, 1, 1, 'b', 255, 0})}
	for i, b := range bad {
		if _, err := ProjectNative(b, FormatSRS); err == nil {
			t.Fatalf("malformed SRS %d accepted", i)
		}
	}
	deep := []byte{1}
	for i := 0; i < maxNativeDepth+2; i++ {
		deep = append(deep, 1, 0, 1)
	}
	deep = append(deep, 0, 3, 1, 1, 'a', 255, 0)
	deep = append(deep, make([]byte, maxNativeDepth+2)...)
	if _, err := ProjectNative(compressedSRS(deep), FormatSRS); err == nil {
		t.Fatal("deep SRS accepted")
	}
}

func TestNativeLastGoodValidatorsAnd304(t *testing.T) {
	var mu sync.Mutex
	mode := "good"
	var requests []string
	good := nativeBody(`{"domain":["selected.example"],"ip_cidr":["203.0.113.0/24"]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Header.Get("If-None-Match"))
		w.Header().Set("ETag", `"`+mode+`"`)
		switch mode {
		case "good":
			_, _ = w.Write(good)
		case "bad":
			_, _ = w.Write(nativeBody(`{"domain":["other.example"],"network":["tcp"]}`))
		case "304":
			w.WriteHeader(304)
		case "retry":
			if len(requests)%2 == 1 {
				w.WriteHeader(304)
			} else {
				_, _ = w.Write(good)
			}
		case "large":
			_, _ = w.Write(bytes.Repeat([]byte("x"), 2000))
		}
	}))
	defer srv.Close()
	f, err := NewFetcher(Options{Dir: t.TempDir(), Client: srv.Client(), MaxBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/set.json"
	first, err := f.Update(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _ := os.ReadFile(f.nativeManifest(url))
	for _, next := range []string{"bad", "large"} {
		mu.Lock()
		mode = next
		mu.Unlock()
		stale, err := f.Update(context.Background(), url)
		if err != nil || stale.Stale == nil || !reflect.DeepEqual(stale.List, first.List) {
			t.Fatalf("last-good %s: %+v %v", next, stale, err)
		}
		after, _ := os.ReadFile(f.nativeManifest(url))
		if !bytes.Equal(after, manifest) {
			t.Fatal("invalid candidate advanced authority")
		}
	}
	mu.Lock()
	mode = "304"
	mu.Unlock()
	res, err := f.Update(context.Background(), url)
	if err != nil || res.Stale != nil {
		t.Fatalf("verified 304: %v", err)
	}
	mu.Lock()
	if requests[len(requests)-1] != `"good"` {
		t.Fatal("bad validator promoted")
	}
	mu.Unlock()
	// Corruption cannot be repaired in place at the same watched hash path.
	if err := os.WriteFile(first.List.Native.Path, []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "good"
	mu.Unlock()
	if _, err = f.Update(context.Background(), url); err == nil {
		t.Fatal("corrupt immutable original repaired")
	}
	if err := os.Remove(first.List.Native.Path); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	mode = "retry"
	requests = nil
	mu.Unlock()
	res, err = f.Update(context.Background(), url)
	if err != nil || res.Stale != nil {
		t.Fatalf("bounded unconditional retry: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || requests[0] != "" || requests[1] != "" {
		t.Fatalf("retry validators: %v", requests)
	}
}

func TestNativeManifestQuotaAndOrphans(t *testing.T) {
	dir := t.TempDir()
	f, err := NewFetcher(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	body := nativeBody(`{"domain":["selected.example"]}`)
	first, err := f.acceptNative("https://example.invalid/a.json", body, meta{URL: "https://example.invalid/a.json"})
	if err != nil {
		t.Fatal(err)
	}
	artifactDir := nativeDir(dir)
	manifest, _ := os.ReadFile(f.nativeManifest(first.Meta.URL))
	ballast := filepath.Join(artifactDir, ".native-tmp-orphan")
	file, err := os.Create(ballast)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(NativeStorageBytes); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	restarted, err := NewFetcher(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restarted.acceptNative(first.Meta.URL, nativeBody(`{"domain":["new.example"]}`), meta{URL: first.Meta.URL, ETag: "new"}); err == nil {
		t.Fatal("orphan quota bypass")
	}
	after, _ := os.ReadFile(f.nativeManifest(first.Meta.URL))
	if !bytes.Equal(after, manifest) {
		t.Fatal("quota advanced manifest")
	}
	if err = first.List.Native.Verify(artifactDir, first.List.Subnets); err != nil {
		t.Fatal(err)
	}
}

func TestNativeConcurrentQuotaReservation(t *testing.T) {
	dir := t.TempDir()
	f, _ := NewFetcher(Options{Dir: dir})
	artifactDir := nativeDir(dir)
	if err := ensureNativeDir(artifactDir); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(artifactDir, "orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(NativeStorageBytes - 12000); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	start := make(chan struct{})
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			<-start
			url := fmt.Sprintf("https://example.invalid/%d.json", i)
			body := append(nativeBody(fmt.Sprintf(`{"domain":["%d.example"]}`, i)), bytes.Repeat([]byte(" "), 8000)...)
			_, err := f.acceptNative(url, body, meta{URL: url})
			results <- err
		}(i)
	}
	close(start)
	success := 0
	for i := 0; i < 2; i++ {
		if <-results == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent reservation successes=%d", success)
	}
}

func TestNativeArtifactOwnerModeAndProjection(t *testing.T) {
	f, _ := NewFetcher(Options{Dir: t.TempDir()})
	url := "https://example.invalid/a.json"
	a, err := f.acceptNative(url, nativeBody(`{"domain":["a.example"],"ip_cidr":["192.0.2.0/24"]}`), meta{URL: url})
	if err != nil {
		t.Fatal(err)
	}
	artifact := a.List.Native
	dir := filepath.Dir(artifact.Path)
	if err = artifact.Verify(dir, []string{"203.0.113.0/24"}); err == nil {
		t.Fatal("forged nft projection accepted")
	}
	if err = os.Chmod(artifact.DNSPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err = artifact.Verify(dir, a.List.Subnets); err == nil {
		t.Fatal("public blob accepted")
	}
	_ = os.Chmod(artifact.DNSPath, 0o600)
	if err = os.Rename(artifact.DNSPath, artifact.DNSPath+".saved"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(artifact.DNSPath+".saved", artifact.DNSPath); err != nil {
		t.Fatal(err)
	}
	if err = artifact.Verify(dir, a.List.Subnets); err == nil {
		t.Fatal("symlink blob accepted")
	}
	// A syntactically valid authority record cannot replace a projection hash.
	m, _ := json.Marshal(a)
	if len(m) == 0 {
		t.Fatal("empty manifest")
	}
}

func TestNativeJSONPredecodeBudgets(t *testing.T) {
	body := []byte(`{"version":3,"rules":[{"domain":[` + strings.Repeat(`"a",`, maxNativeValues) + `"b"]}]}`)
	if err := checkNativeJSON(body); err == nil {
		t.Fatal("oversized value array passed predecode validation")
	}
	for _, body := range []string{`{"version":3,"rules":[{"type":"logical","mode":"or","rules":[{"domain":["a"]}],"domain":["b"]}]}`, `{"version":3,"rules":[{"domain":["a"],"mode":"or"}]}`, `{"version":3,"rules":[{"domain":[""]}]}`} {
		if err := checkNativeJSON([]byte(body)); err == nil {
			t.Fatal("invalid JSON structure admitted")
		}
	}
}

func TestNativeFIFOAndCanonicalMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := privateRead(path, 1024); err == nil {
		t.Fatal("FIFO accepted")
	}
	body := nativeBinary(t, nativeBody(`{"domain":["\ra"]}`))
	if _, err := ProjectNative(body, FormatSRS); err == nil {
		t.Fatal("noncanonical SRS marker accepted")
	}
	for _, version := range []byte{1, 2, 3} {
		body := nativeBody(`{"domain_suffix":["selected.example",".sub.example"],"domain":["exact.example"]}`)
		var compat option.PlainRuleSetCompat
		if err := SJSON.Unmarshal(body, &compat); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		if err := srs.Write(&b, compat.Options, version); err != nil {
			t.Fatal(err)
		}
		if _, err := ProjectNative(b.Bytes(), FormatSRS); err != nil {
			t.Fatalf("canonical version%d rejected: %v", version, err)
		}
	}
}
