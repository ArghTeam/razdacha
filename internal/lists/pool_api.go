package lists

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// APIpoolURL is the experimental provider endpoint; it is not the built-in default.
const APIpoolURL = "https://vpnkeys.me/api/v1/keys"

// APIpoolMaxItems bounds the single API batch, including operator probes.
const APIpoolMaxItems = 240

// PoolCatalogObservation separates source admission from local connectivity.
type PoolCatalogObservation struct {
	CatalogValid      bool `json:"catalog_valid"`
	HasMore           bool `json:"has_more"`
	CatalogItems      int  `json:"catalog_items"`
	AdmissionRejected int  `json:"admission_rejected"`
	ParserRejected    int  `json:"parser_rejected"`
	Duplicates        int  `json:"duplicates"`
	Filtered          int  `json:"filtered"`
	Eligible          int  `json:"eligible"`
	Requests          int  `json:"requests"`
}

// apiPool admits the new JSON contract, never the retired HTML catalogs.
type apiPool struct{}

func (apiPool) Name() string              { return "vpnkeys JSON API" }
func (apiPool) KeyType() store.TunnelType { return store.TunnelVLESS }

func apiPoolHost(host string) bool { return host == "vpnkeys.me" || host == "freevpnkeys.com" }
func apiPoolURL(u *url.URL) bool {
	return apiPoolHost(strings.ToLower(u.Hostname())) && u.Scheme == "https" && u.Host == u.Hostname() && u.User == nil && u.Path == "/api/v1/keys" && u.RawPath == "" && u.Fragment == ""
}

type apiPoolResponse struct {
	Items     *[]apiPoolItem `json:"items"`
	HasMore   bool           `json:"has_more"`
	Limit     int            `json:"limit"`
	Offset    int            `json:"offset"`
	UpdatedAt string         `json:"updated_at"`
}
type apiPoolItem struct {
	URI        string   `json:"uri"`
	Country    string   `json:"country"`
	Protocol   string   `json:"protocol"`
	Transport  string   `json:"transport"`
	Security   string   `json:"security"`
	LatencyMS  int      `json:"latency_ms"`
	RUVerified bool     `json:"ru_verified"`
	Services   []string `json:"services"`
	Categories []string `json:"categories"`
}

func (apiPool) Servers(ctx context.Context, c *poolCrawl, catalog *url.URL) ([]store.PoolServer, error) {
	limit := c.limit
	if limit <= 0 {
		limit = poolMaxKeys
	}
	if limit > APIpoolMaxItems {
		return nil, fmt.Errorf("%w: API batch limit exceeds 240", store.ErrInvalid)
	}
	u := *catalog
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid API query", store.ErrInvalid)
	}
	if raw := q.Get("limit"); raw != "" {
		n, e := strconv.Atoi(raw)
		if e != nil || n < 1 || n > APIpoolMaxItems {
			return nil, fmt.Errorf("%w: invalid API batch limit", store.ErrInvalid)
		}
		if n < limit {
			limit = n
		}
	}
	q.Set("limit", strconv.Itoa(limit))
	q.Set("format", "json")
	u.RawQuery = q.Encode()
	body, err := c.page(ctx, u.String())
	if err != nil && ctx.Err() == nil {
		if u.Host == "vpnkeys.me" {
			u.Host = "freevpnkeys.com"
		} else {
			u.Host = "vpnkeys.me"
		}
		body, err = c.page(ctx, u.String())
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("%w: API catalog request failed", ErrBadResponse)
	}
	var envelope apiPoolResponse
	if json.Unmarshal([]byte(body), &envelope) != nil || envelope.Items == nil || len(*envelope.Items) > limit {
		return nil, fmt.Errorf("%w: invalid API catalog response", ErrBadResponse)
	}
	out := make([]store.PoolServer, 0, len(*envelope.Items))
	c.observation.CatalogItems = len(*envelope.Items)
	c.observation.CatalogValid = true
	c.observation.HasMore = envelope.HasMore
	seen := make(map[string]bool)
	for _, item := range *envelope.Items {
		srv, err := apiPoolServer(item)
		if err != nil {
			c.observation.AdmissionRejected++
			c.skip("API item rejected")
			continue
		}
		if requested := q.Get("country"); requested != "" && !apiCountrySelected(srv.Country, requested) {
			c.observation.AdmissionRejected++
			c.skip("API country selection mismatch")
			continue
		}
		if err := c.keyOK(srv.URL); err != nil {
			c.observation.ParserRejected++
			c.skip("API key parser rejected item")
			continue
		}
		if seen[srv.URL] {
			c.observation.Duplicates++
			continue
		}
		seen[srv.URL] = true
		out = append(out, srv)
	}
	return out, nil
}

func apiCountrySelected(country, requested string) bool {
	if country == "" {
		return false
	}
	for _, code := range strings.Split(requested, ",") {
		if strings.EqualFold(strings.TrimSpace(code), country) {
			return true
		}
	}
	return false
}

func apiPoolServer(item apiPoolItem) (store.PoolServer, error) {
	reject := errors.New("invalid API item")
	if len(item.URI) > 16384 || item.LatencyMS < 0 {
		return store.PoolServer{}, reject
	}
	for _, label := range append(append([]string{}, item.Services...), item.Categories...) {
		if strings.EqualFold(strings.TrimSpace(label), "ru_blocked") {
			return store.PoolServer{}, reject
		}
	}
	country := strings.ToUpper(strings.TrimSpace(item.Country))
	if country != "" && (len(country) != 2 || country[0] < 'A' || country[0] > 'Z' || country[1] < 'A' || country[1] > 'Z') {
		return store.PoolServer{}, reject
	}
	u, err := url.Parse(item.URI)
	if err != nil || u.Hostname() == "" || u.User == nil || u.User.Username() == "" {
		return store.PoolServer{}, reject
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return store.PoolServer{}, reject
	}
	switch u.Scheme {
	case "vless", "ss", "trojan", "hysteria2", "hy2":
	default:
		return store.PoolServer{}, reject
	}
	if item.Protocol != "" && !strings.EqualFold(item.Protocol, u.Scheme) && (u.Scheme != "hy2" || item.Protocol != "hysteria2") {
		return store.PoolServer{}, reject
	}
	title := u.Fragment
	// Preserve exact URI bytes apart from the remark: URL.String would normalize identity.
	identity, _, _ := strings.Cut(item.URI, "#")
	return store.PoolServer{URL: identity, Title: title, Country: country, PingMS: item.LatencyMS}, nil
}
