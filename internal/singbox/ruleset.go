package singbox

import (
	"fmt"
	"net/url"
	"path"
	"strings"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json/badoption"

	"github.com/ArghTeam/razdacha/internal/store"
)

// PlainList — разобранное содержимое внешнего списка, который sing-box сам
// прочитать не может: домены и подсети «по строке на запись».
type PlainList struct {
	Domains []string
	Subnets []string
}

// PlainLists отдаёт содержимое такого списка по его адресу. Второе значение
// false означает, что списка ещё нет: не скачался, скачается позже или
// планировщик не поднят. Нулевое замыкание допустимо и равнозначно «нет ни
// одного списка».
//
// Замыкание, а не тип слоя lists: генератор про загрузку и кэш не знает,
// проводку даёт демон (cmd/razdachad/lists.go) — тем же приёмом, каким слою api
// достаётся заливка nft.
type PlainLists func(url string) (PlainList, bool)

// ruleSets separates full destination matching from immediate DNS matching.
// Missing contains unavailable or empty plain sources, preserving diagnostics.
type ruleSets struct {
	Sets      []option.RuleSet
	RouteTags []string
	DNSTags   []string
	Missing   []string
}

// buildRuleSets reads each plain source once for both projections (ADR 0024).
// Native formats retain their predicates and shared tags; see issue #220.
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
	for _, key := range r.CommunityLists {
		tag := communityTag(key)
		result.Sets = append(result.Sets, remoteRuleSet(tag, fmt.Sprintf(communityListURL, key), s))
		result.RouteTags = append(result.RouteTags, tag)
		result.DNSTags = append(result.DNSTags, tag)
	}
	addInline(ruleSetTag(r.ID), PlainList{Domains: r.Domains, Subnets: r.Subnets})
	for i, raw := range r.RemoteLists {
		format, native := ruleSetFormat(raw)
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
		tag := remoteListTag(r.ID, i)
		set := remoteRuleSet(tag, raw, s)
		set.Format = format
		result.Sets = append(result.Sets, set)
		result.RouteTags = append(result.RouteTags, tag)
		result.DNSTags = append(result.DNSTags, tag)
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
	return len(buildRuleSets(r, store.Settings{}, plain).RouteTags) > 0
}

// inlineRuleSet собирает набор из доменов и подсетей: своих у правила либо
// разобранных слоем lists из plain-списка.
//
// Домены и подсети — две отдельные записи набора: внутри одной записи условия
// складываются по «и», а нам нужно «или».
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

// remoteRuleSet — удалённый набор, который sing-box обновляет сам по
// update_interval из настроек.
func remoteRuleSet(tag, source string, s store.Settings) option.RuleSet {
	return option.RuleSet{
		Type: C.RuleSetTypeRemote,
		Tag:  tag,
		RemoteOptions: option.RemoteRuleSet{
			URL:            source,
			UpdateInterval: badoption.Duration(s.ListUpdateInterval),
		},
	}
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
