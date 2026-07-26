package lists

import (
	"context"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// DefaultPoolCatalogURL — каталог бесплатных vless-ключей.
//
// Ключи снимаются со страницы, а не с подписки: штатная /sub отдаёт 500 на любой
// User-Agent. Разметка server-side (Blade), её правка ломает разбор молча — поэтому
// разборщик проверяется на сохранённой странице (testdata/vpnkeys-vless-page*.html),
// а не по сети.
const DefaultPoolCatalogURL = "https://vpnkeys.me/protocol/vless"

// Ограничения обхода каталога.
const (
	// poolPerPage — размер страницы. Сайт принимает только 20, 50 и 100; всё
	// больше сотни молча схлопывается в 50, поэтому шаг фиксирован.
	poolPerPage = 100

	// poolMaxPages — потолок на число страниц. Каталог из ста с небольшим ключей
	// укладывается в две; потолок страхует от бесконечного обхода, если сайт
	// начнёт отдавать одну и ту же страницу.
	poolMaxPages = 20

	// poolMaxPageBytes — потолок размера одной страницы. Сотня карточек — около
	// 200 КиБ.
	poolMaxPageBytes = 8 << 20
)

// Разметка карточки. Карточка режется по открывающему div, поля выбираются
// точечно: классы стабильны, полноценный парсер HTML тут лишний.
var (
	rePoolCard    = regexp.MustCompile(`<div class="key-card\s`)
	rePoolKeyURL  = regexp.MustCompile(`data-key-url="([^"]+)"`)
	rePoolCountry = regexp.MustCompile(`class="country-name">([^<]*)<`)
	rePoolTitle   = regexp.MustCompile(`class="key-title">([^<]*)<`)
	// (?s) обязателен: между key-ping и span лежит многострочный svg.
	rePoolPing   = regexp.MustCompile(`(?s)class="key-ping">.*?<span>\s*(\d+)\s*ms\s*</span>`)
	rePoolStatus = regexp.MustCompile(`class="key-status status-(\w+)"`)
)

// PoolCatalog обходит страницы каталога и отдаёт серверы для туннеля-пула.
type PoolCatalog struct {
	// Client — пустой означает клиент с таймаутом по умолчанию.
	Client HTTPDoer
	// Log — пустой означает slog.Default().
	Log *slog.Logger
	// Timeout — потолок на одну страницу. Ноль означает defaultTimeout.
	Timeout time.Duration
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

// Servers обходит каталог целиком и отдаёт серверы в том виде, в каком они лежат
// в БД. Ошибка любой страницы отменяет обход: половина каталога, записанная как
// целый пул, выкинула бы из конфига живые серверы.
func (c *PoolCatalog) Servers(ctx context.Context, catalogURL string) ([]store.PoolServer, error) {
	if catalogURL == "" {
		catalogURL = DefaultPoolCatalogURL
	}

	seen := make(map[string]bool)
	var out []store.PoolServer

	for page := 1; page <= poolMaxPages; page++ {
		pageURL, err := poolPageURL(catalogURL, page)
		if err != nil {
			return nil, err
		}
		body, err := c.page(ctx, pageURL)
		if err != nil {
			return nil, err
		}

		keys, cards := parseVPNKeysPage(body)
		c.log().Debug("страница каталога разобрана",
			"url", pageURL, "карточек", cards, "ключей", len(keys))
		if cards == 0 {
			break
		}
		for _, k := range keys {
			if seen[k.URL] {
				continue
			}
			seen[k.URL] = true
			out = append(out, store.PoolServer{
				URL:     k.URL,
				Country: k.Country,
				Title:   k.Title,
				PingMS:  k.PingMS,
			})
		}
		// Конец обхода определяется числом карточек, а не числом ключей: карточек
		// без data-key-url на странице бывает от одной до нескольких, и порог по
		// ключам обрывал обход на первой же странице.
		if cards < poolPerPage {
			break
		}
	}

	if len(out) == 0 {
		return nil, fmt.Errorf("%w: каталог %s не дал ни одного ключа", ErrBadResponse, catalogURL)
	}
	return out, nil
}

// page тянет одну страницу каталога.
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

// poolPageURL добавляет к адресу каталога пагинацию, не теряя его собственных
// параметров.
func poolPageURL(catalogURL string, page int) (string, error) {
	u, err := url.Parse(catalogURL)
	if err != nil {
		return "", fmt.Errorf("%w: адрес каталога %q неразборчив: %w",
			store.ErrInvalid, catalogURL, err)
	}
	q := u.Query()
	q.Set("per_page", strconv.Itoa(poolPerPage))
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// poolKey — одна карточка каталога.
type poolKey struct {
	URL     string
	Country string
	Title   string
	PingMS  int
}

// parseVPNKeysPage выбирает карточки со ссылкой и вторым значением отдаёт общее
// число карточек: по нему определяется последняя страница. Карточка без
// data-key-url ключа не несёт и пропускается — такие на странице встречаются.
//
// Неактивные карточки тоже пропускаются: сайт сам помечает мёртвые ключи, и
// тратить на них место в конфиге и пробы urltest незачем.
func parseVPNKeysPage(body string) ([]poolKey, int) {
	idx := rePoolCard.FindAllStringIndex(body, -1)
	keys := make([]poolKey, 0, len(idx))
	for i, loc := range idx {
		end := len(body)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		card := body[loc[0]:end]

		raw := poolGroup(rePoolKeyURL, card)
		if raw == "" {
			continue
		}
		// В data-key-url после самого URL через пробел идёт подпись карточки.
		keyURL := raw
		if sp := strings.IndexByte(raw, ' '); sp >= 0 {
			keyURL = raw[:sp]
		}
		if !strings.Contains(keyURL, "://") {
			continue
		}
		if poolGroup(rePoolStatus, card) != "active" {
			continue
		}
		ping, _ := strconv.Atoi(poolGroup(rePoolPing, card))
		keys = append(keys, poolKey{
			URL:     keyURL,
			Country: poolGroup(rePoolCountry, card),
			Title:   poolGroup(rePoolTitle, card),
			PingMS:  ping,
		})
	}
	return keys, len(idx)
}

// poolGroup отдаёт первую группу совпадения с распакованными HTML-сущностями.
func poolGroup(re *regexp.Regexp, s string) string {
	m := re.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(html.UnescapeString(m[1]))
}
