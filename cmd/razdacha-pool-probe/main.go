// Command razdacha-pool-probe performs explicit, shadow-only catalog admission.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type report struct {
	Catalog       string                       `json:"catalog"`
	Country       string                       `json:"country"`
	Counts        lists.PoolCatalogObservation `json:"counts"`
	Configuration string                       `json:"configuration"`
	ConfigMembers int                          `json:"config_members"`
	CheckerSHA256 string                       `json:"checker_sha256,omitempty"`
	Connectivity  string                       `json:"connectivity"`
	Media         string                       `json:"media"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, nil)
	stop()
	os.Exit(code)
}

// A test may inject a bounded HTTP client; production uses the shared catalog client.
func run(ctx context.Context, args []string, out, diagnostics io.Writer, client lists.HTTPDoer) int {
	fs := flag.NewFlagSet("razdacha-pool-probe", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	country := fs.String("country", "auto", "Auto or one two-letter country code")
	limit := fs.Int("limit", 100, "One catalog batch, from 1 to 240 items")
	checker := fs.String("sing-box", "", "Explicit absolute checker binary path; enables config checking only")
	timeout := fs.Duration("timeout", 45*time.Second, "Overall timeout (maximum 1 minute)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fs.SetOutput(diagnostics)
			fs.PrintDefaults()
			return 0
		}
		_, _ = fmt.Fprintln(diagnostics, "Invalid command arguments")
		return 2
	}
	if fs.NArg() != 0 || *limit < 1 || *limit > lists.APIpoolMaxItems || *timeout <= 0 || *timeout > time.Minute {
		_, _ = fmt.Fprintln(diagnostics, "Invalid arguments: limit 1..240, timeout above zero and at most 1m, no positional arguments")
		return 2
	}
	selected := strings.ToUpper(strings.TrimSpace(*country))
	if selected != "AUTO" && (len(selected) != 2 || selected[0] < 'A' || selected[0] > 'Z' || selected[1] < 'A' || selected[1] > 'Z') {
		_, _ = fmt.Fprintln(diagnostics, "Country must be Auto or one two-letter code")
		return 2
	}
	if *checker != "" && !filepath.IsAbs(*checker) {
		_, _ = fmt.Fprintln(diagnostics, "Checker path must be absolute")
		return 2
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	r := report{Catalog: "failed", Country: selected, Configuration: "not_requested", Connectivity: "not_measured", Media: "not_measured"}
	finish := func(code int) int {
		if json.NewEncoder(out).Encode(r) != nil {
			return 1
		}
		return code
	}
	u, _ := url.Parse(lists.APIpoolURL)
	q := url.Values{"protocol": {"vless,ss,trojan,hysteria2"}, "exclude_country": {"RU,BY"}}
	if selected != "AUTO" {
		q.Set("country", selected)
	}
	u.RawQuery = q.Encode()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := lists.PoolCatalog{
		Client: client, Log: quiet, Limit: *limit, Timeout: 15 * time.Second,
		Observe:  func(v lists.PoolCatalogObservation) { r.Counts = v },
		CheckKey: func(raw string) error { _, err := singbox.Parse(raw); return err },
	}
	servers, err := catalog.Servers(ctx, u.String(), store.PoolFilter{Countries: store.DefaultPoolCountryBlocklist()})
	if r.Counts.CatalogValid {
		r.Catalog = "accepted"
	}
	if err != nil {
		if r.Counts.CatalogValid {
			r.Catalog = "no_eligible_members"
		}
		return finish(1)
	}
	if *checker == "" {
		return finish(0)
	}
	r.Configuration = "rejected"
	hash, members, err := checkCandidate(ctx, servers, *checker, "")
	r.CheckerSHA256 = hash
	r.ConfigMembers = members
	if err != nil {
		return finish(1)
	}
	r.Configuration = "accepted"
	return finish(0)
}

// checkCandidate owns all files and never exposes a Prepared.Commit operation.
func checkCandidate(ctx context.Context, servers []store.PoolServer, binary, tempRoot string) (string, int, error) {
	dir, err := os.MkdirTemp(tempRoot, "razdacha-pool-probe-*")
	if err != nil {
		return "", 0, errors.New("private temporary directory unavailable")
	}
	defer func() { _ = os.RemoveAll(dir) }()
	// Copying pins exactly the bytes checked; installed configuration is never opened.
	input, err := os.Open(binary)
	if err != nil {
		return "", 0, errors.New("checker unavailable")
	}
	defer func() { _ = input.Close() }()
	info, err := input.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", 0, errors.New("checker must be a regular file")
	}
	pinned := filepath.Join(dir, "checker")
	dst, err := os.OpenFile(pinned, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return "", 0, errors.New("cannot pin checker")
	}
	digest := sha256.New()
	// A bounded copy also rejects unexpectedly large executable inputs.
	n, copyErr := io.Copy(io.MultiWriter(dst, digest), io.LimitReader(input, 256<<20+1))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil || n > 256<<20 {
		return "", 0, errors.New("cannot pin checker")
	}
	hash := hex.EncodeToString(digest.Sum(nil))
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	checked := &privateChecker{binary: pinned}
	snap := store.Snapshot{Settings: store.DefaultSettings(), Tunnels: []store.Tunnel{{ID: "probe", Name: "API shadow candidate", Type: store.TunnelVLESS, Source: store.SourcePool, Enabled: true, Pool: servers}}}
	a := singbox.Applier{ConfigPath: filepath.Join(dir, "unpublished.json"), Checker: checked, Reloader: neverReload{}, Log: quiet}
	prepared, err := a.Prepare(ctx, snap)
	if err != nil {
		return hash, 0, errors.New("candidate configuration rejected")
	}
	defer prepared.Close()
	return hash, checked.members, nil
}

type neverReload struct{}

func (neverReload) Reload(context.Context) error { return errors.New("probe cannot reload services") }

// Discard checker output before returning errors: diagnostics may contain credentials.
// Generic rejection deliberately avoids retries that would hide candidate removal.
type privateChecker struct {
	binary  string
	members int
}

func (c *privateChecker) Check(ctx context.Context, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("private candidate unavailable")
	}
	var config struct {
		Outbounds []struct {
			Tag string `json:"tag"`
		} `json:"outbounds"`
	}
	if json.Unmarshal(data, &config) != nil {
		return errors.New("private candidate invalid")
	}
	c.members = 0
	for _, o := range config.Outbounds {
		if strings.HasPrefix(o.Tag, singbox.TunnelTag("probe")+"-") {
			c.members++
		}
	}
	if c.members == 0 {
		return errors.New("candidate has no pool members")
	}
	command := exec.CommandContext(ctx, c.binary, "check", "-c", path)
	command.WaitDelay = time.Second
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if command.Run() != nil {
		return errors.New("checker rejected candidate")
	}
	return nil
}
