package lists

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Драйвер источника igareck — каталог встроенного общего пула (ADR 0018).
//
// Источник страноагностичен: драйвер отдаёт все прошедшие парсер ключи всех подписок
// одним списком, без страны. geo-IP убран вместе со страновыми пулами — единый пул
// страну выхода не различает.
const (
	// igareckPrimaryHost — основное зеркало, CDN поверх репозитория. Оно же стоит в
	// [igareckCatalogURL] демона, по нему и выбирается драйвер.
	igareckPrimaryHost = "raw.githack.com"
	// igareckFallbackHost — резерв: raw github режется у части РФ-провайдеров, а
	// именно там обход и должен работать (ADR 0018). Тот же путь, другой хост.
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
// все ключи идут в единственный пул, и урезание отсекло бы часть без нужды. Размер
// каждого файла всё равно ограничен потолком страницы (`poolMaxPageBytes`).
type igareck struct {
	// fallback — резервное зеркало. Пусто означает [igareckFallbackHost]. Поле нужно
	// тестам: настоящий резерв (raw.githubusercontent.com) в тесте недоступен, а без
	// подмены перебор зеркал сходил бы за реальными подписками в интернет.
	fallback string
}

func (igareck) Name() string { return igareckPrimaryHost }

// KeyType — пул смешанный (vless + shadowsocks + trojan), и тип тут косметичен:
// участники группы `urltest` разбираются каждый по своей ссылке (ADR 0010/0015),
// настоящий протокол приходит из неё. Большинство ключей — vless.
func (igareck) KeyType() store.TunnelType { return store.TunnelVLESS }

// Servers тянет три подписки и отдаёт все прошедшие парсер серверы одним списком.
//
// Ошибка одной подписки обход не срывает: их три, и падение любой оставило бы ключи
// из других файлов без обновления. Совсем пустую выдачу (все файлы упали или пусты)
// отбраковывает уже [PoolCatalog.Servers] — это настоящая неудача.
func (d igareck) Servers(ctx context.Context, c *poolCrawl, catalog *url.URL) (
	[]store.PoolServer, error,
) {
	seen := make(map[string]bool)
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
			// такие ключи отсеются здесь (это допустимое обеднение — ADR 0018).
			if err := c.keyOK(line); err != nil {
				c.skip("ключ не разобрался нашим парсером", "схема", schemePrefix(line), "ошибка", err)
				continue
			}

			// PingMS не заполняется: источник задержки не даёт, выдумывать её нельзя.
			// Отбор живых ведёт `urltest` (ADR 0010).
			out = append(out, store.PoolServer{URL: line, Title: igareckTitle(line)})
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

// igareckTitle — человекочитаемое имя члена пула из ссылки-ключа. Только отображение:
// на конфиг sing-box не влияет, тег члена остаётся синтетическим (ADR 0010/0015).
//
// Имя берётся из фрагмента (часть после первого `#`), обычно «флаг страна, город»
// в percent-encoding. Пустой фрагмент — fallback на хост:порт из ссылки; если и это
// не разобралось — пустая строка (UI сам покажет прочерк).
func igareckTitle(line string) string {
	if _, frag, ok := strings.Cut(line, "#"); ok && frag != "" {
		name, err := url.PathUnescape(frag)
		if err != nil {
			// Битый percent-encoding — отдаём сырой фрагмент, он всё же читаем.
			name = frag
		}
		return strings.TrimSpace(name)
	}
	// Фрагмента нет — берём адрес сервера best-effort. Парсер тут строгий (net/url),
	// но на нужное — хост:порт — ему хватает: shadowsocks с «/» в userinfo без
	// фрагмента до этой ветки и не доходит.
	if u, err := url.Parse(line); err == nil {
		if host := u.Host; host != "" {
			return strings.TrimSpace(host)
		}
		if h := u.Hostname(); h != "" {
			if p := u.Port(); p != "" {
				return strings.TrimSpace(h + ":" + p)
			}
			return strings.TrimSpace(h)
		}
	}
	return ""
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
