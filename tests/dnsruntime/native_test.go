package dnsruntime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	runtimeRule "github.com/sagernet/sing-box/route/rule"
	SJSON "github.com/sagernet/sing/common/json"
	M "github.com/sagernet/sing/common/metadata"
)

func TestNativeOriginalAndProjectionRuntime(t *testing.T) {
	cases := []struct{ name, rule string }{
		{"default-domain-IP-OR", `{"domain":["selected.example"],"ip_cidr":["203.0.113.0/24"]}`},
		{"nested-domain-AND-NOT", `{"type":"logical","mode":"and","rules":[{"domain_keyword":["selected"]},{"domain_suffix":["excluded.example"],"invert":true}]}`},
		{"exact-suffix-OR", `{"domain":["selected.example"],"domain_suffix":["excluded.example",".sub.example"]}`},
		{"regex-all-names", `{"domain_regex":[".*"]}`},
		{"regex-empty", `{"domain_regex":["^$"]}`},
		{"regex-zero-repeat", `{"domain_regex":["a{0}"]}`},
		{"regex-class-repeat-capture", `{"domain_regex":["^(?P<label>[a-z]{1,16})[.]example$","^\\pL+[.]example$"]}`},
		{"address-AND", `{"type":"logical","mode":"and","rules":[{"ip_cidr":["203.0.113.0/24"]},{"ip_cidr":["203.0.113.10/32"]}]}`},
		{"false-address", `{"type":"logical","mode":"and","rules":[{"ip_cidr":["203.0.113.0/24"]},{"ip_cidr":["192.0.2.0/24"]}]}`},
		{"double-domain-NOT", `{"type":"logical","mode":"and","invert":true,"rules":[{"domain":["selected.example"],"invert":true}]}`},
	}
	for _, tt := range cases {
		for _, format := range []lists.Format{lists.FormatJSON, lists.FormatSRS} {
			versions := []uint8{3}
			if format == lists.FormatSRS {
				versions = []uint8{1, 2, 3}
			}
			for _, version := range versions {
				t.Run(fmt.Sprintf("%s/%s/v%d", tt.name, format, version), func(t *testing.T) {
					body := []byte(fmt.Sprintf(`{"version":3,"rules":[%s]}`, tt.rule))
					if format == lists.FormatSRS {
						var compat option.PlainRuleSetCompat
						if err := SJSON.Unmarshal(body, &compat); err != nil {
							t.Fatal(err)
						}
						var b bytes.Buffer
						if err := srs.Write(&b, compat.Options, version); err != nil {
							t.Fatal(err)
						}
						body = b.Bytes()
					}
					source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
					defer source.Close()
					fetcher, err := lists.NewFetcher(lists.Options{Dir: t.TempDir(), Client: source.Client()})
					if err != nil {
						t.Fatal(err)
					}
					list, err := fetcher.Parse(context.Background(), source.URL+"/native."+string(format))
					if err != nil {
						t.Fatal(err)
					}
					ctx := context.Background()
					factory, err := log.New(log.Options{Context: ctx, Options: option.LogOptions{Level: "error"}, DefaultWriter: io.Discard})
					if err != nil {
						t.Fatal(err)
					}
					nativeFormat := C.RuleSetFormatSource
					if format == lists.FormatSRS {
						nativeFormat = C.RuleSetFormatBinary
					}
					original, err := runtimeRule.NewLocalRuleSet(ctx, factory.NewLogger("original"), option.RuleSet{Type: C.RuleSetTypeLocal, Tag: "original", Format: nativeFormat, LocalOptions: option.LocalRuleSet{Path: list.Native.Path}})
					if err != nil {
						t.Fatal(err)
					}
					defer original.Close()
					var projection adapter.RuleSet
					if list.Native.DNSPath != "" {
						projection, err = runtimeRule.NewLocalRuleSet(ctx, factory.NewLogger("projection"), option.RuleSet{Type: C.RuleSetTypeLocal, Tag: "projection", Format: C.RuleSetFormatSource, LocalOptions: option.LocalRuleSet{Path: list.Native.DNSPath}})
						if err != nil {
							t.Fatal(err)
						}
						defer projection.Close()
					}
					for _, domain := range []string{"selected.example", "selected.excluded.example", "unrelated.example", ""} {
						for _, ip := range []string{"203.0.113.10", "203.0.113.11", "192.0.2.8"} {
							metadata := func() *adapter.InboundContext {
								return &adapter.InboundContext{Domain: domain, Destination: M.Socksaddr{Addr: netip.MustParseAddr(ip)}}
							}
							route := original.Match(metadata())
							dns := false
							if projection != nil {
								dns = projection.Match(metadata())
							}
							intercepted := false
							for _, prefix := range list.Subnets {
								intercepted = intercepted || netip.MustParsePrefix(prefix).Contains(netip.MustParseAddr(ip))
							}
							if route != (dns || intercepted) {
								t.Fatalf("original/projection mismatch domain=%q IP=%s route=%v DNS=%v nft=%v", domain, ip, route, dns, intercepted)
							}
							if domain == "" && dns {
								t.Fatal("DNS projection admitted literal IP")
							}
						}
					}
				})
			}
		}
	}
}

func TestNativeUnsafeRuntimePredicatesRejected(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, rule string
		emptyMatch bool
	}{
		{"negated-domain", `{"domain":["selected.example"],"invert":true}`, true},
		{"mixed-logical-AND", `{"type":"logical","mode":"and","rules":[{"domain":["selected.example"]},{"ip_cidr":["203.0.113.0/24"]}]}`, false},
		{"source-context", `{"domain":["selected.example"],"source_ip_cidr":["10.8.0.2"]}`, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"version":3,"rules":[%s]}`, tt.rule))
			var compat option.PlainRuleSetCompat
			if err := SJSON.Unmarshal(body, &compat); err != nil {
				t.Fatal(err)
			}
			matcher, err := runtimeRule.NewHeadlessRule(ctx, compat.Options.Rules[0])
			if err != nil {
				t.Fatal(err)
			}
			metadata := &adapter.InboundContext{Destination: M.Socksaddr{Addr: netip.MustParseAddr("203.0.113.10")}}
			if got := matcher.Match(metadata); got != tt.emptyMatch {
				t.Fatalf("unexpected pinned literal-IP behavior: %v", got)
			}
			for _, format := range []lists.Format{lists.FormatJSON, lists.FormatSRS} {
				b := body
				if format == lists.FormatSRS {
					var buffer bytes.Buffer
					if err := srs.Write(&buffer, compat.Options, 3); err != nil {
						t.Fatal(err)
					}
					b = buffer.Bytes()
				}
				if _, err := lists.ProjectNative(b, format); err == nil {
					t.Fatal("unsafe predicate admitted")
				}
			}
		})
	}
	// The compiled binary matcher below has valid LOUDS structure but Dump changes
	// prefix-marker semantics: the original matches ba, its recovered suffix does not.
	originalRule := option.HeadlessRule{Type: C.RuleTypeDefault, DefaultOptions: option.DefaultHeadlessRule{Domain: []string{"\ra"}}}
	var binary bytes.Buffer
	if err := srs.Write(&binary, option.PlainRuleSet{Rules: []option.HeadlessRule{originalRule}}, 3); err != nil {
		t.Fatal(err)
	}
	compat, err := srs.Read(bytes.NewReader(binary.Bytes()), false)
	if err != nil {
		t.Fatal(err)
	}
	original, err := runtimeRule.NewHeadlessRule(ctx, compat.Options.Rules[0])
	if err != nil {
		t.Fatal(err)
	}
	if !original.Match(&adapter.InboundContext{Domain: "ba"}) {
		t.Fatal("noncanonical original premise absent")
	}
	recovered, err := srs.Read(bytes.NewReader(binary.Bytes()), true)
	if err != nil {
		t.Fatal(err)
	}
	rule := recovered.Options.Rules[0]
	rule.DefaultOptions.DomainMatcher = nil
	projection, err := runtimeRule.NewHeadlessRule(ctx, rule)
	if err != nil {
		t.Fatal(err)
	}
	if projection.Match(&adapter.InboundContext{Domain: "ba"}) {
		t.Fatal("expected lossy Dump premise absent")
	}
	if _, err := lists.ProjectNative(binary.Bytes(), lists.FormatSRS); err == nil {
		t.Fatal("noncanonical compiled SRS accepted")
	}
}
