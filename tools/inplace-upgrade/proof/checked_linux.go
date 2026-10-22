//go:build linux

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/singbox"
)

const installedRuntime = "/usr/local/bin/sing-box"

type pinnedChecker struct{ binary, digest, version string }

func runtimeDigest(path string) (string, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return "", err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o022 != 0 || !ok || owner.Uid != uint32(os.Geteuid()) {
		return "", errors.New("checker must be an owner-controlled regular executable")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func newPinnedChecker(ctx context.Context) (*pinnedChecker, error) {
	digest, err := runtimeDigest(installedRuntime)
	if err != nil {
		return nil, err
	}
	version, err := packaging.SingboxRuntimeVersion(ctx, installedRuntime)
	if err != nil {
		return nil, err
	}
	library, err := packaging.SingboxLibraryVersion()
	if err != nil {
		return nil, err
	}
	if version != library {
		return nil, errors.New("installed checker differs from pinned runtime library")
	}
	checker := &pinnedChecker{binary: installedRuntime, digest: digest, version: version}
	return checker, checker.verify()
}

func (c *pinnedChecker) verify() error {
	digest, err := runtimeDigest(c.binary)
	if err != nil {
		return err
	}
	if digest != c.digest {
		return errors.New("pinned checker executable changed during proof")
	}
	return nil
}

func (c *pinnedChecker) Check(ctx context.Context, path string) error {
	if err := c.verify(); err != nil {
		return err
	}
	checkErr := (singbox.BinaryChecker{Binary: c.binary}).Check(ctx, path)
	if err := c.verify(); err != nil {
		return err
	}
	return checkErr
}

type capturingChecker struct {
	delegate        singbox.Checker
	first, accepted []byte
}

func (c *capturingChecker) Check(ctx context.Context, path string) error {
	candidate, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if c.first == nil {
		c.first = candidate
	}
	if err := c.delegate.Check(ctx, path); err != nil {
		return err
	}
	c.accepted = candidate
	return nil
}

type forbiddenReloader struct{}

func (forbiddenReloader) Reload(context.Context) error {
	return errors.New("checked proof cannot reload a runtime")
}

// checkedConfiguration follows the daemon's bounded ADR 0019 retries in a fresh
// private directory. It never calls Commit or publishes a runtime configuration.
func checkedConfiguration(ctx context.Context, gen networkstate.Generation, checker singbox.Checker, parent string) (rawSHA, checkedSHA string, err error) {
	dir, err := os.MkdirTemp(parent, ".razdacha-checked-proof-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if cleanupErr := os.RemoveAll(dir); cleanupErr != nil {
			err = errors.Join(err, fmt.Errorf("remove owned checked-proof directory: %w", cleanupErr))
		}
	}()
	capture := &capturingChecker{delegate: checker}
	applier := singbox.Applier{
		ConfigPath: filepath.Join(dir, "never-published.json"), Checker: capture, Reloader: forbiddenReloader{},
		PlainLists: func(url string) (singbox.PlainList, bool) {
			list, ok := gen.Lists[url]
			return singbox.PlainList{Domains: list.Domains, Subnets: list.Subnets, Native: list.Native}, ok
		},
	}
	prepared, err := applier.PrepareRecovery(ctx, gen.Policy)
	if err != nil {
		return "", "", fmt.Errorf("prepare checked committed runtime config: %w", err)
	}
	defer prepared.Close()
	if len(capture.first) == 0 || len(capture.accepted) == 0 {
		return "", "", errors.New("no checked runtime candidate captured")
	}
	rawHash, checkedHash := sha256.Sum256(capture.first), sha256.Sum256(capture.accepted)
	return hex.EncodeToString(rawHash[:]), hex.EncodeToString(checkedHash[:]), nil
}
