package singbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/ArghTeam/razdacha/internal/store"
)

// RestoreWireGuardIDs follows buildTunnels/tunnelOptions precedence without network I/O.
// Disabled/unreferenced tunnels are inspected too: a future enable must not bypass pause.
func RestoreWireGuardIDs(tunnels []store.Tunnel) ([]string, error) {
	var ids []string
	for _, t := range tunnels {
		if t.Source == store.SourcePool {
			if t.Type == store.TunnelWireGuard || t.Type == store.TunnelRaw || len(t.Parsed) != 0 {
				return nil, errors.New("unsupported restore pool metadata")
			}
			for _, member := range t.Pool {
				p, e := Parse(member.URL)
				if e != nil || p.Outbound == nil {
					return nil, errors.New("unsupported restore pool endpoint")
				}
			}
			continue
		}
		if e := uniqueJSON(t.Parsed); e != nil {
			return nil, fmt.Errorf("restore tunnel %s has ambiguous JSON: %w", t.ID, e)
		}
		typ, _, e := tunnelOptions(t)
		if e != nil {
			return nil, fmt.Errorf("classify restore tunnel %s: %w", t.ID, e)
		}
		if t.Source == store.SourceWARP && typ != "wireguard" {
			return nil, errors.New("inconsistent restore WARP metadata")
		}
		if typ == "wireguard" {
			ids = append(ids, t.ID)
		}
	}
	return ids, nil
}

// uniqueJSON rejects duplicate object keys at every depth instead of last-key-wins.
func uniqueJSON(raw []byte) error {
	if b := bytes.TrimSpace(raw); len(b) == 0 || b[0] != '{' {
		return errors.New("tunnel options must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if e := jsonValue(d); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func jsonValue(d *json.Decoder) error {
	token, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			k, e := d.Token()
			if e != nil {
				return e
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return errors.New("duplicate or invalid JSON key")
			}
			seen[key] = true
			if e = jsonValue(d); e != nil {
				return e
			}
		}
	case '[':
		for d.More() {
			if e = jsonValue(d); e != nil {
				return e
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, e = d.Token()
	return e
}
