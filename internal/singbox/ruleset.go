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

// buildRuleSets собирает наборы совпадений одного правила и их теги в порядке
// «community-списки, свои домены и подсети, свои внешние списки».
//
// На один и тот же набор ссылаются и route.rules, и dns.rules — так DNS и
// маршрутизация не разъезжаются. Пустой список тегов означает правило без единого
// условия: вызывающий обязан такое правило пропустить, иначе оно поймает всё.
//
// Третье значение — адреса plain-списков, содержимого которых у генератора нет.
// Молчать про них нельзя: их домены в конфиг не попали, и если других условий у
// правила не осталось, оно выпадет целиком (issue #125).
func buildRuleSets(r store.Rule, s store.Settings, plain PlainLists) ([]option.RuleSet, []string, []string) {
	var (
		sets    []option.RuleSet
		tags    []string
		missing []string
	)

	for _, key := range r.CommunityLists {
		tag := communityTag(key)
		sets = append(sets, remoteRuleSet(tag, fmt.Sprintf(communityListURL, key), s))
		tags = append(tags, tag)
	}

	if inline, ok := inlineRuleSet(ruleSetTag(r.ID), PlainList{Domains: r.Domains, Subnets: r.Subnets}); ok {
		sets = append(sets, inline)
		tags = append(tags, inline.Tag)
	}

	for i, raw := range r.RemoteLists {
		format, ok := ruleSetFormat(raw)
		if !ok {
			// Не .srs и не .json — sing-box такой список не прочитает, но
			// слой lists его качает и разбирает. Содержимое уходит в
			// inline-набор правила: иначе домены списка пропадали бы молча, а
			// правило, у которого он единственный, выпадало бы из конфига —
			// и его трафик забирал бы route.final, то есть прямой выход
			// (issue #125, docs/04-dns-fakeip.md).
			set, ok := plainRuleSet(plainListTag(r.ID, i), raw, plain)
			if !ok {
				missing = append(missing, raw)
				continue
			}
			sets = append(sets, set)
			tags = append(tags, set.Tag)
			continue
		}
		tag := remoteListTag(r.ID, i)
		set := remoteRuleSet(tag, raw, s)
		set.Format = format
		sets = append(sets, set)
		tags = append(tags, tag)
	}

	return sets, tags, missing
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
	_, tags, _ := buildRuleSets(r, store.Settings{}, plain)
	return len(tags) > 0
}

// plainRuleSet собирает набор из скачанного plain-списка. Второе значение
// false — содержимого нет вовсе: список не скачан либо в нём не нашлось ни
// одного домена и ни одной подсети.
func plainRuleSet(tag, url string, plain PlainLists) (option.RuleSet, bool) {
	if plain == nil {
		return option.RuleSet{}, false
	}
	l, ok := plain(url)
	if !ok {
		return option.RuleSet{}, false
	}
	return inlineRuleSet(tag, l)
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
