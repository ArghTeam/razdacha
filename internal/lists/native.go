package lists

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"reflect"
	"regexp"

	"github.com/sagernet/sing-box/common/srs"
	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	SJSON "github.com/sagernet/sing/common/json"
	"go4.org/netipx"
)

// ProjectionVersion pins the native semantic compiler contract.
const ProjectionVersion = 1
const maxNativeNodes = 10000
const maxNativeDepth = 32
const maxNativeValues = 200000

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
	if format == FormatSRS {
		if err = preflightSRS(body); err != nil {
			return out, err
		}
		compat, err = srs.Read(bytes.NewReader(body), true)
	} else if format == FormatJSON {
		if err = checkNativeJSON(body); err != nil {
			return out, err
		}
		err = SJSON.Unmarshal(body, &compat)
	} else {
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
	exprs := make([]nativeExpr, 0, len(set.Rules))
	for _, r := range set.Rules {
		e, eerr := compileNative(r, 0, &nodes, &values)
		if eerr != nil {
			return out, eerr
		}
		exprs = append(exprs, e)
	}
	e, err := combineNative(exprs, C.LogicalTypeOr, false)
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

func compileNative(r option.HeadlessRule, depth int, nodes, values *int) (nativeExpr, error) {
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
			e, err := compileNative(rr, depth+1, nodes, values)
			if err != nil {
				return e, err
			}
			ee = append(ee, e)
		}
		return combineNative(ee, l.Mode, l.Invert)
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
	}
	if e.domain == nil && e.address == nil {
		return bad("empty default node")
	}
	return invertNative(e, d.Invert)
}

func combineNative(ee []nativeExpr, mode string, invert bool) (nativeExpr, error) {
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
	var addr *netipx.IPSet
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
			var b netipx.IPSetBuilder
			if addr == nil {
				b.AddSet(e.address)
			} else {
				b.AddSet(addr)
				if mode == C.LogicalTypeAnd {
					b.Intersect(e.address)
				} else {
					b.AddSet(e.address)
				}
			}
			var err error
			addr, err = b.IPSet()
			if err != nil {
				return out, err
			}
		}
	}
	if len(dr) == 1 {
		out.domain = &dr[0]
	} else if len(dr) > 1 {
		r := option.HeadlessRule{Type: C.RuleTypeLogical, LogicalOptions: option.LogicalHeadlessRule{Mode: mode, Rules: dr}}
		out.domain = &r
	}
	out.address = addr
	out.emptyDomain = empty
	return invertNative(out, invert)
}

func invertNative(e nativeExpr, invert bool) (nativeExpr, error) {
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
		var b netipx.IPSetBuilder
		b.AddPrefix(netip.MustParsePrefix("0.0.0.0/0"))
		b.RemoveSet(e.address)
		var err error
		e.address, err = b.IPSet()
		if err != nil {
			return e, err
		}
	}
	return e, nil
}

// Token validation rejects duplicate and unknown fields even when null/zero.
func checkNativeJSON(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	nodes, values := 0, 0
	bad := func(reason string) error { return fmt.Errorf("%w: %s", ErrUnsupportedNative, reason) }
	var object func(int, bool) error
	var rules func(int) error
	rules = func(depth int) error {
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
		if top {
			if !seen["version"] || !seen["rules"] {
				return bad("missing document fields")
			}
		} else if kind == "logical" {
			if !seen["rules"] || !seen["mode"] {
				return bad("missing logical fields")
			}
			for key := range seen {
				if key != "type" && key != "rules" && key != "mode" && key != "invert" {
					return bad("default field in logical node")
				}
			}
		} else if seen["rules"] || seen["mode"] {
			return bad("logical field in default node")
		}
		return nil
	}
	if e := object(0, true); e != nil {
		return e
	}
	if _, e := dec.Token(); e != io.EOF {
		return bad("trailing JSON")
	}
	return nil
}
