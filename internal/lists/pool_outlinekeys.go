package lists

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// outlineKeysHost — хост, по которому выбирается этот драйвер.
const outlineKeysHost = "outlinekeys.com"

// outlineKeysSection — единственный раздел сайта, который мы берём.
//
// Разделы `/protocols/vless/` и `/protocols/trojan/` того же сайта не собираются
// вовсе: их ссылки приходят со срезанным query — ни `security`, ни `type`, ни `sni`,
// ни `pbk`. Синтаксически это валидный vless без TLS, парсер принимает все сто, а
// соединение падает с `unknown version: 72` — открытым текстом в TLS-эндпоинт. Пул
// набирается «хорошими» ключами, `urltest` не находит ни одного живого, и правила
// пула отказывают по ADR 0013. Ключ, который проходит парсер и гарантированно не
// соединяется, хуже отсутствующего (ADR 0015).
const outlineKeysSection = "/protocols/outline/"

// outlineKeysProtocol — подпись протокола на карточке списка.
const outlineKeysProtocol = "outline"

// outlineKeysScheme — схема ключа, который отдаёт раздел outline. Раздел с сайта
// мы выбрали, но подтверждение берём из самой ссылки: смена разметки не должна
// протащить в shadowsocks-пул чужой протокол.
const outlineKeysScheme = "ss://"

// Разметка сайта. Server-side (PHP), её правка ломает разбор молча — поэтому
// разборщик проверяется на сохранённых страницах в testdata, а не по сети.
var (
	// Карточка списка режется по открывающему div; поля берутся точечно.
	reOKCard    = regexp.MustCompile(`<div class="card mb-3"`)
	reOKKeyPath = regexp.MustCompile(`href="(/key/\d+/)"`)
	reOKTitle   = regexp.MustCompile(`(?s)<h3>\s*(.*?)\s*</h3>`)
	reOKFlag    = regexp.MustCompile(`/assets/flags/([a-z0-9_\-]+)\.svg`)
	reOKStatus  = regexp.MustCompile(`<span class="text-(success|danger|warning)">\s*([^<]*)</span>`)
	reOKProto   = regexp.MustCompile(`<span class="me-2">\s*([^<]*)</span>`)

	// Страница ключа: сам ключ лежит в единственной textarea.
	reOKAccessKey = regexp.MustCompile(`(?s)<textarea[^>]*id="accessKey"[^>]*>(.*?)</textarea>`)
)

// outlineKeys — драйвер каталога outlinekeys.com, раздел outline (shadowsocks).
//
// Обход двухшаговый: страница списка отдаёт двадцать карточек со ссылками
// `/key/<id>/`, самого ключа в карточке нет. Поэтому со списка снимается всё, что
// он знает — страна, подпись и статус, — и за ключом идём только к тем карточкам,
// которые сайт считает живыми: мёртвая карточка не стоит отдельного запроса.
type outlineKeys struct{}

func (outlineKeys) Name() string { return outlineKeysHost }

// KeyType — раздел outline отдаёт `ss://`, то есть shadowsocks. Прежний прошитый
// vless для любой каталожной ссылки ушёл вместе с прежним источником (ADR 0015).
func (outlineKeys) KeyType() store.TunnelType { return store.TunnelShadowsocks }

// Servers обходит страницы списка и страницы ключей, пока не наберёт лимит или не
// кончатся карточки.
func (d outlineKeys) Servers(ctx context.Context, c *poolCrawl, catalog *url.URL) (
	[]store.PoolServer, error,
) {
	if err := outlineKeysCheckSection(catalog); err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var out []store.PoolServer

	for page := 1; page <= poolMaxPages && len(out) < c.limit; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := c.page(ctx, outlineKeysPageURL(catalog, page))
		if err != nil {
			return nil, err
		}

		cards := parseOutlineKeysList(body)
		c.log.Debug("страница списка разобрана",
			"каталог", outlineKeysHost, "страница", page, "карточек", len(cards))
		// Пустая страница — конец каталога: сайт отдаёт её без карточек и с
		// кодом 200, а не 404.
		if len(cards) == 0 {
			break
		}

		for _, card := range cards {
			if len(out) >= c.limit {
				break
			}
			srv, ok := d.server(ctx, c, catalog, card)
			if !ok {
				continue
			}
			if seen[srv.URL] {
				continue
			}
			seen[srv.URL] = true
			out = append(out, srv)
		}
	}
	return out, nil
}

// server добирает ключ по карточке списка. Второе значение — годится ли он.
func (outlineKeys) server(ctx context.Context, c *poolCrawl, catalog *url.URL, card outlineKeysCard) (
	store.PoolServer, bool,
) {
	// Мёртвая по мнению сайта карточка отдельного запроса не стоит: за ключом мы
	// ходим на вторую страницу, и это половина цены обхода.
	if !card.online {
		c.skip("сайт считает ключ мёртвым", "карточка", card.title)
		return store.PoolServer{}, false
	}
	// Подпись протокола на карточке сверяется с разделом: смешанная выдача
	// притащила бы в пул тот самый vless со срезанным query.
	if card.protocol != "" && !strings.EqualFold(card.protocol, outlineKeysProtocol) {
		c.skip("чужой протокол на карточке", "карточка", card.title, "протокол", card.protocol)
		return store.PoolServer{}, false
	}

	keyURL := *catalog
	keyURL.Path = card.path
	keyURL.RawQuery = ""
	body, err := c.page(ctx, keyURL.String())
	if err != nil {
		// Одна недоступная страница ключа обход не отменяет: их сотня, и
		// падение на любой оставило бы пул без обновления вовсе.
		c.skip("страница ключа не загрузилась", "карточка", card.title, "ошибка", err)
		return store.PoolServer{}, false
	}

	raw := outlineKeysAccessKey(body)
	if raw == "" {
		c.skip("на странице ключа нет поля с ключом", "карточка", card.title)
		return store.PoolServer{}, false
	}
	if !strings.HasPrefix(strings.ToLower(raw), outlineKeysScheme) {
		c.skip("ключ не shadowsocks", "карточка", card.title, "схема", schemePrefix(raw))
		return store.PoolServer{}, false
	}
	if err := c.keyOK(raw); err != nil {
		c.skip("ключ не разобрался нашим парсером", "карточка", card.title, "ошибка", err)
		return store.PoolServer{}, false
	}

	// PingMS не заполняется: этот источник задержки не измеряет, и любое число
	// здесь было бы выдумкой. Отбор живых ведёт `urltest` (ADR 0010, ADR 0015).
	return store.PoolServer{URL: raw, Country: card.country, Title: card.title}, true
}

// outlineKeysCheckSection отказывает всему, кроме раздела outline.
//
// Отказ, а не тихий пропуск: пользователь, вписавший раздел vless, обязан узнать,
// почему оттуда ничего не берётся.
func outlineKeysCheckSection(catalog *url.URL) error {
	path := strings.ToLower(catalog.Path)
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	if path == outlineKeysSection {
		return nil
	}
	return fmt.Errorf("%w: у %s берётся только раздел %s — разделы vless и trojan "+
		"отдают ссылки без параметров TLS, такие ключи не соединяются",
		store.ErrInvalid, outlineKeysHost, outlineKeysSection)
}

// outlineKeysPageURL — адрес страницы списка. Пагинация у сайта одна — `?page=N`,
// размер страницы не настраивается.
func outlineKeysPageURL(catalog *url.URL, page int) string {
	u := *catalog
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

// outlineKeysCard — карточка списка: всё, что известно о ключе до похода за ним.
type outlineKeysCard struct {
	path     string
	title    string
	country  string
	protocol string
	online   bool
}

// parseOutlineKeysList режет страницу списка на карточки.
//
// Карточка без ссылки на ключ пропускается молча: это не ключ, а вёрстка. Конец
// каталога определяется числом карточек — пустая страница приходит с кодом 200.
func parseOutlineKeysList(body string) []outlineKeysCard {
	idx := reOKCard.FindAllStringIndex(body, -1)
	out := make([]outlineKeysCard, 0, len(idx))
	for i, loc := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		card := body[loc[0]:end]

		m := reOKKeyPath.FindStringSubmatch(card)
		if m == nil {
			continue
		}
		out = append(out, outlineKeysCard{
			path:     m[1],
			title:    poolGroup(reOKTitle, card),
			country:  outlineKeysCountry(card),
			protocol: poolGroup(reOKProto, card),
			online:   outlineKeysOnline(card),
		})
	}
	return out
}

// outlineKeysOnline — считает ли сайт ключ живым. Статус лежит цветом текста:
// `text-success` — Online, `text-danger` — Offline.
func outlineKeysOnline(card string) bool {
	m := reOKStatus.FindStringSubmatch(card)
	return m != nil && m[1] == "success"
}

// outlineKeysCountry достаёт страну.
//
// Берётся из подписи карточки («Canada #39729»), а имя файла флага —
// запасной путь: подпись человекочитаема и совпадает с тем, что показано
// пользователю, а `united_states.svg` пришлось бы расшифровывать.
func outlineKeysCountry(card string) string {
	title := poolGroup(reOKTitle, card)
	if i := strings.IndexByte(title, '#'); i > 0 {
		if s := strings.TrimSpace(title[:i]); s != "" {
			return s
		}
	}
	if title != "" {
		return title
	}
	if m := reOKFlag.FindStringSubmatch(card); m != nil {
		return strings.ReplaceAll(m[1], "_", " ")
	}
	return ""
}

// outlineKeysAccessKey достаёт ключ со страницы ключа.
func outlineKeysAccessKey(body string) string {
	m := reOKAccessKey.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}

// schemePrefix — схема ссылки для лога. Сама ссылка в лог не идёт никогда: в ней
// пароль ключа (issue #124).
func schemePrefix(raw string) string {
	if i := strings.Index(raw, "://"); i > 0 {
		return raw[:i]
	}
	return "без схемы"
}

// poolGroup отдаёт первую группу совпадения с распакованными HTML-сущностями.
func poolGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}
