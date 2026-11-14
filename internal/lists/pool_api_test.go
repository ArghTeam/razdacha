package lists

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

const syntheticAPIKey = "vless://00000000-0000-4000-8000-000000000001@edge.example:443?security=tls&type=tcp"

type apiDoer func(*http.Request) (*http.Response, error)

func (f apiDoer) Do(r *http.Request) (*http.Response, error) { return f(r) }
func apiBody(items ...apiPoolItem) string {
	if items == nil {
		items = []apiPoolItem{}
	}
	b, _ := json.Marshal(map[string]any{"items": items, "has_more": true, "limit": 100, "offset": 0})
	return string(b)
}

func apiResponse(body string, status int) *http.Response {
	return &http.Response{StatusCode: status, Status: http.StatusText(status), Body: io.NopCloser(strings.NewReader(body))}
}

func TestAPIPoolPathBoundary(t *testing.T) {
	for _, raw := range []string{APIpoolURL, "https://freevpnkeys.com/api/v1/keys?country=DE"} {
		if _, _, err := PoolDriverFor(raw); err != nil || PoolCatalogRetired(raw) {
			t.Fatalf("API rejected: %s", raw)
		}
	}
	for _, raw := range []string{"https://vpnkeys.me/protocol/vless", "https://vpnkeys.me/", "https://www.vpnkeys.me/api/v1/keys", "http://vpnkeys.me/api/v1/keys", "https://vpnkeys.me:443/api/v1/keys", "https://secret@vpnkeys.me/api/v1/keys", "https://vpnkeys.me/api/v1/keys/", "https://vpnkeys.me/api/v1/%6beys"} {
		if _, _, err := PoolDriverFor(raw); err == nil {
			t.Fatalf("unapproved endpoint accepted: %s", raw)
		}
	}
	if !PoolCatalogRetired("https://vpnkeys.me/protocol/vless") {
		t.Fatal("legacy migration lost")
	}
}

func TestAPIPoolMirrorPreservesFiltersAndRedactsErrors(t *testing.T) {
	var requests []*http.Request
	var logs bytes.Buffer
	catalog := PoolCatalog{
		Pause: -1, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
		Client: apiDoer(func(r *http.Request) (*http.Response, error) {
			requests = append(requests, r)
			if len(requests) == 1 {
				return nil, errors.New("secret-network-error")
			}
			return apiResponse(apiBody(apiPoolItem{URI: syntheticAPIKey + "#secret-title", Country: "de"}), 200), nil
		}), CheckKey: func(string) error { return errors.New("secret-parser-error " + syntheticAPIKey) },
	}
	_, err := catalog.Servers(context.Background(), "https://VPNKEYS.ME/api/v1/keys?country=DE&security=tls&service=video&min_reputation=0.4&format=plain&caller=secret-query", store.PoolFilter{})
	if err == nil || len(requests) != 2 {
		t.Fatalf("expected bounded retry and empty admission: %v", err)
	}
	if requests[0].URL.Host != "vpnkeys.me" || requests[1].URL.Host != "freevpnkeys.com" || requests[0].URL.RawQuery != requests[1].URL.RawQuery {
		t.Fatal("mirror changed filters")
	}
	q := requests[1].URL.Query()
	if q.Get("format") != "json" || q.Get("limit") != "100" || q.Get("service") != "video" || q.Get("country") != "DE" || q.Get("min_reputation") != "0.4" {
		t.Fatal("query changed")
	}
	for _, secret := range []string{"secret-", syntheticAPIKey} {
		if strings.Contains(logs.String()+err.Error(), secret) {
			t.Fatal("sensitive content exposed")
		}
	}
}

func TestAPIPoolAdmissionIdentityAndCountry(t *testing.T) {
	key2 := strings.Replace(syntheticAPIKey, "000000000001", "000000000002", 1)
	items := []apiPoolItem{
		{URI: syntheticAPIKey + "#DE-provider-a", Country: "de", LatencyMS: 42},
		{URI: syntheticAPIKey + "#DE-provider-b", Country: "DE", LatencyMS: 20},
		{URI: key2, Country: "DE"},
		{URI: strings.Replace(syntheticAPIKey, "security=tls", "security=none", 1), Country: "DE", Security: "tls"},
		{URI: syntheticAPIKey + "&x=country", Country: "RU"},
		{URI: syntheticAPIKey + "&x=title#RU", Country: "DE"},
		{URI: syntheticAPIKey + "&x=blocked", Country: "DE", RUVerified: true, Services: []string{"ru_blocked"}},
		{URI: "tg://proxy?server=edge.example", Country: "DE"},
		{URI: syntheticAPIKey + "&x=badcountry", Country: "Germany"},
	}
	var stats PoolCatalogObservation
	catalog := PoolCatalog{Pause: -1, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Observe: func(v PoolCatalogObservation) { stats = v }, Client: apiDoer(func(*http.Request) (*http.Response, error) { return apiResponse(apiBody(items...), 200), nil })}
	got, err := catalog.Servers(context.Background(), APIpoolURL, store.PoolFilter{Countries: []string{"RU", "BY"}})
	if err != nil || len(got) != 2 {
		t.Fatalf("admission: %d %v", len(got), err)
	}
	if got[0].URL != syntheticAPIKey || got[0].Title != "DE-provider-a" || got[0].Country != "DE" || got[0].PingMS != 42 || got[1].URL != key2 {
		t.Fatal("identity or metadata changed")
	}
	if stats.CatalogItems != 9 || stats.Duplicates != 1 || stats.AdmissionRejected != 3 || stats.Filtered != 3 || stats.Eligible != 2 {
		t.Fatalf("counts: %+v", stats)
	}
}

func TestAPIPoolMalformedAndEmptyDoNotBroaden(t *testing.T) {
	for _, body := range []string{"<html>error</html>", `{"items":[`, `{"items":{}}`, `{"items":[{"uri":9}]}`, `{"items":[{"uri":"x","services":{}}]}`, `{"items":null}`, `{}`} {
		t.Run(body, func(t *testing.T) {
			calls := 0
			c := PoolCatalog{Client: apiDoer(func(*http.Request) (*http.Response, error) { calls++; return apiResponse(body, 200), nil })}
			if _, err := c.Servers(context.Background(), APIpoolURL, store.PoolFilter{}); err == nil || calls != 1 {
				t.Fatalf("invalid body accepted or retried: %d %v", calls, err)
			}
		})
	}
	calls := 0
	c := PoolCatalog{Client: apiDoer(func(*http.Request) (*http.Response, error) { calls++; return apiResponse(apiBody(), 200), nil })}
	if _, err := c.Servers(context.Background(), APIpoolURL+"?country=DE", store.PoolFilter{}); err == nil || calls != 1 {
		t.Fatal("empty response broadened")
	}
}

func TestAPIPoolBudgetsCancellationAndHTTPFailures(t *testing.T) {
	for _, status := range []int{429, 500, 503} {
		calls := 0
		c := PoolCatalog{Pause: -1, Client: apiDoer(func(*http.Request) (*http.Response, error) { calls++; return apiResponse("secret-body", status), nil })}
		if _, err := c.Servers(context.Background(), APIpoolURL, store.PoolFilter{}); err == nil || calls != 2 {
			t.Fatal("HTTP retry budget violated")
		}
	}
	for _, body := range []string{strings.Repeat("x", poolMaxPageBytes+1), apiBody(apiPoolItem{URI: syntheticAPIKey}, apiPoolItem{URI: syntheticAPIKey})} {
		c := PoolCatalog{Pause: -1, Limit: 1, Client: apiDoer(func(*http.Request) (*http.Response, error) { return apiResponse(body, 200), nil })}
		if _, err := c.Servers(context.Background(), APIpoolURL, store.PoolFilter{}); err == nil {
			t.Fatal("response budget ignored")
		}
	}
	calls := 0
	ctx, cancel := context.WithCancel(context.Background())
	c := PoolCatalog{Pause: -1, Client: apiDoer(func(r *http.Request) (*http.Response, error) { calls++; cancel(); return nil, r.Context().Err() })}
	if _, err := c.Servers(ctx, APIpoolURL, store.PoolFilter{}); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("cancel: %v calls %d", err, calls)
	}
	c.Limit = 241
	if _, err := c.Servers(context.Background(), APIpoolURL, store.PoolFilter{}); err == nil || calls != 1 {
		t.Fatal("batch cap ignored")
	}
}

func TestAPICountrySelectionIsLocal(t *testing.T) {
	u, _ := url.Parse(APIpoolURL + "?country=DE")
	crawl := &poolCrawl{limit: 100, log: slog.New(slog.NewTextHandler(io.Discard, nil)), fetch: func(context.Context, string) (string, error) {
		return apiBody(apiPoolItem{URI: syntheticAPIKey, Country: ""}, apiPoolItem{URI: syntheticAPIKey, Country: "US"}), nil
	}}
	got, err := (apiPool{}).Servers(context.Background(), crawl, u)
	if err != nil || len(got) != 0 {
		t.Fatal("country selector trusted source filtering")
	}
}

func TestAPIRedirectCannotChangeFiltersOrRequestBudget(t *testing.T) {
	client := (&PoolCatalog{}).client().(*http.Client)
	original, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, APIpoolURL+"?country=DE&min_reputation=0.9&service=video", nil)
	for _, target := range []string{APIpoolURL + "?format=json&limit=100", APIpoolURL + "?country=DE&min_reputation=0.9&service=video", "https://elsewhere.example/secret"} {
		next, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		if client.CheckRedirect(next, []*http.Request{original}) == nil {
			t.Fatal("API redirect followed")
		}
	}
}

func TestAPITimeoutAndTruncationAreBounded(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		calls := 0
		c := PoolCatalog{Pause: -1, Timeout: time.Millisecond, Client: apiDoer(func(r *http.Request) (*http.Response, error) {
			calls++
			if truncated {
				resp := apiResponse(apiBody(), 200)
				resp.ContentLength = 999
				return resp, nil
			}
			<-r.Context().Done()
			return nil, r.Context().Err()
		})}
		if _, err := c.Servers(context.Background(), APIpoolURL, store.PoolFilter{}); err == nil || calls != 2 {
			t.Fatalf("bounded timeout/truncation: calls=%d err=%v", calls, err)
		}
	}
}
