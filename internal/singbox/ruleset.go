package singbox

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// PlainList supplies one frozen source: inline plain content or native artifact authority.
type PlainList struct {
	Domains []string
	Subnets []string
	Native  *lists.NativeArtifact
}

// PlainLists is the compatibility callback for one frozen source read per generation.
// Native source acquisition and validation belong to the daemon, not this generator.
type PlainLists func(url string) (PlainList, bool)

// ruleSets separates full destination matching from immediate DNS matching.
// Missing contains unavailable or empty plain sources, preserving diagnostics.
type ruleSets struct {
	Sets      []option.RuleSet
	RouteTags []string
	DNSTags   []string
	Missing   []string
	Err       error
}

// buildRuleSets reads each source once for full routing and exact DNS projections.
// Native originals and projections are immutable local artifacts (ADR 0025).
func buildRuleSets(r store.Rule, s store.Settings, plain PlainLists) ruleSets {
	var result ruleSets
	addInline := func(tag string, content PlainList) bool {
		set, ok := inlineRuleSet(tag, content)
		if !ok {
			return false
		}
		result.Sets = append(result.Sets, set)
		result.RouteTags = append(result.RouteTags, tag)
		if dns, ok := inlineRuleSet(tag+"-dns", PlainList{Domains: content.Domains}); ok {
			result.Sets = append(result.Sets, dns)
			result.DNSTags = append(result.DNSTags, dns.Tag)
		}
		return true
	}
	addNative := func(tag, raw string) {
		if result.Err != nil {
			return
		}
		var content PlainList
		var found bool
		if plain != nil {
			content, found = plain(raw)
		}
		if !found || content.Native == nil {
			result.Err = fmt.Errorf("required native list is not loaded")
			return
		}
		artifact := content.Native
		format := C.RuleSetFormatSource
		if artifact.Format == lists.FormatSRS {
			format = C.RuleSetFormatBinary
		}
		result.Sets = append(result.Sets, option.RuleSet{Type: C.RuleSetTypeLocal, Tag: tag, Format: format, LocalOptions: option.LocalRuleSet{Path: artifact.Path}})
		result.RouteTags = append(result.RouteTags, tag)
		if artifact.DNSPath != "" {
			dnsTag := tag + "-dns"
			result.Sets = append(result.Sets, option.RuleSet{Type: C.RuleSetTypeLocal, Tag: dnsTag, Format: C.RuleSetFormatSource, LocalOptions: option.LocalRuleSet{Path: artifact.DNSPath}})
			result.DNSTags = append(result.DNSTags, dnsTag)
		}
	}
	for _, key := range r.CommunityLists {
		addNative(communityTag(key), fmt.Sprintf(communityListURL, key))
	}
	addInline(ruleSetTag(r.ID), PlainList{Domains: r.Domains, Subnets: r.Subnets})
	for i, raw := range r.RemoteLists {
		_, native := ruleSetFormat(raw)
		if !native {
			var content PlainList
			var found bool
			if plain != nil {
				content, found = plain(raw)
			}
			if !found || !addInline(plainListTag(r.ID, i), content) {
				result.Missing = append(result.Missing, raw)
			}
			continue
		}
		addNative(remoteListTag(r.ID, i), raw)
	}
	return result
}

// RuleInConfig — попадёт ли включённое правило в конфиг.
//
// Единственная причина не попасть — не осталось ни одного условия совпадения:
// такое правило нельзя выразить ни маршрутом, ни отказом, потому что совпадать
// оно будет со всем ([GenerateWithDiag]). Чаще всего это правило, у которого
// единственное условие — plain-список, ещё не приехавший в кэш.
//
// Отдельная функция нужна пробнику маршрута: он сверяет число правил в живом
// конфиге с состоянием БД, и считать по одному `Enabled` нельзя — правило со
// сломанным списком тогда выглядит непримененной правкой, хотя применять
// нечего (issue #149).
func RuleInConfig(r store.Rule, plain PlainLists) bool {
	if !r.Enabled {
		return false
	}
	// Настройки влияют только на период обновления remote-наборов, на состав
	// тегов — нет: считать их ради счёта незачем.
	built := buildRuleSets(r, store.Settings{}, plain)
	return built.Err == nil && len(built.RouteTags) > 0
}

// inlineRuleSet собирает набор из доменов и подсетей: своих у правила либо
// разобранных слоем lists из plain-списка.
//
// Domains and CIDRs are separate entries; both represent the destination OR
// semantics of the pinned runtime. DNS uses a separate domain-only set.
//
// Набор inline, а не local: конфиг остаётся единственным артефактом на диске.
// Файлы наборов пришлось бы писать рядом и держать в согласии с конфигом.
func inlineRuleSet(tag string, l PlainList) (option.RuleSet, bool) {
	var rules []option.HeadlessRule
	if len(l.Domains) > 0 {
		rules = append(rules, headless(option.DefaultHeadlessRule{
			DomainSuffix: badoption.Listable[string](l.Domains),
		}))
	}
	if len(l.Subnets) > 0 {
		rules = append(rules, headless(option.DefaultHeadlessRule{
			IPCIDR: badoption.Listable[string](l.Subnets),
		}))
	}
	if len(rules) == 0 {
		return option.RuleSet{}, false
	}
	return option.RuleSet{
		Type:          C.RuleSetTypeInline,
		Tag:           tag,
		InlineOptions: option.PlainRuleSet{Rules: rules},
	}, true
}

// headless оборачивает условия в запись набора.
func headless(rule option.DefaultHeadlessRule) option.HeadlessRule {
	return option.HeadlessRule{Type: C.RuleTypeDefault, DefaultOptions: rule}
}

// ruleSetFormat определяет формат внешнего списка по расширению. Всё, кроме .srs
// и .json, для sing-box не подходит.
func ruleSetFormat(raw string) (string, bool) {
	p := raw
	if u, err := url.Parse(raw); err == nil {
		p = u.Path
	}
	switch strings.ToLower(path.Ext(p)) {
	case ".srs":
		return C.RuleSetFormatBinary, true
	case ".json":
		return C.RuleSetFormatSource, true
	default:
		return "", false
	}
}
