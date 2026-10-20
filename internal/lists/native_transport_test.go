package lists

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
)

func TestNativeTransportProjection(t *testing.T) {
	for _, rule := range []string{
		`{"network":"udp","ip_cidr":"203.0.113.0/24","port_range":"50000:65535"}`,
		`{"type":"logical","mode":"and","rules":[{"network":"udp"},{"ip_cidr":"203.0.113.0/24"},{"port":[443],"port_range":"50000:65535"}]}`,
	} {
		body := nativeBody(`{"domain_suffix":"selected.example"},` + rule + `,{"ip_cidr":"192.0.2.0/24"}`)
		jsonProjection, err := ProjectNative(body, FormatJSON)
		if err != nil {
			t.Fatal(err)
		}
		binaryProjection, err := ProjectNative(nativeBinary(t, body), FormatSRS)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(jsonProjection, binaryProjection) {
			t.Fatalf("JSON/SRS differ: %+v %+v", jsonProjection, binaryProjection)
		}
		if !reflect.DeepEqual(jsonProjection.TransportSubnets, []string{"203.0.113.0/24"}) || !reflect.DeepEqual(jsonProjection.Subnets, []string{"192.0.2.0/24"}) {
			t.Fatalf("wrong envelopes: %+v", jsonProjection)
		}
		if bytes.Contains(jsonProjection.DNS, []byte("network")) || bytes.Contains(jsonProjection.DNS, []byte("port")) || bytes.Contains(jsonProjection.DNS, []byte("ip_cidr")) {
			t.Fatalf("context leaked into DNS: %s", jsonProjection.DNS)
		}
		if _, err = projectNativeVersion(body, FormatJSON, 1); err == nil {
			t.Fatal("legacy compiler accepted transport")
		}
	}
}

func TestNativeTransportRejectsUnsupported(t *testing.T) {
	rules := []string{
		`{"network":"icmp","ip_cidr":"203.0.113.0/24"}`,
		`{"network":"udp","ip_cidr":"0.0.0.0/0"}`,
		`{"port":443}`, `{"network":"udp"}`,
		`{"ip_cidr":"0.0.0.0/1"},{"network":"udp","ip_cidr":"128.0.0.0/1"}`,
		`{"network":"udp","domain":"selected.example","ip_cidr":"203.0.113.0/24"}`,
		`{"network":"udp","ip_cidr":"203.0.113.0/24","invert":true}`,
		`{"network":"udp","ip_cidr":"203.0.113.0/24","port_range":"65535:1"}`,
		`{"network":"udp","ip_cidr":"203.0.113.0/24","source_port":443}`,
		`{"type":"logical","mode":"and","rules":[{"domain":"selected.example"},{"network":"udp","ip_cidr":"203.0.113.0/24"}]}`,
		`{"type":"logical","mode":"or","invert":true,"rules":[{"network":"udp","ip_cidr":"203.0.113.0/24"}]}`,
	}
	for _, rule := range rules {
		if _, err := ProjectNative(nativeBody(rule), FormatJSON); err == nil {
			t.Errorf("accepted %s", rule)
		}
	}
}

func TestNativeTransportPublicFixture(t *testing.T) {
	path := os.Getenv("RAZDACHA_NATIVE_PUBLIC_FIXTURE")
	if path == "" {
		t.Skip("optional public upstream SRS fixture")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, err := ProjectNative(body, FormatSRS)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.DNS) == 0 || len(p.Subnets) != 0 || len(p.TransportSubnets) != 8 {
		t.Fatalf("unexpected public projections: %+v", p)
	}
}

func TestNativeTransportCacheRetainsLastGoodAndValidators(t *testing.T) {
	var invalid atomic.Bool
	var conditional atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == "accepted" {
			conditional.Store(true)
		}
		if invalid.Load() {
			w.Header().Set("ETag", "rejected")
			_, _ = w.Write(nativeBody(`{"network":"udp","port":443}`))
			return
		}
		w.Header().Set("ETag", "accepted")
		_, _ = w.Write(nativeBody(`{"network":"udp","ip_cidr":"203.0.113.0/24","port":443}`))
	}))
	defer srv.Close()
	f, err := NewFetcher(Options{Dir: t.TempDir(), Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/native.json"
	first, err := f.updateNative(context.Background(), url)
	if err != nil || first.Stale != nil {
		t.Fatalf("initial acceptance: %v %v", err, first.Stale)
	}
	invalid.Store(true)
	second, err := f.updateNative(context.Background(), url)
	if err != nil || second.Stale == nil || !conditional.Load() {
		t.Fatalf("refresh did not retain accepted authority: %v %+v", err, second)
	}
	accepted, err := f.accepted(url)
	if err != nil || accepted.Meta.ETag != "accepted" || !reflect.DeepEqual(first.List, second.List) {
		t.Fatalf("accepted validators/projection changed: %v", err)
	}
	invalid.Store(false)
	mgr := NewManager(ManagerOptions{Fetcher: f})
	mgr.SetSources([]Source{{URL: url, Format: FormatJSON}})
	if err = mgr.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	copied := mgr.Snapshot()
	copied[url].TransportSubnets[0] = "192.0.2.0/24"
	if mgr.Snapshot()[url].TransportSubnets[0] != "203.0.113.0/24" {
		t.Fatal("transport snapshot aliases live contents")
	}
	a := *accepted.List.Native
	a.Version = 1
	if err = a.Verify(filepath.Dir(a.Path), first.List.Subnets, first.List.TransportSubnets); err == nil {
		t.Fatal("legacy verifier accepted transport bytes")
	}
	legacy, err := f.acceptNative(srv.URL+"/legacy.json", nativeBody(`{"ip_cidr":"203.0.113.0/24"}`), meta{})
	if err != nil {
		t.Fatal(err)
	}
	a = *legacy.List.Native
	a.Version = 1
	if err = a.Verify(filepath.Dir(a.Path), legacy.List.Subnets); err != nil {
		t.Fatalf("legacy authority refused: %v", err)
	}
}

func TestNativeTransportANDPreservesUnconditionalAddresses(t *testing.T) {
	body := nativeBody(`{"type":"logical","mode":"and","rules":[{"type":"logical","mode":"or","rules":[{"ip_cidr":"203.0.113.0/25"},{"network":"udp","ip_cidr":"203.0.113.128/25"}]},{"ip_cidr":"203.0.113.0/24"}]}`)
	for _, format := range []Format{FormatJSON, FormatSRS} {
		input := body
		if format == FormatSRS {
			input = nativeBinary(t, body)
		}
		projection, err := ProjectNative(input, format)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(projection.Subnets, []string{"203.0.113.0/25"}) || !reflect.DeepEqual(projection.TransportSubnets, []string{"203.0.113.0/24"}) {
			t.Fatalf("%s lost unconditional coverage: %+v", format, projection)
		}
	}
}
