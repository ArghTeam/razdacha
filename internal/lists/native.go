package lists

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/bits"
	"net/netip"
	"reflect"
	"regexp"
	"sort"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	SJSON "github.com/sagernet/sing/common/json"
	"go4.org/netipx"
)

// ProjectionVersion pins the native semantic compiler contract.
const (
	ProjectionVersion       = 1
	maxNativeNodes          = 10000
	maxNativeDepth          = 32
	maxNativeValues         = 200000
	maxNativeProjectionWork = 4 << 20
)

var ErrUnsupportedNative = errors.New("unsupported native predicate")

// NativeProjection is derived from the entire AST, never from flattened entries.
type NativeProjection struct {
	DNS     []byte
	Subnets []string
}

type nativeExpr struct {
	domain      *option.HeadlessRule
	address     *netipx.IPSet
	emptyDomain bool
	ranges      []netipx.IPRange
}

// Projection work counts range visits and a conservative normalization log factor
// across the entire AST, including intermediate complements and intersections.
type nativeWork struct {
	remaining int
	regex     nativeRegexBudget
}

func (w *nativeWork) charge(n int, normalize bool) error {
	if normalize && n > 0 {
		n *= bits.Len(uint(n)) + 1
	}
	if n > w.remaining {
		return fmt.Errorf("%w: native projection work budget", ErrUnsupportedNative)
	}
	w.remaining -= n
	return nil
}

// ProjectNative accepts only an exact separable D(domain) OR A(address) expression.
// Domain matchers in the pinned runtime always return false without a domain,
// even when their regex matches the empty string. Inversion changes that fact.
func ProjectNative(body []byte, format Format) (out NativeProjection, err error) {
	defer func() {
		if r := recover(); r != nil {
			out = NativeProjection{}
			err = fmt.Errorf("%w: malformed native structure", ErrUnsupportedNative)
		}
	}()
	if len(body) == 0 || len(body) > defaultMaxBytes {
		return out, fmt.Errorf("%w: native body size", ErrUnsupportedNative)
	}
	var compat option.PlainRuleSetCompat
	switch format {
	case FormatSRS:
		if err = preflightSRS(body); err != nil {
			return out, err
		}
		compat, err = srs.Read(bytes.NewReader(body), true)
	case FormatJSON:
		if err = checkNativeJSON(body); err != nil {
			return out, err
		}
		err = SJSON.Unmarshal(body, &compat)
	default:
		return out, fmt.Errorf("%w: native format", ErrUnsupportedNative)
	}
	if err != nil {
		return out, fmt.Errorf("%w: decode: %w", ErrUnsupportedNative, err)
	}
	if compat.Version < 1 || compat.Version > 3 {
		return out, fmt.Errorf("%w: version", ErrUnsupportedNative)
	}
	set, err := compat.Upgrade()
	if err != nil {
		return out, err
	}
	if len(set.Rules) == 0 {
		return out, fmt.Errorf("%w: empty native set", ErrUnsupportedNative)
	}
	nodes, values := 0, 0
	work := &nativeWork{remaining: maxNativeProjectionWork}
	exprs := make([]nativeExpr, 0, len(set.Rules))
	for _, r := range set.Rules {
		e, eerr := compileNative(r, 0, &nodes, &values, work)
		if eerr != nil {
			return out, eerr
		}
		exprs = append(exprs, e)
	}
	e, err := combineNative(exprs, C.LogicalTypeOr, false, work)
	if err != nil {
		return out, err
	}
	if e.emptyDomain {
		return out, fmt.Errorf("%w: unbounded literal-IP domain coverage", ErrUnsupportedNative)
	}
	if e.address != nil {
		prefixes := e.address.Prefixes()
		if len(prefixes) == 1 && prefixes[0] == netip.MustParsePrefix("0.0.0.0/0") {
			return out, fmt.Errorf("%w: unbounded IPv4 coverage", ErrUnsupportedNative)
		}
		if len(prefixes) > maxNativeValues {
			return out, fmt.Errorf("%w: projection budget", ErrUnsupportedNative)
		}
		for _, p := range prefixes {
			out.Subnets = append(out.Subnets, p.String())
		}
	}
	if e.domain != nil {
		out.DNS, err = SJSON.Marshal(option.PlainRuleSetCompat{Version: 3, Options: option.PlainRuleSet{Rules: []option.HeadlessRule{*e.domain}}})
	}
	return out, err
}

func compileNative(r option.HeadlessRule, depth int, nodes, values *int, work *nativeWork) (nativeExpr, error) {
	*nodes++
	bad := func(reason string) (nativeExpr, error) {
		return nativeExpr{}, fmt.Errorf("%w: %s", ErrUnsupportedNative, reason)
	}
	if depth > maxNativeDepth || *nodes > maxNativeNodes {
		return bad("AST complexity budget")
	}
	if r.Type == C.RuleTypeLogical {
		l := r.LogicalOptions
		if len(l.Rules) == 0 || (l.Mode != C.LogicalTypeAnd && l.Mode != C.LogicalTypeOr) {
			return bad("empty or invalid logical node")
		}
		ee := make([]nativeExpr, 0, len(l.Rules))
		for _, rr := range l.Rules {
			e, err := compileNative(rr, depth+1, nodes, values, work)
			if err != nil {
				return e, err
			}
			ee = append(ee, e)
		}
		return combineNative(ee, l.Mode, l.Invert, work)
	}
	if r.Type != "" && r.Type != C.RuleTypeDefault {
		return bad("rule type")
	}
	d := r.DefaultOptions
	// Reject every field outside the explicit whitelist, including compiled SRS state.
	allowed := option.DefaultHeadlessRule{Domain: d.Domain, DomainSuffix: d.DomainSuffix, DomainKeyword: d.DomainKeyword, DomainRegex: d.DomainRegex, IPCIDR: d.IPCIDR, Invert: d.Invert, DomainMatcher: d.DomainMatcher, IPSet: d.IPSet}
	if !reflect.DeepEqual(d, allowed) {
		return bad("context-dependent or unknown field")
	}
	for _, vv := range [][]string{d.Domain, d.DomainSuffix, d.DomainKeyword, d.DomainRegex, d.IPCIDR} {
		*values += len(vv)
		for _, v := range vv {
			if v == "" || len(v) > 4096 {
				return bad("empty or oversized value")
			}
		}
	}
	if *values > maxNativeValues {
		return bad("predicate value budget")
	}
	for _, v := range d.DomainRegex {
		if err := work.regex.admit(v); err != nil {
			return nativeExpr{}, err
		}
		if _, err := regexp.Compile(v); err != nil {
			return bad("invalid domain regex")
		}
	}
	e := nativeExpr{}
	if len(d.Domain)+len(d.DomainSuffix)+len(d.DomainKeyword)+len(d.DomainRegex) > 0 {
		dom := d
		dom.IPCIDR = nil
		dom.IPSet = nil
		dom.DomainMatcher = nil
		dom.Invert = false
		rr := option.HeadlessRule{Type: C.RuleTypeDefault, DefaultOptions: dom}
		e.domain = &rr
	}
	if len(d.IPCIDR) > 0 {
		if err := work.charge(len(d.IPCIDR), true); err != nil {
			return e, err
		}
		var b netipx.IPSetBuilder
		for _, v := range d.IPCIDR {
			p, err := netip.ParsePrefix(v)
			if err != nil {
				a, aerr := netip.ParseAddr(v)
				if aerr != nil {
					return bad("invalid address")
				}
				p = netip.PrefixFrom(a, a.BitLen())
			}
			if p.Addr().Is4() {
				b.AddPrefix(p.Masked())
			}
		}
		var err error
		e.address, err = b.IPSet()
		if err != nil {
			return bad("invalid address set")
		}
		e.ranges = e.address.Ranges()
	}
	if e.domain == nil && e.address == nil {
		return bad("empty default node")
	}
	return invertNative(e, d.Invert, work)
}

func combineNative(ee []nativeExpr, mode string, invert bool, work *nativeWork) (nativeExpr, error) {
	out := nativeExpr{}
	if mode == C.LogicalTypeAnd {
		allD, allA := true, true
		for _, e := range ee {
			allD = allD && e.domain != nil && e.address == nil
			allA = allA && e.domain == nil && e.address != nil
		}
		if !allD && !allA {
			return out, fmt.Errorf("%w: mixed domain/address logical AND", ErrUnsupportedNative)
		}
	}
	var dr []option.HeadlessRule
	var addressSets [][]netipx.IPRange
	totalRanges := 0
	empty := mode == C.LogicalTypeAnd
	for _, e := range ee {
		if e.domain != nil {
			dr = append(dr, *e.domain)
		}
		if mode == C.LogicalTypeAnd {
			empty = empty && e.emptyDomain
		} else {
			empty = empty || e.emptyDomain
		}
		if e.address != nil {
			addressSets = append(addressSets, e.ranges)
			totalRanges += len(e.ranges)
		}
	}
	var addr *netipx.IPSet
	if len(addressSets) > 0 {
		var builder netipx.IPSetBuilder
		if mode == C.LogicalTypeOr {
			// Accumulate all siblings and normalize once; repeated cumulative union is quadratic.
			if err := work.charge(totalRanges, true); err != nil {
				return out, err
			}
			for _, ranges := range addressSets {
				for _, r := range ranges {
					builder.AddRange(r)
				}
			}
		} else {
			sort.Slice(addressSets, func(i, j int) bool { return len(addressSets[i]) < len(addressSets[j]) })
			ranges := addressSets[0]
			for _, next := range addressSets[1:] {
				if err := work.charge(len(ranges)+len(next), false); err != nil {
					return out, err
				}
				ranges = intersectNativeRanges(ranges, next)
				if len(ranges) == 0 {
					break
				}
			}
			if err := work.charge(len(ranges), true); err != nil {
				return out, err
			}
			for _, r := range ranges {
				builder.AddRange(r)
			}
		}
		var err error
		addr, err = builder.IPSet()
		if err != nil {
			return out, err
		}
		out.ranges = addr.Ranges()
	}

	if len(dr) == 1 {
		out.domain = &dr[0]
	} else if len(dr) > 1 {
		r := option.HeadlessRule{Type: C.RuleTypeLogical, LogicalOptions: option.LogicalHeadlessRule{Mode: mode, Rules: dr}}
		out.domain = &r
	}
	out.address = addr
	out.emptyDomain = empty
	return invertNative(out, invert, work)
}

func invertNative(e nativeExpr, invert bool, work *nativeWork) (nativeExpr, error) {
	if !invert {
		return e, nil
	}
	if e.domain != nil && e.address != nil {
		return nativeExpr{}, fmt.Errorf("%w: mixed domain/address inversion", ErrUnsupportedNative)
	}
	if e.domain != nil {
		r := *e.domain
		if r.Type == C.RuleTypeLogical {
			r.LogicalOptions.Invert = !r.LogicalOptions.Invert
		} else {
			r.DefaultOptions.Invert = !r.DefaultOptions.Invert
		}
		e.domain = &r
		e.emptyDomain = !e.emptyDomain
	}
	if e.address != nil {
		if err := work.charge(len(e.ranges)+1, true); err != nil {
			return e, err
		}
		var b netipx.IPSetBuilder
		b.AddPrefix(netip.MustParsePrefix("0.0.0.0/0"))
		b.RemoveSet(e.address)
		var err error
		e.address, err = b.IPSet()
		if err != nil {
			return e, err
		}
		e.ranges = e.address.Ranges()
	}
	return e, nil
}

// Inputs are canonical sorted disjoint ranges; a linear merge preserves that order.
func intersectNativeRanges(a, b []netipx.IPRange) []netipx.IPRange {
	var out []netipx.IPRange
	for i, j := 0, 0; i < len(a) && j < len(b); {
		from, to := a[i].From(), a[i].To()
		if b[j].From().Compare(from) > 0 {
			from = b[j].From()
		}
		if b[j].To().Compare(to) < 0 {
			to = b[j].To()
		}
		if from.Compare(to) <= 0 {
			out = append(out, netipx.IPRangeFrom(from, to))
		}
		if a[i].To().Compare(b[j].To()) < 0 {
			i++
		} else {
			j++
		}
	}
	return out
}

// Token validation rejects duplicate and unknown fields even when null/zero.
func checkNativeJSON(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	nodes, values := 0, 0
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrUnsupportedNative, reason) }
	var object func(int, bool) error
	rules := func(depth int) error {
		t, e := dec.Token()
		if e != nil || t != json.Delim('[') {
			return bad("invalid rules array")
		}
		count := 0
		for dec.More() {
			count++
			if e := object(depth, false); e != nil {
				return e
			}
		}
		_, e = dec.Token()
		if count == 0 {
			return bad("empty rules array")
		}
		return e
	}
	stringsValue := func() error {
		t, e := dec.Token()
		if e != nil {
			return e
		}
		value := func(t json.Token) error {
			s, ok := t.(string)
			values++
			if !ok || s == "" || len(s) > 4096 || values > maxNativeValues {
				return bad("string value budget/type")
			}
			return nil
		}
		if t == json.Delim('[') {
			for dec.More() {
				t, e = dec.Token()
				if e != nil {
					return e
				}
				if e = value(t); e != nil {
					return e
				}
			}
			_, e = dec.Token()
			return e
		}
		return value(t)
	}
	object = func(depth int, top bool) error {
		nodes++
		if depth > maxNativeDepth || nodes > maxNativeNodes {
			return bad("JSON complexity budget")
		}
		t, e := dec.Token()
		if e != nil || t != json.Delim('{') {
			return bad("invalid native object")
		}
		seen := map[string]bool{}
		kind := ""
		for dec.More() {
			t, e = dec.Token()
			if e != nil {
				return e
			}
			key, ok := t.(string)
			if !ok || seen[key] {
				return bad("duplicate native field")
			}
			seen[key] = true
			if key == "rules" {
				if e = rules(depth + 1); e != nil {
					return e
				}
				continue
			}
			if top {
				if key != "version" {
					return bad("unknown native document field")
				}
				t, e = dec.Token()
				if e != nil {
					return e
				}
				v, ok := t.(float64)
				if !ok || v < 1 || v > 3 || v != float64(uint8(v)) {
					return bad("version")
				}
				continue
			}
			switch key {
			case "domain", "domain_suffix", "domain_keyword", "domain_regex", "ip_cidr":
				if e = stringsValue(); e != nil {
					return e
				}
			case "invert":
				t, e = dec.Token()
				if e != nil {
					return e
				}
				if _, ok := t.(bool); !ok {
					return bad("invert type")
				}
			case "type", "mode":
				t, e = dec.Token()
				if e != nil {
					return e
				}
				v, ok := t.(string)
				if !ok {
					return bad("rule type/mode")
				}
				if key == "type" {
					if v != "" && v != "default" && v != "logical" {
						return bad("rule type")
					}
					kind = v
				} else if v != "and" && v != "or" {
					return bad("logical mode")
				}
			default:
				return bad("unknown or unsupported native field")
			}
		}
		_, e = dec.Token()
		if e != nil {
			return e
		}
		switch {
		case top:
			if !seen["version"] || !seen["rules"] {
				return bad("missing document fields")
			}
		case kind == "logical":
			if !seen["rules"] || !seen["mode"] {
				return bad("missing logical fields")
			}
			for key := range seen {
				if key != "type" && key != "rules" && key != "mode" && key != "invert" {
					return bad("default field in logical node")
				}
			}
		case seen["rules"] || seen["mode"]:
			return bad("logical field in default node")
		}
		return nil
	}
	if e := object(0, true); e != nil {
		return e
	}
	if _, e := dec.Token(); !errors.Is(e, io.EOF) {
		return bad("trailing JSON")
	}
	return nil
}
