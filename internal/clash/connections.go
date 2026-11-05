package clash

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	connectionReadTimeout = 2 * time.Second
	maxConnectionFlows    = 4096
)

// RealityConnections returns authenticated singleton user counts, never addresses
// or destinations. The caller must intersect these users with live admission.
func (c *Client) RealityConnections(ctx context.Context) (map[string]int, error) {
	ctx, cancel := context.WithTimeout(ctx, connectionReadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/connections", nil)
	if err != nil {
		return nil, fmt.Errorf("build connection observation: %w", err)
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: connection observation: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: connection status %d", ErrBadResponse, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, fmt.Errorf("read connection observation: %w", err)
	}
	if len(body) > maxBody {
		return nil, fmt.Errorf("%w: connection response too large", ErrBadResponse)
	}
	var out struct {
		Connections *[]struct {
			ID       string `json:"id"`
			Metadata struct {
				Type string `json:"type"`
			} `json:"metadata"`
			Rule string `json:"rule"`
		} `json:"connections"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: connection JSON: %w", ErrBadResponse, err)
	}
	if out.Connections == nil || len(*out.Connections) > maxConnectionFlows {
		return nil, fmt.Errorf("%w: missing or excessive connections", ErrBadResponse)
	}
	counts := make(map[string]int)
	seen := make(map[string]bool)
	for _, conn := range *out.Connections {
		if conn.Metadata.Type != "vless/reality-in" {
			continue
		}
		if conn.ID == "" || seen[conn.ID] {
			return nil, fmt.Errorf("%w: invalid connection identity", ErrBadResponse)
		}
		seen[conn.ID] = true
		user, ok := realityRuleUser(conn.Rule)
		if !ok {
			return nil, fmt.Errorf("%w: unsupported authenticated connection rule", ErrBadResponse)
		}
		counts[user]++
	}
	return counts, nil
}

// The pinned runtime emits these first two matchers for generated terminal rules.
// Requiring the prefix prevents destination text from impersonating an auth user.
func realityRuleUser(rule string) (string, bool) {
	const prefix = "inbound=reality-in auth_user="
	if !strings.HasPrefix(rule, prefix) {
		return "", false
	}
	user, rest, found := strings.Cut(strings.TrimPrefix(rule, prefix), " ")
	if !found || !validRealityUser(user) || strings.Contains(rest, "auth_user=") {
		return "", false
	}
	_, action, found := strings.Cut(rest, "=> ")
	if !found || !strings.HasPrefix(action, "route(") || !strings.HasSuffix(action, ")") {
		return "", false
	}
	return user, true
}

func validRealityUser(user string) bool {
	if !strings.HasPrefix(user, "client-") {
		return false
	}
	id := strings.TrimPrefix(user, "client-")
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
