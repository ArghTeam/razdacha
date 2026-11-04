package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

type (
	freeStartChoice struct {
		enabled bool
		country string
	}
	freeStartPrepared struct {
		catalog     string
		members     []store.PoolServer
		contents    map[string]lists.List
		observation lists.PoolCatalogObservation
	}
)

func freeStartEnvironment(opts *setupOptions, lookup func(string) (string, bool)) error {
	if !opts.freeStartSet {
		if raw, ok := lookup("RAZDACHA_FREE_START"); ok {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				return errors.New("RAZDACHA_FREE_START must be true/false or 1/0")
			}
			opts.freeStart, opts.freeStartSet = value, true
		}
	}
	if !opts.freeCountrySet {
		if value, ok := lookup("RAZDACHA_FREE_COUNTRY"); ok {
			opts.freeCountry, opts.freeCountrySet = value, true
		}
	}
	return nil
}

func normalizeFreeCountry(raw string) (string, error) {
	country := strings.ToUpper(strings.TrimSpace(raw))
	if country == "" || country == "AUTO" {
		return "AUTO", nil
	}
	if len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z' {
		return "", errors.New("free-start country must be Auto or one two-letter code")
	}
	return country, nil
}

func chooseFreeStart(fresh bool, opts setupOptions, tty io.ReadWriter) (freeStartChoice, error) {
	if !fresh {
		if (opts.freeStartSet && opts.freeStart) || opts.freeCountrySet {
			return freeStartChoice{}, errors.New("free-start onboarding is only available on a fresh installation; existing policy was not changed")
		}
		return freeStartChoice{}, nil
	}
	country, err := normalizeFreeCountry(opts.freeCountry)
	if err != nil {
		return freeStartChoice{}, err
	}
	choice := freeStartChoice{enabled: opts.freeStart, country: country}
	if opts.freeStartSet || tty == nil {
		return choice, nil
	}
	access := "The HTTPS panel is public and password-protected before VPN connection.\n"
	if opts.publicSet && !opts.public {
		access = "Private panel mode was explicitly selected; connect the VPN before opening it.\n"
	}
	if _, err := fmt.Fprint(tty, access); err != nil {
		return freeStartChoice{}, err
	}
	if _, err := fmt.Fprint(tty, "Optional free start uses third-party exits without a usability guarantee.\nEnable free tunnels and selective starter rules for the first client? [y/N]: "); err != nil {
		return freeStartChoice{}, err
	}
	reader := bufio.NewReader(io.LimitReader(tty, 4096))
	line, err := reader.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return freeStartChoice{}, nil
	}
	if err != nil {
		return freeStartChoice{}, errors.New("free-start terminal read failed")
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		choice.enabled = true
	default:
		return freeStartChoice{}, nil
	}
	if opts.freeCountrySet {
		return choice, nil
	}
	if _, err := fmt.Fprint(tty, "Exit country [Auto], or one two-letter code: "); err != nil {
		return freeStartChoice{}, err
	}
	line, err = reader.ReadString('\n')
	if errors.Is(err, io.EOF) {
		return freeStartChoice{}, nil
	}
	if err != nil {
		return freeStartChoice{}, errors.New("free-start terminal read failed")
	}
	choice.country, err = normalizeFreeCountry(line)
	return choice, err
}

func selectFreeStart(ctx context.Context, fresh bool, opts setupOptions) (freeStartChoice, error) {
	if err := ctx.Err(); err != nil {
		return freeStartChoice{}, err
	}
	if !fresh || opts.freeStartSet || !opts.prompt {
		return chooseFreeStart(fresh, opts, nil)
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return chooseFreeStart(fresh, opts, nil)
	}
	defer func() { _ = tty.Close() }()
	return chooseFreeStartCancelable(ctx, fresh, opts, tty)
}

// Only the owned terminal is closed on cancellation, never piped installer stdin.
func chooseFreeStartCancelable(ctx context.Context, fresh bool, opts setupOptions, tty io.ReadWriteCloser) (freeStartChoice, error) {
	if err := ctx.Err(); err != nil {
		return freeStartChoice{}, err
	}
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = tty.Close(); close(done) })
	defer func() {
		if !stop() {
			<-done
		}
	}()
	choice, err := chooseFreeStart(fresh, opts, tty)
	if ctx.Err() != nil {
		return freeStartChoice{}, ctx.Err()
	}
	return choice, err
}

func freeStartPreset() []string {
	var keys []string
	for _, key := range lists.Preset() {
		if key != "google_ai" {
			keys = append(keys, key)
		}
	}
	return keys
}

// Acquire before Store.Open so transient source failures leave a retryable fresh install.
func prepareFreeStart(ctx context.Context, choice freeStartChoice, dbPath string, client lists.HTTPDoer) (freeStartPrepared, error) {
	prepared := freeStartPrepared{contents: map[string]lists.List{}}
	if !choice.enabled {
		return prepared, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	u, _ := url.Parse(lists.APIpoolURL)
	query := url.Values{"protocol": {"vless,ss,trojan,hysteria2"}, "exclude_country": {"RU,BY"}}
	if choice.country != "AUTO" {
		query.Set("country", choice.country)
	}
	u.RawQuery = query.Encode()
	prepared.catalog = u.String()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	catalog := lists.PoolCatalog{
		Client: client, Log: quiet, Limit: 100, Timeout: 15 * time.Second,
		Observe:  func(v lists.PoolCatalogObservation) { prepared.observation = v },
		CheckKey: func(raw string) error { _, err := singbox.Parse(raw); return err },
	}
	// An unavailable pool remains enabled and empty: selected resources reject.
	prepared.members, _ = catalog.Servers(ctx, prepared.catalog, store.PoolFilter{Countries: store.DefaultPoolCountryBlocklist()})
	if ctx.Err() != nil {
		return freeStartPrepared{}, fmt.Errorf("free-start acquisition canceled before database creation: %w", ctx.Err())
	}
	fetcher, err := lists.NewFetcher(lists.Options{Dir: filepath.Join(filepath.Dir(dbPath), "lists"), Client: client, Logger: quiet, Timeout: 15 * time.Second})
	if err != nil {
		return freeStartPrepared{}, errors.New("free-start source cache unavailable before database creation")
	}
	intent := store.Snapshot{Rules: []store.Rule{{Enabled: true, CommunityLists: freeStartPreset()}}}
	for _, source := range lists.Sources(intent) {
		refreshed, err := fetcher.Update(ctx, source.URL)
		if err != nil || refreshed.Stale != nil {
			return freeStartPrepared{}, errors.New("free-start starter source acquisition failed before database creation; retry setup")
		}
		prepared.contents[source.URL] = refreshed.List
	}
	return prepared, nil
}

func installFreeStart(ctx context.Context, st *store.Store, peer store.Peer, prepared freeStartPrepared) error {
	if peer.ID == "" {
		return errors.New("free-start requires the newly created first client")
	}
	if _, err := st.EnsureBuiltinPool(ctx, prepared.catalog, store.TunnelVLESS); err != nil {
		return err
	}
	tunnels, err := st.Tunnels(ctx)
	if err != nil {
		return err
	}
	var pool store.Tunnel
	for _, tunnel := range tunnels {
		if tunnel.Builtin && tunnel.Source == store.SourcePool {
			pool = tunnel
			break
		}
	}
	if pool.ID == "" {
		return errors.New("free-start built-in pool unavailable")
	}
	pool.Enabled = true
	pool.Pool = prepared.members
	if prepared.observation.CatalogValid {
		pool.PoolUpdatedAt = time.Now().UTC()
	}
	if err := st.UpdateTunnel(ctx, pool); err != nil {
		return err
	}
	_, err = st.CreateRule(ctx, store.Rule{Name: "Free start", Action: store.ActionTunnel, TunnelID: pool.ID, Enabled: true, CommunityLists: freeStartPreset(), PeerScope: store.ScopeSelected, PeerIDs: []string{peer.ID}})
	return err
}

func frozenSetupLists(contents map[string]lists.List) singbox.PlainLists {
	return func(raw string) (singbox.PlainList, bool) {
		l, ok := contents[raw]
		return singbox.PlainList{Domains: l.Domains, Subnets: l.Subnets, Native: l.Native}, ok
	}
}

func verifySetupLists(snap store.Snapshot, contents map[string]lists.List, dbPath string) error {
	dir, err := filepath.Abs(filepath.Join(filepath.Dir(dbPath), "lists", "native"))
	if err != nil {
		return err
	}
	for _, source := range lists.Sources(snap) {
		list, ok := contents[source.URL]
		if !ok {
			return errors.New("fresh bootstrap refused: required starter source is missing")
		}
		if source.Format != lists.FormatPlain {
			if list.Native == nil {
				return errors.New("fresh bootstrap refused: native source authority is missing")
			}
			if err := list.Native.Verify(dir, list.Subnets, list.TransportSubnets); err != nil {
				return fmt.Errorf("fresh bootstrap native verification: %w", err)
			}
		}
	}
	return nil
}
