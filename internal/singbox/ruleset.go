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

// buildRuleSets собирает наборы совпадений одного правила и их теги в порядке
// «community-списки, свои домены и подсети, свои внешние списки».
//
// На один и тот же набор ссылаются и route.rules, и dns.rules — так DNS и
// маршрутизация не разъезжаются. Пустой список тегов означает правило без единого
// условия: вызывающий обязан такое правило пропустить, иначе оно поймает всё.
func buildRuleSets(r store.Rule, s store.Settings) ([]option.RuleSet, []string, error) {
	var (
		sets []option.RuleSet
		tags []string
	)

	for _, key := range r.CommunityLists {
		tag := communityTag(key)
		sets = append(sets, remoteRuleSet(tag, fmt.Sprintf(communityListURL, key), s))
		tags = append(tags, tag)
	}

	if inline, ok := inlineRuleSet(r); ok {
		sets = append(sets, inline)
		tags = append(tags, inline.Tag)
	}

	for i, raw := range r.RemoteLists {
		format, ok := ruleSetFormat(raw)
		if !ok {
			// Не .srs и не .json — sing-box такой список не прочитает.
			// Его скачивает слой lists и заливает подсети в nft-сет.
			continue
		}
		tag := remoteListTag(r.ID, i)
		set := remoteRuleSet(tag, raw, s)
		set.Format = format
		sets = append(sets, set)
		tags = append(tags, tag)
	}

	return sets, tags, nil
}

// inlineRuleSet собирает набор из своих доменов и подсетей правила.
//
// Домены и подсети — две отдельные записи набора: внутри одной записи условия
// складываются по «и», а нам нужно «или».
//
// Набор inline, а не local: конфиг остаётся единственным артефактом на диске.
// Файлы наборов пришлось бы писать рядом и держать в согласии с конфигом.
func inlineRuleSet(r store.Rule) (option.RuleSet, bool) {
	var rules []option.HeadlessRule
	if len(r.Domains) > 0 {
		rules = append(rules, headless(option.DefaultHeadlessRule{
			DomainSuffix: badoption.Listable[string](r.Domains),
		}))
	}
	if len(r.Subnets) > 0 {
		rules = append(rules, headless(option.DefaultHeadlessRule{
			IPCIDR: badoption.Listable[string](r.Subnets),
		}))
	}
	if len(rules) == 0 {
		return option.RuleSet{}, false
	}
	return option.RuleSet{
		Type:          C.RuleSetTypeInline,
		Tag:           ruleSetTag(r.ID),
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
