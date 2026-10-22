//go:build linux

package main

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
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/ArghTeam/razdacha/internal/networkstate"
	"github.com/ArghTeam/razdacha/internal/singbox"
)

// authorityProof retains exact private evidence as well as the previous canonical
// digest. Acceptance compares generations in the controller; artifact verification
// is repeated for every current proof, regardless of catalog-only differences.
func authorityProof(dir string) (map[string]any, error) {
	if _, err := os.Lstat(filepath.Join(dir, networkstate.BootstrapFile)); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("bootstrap authority is not supported")
	}
	path := filepath.Join(dir, networkstate.CommittedFile)
	gen, err := networkstate.Read(path, networkstate.CommittedKind)
	if err != nil {
		return nil, err
	}
	if gen.Version != 4 {
		return nil, errors.New("only committed generation version four is supported")
	}
	count := 0
	for _, list := range gen.Lists {
		if list.Native == nil {
			continue
		}
		if err := list.Native.Verify(filepath.Join(dir, "lists", "native"), list.Subnets, list.TransportSubnets); err != nil {
			return nil, fmt.Errorf("verify native authority: %w", err)
		}
		count++
	}
	config, err := singbox.Generate(gen.Policy, func(url string) (singbox.PlainList, bool) {
		list, ok := gen.Lists[url]
		return singbox.PlainList{Domains: list.Domains, Subnets: list.Subnets, Native: list.Native}, ok
	})
	if err != nil {
		return nil, fmt.Errorf("generate committed runtime config: %w", err)
	}
	configBytes, err := singbox.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("marshal committed runtime config: %w", err)
	}
	configHash := sha256.Sum256(configBytes)
	encoded, err := json.Marshal(gen)
	if err != nil {
		return nil, err
	}
	raw, err := readAuthorityEvidence(path)
	if err != nil {
		return nil, err
	}
	var observed networkstate.Generation
	if err := json.Unmarshal(raw, &observed); err != nil {
		return nil, err
	}
	observedEncoded, err := json.Marshal(observed)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded, observedEncoded) {
		return nil, errors.New("committed authority changed during proof")
	}
	canonicalHash, rawHash := sha256.Sum256(encoded), sha256.Sum256(raw)
	return map[string]any{
		"version": 4, "sha256": hex.EncodeToString(canonicalHash[:]),
		"native_sources": count, "raw_sha256": hex.EncodeToString(rawHash[:]),
		"generation_raw": string(raw), "generated_config_sha256": hex.EncodeToString(configHash[:]),
	}, nil
}

func readAuthorityEvidence(path string) ([]byte, error) {
	descriptor, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(descriptor), path)
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	const limit = 256 << 20
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !ok || owner.Uid != uint32(os.Geteuid()) || info.Size() > limit {
		return nil, errors.New("authority evidence must be a bounded private owner-controlled file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > limit {
		return nil, errors.New("authority evidence exceeds size limit")
	}
	return raw, nil
}
