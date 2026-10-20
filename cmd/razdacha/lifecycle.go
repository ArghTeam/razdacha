package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"path/filepath"

	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/restorestate"
)

func installPreflight(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("install-preflight", flag.ContinueOnError)
	daemon := flags.String("staged-daemon", "", "private staged daemon executable")
	cli := flags.String("staged-cli", "", "private staged CLI executable")
	fd := flags.Int("lifecycle-lock-fd", -1, "inherited managed installer lock descriptor")
	if e := flags.Parse(args); e != nil {
		return e
	}
	if *daemon == "" || *cli == "" || *fd < 3 {
		return errors.New("managed install preflight requires staged binaries and a validated inherited lock")
	}
	lock, e := restorestate.Acquire(defaultStateDir, *fd)
	if e != nil {
		return e
	}
	defer func() { _ = lock.Close() }()
	if _, e = restorestate.Read(defaultStateDir); e != nil {
		return e
	}
	for _, binary := range []string{*daemon, *cli} {
		if _, e = packaging.AttestRestoreBinary(ctx, binary); e != nil {
			return e
		}
	}
	return nil
}

func setupLifecycle(opts setupOptions) (*restorestate.Lock, error) {
	dir := filepath.Dir(filepath.Join(opts.root, opts.dbPath))
	fd := -1
	if opts.lockInherited {
		fd = opts.lockFD
	}
	lock, e := restorestate.Acquire(dir, fd)
	if e != nil {
		return nil, e
	}
	state, e := restorestate.Read(dir)
	if e != nil {
		_ = lock.Close()
		return nil, e
	}
	if state != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("setup refuses trusted restore state; preserve held/runtime/authority state and use explicit restore administration or recovery")
	}
	return lock, nil
}
