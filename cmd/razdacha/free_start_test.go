package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"
)

type onboardingTTY struct {
	io.Reader
	bytes.Buffer
}

func (tty *onboardingTTY) Read(p []byte) (int, error) { return tty.Reader.Read(p) }

func TestFreeStartChoice(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		enabled     bool
		country     string
	}{
		{"yes_auto", "yes\n\n", true, "AUTO"}, {"yes_country", "y\n de \n", true, "DE"}, {"skip", "n\n", false, ""}, {"default", "\n", false, ""}, {"eof", "", false, ""}, {"country_eof", "yes\n", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tty := &onboardingTTY{Reader: strings.NewReader(tc.input)}
			got, err := chooseFreeStart(true, setupOptions{}, tty)
			if err != nil || got.enabled != tc.enabled || (tc.enabled && got.country != tc.country) {
				t.Fatalf("choice %+v %v", got, err)
			}
			if !strings.Contains(tty.String(), "third-party") {
				t.Fatal("missing disclosure")
			}
		})
	}
	got, err := chooseFreeStart(true, setupOptions{}, nil)
	if err != nil || got.enabled {
		t.Fatal("noninteractive unexpectedly enabled")
	}
	tty := &onboardingTTY{Reader: strings.NewReader("yes\nsecret-country\n")}
	if _, err := chooseFreeStart(true, setupOptions{}, tty); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid country accepted or leaked")
	}
}

func TestFreeStartFlagsEnvironmentAndUpgradeIsolation(t *testing.T) {
	t.Setenv("RAZDACHA_FREE_START", "1")
	t.Setenv("RAZDACHA_FREE_COUNTRY", "de")
	opts, err := parseSetupFlags(nil)
	if err != nil || !opts.freeStart || !opts.freeStartSet || opts.freeCountry != "de" {
		t.Fatalf("env: %+v %v", opts, err)
	}
	choice, err := chooseFreeStart(true, opts, nil)
	if err != nil || !choice.enabled || choice.country != "DE" {
		t.Fatalf("env choice: %+v %v", choice, err)
	}
	if _, err := chooseFreeStart(false, opts, nil); err == nil {
		t.Fatal("upgrade enabled onboarding")
	}
	opts, err = parseSetupFlags([]string{"--free-start=false", "--free-country=auto"})
	if err != nil || opts.freeStart || opts.freeCountry != "auto" {
		t.Fatal("explicit flags did not override env")
	}
	t.Setenv("RAZDACHA_FREE_START", "secret-invalid")
	if _, err := parseSetupFlags(nil); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal("invalid environment accepted or exposed")
	}
	if _, err := parseSetupFlags([]string{"--free-start=true"}); err != nil {
		t.Fatal("explicit flag did not override invalid env")
	}
	tty := &onboardingTTY{Reader: strings.NewReader("yes\nDE\n")}
	if choice, err := chooseFreeStart(false, setupOptions{}, tty); err != nil || choice.enabled || tty.Len() != 0 {
		t.Fatal("upgrade prompted")
	}
}

type onboardingDoer func(*http.Request) (*http.Response, error)

func (f onboardingDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }

const onboardingKey = "vless://00000000-0000-4000-8000-000000000001@edge.example:443?security=tls&type=tcp"

func onboardingClient(t *testing.T, catalogFailure bool) lists.HTTPDoer {
	t.Helper()
	var native bytes.Buffer
	set := option.PlainRuleSet{Rules: []option.HeadlessRule{{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{DomainSuffix: badoption.Listable[string]{"video.example"}}}}}
	if err := srs.Write(&native, set, C.RuleSetVersion2); err != nil {
		t.Fatal(err)
	}
	return onboardingDoer(func(req *http.Request) (*http.Response, error) {
		var body []byte
		switch {
		case strings.HasSuffix(req.URL.Path, "/api/v1/keys"):
			if catalogFailure {
				return nil, errors.New("secret-provider-failure")
			}
			body = []byte(`{"items":[{"uri":"` + onboardingKey + `","country":"DE"}]}`)
		case strings.HasSuffix(req.URL.Path, ".srs"):
			body = native.Bytes()
		default:
			body = []byte("203.0.113.0/24\n")
		}
		return &http.Response{StatusCode: 200, ContentLength: int64(len(body)), Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
}

type blockingOnboardingTTY struct{ *io.PipeReader }

func (*blockingOnboardingTTY) Write(p []byte) (int, error) { return len(p), nil }

func TestFreeStartPromptCancellation(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	defer reader.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	choice, err := chooseFreeStartCancelable(ctx, true, setupOptions{}, &blockingOnboardingTTY{reader})
	if !errors.Is(err, context.DeadlineExceeded) || choice.enabled {
		t.Fatalf("canceled prompt: %+v %v", choice, err)
	}
}

func TestFreeStartAcquisitionFailurePrecedesDatabaseAndSkipHasNoRequests(t *testing.T) {
	calls := 0
	client := onboardingDoer(func(req *http.Request) (*http.Response, error) {
		calls++
		if strings.HasSuffix(req.URL.Path, "/api/v1/keys") {
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"items":[]}`))}, nil
		}
		return nil, errors.New("synthetic failure")
	})
	db := filepath.Join(t.TempDir(), "state.db")
	if _, err := prepareFreeStart(context.Background(), freeStartChoice{}, db, client); err != nil || calls != 0 {
		t.Fatal("skip contacted sources")
	}
	opts := setupOptions{dbPath: db, freeStart: true, freeStartSet: true, freeCountry: "auto", freeClient: client}
	if _, err := setup(context.Background(), opts); err == nil {
		t.Fatal("failed source accepted")
	}
	if _, err := os.Stat(db); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source failure created database")
	}
	if err := os.WriteFile(db, []byte("existing database sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := calls
	if _, err := setup(context.Background(), opts); err == nil || calls != before {
		t.Fatal("upgrade fetched onboarding data")
	}
	data, _ := os.ReadFile(db)
	if string(data) != "existing database sentinel" {
		t.Fatal("upgrade modified existing database")
	}
}

func TestFreeStartFrozenBootstrapAndFirstPeerScope(t *testing.T) {
	for _, catalogFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "admitted", true: "unavailable"}[catalogFailure], func(t *testing.T) {
			ctx := context.Background()
			db := filepath.Join(t.TempDir(), "state.db")
			prepared, err := prepareFreeStart(ctx, freeStartChoice{enabled: true, country: "AUTO"}, db, onboardingClient(t, catalogFailure))
			if err != nil {
				t.Fatal(err)
			}
			st, err := store.Open(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			peer, _, err := ensureFirstPeer(ctx, st, "first", slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			if err := installFreeStart(ctx, st, peer, prepared); err != nil {
				t.Fatal(err)
			}
			snap, err := st.Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(snap.Tunnels) != 1 || !snap.Tunnels[0].Builtin || !snap.Tunnels[0].Enabled || snap.Tunnels[0].Raw != prepared.catalog {
				t.Fatal("wrong pool")
			}
			if (len(snap.Tunnels[0].Pool) == 0) != catalogFailure {
				t.Fatal("catalog failure silently fell back")
			}
			if len(snap.Rules) != 1 || snap.Rules[0].PeerScope != store.ScopeSelected || len(snap.Rules[0].PeerIDs) != 1 || snap.Rules[0].PeerIDs[0] != peer.ID {
				t.Fatal("starter rule scope broadened")
			}
			for _, key := range snap.Rules[0].CommunityLists {
				if key == "google_ai" || key == "cloudflare" || key == "digitalocean" {
					t.Fatal("unsupported starter expansion")
				}
			}
			if err := verifySetupLists(snap, prepared.contents, db); err != nil {
				t.Fatal(err)
			}
			if len(prepared.contents) != len(lists.Sources(snap)) || len(prepared.contents) == 0 {
				t.Fatal("bootstrap sources incomplete")
			}
			opts, _, err := singbox.GenerateWithDiag(snap, frozenSetupLists(prepared.contents))
			if err != nil {
				t.Fatal(err)
			}
			config, err := singbox.Marshal(opts)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(config, []byte("video.example")) && !bytes.Contains(config, []byte("dns_path")) && !bytes.Contains(config, []byte("native")) {
				t.Fatal("frozen native source not represented")
			}
			path := filepath.Join(filepath.Dir(db), networkstate.BootstrapFile)
			seed := networkstate.Generation{Version: 4, Kind: networkstate.BootstrapKind, Policy: snap, Lists: prepared.contents}
			if err := networkstate.Write(path, seed); err != nil {
				t.Fatal(err)
			}
			read, err := networkstate.Read(path, networkstate.BootstrapKind)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySetupLists(read.Policy, read.Lists, db); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(prepared.contents)
			restored, _ := json.Marshal(read.Lists)
			if !bytes.Equal(encoded, restored) {
				t.Fatal("bootstrap source authority changed")
			}
			delete(prepared.contents, lists.Sources(snap)[0].URL)
			if err := verifySetupLists(snap, prepared.contents, db); err == nil {
				t.Fatal("missing frozen source accepted")
			}
		})
	}
}
