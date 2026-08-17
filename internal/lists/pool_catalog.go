package lists

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// DefaultPoolCatalogURL — каталог, с которого наполняется встроенный пул.
//
// Раздел выбран не по вкусу: у outlinekeys разделы vless и trojan отдают ссылки со
// срезанными query-параметрами, такой ключ проходит парсер и гарантированно не
// соединяется (ADR 0015). Самодостаточные ключи есть только в разделе outline.
const DefaultPoolCatalogURL = "https://outlinekeys.com/protocols/outline/"

// ErrNoPoolDriver — для хоста этой ссылки драйвера каталога нет.
//
// Отдельная ошибка, а не текст: по ней узнаётся установка, где в БД остался адрес
// умершего каталога, и ей же отвечает панель на попытку вписать чужой сайт.
var ErrNoPoolDriver = errors.New("каталог не поддерживается")

// ErrPoolCrawlBusy — этот каталог уже обходится, второй обход не запускался.
//
// Сентинел, а не текст: по нему расписание отличает подавленный такт от неудачи и не
// уходит в ретраи, а ручка API отвечает пользовательским кодом вместо `502`.
var ErrPoolCrawlBusy = errors.New("обход каталога уже идёт")

// Ограничения обхода. Общие для всех драйверов: вежливость к чужому бесплатному
// сайту — обязанность слоя, а не каждого разборщика по отдельности (ADR 0015).
const (
	// poolMaxKeys — потолок на число собранных ключей за обход.
	//
	// Именно собранных, а не рабочих: обход двухшаговый (страница списка плюс
	// страница на каждый ключ), и сотня ключей — это 105 запросов и около двух
	// с половиной минут. Гнаться за сотней рабочих значило бы вчетверо больше.
	// Отбор живых остаётся за `urltest` внутри sing-box (ADR 0010).
	poolMaxKeys = 100

	// poolRequestPause — пауза между запросами к каталогу. На ней прошло больше
	// двухсот запросов подряд без 429 и блокировок.
	poolRequestPause = 300 * time.Millisecond

	// poolMaxPages — потолок на число страниц списка. Страхует от бесконечного
	// обхода, если сайт начнёт отдавать одну и ту же страницу.
	poolMaxPages = 20

	// poolMaxPageBytes — потолок размера одной страницы.
	poolMaxPageBytes = 8 << 20
)

// PoolDriver — разборщик одного каталога ключей.
//
// Драйвер знает разметку своего источника и сам сообщает, ключи какого протокола он
// отдаёт: пул перестал быть vless-пулом по построению (ADR 0015). Сеть, таймауты,
// паузы, счётчик запросов и потолок собранного — не его дело, всё это даёт [poolCrawl].
type PoolDriver interface {
	// Name — как источник называется в логе.
	Name() string
	// KeyType — протокол ключей, которые драйвер отдаёт.
	KeyType() store.TunnelType
	// Servers обходит каталог по разобранному адресу. Отданное идёт в БД как есть.
	Servers(ctx context.Context, c *poolCrawl, catalog *url.URL) ([]store.PoolServer, error)
}

// poolDrivers — драйверы по хосту каталожной ссылки.
//
// Добавление источника — запись в этой карте и свой файл рядом; существующие
// разборщики при этом не трогаются.
var (
	poolDriversMu sync.RWMutex
	poolDrivers   = map[string]PoolDriver{
		outlineKeysHost: outlineKeys{},
		// Один драйвер igareck на оба зеркала: каталог заведён на основном
		// (raw.githack.com), но если у пользователя в БД окажется резервный хост —
		// разбор тот же (ADR 0017).
		igareckPrimaryHost:  igareck{},
		igareckFallbackHost: igareck{},
	}
)

// RegisterPoolDriver добавляет драйвер для хоста и отдаёт функцию отката.
//
// Публичный вход в реестр: источник добавляется этим вызовом либо записью в
// [poolDrivers], и ни то, ни другое не требует правки существующих разборщиков.
// Тестам он нужен и вовсе постоянно — фикстуры раздаёт httptest на 127.0.0.1 со
// случайным портом, а драйвер выбирается по хосту.
func RegisterPoolDriver(host string, d PoolDriver) func() {
	host = strings.ToLower(host)
	poolDriversMu.Lock()
	prev, had := poolDrivers[host]
	poolDrivers[host] = d
	poolDriversMu.Unlock()
	return func() {
		poolDriversMu.Lock()
		if had {
			poolDrivers[host] = prev
		} else {
			delete(poolDrivers, host)
		}
		poolDriversMu.Unlock()
	}
}

// retiredPoolCatalogs — хосты каталогов, которых больше нет.
//
// Нужны не разбору, а установкам: адрес каталога лежит в БД у самого туннеля, и
// у всех, кто поставил razdacha до ADR 0015, там мёртвый vpnkeys.me. Такой пул
// демон переводит на [DefaultPoolCatalogURL] при старте — молча пустым он остаться
// не может (issue #153).
var retiredPoolCatalogs = map[string]bool{
	"vpnkeys.me":          true,
	"www.vpnkeys.me":      true,
	"freevpnkeys.com":     true,
	"www.freevpnkeys.com": true,
}

// PoolCatalogRetired — умер ли источник по этому адресу целиком.
func PoolCatalogRetired(catalogURL string) bool {
	u, err := url.Parse(strings.TrimSpace(catalogURL))
	if err != nil {
		return false
	}
	return retiredPoolCatalogs[strings.ToLower(u.Hostname())]
}

// PoolDriverFor выбирает драйвер по хосту каталожной ссылки.
func PoolDriverFor(catalogURL string) (PoolDriver, *url.URL, error) {
	raw := strings.TrimSpace(catalogURL)
	if raw == "" {
		raw = DefaultPoolCatalogURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: адрес каталога %q неразборчив: %w",
			store.ErrInvalid, catalogURL, err)
	}
	host := strings.ToLower(u.Hostname())

	poolDriversMu.RLock()
	d, ok := poolDrivers[host]
	poolDriversMu.RUnlock()
	if !ok {
		if retiredPoolCatalogs[host] {
			return nil, nil, fmt.Errorf("%w: источник %s закрылся, каталог надо сменить",
				ErrNoPoolDriver, host)
		}
		return nil, nil, fmt.Errorf("%w: разборщика для %s нет", ErrNoPoolDriver, host)
	}
	return d, u, nil
}

// PoolKeyType — протокол ключей, которые отдаёт каталог по этому адресу.
//
// Отвечает драйвер, а не разбор ссылки: `http(s)://` говорит только о том, что это
// каталог, а какие в нём ключи — знает тот, кто умеет читать его страницы (ADR 0015).
func PoolKeyType(catalogURL string) (store.TunnelType, error) {
	d, _, err := PoolDriverFor(catalogURL)
	if err != nil {
		return "", err
	}
	return d.KeyType(), nil
}

// PoolCatalog обходит каталог ключей и отдаёт серверы для туннеля-пула.
type PoolCatalog struct {
	// Client — пустой означает клиент с таймаутом по умолчанию.
	Client HTTPDoer
	// Log — пустой означает slog.Default().
	Log *slog.Logger
	// Timeout — потолок на одну страницу. Ноль означает defaultTimeout.
	Timeout time.Duration
	// Limit — потолок на число собранных ключей. Ноль означает poolMaxKeys.
	Limit int
	// Pause — пауза между запросами. Отрицательная означает «без паузы»
	// (тесты), ноль — poolRequestPause.
	Pause time.Duration
	// CheckKey — проверка ключа нашим парсером. Замыкание от демона: слой lists
	// про singbox не знает и знать не должен (та же связь, что у PlainLists в
	// обратную сторону). Не прошедший проверку ключ в БД не попадает: ссылка,
	// которую не осилит генератор конфига, занимает место и проходит как
	// исправная до первой пробы. Пустое означает «не проверять» — так каталог
	// разбирается в тестах слоя.
	CheckKey func(rawURL string) error

	// crawlsMu и crawls держат по одному идущему обходу на каталог.
	//
	// Входов в обход два — кнопка «Обновить» в панели и такт расписания, — и они
	// совпадают по времени: на стенде такое совпадение стоило 210 запросов к чужому
	// бесплатному сайту вместо 105 (issue #156). Ключ — разобранный адрес каталога,
	// а не туннель: два пула с одним каталогом бьют по одному сайту и обязаны друг
	// друга ждать, два пула с разными источниками — нет.
	crawlsMu sync.Mutex
	crawls   map[string]bool
}

// beginCrawl занимает каталог под обход. false означает, что обход по этому адресу
// уже идёт и второй запускать нельзя.
func (c *PoolCatalog) beginCrawl(key string) bool {
	c.crawlsMu.Lock()
	defer c.crawlsMu.Unlock()
	if c.crawls[key] {
		return false
	}
	if c.crawls == nil {
		c.crawls = make(map[string]bool)
	}
	c.crawls[key] = true
	return true
}

// endCrawl освобождает каталог.
func (c *PoolCatalog) endCrawl(key string) {
	c.crawlsMu.Lock()
	delete(c.crawls, key)
	c.crawlsMu.Unlock()
}

func (c *PoolCatalog) log() *slog.Logger {
	if c.Log == nil {
		return slog.Default()
	}
	return c.Log
}

func (c *PoolCatalog) timeout() time.Duration {
	if c.Timeout <= 0 {
		return defaultTimeout
	}
	return c.Timeout
}

func (c *PoolCatalog) client() HTTPDoer {
	if c.Client == nil {
		return &http.Client{Timeout: c.timeout()}
	}
	return c.Client
}

func (c *PoolCatalog) limit() int {
	if c.Limit <= 0 {
		return poolMaxKeys
	}
	return c.Limit
}

func (c *PoolCatalog) pause() time.Duration {
	switch {
	case c.Pause < 0:
		return 0
	case c.Pause == 0:
		return poolRequestPause
	default:
		return c.Pause
	}
}

// Servers обходит каталог и отдаёт серверы в том виде, в каком они лягут в БД.
//
// Ошибка обхода отменяет его целиком: половина каталога, записанная как целый пул,
// выкинула бы из конфига живые серверы. Сколько собрано и сколько это стоило
// запросов, видно в логе — обход двухшаговый и дорогой, и цену надо знать (ADR 0015).
func (c *PoolCatalog) Servers(ctx context.Context, catalogURL string) ([]store.PoolServer, error) {
	d, u, err := PoolDriverFor(catalogURL)
	if err != nil {
		return nil, err
	}

	// Второй обход того же каталога не запускается: он стоил бы сотни запросов к
	// чужому сайту ради того же результата (issue #156). Отказ, а не ожидание, —
	// ждать нечего: идущий обход запишет свежий состав в БД сам, и тот, кто нажал
	// кнопку, получил бы то же самое двумя минутами позже. Молча пропустить нельзя:
	// без строки в логе удвоенный обход и подавленный выглядят одинаково — никак.
	key := u.String()
	if !c.beginCrawl(key) {
		c.log().Info("каталог уже обходится, второй обход не запущен", "каталог", key)
		return nil, fmt.Errorf("%w: %s", ErrPoolCrawlBusy, key)
	}
	defer c.endCrawl(key)

	crawl := &poolCrawl{
		fetch:    c.page,
		log:      c.log(),
		limit:    c.limit(),
		pause:    c.pause(),
		checkKey: c.CheckKey,
	}
	started := time.Now()
	out, err := d.Servers(ctx, crawl, u)
	c.log().Info("каталог обойдён",
		"источник", d.Name(), "каталог", u.String(), "собрано", len(out),
		"запросов", crawl.requests, "пропущено", crawl.skipped,
		"за", time.Since(started).Round(time.Millisecond))
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%w: каталог %s не дал ни одного ключа", ErrBadResponse, u)
	}
	return out, nil
}

// poolCrawl — общее для всех драйверов: как взять страницу, куда писать, когда
// остановиться и что считать пригодным ключом.
type poolCrawl struct {
	fetch    func(ctx context.Context, pageURL string) (string, error)
	log      *slog.Logger
	limit    int
	pause    time.Duration
	checkKey func(rawURL string) error

	// requests и skipped ведёт сам обход: они уходят в лог одной строкой на пул.
	requests int
	skipped  int
}

// page тянет одну страницу, выдерживая паузу между запросами и считая их.
//
// Пауза берётся до запроса, а не после: последний запрос обхода тогда ничего не
// ждёт, а между соседними интервал соблюдается.
func (c *poolCrawl) page(ctx context.Context, pageURL string) (string, error) {
	if c.requests > 0 && c.pause > 0 {
		timer := time.NewTimer(c.pause)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
	c.requests++
	return c.fetch(ctx, pageURL)
}

// keyOK — проходит ли ключ наш парсер. Не прошедший считается пропущенным и в БД
// не попадает.
func (c *poolCrawl) keyOK(rawURL string) error {
	if c.checkKey == nil {
		return nil
	}
	return c.checkKey(rawURL)
}

// skip отмечает пропуск и пишет строку в лог. Сама ссылка в лог не идёт: в ней
// пароль ключа (issue #124).
func (c *poolCrawl) skip(reason string, args ...any) {
	c.skipped++
	c.log.Debug("ключ каталога пропущен", append([]any{"причина", reason}, args...)...)
}

// page тянет одну страницу каталога: таймаут, User-Agent и потолок размера общие
// для всех драйверов.
func (c *PoolCatalog) page(ctx context.Context, pageURL string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return "", fmt.Errorf("запрос каталога %s: %w", pageURL, err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("загрузка каталога %s: %w", pageURL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: каталог %s: сервер ответил %s",
			ErrBadResponse, pageURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, poolMaxPageBytes))
	if err != nil {
		return "", fmt.Errorf("%w: каталог %s: чтение тела: %w", ErrBadResponse, pageURL, err)
	}
	return string(body), nil
}
