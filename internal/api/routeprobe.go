package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Пробник маршрута: «куда уйдёт этот домен и по какому правилу».
//
// Спрашивается работающий sing-box, а не БД, и это главное свойство ручки.
// Порядок «правки копятся — применяются кнопкой» означает, что накопленное в
// панели и работающее в ядре расходятся штатно, и объяснять словами порядок
// правил бессмысленно, если применён другой их набор. Поэтому источников
// ровно два, оба живые:
//
//   - `GET /rules` Clash API — правила маршрутизации в том порядке, в каком их
//     проверяет ядро, и действие каждого (`route(tun-…)`, `reject`);
//   - `GET /dns/query` — ответ того самого резолвера, к которому ходят клиенты:
//     FakeIP означает, что домен поймало правило, настоящий адрес — что не
//     поймало ни одно (docs/04-dns-fakeip.md).
//
// Чего ядро не отдаёт — состав наборов `rule_set`: содержимое `.srs`
// community-списков живёт внутри него. Поэтому правило, чьи условия демону не
// видны целиком, называется кандидатом, а не победителем: соврать про
// сработавшее правило хуже, чем сказать «одно из этих».
//
// Недоступный Clash API — отказ с объяснением (503), а не пустой ответ: пустой
// результат читается как «ни одно правило не сработает», а это утверждение о
// маршрутизации, которого мы не проверяли.

// probeRule — правило в ответе пробника.
type probeRule struct {
	// ID и Name — правило из БД, к которому удалось привязать живое.
	ID   string `json:"id"`
	Name string `json:"name"`
	// Action — действие правила из БД (`tunnel`, `direct`, `block`).
	Action string `json:"action"`
	// Outbound — действие живого ядра как есть: `route(tun-…)`, `reject`.
	Outbound string `json:"outbound"`
	// Certain — видит ли демон условия правила целиком. false означает, что у
	// правила есть наборы, состав которых ведёт сам sing-box.
	Certain bool `json:"certain"`
}

// probeResponse — тело `POST /api/route/test`.
type probeResponse struct {
	Domain string `json:"domain"`
	// Addresses — что ответил резолвер ядра. Пусто означает «не ответил».
	Addresses []string `json:"addresses"`
	// FakeIP — выдан ли адрес из диапазона FakeIP, то есть поймало ли домен
	// хоть одно правило с туннелем.
	FakeIP bool `json:"fakeip"`
	// ResolveError — почему резолвер не выдал адрес. Пусто — выдал.
	ResolveError string `json:"resolve_error,omitempty"`
	// Refused — резолвер отклонил запрос по правилу (REFUSED). Это ответ о
	// маршрутизации, а не поломка DNS: так отвечает правило «блокировать» и
	// правило с недоступным туннелем (ADR 0013).
	Refused bool `json:"refused"`
	// Rule — победившее правило. null означает, что назвать его нечем.
	Rule *probeRule `json:"rule"`
	// Candidates — правила, которые могли поймать домен: их наборы ведёт ядро,
	// и состава демон не видит.
	Candidates []probeRule `json:"candidates"`
	// CoreRules — сколько правил маршрутизации в применённом конфиге (без
	// служебного перехвата DNS).
	CoreRules int `json:"core_rules"`
	// Diverged — набор правил ядра разошёлся с базой: есть непримененные правки.
	Diverged bool `json:"diverged"`
	// Verdict — тот же вывод словами, на русском. Показывается как есть.
	Verdict string `json:"verdict"`
}

// probeRequest — тело запроса.
type probeRequest struct {
	Domain string `json:"domain"`
}

// handleRouteTest — `POST /api/route/test`.
func (s *Server) handleRouteTest(w http.ResponseWriter, r *http.Request) {
	var req probeRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	domain := normalizeProbeDomain(req.Domain)
	if domain == "" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Введите домен: example.com или www.example.com — без http:// и путей.")
		return
	}

	live, err := s.clash.Rules(r.Context())
	if err != nil {
		s.log.Warn("пробник маршрута: правила ядра не прочитаны", "домен", domain, "ошибка", err)
		writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady,
			"Пробник спрашивает работающий sing-box, а он не ответил: "+err.Error()+
				". Проверьте состояние sing-box на экране «Диагностика».")
		return
	}

	snap, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.storeError(w, err, "Состояние не прочитано")
		return
	}

	out := s.probe(r.Context(), domain, live, snap)
	writeJSON(w, s.log, http.StatusOK, out)
}

// probe собирает ответ: живые правила слева, состояние БД справа.
func (s *Server) probe(ctx context.Context, domain string, live []clash.Rule, snap store.Snapshot) probeResponse {
	out := probeResponse{Domain: domain, Candidates: []probeRule{}}

	matched := matchLiveRules(live, snap.Rules)
	out.CoreRules = len(matched)
	// Считается по тем правилам, что реально попадают в конфиг, а не по числу
	// включённых: правило, у которого единственное условие — не скачавшийся
	// список, в конфиг не идёт, и применять там нечего. Счёт по `Enabled`
	// объявлял бы непримененной правкой ровно тот случай, ради которого
	// задача и заводилась (issue #149).
	out.Diverged = len(matched) != s.countInConfig(snap.Rules)

	// Резолв — отдельный вопрос к ядру, и его неудача не отменяет разбора
	// правил: сказать, какое правило ловит домен, можно и без адреса.
	addrs, rerr := s.clash.ResolveA(ctx, domain)
	var resolveErr *clash.ResolveError
	if rerr != nil {
		out.ResolveError = rerr.Error()
		errors.As(rerr, &resolveErr)
		s.log.Debug("пробник маршрута: адреса нет", "домен", domain, "ошибка", rerr)
	}
	fake := singbox.FakeIPRange()
	for _, a := range addrs {
		out.Addresses = append(out.Addresses, a.String())
		if fake.Contains(a) {
			out.FakeIP = true
		}
	}
	// Настоящий адрес от резолвера — доказательство от ядра: ни одно правило с
	// записью в dns.rules не совпало. Такие записи есть у правила в туннель
	// (FakeIP), у правила «блокировать» и у правила с недоступным туннелем
	// (отказ, ADR 0013); нет — только у `direct` и у `resolve_real_ip`
	// (docs/04-dns-fakeip.md). Отказ резолвера ничего не доказывает: он как раз
	// и означает, что одно из правил с отказом сработало.
	proven := rerr == nil && !out.FakeIP && len(addrs) > 0

	// Порядок ядра — тот же, в каком оно проверяет правила, поэтому первое
	// совпадение и есть победитель. Ниже достоверного совпадения смотреть
	// нечего: до тех правил трафик не дойдёт.
	var hits []probeRule
	for _, m := range matched {
		hit, certain := s.ruleCatches(m.rule, domain)
		if !hit {
			continue
		}
		if !certain && proven && dnsRuled(m.rule) {
			// «Может быть» против доказательства ядра: правило с DNS-записью
			// не совпало, иначе адрес был бы подставным или его не было бы
			// вовсе. Кандидат снимается, а не остаётся висеть предположением.
			continue
		}
		hits = append(hits, probeRule{
			ID:       m.rule.ID,
			Name:     m.rule.Name,
			Action:   string(m.rule.Action),
			Outbound: m.live.Proxy,
			Certain:  certain,
		})
		if certain {
			break
		}
	}
	// Достоверное совпадение побеждает «может быть»: оно проверено по условиям,
	// которые демон видит целиком. Кандидаты выше него не выбрасываются — они
	// стоят раньше по порядку и выиграли бы, окажись домен в их списках, — но
	// победителем называется то, что известно.
	switch {
	case len(hits) > 0 && hits[len(hits)-1].Certain:
		out.Rule = &hits[len(hits)-1]
		out.Candidates = hits[:len(hits)-1]
	case len(hits) == 1 && out.FakeIP:
		// Кандидат один, и ядро подтвердило совпадение FakeIP — поймать домен
		// было больше некому.
		out.Rule = &hits[0]
	default:
		out.Candidates = hits
	}
	if out.Candidates == nil {
		out.Candidates = []probeRule{}
	}
	if resolveErr != nil && resolveErr.Refused() {
		out.Refused = true
	}

	out.Verdict = probeVerdict(out)
	return out
}

// probeVerdict переводит разбор в текст. Он показывается пользователю как есть,
// поэтому здесь и стоит, а не собирается в интерфейсе: правило «не выдумывать
// пустоту» проще держать в одном месте.
func probeVerdict(p probeResponse) string {
	var b strings.Builder
	names := func(rr []probeRule) string {
		out := make([]string, 0, len(rr))
		for _, c := range rr {
			out = append(out, fmt.Sprintf("«%s» (%s)", c.Name, c.Outbound))
		}
		return strings.Join(out, ", ")
	}

	switch {
	case p.Rule != nil:
		fmt.Fprintf(&b, "Домен ловит правило «%s», ядро отправляет его в %s.",
			p.Rule.Name, p.Rule.Outbound)
		if len(p.Candidates) > 0 {
			b.WriteString(" Выше него стоят правила со списками: " + names(p.Candidates) +
				". Состав готовых списков ведёт сам sing-box и наружу не отдаёт — если домен есть" +
				" и в них, выиграет то, что выше.")
		}
	case len(p.Candidates) > 0:
		b.WriteString("Домен ловит одно из правил со списками: " + names(p.Candidates) +
			". Какое именно — сказать нечем: состав готовых списков ведёт сам sing-box и наружу не отдаёт. " +
			"Выигрывает первое из перечисленных.")
	case p.FakeIP:
		b.WriteString("Ядро выдало FakeIP, значит домен поймало какое-то правило, " +
			"но привязать его к правилу из базы не вышло — похоже, применён другой набор правил.")
	case p.Refused:
		b.WriteString("Назвать правило нечем, но ядро отклонило запрос по правилу — " +
			"значит, домен поймало правило «блокировать» либо правило с недоступным туннелем.")
	default:
		b.WriteString("Ни одно правило домен не ловит: трафик уйдёт напрямую, с адреса сервера.")
	}

	switch {
	case p.Refused:
		b.WriteString(" Резолвер ядра адреса не выдал: " + p.ResolveError + ".")
	case p.ResolveError != "":
		b.WriteString(" Резолвер ядра адреса не выдал: " + p.ResolveError +
			". Это вопрос к DNS: правила такой ответ не дают.")
	case p.FakeIP:
		b.WriteString(" Резолвер выдал FakeIP — трафик к домену пойдёт через sing-box.")
	case len(p.Addresses) > 0:
		b.WriteString(" Резолвер выдал настоящий адрес: значит, ни одно правило с записью в DNS" +
			" домен не поймало, и в sing-box трафик попадёт только по подсети из nft-сета.")
	}

	if p.Diverged {
		b.WriteString(" Внимание: в конфиге ядра " + plural(p.CoreRules, "правило", "правила", "правил") +
			", а в базе другое число — есть непримененные правки, нажмите «Применить».")
	}
	return b.String()
}

// plural — «1 правило», «2 правила», «5 правил».
func plural(n int, one, few, many string) string {
	word := many
	switch {
	case n%10 == 1 && n%100 != 11:
		word = one
	case n%10 >= 2 && n%10 <= 4 && (n%100 < 12 || n%100 > 14):
		word = few
	}
	return fmt.Sprintf("%d %s", n, word)
}

// liveMatch — пара «живое правило ядра» ↔ «правило из базы».
type liveMatch struct {
	live clash.Rule
	rule store.Rule
}

// matchLiveRules привязывает правила работающего ядра к правилам базы.
//
// Привязка идёт по тегам наборов в условиях правила: тег `rule-<id>` содержит
// сам идентификатор, `list-<key>` — ключ community-списка. Правило перехвата
// DNS наборов не имеет и отсеивается тем же условием. Правило ядра, которому
// пары не нашлось, в разбор не попадает: назвать его нечем.
func matchLiveRules(live []clash.Rule, rules []store.Rule) []liveMatch {
	byID := make(map[string]store.Rule, len(rules))
	for _, r := range rules {
		byID[r.ID] = r
	}
	used := make(map[string]bool, len(rules))

	out := make([]liveMatch, 0, len(live))
	for _, lr := range live {
		tags := ruleSetTags(lr.Payload)
		if len(tags) == 0 {
			continue
		}
		var found store.Rule
		var ok bool
		for _, tag := range tags {
			if id, hit := ruleIDFromTag(tag, byID); hit {
				found, ok = byID[id], true
				break
			}
		}
		if !ok {
			// Условия правила — одни community-списки: идентификатора в тегах
			// нет. Тогда правило узнаётся по набору ключей и порядку.
			found, ok = byCommunityKeys(tags, rules, used)
		}
		if !ok || used[found.ID] {
			continue
		}
		used[found.ID] = true
		out = append(out, liveMatch{live: lr, rule: found})
	}
	return out
}

// ruleSetTags вынимает теги наборов из строкового представления правила:
// `rule_set=tag` или `rule_set=[tag1 tag2]`.
func ruleSetTags(payload string) []string {
	i := strings.Index(payload, "rule_set=")
	if i < 0 {
		return nil
	}
	rest := payload[i+len("rule_set="):]
	if strings.HasPrefix(rest, "[") {
		end := strings.Index(rest, "]")
		if end < 0 {
			return nil
		}
		return strings.Fields(rest[1:end])
	}
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		rest = rest[:j]
	}
	if rest == "" {
		return nil
	}
	return []string{rest}
}

// ruleIDFromTag узнаёт идентификатор правила в теге его набора. Теги строит
// слой singbox: `rule-<id>`, `rule-<id>-remote-N`, `rule-<id>-plain-N`.
func ruleIDFromTag(tag string, byID map[string]store.Rule) (string, bool) {
	if !strings.HasPrefix(tag, "rule-") {
		return "", false
	}
	rest := strings.TrimPrefix(tag, "rule-")
	for id := range byID {
		if rest == id || strings.HasPrefix(rest, id+"-") {
			return id, true
		}
	}
	return "", false
}

// byCommunityKeys ищет правило по набору ключей community-списков. Совпадение
// требуется полное: правило с теми же ключами, ещё не занятое.
func byCommunityKeys(tags []string, rules []store.Rule, used map[string]bool) (store.Rule, bool) {
	keys := make([]string, 0, len(tags))
	for _, tag := range tags {
		if k, ok := strings.CutPrefix(tag, "list-"); ok {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return store.Rule{}, false
	}
	for _, r := range rules {
		if used[r.ID] || !sameSet(r.CommunityLists, keys) {
			continue
		}
		return r, true
	}
	return store.Rule{}, false
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]int, len(a))
	for _, v := range a {
		seen[v]++
	}
	for _, v := range b {
		seen[v]--
		if seen[v] < 0 {
			return false
		}
	}
	return true
}

// countInConfig — сколько правил базы должно оказаться в конфиге ядра.
//
// Считаются не включённые, а попадающие: правило без единого условия совпадения
// генератор пропускает, и чаще всего это правило, чей единственный список ещё
// не приехал в кэш. Такое правило непримененной правкой не является — применять
// в нём нечего.
func (s *Server) countInConfig(rules []store.Rule) int {
	n := 0
	for _, r := range rules {
		if singbox.RuleInConfig(r, s.plainLists) {
			n++
		}
	}
	return n
}

// dnsRuled — получает ли правило запись в `dns.rules`, то есть виден ли его
// отказ или его FakeIP в ответе резолвера (docs/04-dns-fakeip.md).
//
// Записи нет только у двух видов: `direct` (его трафик и так идёт мимо
// sing-box) и `resolve_real_ip` (там клиенту нужен настоящий адрес). Всё
// остальное — туннель, блокировка, недоступный туннель — в DNS видно.
func dnsRuled(r store.Rule) bool {
	switch {
	case r.Action == store.ActionDirect:
		return false
	case r.Action == store.ActionTunnel && r.ResolveRealIP:
		return false
	default:
		return true
	}
}

// ruleCatches отвечает, ловит ли правило домен. Второе значение — уверенность:
// false означает, что у правила есть наборы, состав которых знает только ядро,
// и «не ловит» по видимым условиям ещё не значит «не ловит».
func (s *Server) ruleCatches(r store.Rule, domain string) (hit bool, certain bool) {
	certain = true
	for _, d := range r.Domains {
		if domainCovers(d, domain) {
			return true, true
		}
	}
	if len(r.CommunityLists) > 0 {
		certain = false
	}
	for _, url := range r.RemoteLists {
		list, ok := plainList(s.plainLists, url)
		if !ok {
			// Список ведёт сам sing-box (.srs, .json) либо ещё не скачан.
			certain = false
			continue
		}
		for _, d := range list.Domains {
			if domainCovers(d, domain) {
				return true, true
			}
		}
	}
	// Подсети правила здесь не смотрятся: домен с ними сравнивать нечем, а по
	// адресу трафик ловит nft-сет, а не это правило.
	return !certain, certain
}

// plainList достаёт содержимое своего списка, если демон его качает.
func plainList(p singbox.PlainLists, url string) (singbox.PlainList, bool) {
	if p == nil {
		return singbox.PlainList{}, false
	}
	return p(url)
}

// domainCovers — ловит ли условие домен. sing-box сравнивает домены по суффиксу
// (docs/04-dns-fakeip.md), и `example.com` ловит `cdn.example.com`.
func domainCovers(pattern, domain string) bool {
	p := strings.ToLower(strings.TrimPrefix(strings.TrimPrefix(pattern, "*."), "."))
	d := strings.ToLower(domain)
	return p != "" && (p == d || strings.HasSuffix(d, "."+p))
}

// normalizeProbeDomain приводит введённое к домену. Пустая строка означает, что
// это не домен: адрес, ссылка с путём или мусор.
func normalizeProbeDomain(raw string) string {
	d := strings.TrimSpace(raw)
	d = strings.TrimPrefix(strings.TrimPrefix(d, "https://"), "http://")
	if i := strings.IndexAny(d, "/?#"); i >= 0 {
		d = d[:i]
	}
	d = strings.TrimSuffix(strings.ToLower(d), ".")
	d = strings.TrimPrefix(d, "*.")
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") {
		return ""
	}
	// Литеральный адрес пробнику не годится: FakeIP на него не выдаётся, и
	// вопрос «какое правило поймает» решает nft-сет, а не DNS.
	if _, err := netip.ParseAddr(d); err == nil {
		return ""
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 {
			return ""
		}
		for _, c := range label {
			switch {
			case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_':
			default:
				return ""
			}
		}
	}
	return d
}
