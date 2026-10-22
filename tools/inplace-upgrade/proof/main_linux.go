//go:build linux

// The operator proof helper never removes an ingress guard. Only successful
// daemon committed-generation recovery can release traffic during this workflow.
package main

import (
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"golang.zx2c4.com/wireguard/wgctrl"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/packaging"
	"github.com/ArghTeam/razdacha/internal/store"
)

const stateDir = "/var/lib/razdacha"

func main() {
	result, err := command(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func command(args []string) (any, error) {
	if len(args) == 0 {
		return nil, errors.New("expected capabilities, inspect, protect, authority, wireguard or backup")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	switch args[0] {
	case "capabilities":
		if len(args) != 1 {
			break
		}
		st, err := store.Open(ctx, store.MemoryPath)
		if err != nil {
			return nil, err
		}
		defer func() { _ = st.Close() }()
		var schema int
		if err := st.DB().QueryRowContext(ctx, "PRAGMA user_version").Scan(&schema); err != nil {
			return nil, err
		}
		runtimeVersion, err := packaging.SingboxLibraryVersion()
		return map[string]any{"schema": schema, "runtime": runtimeVersion}, err
	case "candidate":
		if len(args) != 2 {
			break
		}
		info, err := buildinfo.ReadFile(args[1])
		if err != nil {
			return nil, err
		}
		for _, dependency := range info.Deps {
			if dependency.Path == "github.com/sagernet/sing-box" {
				if dependency.Replace != nil {
					return nil, errors.New("replaced runtime dependency is unsupported")
				}
				return map[string]string{"runtime": strings.TrimPrefix(dependency.Version, "v")}, nil
			}
		}
		return nil, errors.New("candidate runtime dependency is missing")
	case "inspect", "protect":
		if len(args) != 1 {
			break
		}
		conn, err := nftables.New()
		if err != nil {
			return nil, err
		}
		_, out, err := inspect(conn)
		if err != nil {
			return nil, err
		}
		if !out.ExactShape {
			return out, errUnknownShape
		}
		if args[0] == "protect" && !out.Present {
			if err := netstack.SetClientGuard(true); err != nil {
				return nil, err
			}
			fresh, err := nftables.New()
			if err != nil {
				return nil, err
			}
			_, out, err = inspect(fresh)
			if err != nil {
				return nil, err
			}
			if !out.Present || !out.ExactShape {
				return out, errors.New("guard protection was not independently verified")
			}
		}
		return out, nil
	case "authority":
		if len(args) != 1 {
			break
		}
		return authorityProof(ctx, stateDir)
	case "wireguard":
		if len(args) != 1 {
			break
		}
		return wireguardIdentity()
	case "backup":
		if len(args) != 2 || !filepath.IsAbs(args[1]) {
			break
		}
		return map[string]bool{"complete": true}, store.BackupAt(ctx, filepath.Join(stateDir, "state.db"), args[1])
	}
	return nil, errors.New("invalid proof command or arguments")
}

func wireguardIdentity() (any, error) {
	client, err := wgctrl.New()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	device, err := client.Device("wg0")
	if err != nil {
		return nil, err
	}
	link, err := netlink.LinkByName("wg0")
	if err != nil {
		return nil, err
	}
	var peers []string
	for _, peer := range device.Peers {
		var allowed []string
		for _, prefix := range peer.AllowedIPs {
			allowed = append(allowed, prefix.String())
		}
		sort.Strings(allowed)
		body, err := json.Marshal([]any{peer.PublicKey.String(), peer.PresharedKey.String(), allowed})
		if err != nil {
			return nil, err
		}
		peers = append(peers, string(body))
	}
	sort.Strings(peers)
	body, err := json.Marshal([]any{device.PublicKey.String(), device.ListenPort, link.Attrs().MTU, peers})
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(body)
	return map[string]any{"sha256": hex.EncodeToString(hash[:]), "peers": len(peers)}, nil
}
