package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

const testKey = "vless://00000000-0000-4000-8000-000000000001@edge.example:443?security=tls&type=tcp"

type testDoer func(*http.Request) (*http.Response, error)

func (f testDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunRedactsAndSeparatesStages(t *testing.T) {
	body := `{"items":[{"uri":"` + testKey + `#secret-remark","country":"DE"},{"uri":"` + strings.Replace(testKey, "type=tcp", "type=xhttp", 1) + `","country":"DE"},{"uri":"` + testKey + `&different=1","country":"RU"}]}`
	var output, diagnostics bytes.Buffer
	client := testDoer(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("for_russia") != "" || r.URL.Query().Get("country") != "DE" {
			t.Fatal("unexpected selector")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
	})
	if code := run(context.Background(), []string{"--country", "DE"}, &output, &diagnostics, client); code != 0 {
		t.Fatalf("exit %d: %s", code, output.String())
	}
	var r report
	if json.Unmarshal(output.Bytes(), &r) != nil || r.Catalog != "accepted" || r.Configuration != "not_requested" || r.Counts.ParserRejected != 1 || r.Counts.AdmissionRejected != 1 || r.Counts.Eligible != 1 || r.Connectivity != "not_measured" {
		t.Fatalf("report: %s", output.String())
	}
	if strings.Contains(output.String()+diagnostics.String(), "secret") || strings.Contains(output.String(), "vless://") {
		t.Fatal("secret exposed")
	}
}

func TestRunEmptyInvalidAndHelpNeverInvokeNetwork(t *testing.T) {
	calls := 0
	client := testDoer(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[]}`))}, nil
	})
	for _, args := range [][]string{{"--limit", "secret-value"}, {"--secret-flag"}, {"--country", "secret-country"}, {"--timeout", "2m"}, {"--sing-box", "relative"}, {"--help"}} {
		var out, diagnostics bytes.Buffer
		run(context.Background(), args, &out, &diagnostics, client)
		if calls != 0 || strings.Contains(out.String()+diagnostics.String(), "secret-") {
			t.Fatal("invalid/help leaked or invoked network")
		}
	}
	var out bytes.Buffer
	if run(context.Background(), nil, &out, io.Discard, client) != 1 || calls != 1 || !strings.Contains(out.String(), `"catalog":"no_eligible_members"`) {
		t.Fatalf("empty report %s", out.String())
	}
}

func TestCheckCandidatePrivateCleanupAndNoPublication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic executable requires POSIX shell")
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "accepted", true: "rejected"}[fail], func(t *testing.T) {
			root := t.TempDir()
			binary := filepath.Join(t.TempDir(), "synthetic-checker")
			script := "#!/bin/sh\nset -eu\ntest \"$1\" = check\ntest \"$2\" = -c\ntest -f \"$3\"\ntest ! -e \"$(dirname \"$3\")/unpublished.json\"\n"
			if fail {
				script += "echo 'secret-checker-diagnostic' >&2\nexit 1\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			hash, members, err := checkCandidate(context.Background(), []store.PoolServer{{URL: testKey}}, binary, root)
			if (err != nil) != fail || len(hash) != 64 {
				t.Fatalf("check: %d %v", members, err)
			}
			if !fail && members != 1 {
				t.Fatalf("member count %d", members)
			}
			if err != nil && strings.Contains(err.Error(), "secret") {
				t.Fatal("diagnostic leaked")
			}
			entries, err := os.ReadDir(root)
			if err != nil || len(entries) != 0 {
				t.Fatal("private files survived")
			}
		})
	}
}

func TestCheckCandidateConfigPermissions(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(t.TempDir(), "checker")
	// The checker itself verifies owner-only candidate and parent permissions.
	script := "#!/bin/sh\nset -eu\ncase \"$(LC_ALL=C ls -ld \"$3\")\" in -rw-------*) ;; *) exit 1;; esac\ncase \"$(LC_ALL=C ls -ld \"$(dirname \"$3\")\")\" in drwx------*) ;; *) exit 1;; esac\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := checkCandidate(context.Background(), []store.PoolServer{{URL: testKey}}, binary, root); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitLocalChecker(t *testing.T) {
	binary := os.Getenv("RAZDACHA_POOL_PROBE_TEST_CHECKER")
	if binary == "" {
		t.Skip("explicit local checker not selected")
	}
	if hash, members, err := checkCandidate(context.Background(), []store.PoolServer{{URL: testKey}}, binary, t.TempDir()); err != nil || members != 1 || len(hash) != 64 {
		t.Fatalf("local checker: members=%d error=%v", members, err)
	}
}

func TestCanceledCheckerCleansPrivateFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(t.TempDir(), "blocking-checker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := checkCandidate(ctx, []store.PoolServer{{URL: testKey}}, binary, root); err == nil {
		t.Fatal("canceled checker accepted")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("cancellation retained private files")
	}
}
