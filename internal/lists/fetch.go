package lists

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// HTTPDoer — то, что умеет выполнить запрос. Интерфейс, а не *http.Client,
// чтобы тесты подставляли httptest.Server и не ходили в сеть.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Ограничения загрузки. Списки качаются с чужих серверов, поэтому и таймаут, и
// потолок размера обязательны: без потолка «список» на сотни мегабайт выест
// память демона.
const (
	defaultTimeout  = 30 * time.Second
	defaultMaxBytes = 16 << 20 // 16 МиБ
)

// Status — чем кончилась загрузка.
type Status string

// Исходы загрузки.
const (
	StatusUpdated     Status = "updated"      // тело скачано и записано в кэш
	StatusNotModified Status = "not_modified" // источник ответил 304, кэш валиден
)

// Result — итог одной загрузки.
type Result struct {
	URL       string
	Format    Format
	Status    Status
	Path      string // путь к телу в кэше
	Size      int64
	FetchedAt time.Time
}

// Options — параметры загрузчика.
type Options struct {
	// Dir — каталог кэша, в проде /var/lib/razdacha/lists.
	Dir string
	// Client — HTTP-клиент. Пустой означает клиент с таймаутом по умолчанию.
	Client HTTPDoer
	// Logger — пустой означает slog.Default().
	Logger *slog.Logger
	// Timeout — потолок на одну загрузку. Ноль означает defaultTimeout.
	Timeout time.Duration
	// MaxBytes — потолок размера тела. Ноль означает defaultMaxBytes.
	MaxBytes int64
}

// Fetcher качает списки в кэш с условными запросами.
type Fetcher struct {
	cache    *cache
	client   HTTPDoer
	log      *slog.Logger
	timeout  time.Duration
	maxBytes int64
}

// NewFetcher создаёт загрузчик и каталог кэша.
func NewFetcher(o Options) (*Fetcher, error) {
	c, err := openCache(o.Dir)
	if err != nil {
		return nil, err
	}
	f := &Fetcher{
		cache:    c,
		client:   o.Client,
		log:      o.Logger,
		timeout:  o.Timeout,
		maxBytes: o.MaxBytes,
	}
	if f.log == nil {
		f.log = slog.Default()
	}
	if f.timeout <= 0 {
		f.timeout = defaultTimeout
	}
	if f.maxBytes <= 0 {
		f.maxBytes = defaultMaxBytes
	}
	if f.client == nil {
		f.client = &http.Client{Timeout: f.timeout}
	}
	return f, nil
}

// Fetch скачивает список, если он изменился.
//
// Ничто, кроме полностью прочитанного непустого тела, кэш не заменяет: 304,
// ошибка сети, ответ 5xx и оборванное тело оставляют прошлую версию на месте.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, f.timeout)
	defer cancel()

	res := Result{
		URL:    rawURL,
		Format: FormatOf(rawURL),
		Path:   f.cache.bodyPath(rawURL),
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return res, fmt.Errorf("запрос списка %s: %w", rawURL, err)
	}
	req.Header.Set("User-Agent", userAgent)

	prev, cached := f.cache.meta(rawURL)
	if cached {
		// Условный запрос: у GitHub и Cloudflare оба валидатора есть, но
		// зеркала часто отдают только один из них.
		if prev.ETag != "" {
			req.Header.Set("If-None-Match", prev.ETag)
		}
		if prev.LastModified != "" {
			req.Header.Set("If-Modified-Since", prev.LastModified)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return res, fmt.Errorf("загрузка списка %s: %w", rawURL, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode == http.StatusNotModified && cached:
		prev.FetchedAt = time.Now().UTC()
		if err := f.cache.writeMeta(rawURL, prev); err != nil {
			return res, err
		}
		res.Status = StatusNotModified
		res.Size = prev.Size
		res.FetchedAt = prev.FetchedAt
		f.log.Debug("список не изменился", "url", rawURL)
		return res, nil
	case resp.StatusCode == http.StatusNotModified:
		// 304 без кэша: сервер ответил на валидаторы, которых мы не посылали.
		return res, fmt.Errorf("%w: список %s: 304 при пустом кэше", ErrBadResponse, rawURL)
	case resp.StatusCode != http.StatusOK:
		return res, fmt.Errorf("%w: список %s: сервер ответил %s", ErrBadResponse, rawURL, resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, f.maxBytes+1))
	if err != nil {
		// Оборванное тело: кэш не трогаем, на следующем прогоне повторим.
		return res, fmt.Errorf("%w: список %s: чтение тела: %w", ErrBadResponse, rawURL, err)
	}
	if int64(len(body)) > f.maxBytes {
		return res, fmt.Errorf("%w: список %s больше %d байт", ErrBadResponse, rawURL, f.maxBytes)
	}
	if len(body) == 0 {
		return res, fmt.Errorf("%w: список %s пуст", ErrBadResponse, rawURL)
	}
	// Content-Length сверяем сами: net/http отдаёт ErrUnexpectedEOF не на всех
	// видах обрыва, а недосчитанный список молча выкинул бы половину подсетей.
	if resp.ContentLength >= 0 && resp.ContentLength != int64(len(body)) {
		return res, fmt.Errorf("%w: список %s оборван: получено %d из %d байт",
			ErrBadResponse, rawURL, len(body), resp.ContentLength)
	}

	now := time.Now().UTC()
	m := meta{
		URL:          rawURL,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    now,
		Size:         int64(len(body)),
	}
	if err := f.cache.store(rawURL, body, m); err != nil {
		return res, err
	}

	res.Status = StatusUpdated
	res.Size = m.Size
	res.FetchedAt = now
	f.log.Info("список обновлён", "url", rawURL, "size", m.Size)
	return res, nil
}

// userAgent представляется источникам: GitHub отдаёт 403 на запрос без него.
const userAgent = "razdacha/1 (+https://github.com/ArghTeam/razdacha)"

// Cached отдаёт закэшированное тело списка.
func (f *Fetcher) Cached(rawURL string) ([]byte, error) {
	return f.cache.read(rawURL)
}

// Refreshed — что вышло из попытки обновить список.
//
// Разделение свежести и содержимого нужно панели: список, чей источник не
// ответил, продолжает работать на прошлой версии, но выглядеть как удачно
// обновлённый он не должен — иначе правило тихо ловит вчерашние домены
// (issue #149).
type Refreshed struct {
	// List — разобранное содержимое: свежее либо прошлое из кэша.
	List List
	// FetchedAt — когда источник в последний раз ответил телом или 304.
	// Нулевое время означает, что свежести не приехало вовсе.
	FetchedAt time.Time
	// Stale — почему свежая версия не приехала. Не nil означает, что в деле
	// прошлая версия из кэша, а не сегодняшняя.
	Stale error
}

// Update скачивает список, разбирает его и отдельно сообщает, приехала ли
// свежая версия. Ошибка возвращается только когда отдать нечего вовсе: источник
// не ответил и кэша нет.
func (f *Fetcher) Update(ctx context.Context, rawURL string) (Refreshed, error) {
	format := FormatOf(rawURL)
	var out Refreshed

	res, err := f.Fetch(ctx, rawURL)
	if err != nil {
		if _, cached := f.cache.meta(rawURL); !cached {
			return Refreshed{}, err
		}
		f.log.Warn("список не обновлён, берём прошлую версию", "url", rawURL, "err", err)
		out.Stale = err
	} else {
		out.FetchedAt = res.FetchedAt
	}

	body, err := f.cache.read(rawURL)
	if err != nil {
		return Refreshed{}, err
	}
	list, err := Parse(body, format)
	if err != nil {
		return Refreshed{}, err
	}
	out.List = list
	return out, nil
}

// Parse скачивает список и разбирает его в домены и подсети. Если источник
// недоступен, берётся прошлая версия из кэша: устаревший список лучше пустого.
func (f *Fetcher) Parse(ctx context.Context, rawURL string) (List, error) {
	res, err := f.Update(ctx, rawURL)
	if err != nil {
		return List{}, err
	}
	return res.List, nil
}
