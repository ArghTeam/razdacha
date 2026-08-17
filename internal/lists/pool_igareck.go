package lists

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"

	"github.com/ArghTeam/razdacha/internal/geoip"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Драйвер источника igareck — общий каталог всех семи страновых пулов (ADR 0017).
//
// Источник страноагностичен: одна и та же выдача раскладывается по странам слоем
// обновления (`poolServersForCountry` в pool.go), а драйвер лишь проставляет каждому
// серверу страну его exit-адреса. Метку из имени ключа или `#fragment` мы не берём
// принципиально — метки врут (issue #161): страна снимается офлайн-базой geoip по
// адресу сервера, домен для этого резолвится.
const (
	// igareckPrimaryHost — основное зеркало, CDN поверх репозитория. Оно же стоит в
	// [igareckCatalogURL] демона, по нему и выбирается драйвер.
	igareckPrimaryHost = "raw.githack.com"
	// igareckFallbackHost — резерв: raw github режется у части РФ-провайдеров, а
	// именно там обход и должен работать (ADR 0017). Тот же путь, другой хост.
	igareckFallbackHost = "raw.githubusercontent.com"
)

// igareckSubscriptions — три plain-text подписки относительно базового адреса
// каталога. В каждой строке — ссылка на ключ (vless/ss/trojan, изредка vmess).
// Порядок фиксирован лишь ради предсказуемого лога; дедуп ключей — по всей ссылке.
var igareckSubscriptions = []string{
	"BLACK_VLESS_RUS_mobile.txt",
	"BLACK_VLESS_RUS.txt",
	"BLACK_SS+All_RUS.txt",
}

// igareck — драйвер каталога igareck.
//
// Обход дешёвый и одношаговый: три (в худшем случае шесть, с учётом падения на
// резерв) запроса целых файлов против ста с лишним у outlinekeys. Поэтому потолок на
// число собранных ключей (`c.limit`) драйвер намеренно не применяет как у outlinekeys:
// раскладка по семи странам требует всю выдачу, а не первую сотню строк, и урезание
// оставило бы часть стран пустыми. Размер каждого файла всё равно ограничен потолком
// страницы (`poolMaxPageBytes`).
type igareck struct {
	// resolve резолвит домен в адреса. nil означает системный резолвер: DNS-запрос
	// на исполнении допустим — обход идёт в фоне на сервере, а не на машине человека.
	// В тестах подменяется, чтобы в сеть не ходить.
	resolve func(ctx context.Context, host string) ([]net.IP, error)
	// country определяет страну по адресу. nil означает [geoip.Country] — офлайн, из
	// встроенной базы. Подменяется в тестах.
	country func(ip net.IP) string
	// fallback — резервное зеркало. Пусто означает [igareckFallbackHost]. Поле нужно
	// тестам: настоящий резерв (raw.githubusercontent.com) в тесте недоступен, а без
	// подмены перебор зеркал сходил бы за реальными подписками в интернет.
	fallback string
}

func (igareck) Name() string { return igareckPrimaryHost }

// KeyType — пул смешанный (vless + shadowsocks + trojan из одной страны), и тип тут
// косметичен: участники группы `urltest` разбираются каждый по своей ссылке
// (ADR 0010/0015), настоящий протокол приходит из неё. Большинство ключей — vless.
func (igareck) KeyType() store.TunnelType { return store.TunnelVLESS }

// Servers тянет три подписки и отдаёт все серверы с проставленной страной exit-адреса.
//
// Ошибка одной подписки обход не срывает: их три, и падение любой оставило бы страны,
// чьи серверы лежат в других файлах, без обновления. Совсем пустую выдачу (все файлы
// упали или пусты) отбраковывает уже [PoolCatalog.Servers] — это настоящая неудача.
func (d igareck) Servers(ctx context.Context, c *poolCrawl, catalog *url.URL) (
	[]store.PoolServer, error,
) {
	seen := make(map[string]bool)
	// Страна одного адреса не меняется в пределах обхода: многие ключи делят один
	// exit-IP (в разведке 58 ключей на 3 адреса), и кэш экономит и geoip-поиск, и —
	// для доменов — повторный DNS-запрос.
	countryByHost := make(map[string]string)
	var out []store.PoolServer

	for _, file := range igareckSubscriptions {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := d.fetchSubscription(ctx, c, catalog, file)
		if err != nil {
			c.skip("подписка не загрузилась ни с одного зеркала", "файл", file, "ошибка", err)
			continue
		}

		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if !igareckKeyLine(line) {
				continue
			}
			if seen[line] {
				continue
			}
			seen[line] = true

			// Ключ, который не осилит наш парсер, в БД не попадает — как и у
			// outlinekeys. Сюда же уходит vmess: генератор конфига его не берёт, и
			// такие ключи отсеются здесь (это допустимое обеднение — ADR 0017).
			if err := c.keyOK(line); err != nil {
				c.skip("ключ не разобрался нашим парсером", "схема", schemePrefix(line), "ошибка", err)
				continue
			}

			host := poolKeyHost(line)
			country, cached := countryByHost[host]
			if !cached {
				country = d.countryOf(ctx, host)
				countryByHost[host] = country
			}

			// PingMS не заполняется: источник задержки не даёт, выдумывать её нельзя.
			// Отбор живых ведёт `urltest` (ADR 0010).
			out = append(out, store.PoolServer{URL: line, Country: country})
		}
	}
	return out, nil
}

// fetchSubscription тянет один файл подписки, перебирая зеркала: основное (хост
// каталога), затем резерв. Возвращает тело первого ответившего зеркала.
//
// Каждый запрос идёт через `c.page` — он держит паузу между обращениями и ведёт
// счётчик, общий для всего обхода. Падение основного зеркала на резерв переходит
// молча: то, ради чего резерв и заведён (ADR 0017).
func (d igareck) fetchSubscription(ctx context.Context, c *poolCrawl, catalog *url.URL, file string) (
	string, error,
) {
	var errs []error
	for _, host := range d.mirrors(catalog) {
		u := *catalog
		u.Host = host
		u.RawPath = ""
		u.Path = strings.TrimSuffix(catalog.Path, "/") + "/" + file
		body, err := c.page(ctx, u.String())
		if err == nil {
			return body, nil
		}
		errs = append(errs, err)
		c.log.Debug("зеркало igareck не ответило, пробуем следующее",
			"зеркало", host, "файл", file, "ошибка", err)
	}
	return "", errors.Join(errs...)
}

// mirrors — зеркала в порядке перебора: сначала хост каталога (основное), затем
// резерв, если каталог уже не на нём.
func (d igareck) mirrors(catalog *url.URL) []string {
	primary := catalog.Host
	fallback := d.fallback
	if fallback == "" {
		fallback = igareckFallbackHost
	}
	mirrors := []string{primary}
	if !strings.EqualFold(primary, fallback) {
		mirrors = append(mirrors, fallback)
	}
	return mirrors
}

// countryOf определяет страну сервера по его адресу из ссылки. IP берётся напрямую,
// домен резолвится. Не определилась — пустая строка: такой сервер осядет в пуле без
// страны и в страновой срез не попадёт, но это честнее выдуманной метки (issue #161).
func (d igareck) countryOf(ctx context.Context, host string) string {
	if host == "" {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		return d.geo()(ip)
	}
	ips, err := d.resolver()(ctx, host)
	if err != nil {
		return ""
	}
	for _, ip := range ips {
		if c := d.geo()(ip); c != "" {
			return c
		}
	}
	return ""
}

func (d igareck) resolver() func(context.Context, string) ([]net.IP, error) {
	if d.resolve != nil {
		return d.resolve
	}
	return func(ctx context.Context, host string) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(ctx, "ip4", host)
	}
}

func (d igareck) geo() func(net.IP) string {
	if d.country != nil {
		return d.country
	}
	return geoip.Country
}

// igareckKeyLine — похожа ли строка на ссылку-ключ. Комментарии, пустые строки и
// заголовки подписки отсекаются здесь.
func igareckKeyLine(line string) bool {
	for _, scheme := range []string{"vless://", "ss://", "trojan://", "vmess://"} {
		if strings.HasPrefix(strings.ToLower(line), scheme) {
			return true
		}
	}
	return false
}

// poolKeyHost достаёт хост (IP или домен) из ссылки-ключа. Порт не нужен — стране он
// безразличен. Разбор пришлось развести по схемам: у vless/trojan хост лежит в URL,
// у ss он бывает и за base64, у vmess — в base64-JSON.
func poolKeyHost(raw string) string {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(raw), "://")
	if !ok {
		return ""
	}
	switch strings.ToLower(scheme) {
	case "vmess":
		return vmessKeyHost(rest)
	case "ss":
		return ssKeyHost(rest)
	default: // vless, trojan — обычный URL с хостом на месте
		if u, err := url.Parse(raw); err == nil {
			return u.Hostname()
		}
		return ""
	}
}

// ssKeyHost достаёт хост из `ss://`. Два формата: SIP002 (`userinfo@host:port`) и
// legacy (весь хвост — base64 от `method:password@host:port`).
func ssKeyHost(rest string) string {
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		hostport := rest[at+1:]
		if i := strings.IndexAny(hostport, "/?"); i >= 0 {
			hostport = hostport[:i]
		}
		return hostOnly(hostport)
	}
	dec := decodeBase64(rest)
	if at := strings.LastIndexByte(dec, '@'); at >= 0 {
		return hostOnly(dec[at+1:])
	}
	return ""
}

// vmessKeyHost достаёт хост из `vmess://`: хвост — base64 от JSON с полем `add`.
func vmessKeyHost(rest string) string {
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		rest = rest[:i]
	}
	var v struct {
		Add string `json:"add"`
	}
	if err := json.Unmarshal([]byte(decodeBase64(rest)), &v); err != nil {
		return ""
	}
	return strings.TrimSpace(v.Add)
}

// hostOnly отрезает порт, оставляя хост. Скобки IPv6 снимает `net.SplitHostPort`; если
// порта нет — строка уже хост.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

// decodeBase64 пробует обе азбуки (std и url) в обоих вариантах отступа: подписки
// встречаются и с паддингом, и без. Не разобралось — пустая строка.
func decodeBase64(s string) string {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding, base64.RawStdEncoding,
		base64.URLEncoding, base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(s); err == nil {
			return string(b)
		}
	}
	return ""
}
