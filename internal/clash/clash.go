// Package clash — клиент Clash API sing-box.
//
// sing-box поднимает Clash API на loopback (`experimental.clash_api`,
// docs/01-architecture.md), и это единственный способ спросить работающий
// рантайм о туннелях: измерить задержку по тегу и узнать, есть ли такой тег в
// применённом конфиге вообще. Наружу API не выставляется — панель ходит сюда
// сама и отдаёт UI уже свой ответ.
//
// Пакет ничего не знает ни о БД, ни о тегах туннелей: тег строит вызывающий
// (`singbox.TunnelTag`). Адрес — параметр, а не константа: демон и тесты
// подставляют свой.
package clash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Значения по умолчанию.
const (
	// DefaultAddr — адрес Clash API из генератора конфига (singbox.clashListen).
	DefaultAddr = "127.0.0.1:9090"

	// DefaultTestURL — цель проверки. Тот же адрес, что у Podkop
	// (podkop/files/usr/bin/podkop:2234): отдаёт 204 с пустым телом, доступен
	// по всему миру и не считается «сервисом», который где-то заблокируют.
	DefaultTestURL = "https://www.gstatic.com/generate_204"

	// DefaultProbeTimeout — сколько ждёт цели сам sing-box (параметр timeout
	// запроса).
	//
	// Podkop берёт две секунды, и на стенде этого не хватило: рабочий
	// WireGuard-туннель отвечал стабильно за ~5 с и помечался «не отвечает».
	// Пользователь при этом видел его рабочим — трафик через туннель ходил.
	// Ложное «мёртв» на живом туннеле хуже, чем лишние секунды ожидания:
	// по нему принимают решение перевыпустить конфиг или сменить сервер.
	DefaultProbeTimeout = 8 * time.Second

	// SlowThreshold — с какой задержки туннель считается медленным. Отдельный
	// статус нужен, чтобы «работает, но еле-еле» не выглядело как «работает»:
	// на таком туннеле видео не пойдёт, и знать об этом надо до жалоб.
	SlowThreshold = 1500 * time.Millisecond

	// requestSlack — запас поверх probeTimeout на дедлайн HTTP-запроса.
	// Дедлайн заведомо больше таймаута проверки, чтобы первым срабатывал
	// sing-box: тогда «туннель не отвечает» и «sing-box не отвечает» —
	// разные ответы, а не один и тот же обрыв по времени.
	requestSlack = 3 * time.Second

	// maxBody — предел тела ответа. Ответы Clash API мелкие, список прокси
	// на десятке туннелей — единицы килобайт.
	maxBody = 1 << 20
)

// Ошибки пакета. Тексты доходят до пользователя как есть, поэтому на русском.
var (
	// ErrUnavailable — до Clash API не достучались: sing-box не запущен, не
	// дослушал порт или завис. Это состояние демона, а не туннеля.
	ErrUnavailable = errors.New("sing-box не отвечает")

	// ErrNotFound — тега нет в текущем конфиге sing-box. Обычно это туннель,
	// заведённый в панели, но ещё не применённый.
	ErrNotFound = errors.New("туннель не найден в конфиге sing-box")

	// ErrProbeFailed — sing-box проверку выполнил, но цель через туннель не
	// открылась. Это единственная ошибка, которая говорит о самом туннеле.
	ErrProbeFailed = errors.New("туннель не ответил на проверку")

	// ErrBadResponse — ответ Clash API не разобран.
	ErrBadResponse = errors.New("непонятный ответ Clash API")

	// ErrResolveFailed — рантайм принял запрос, но домен не разрешил. Это
	// ответ о домене (или об апстриме DNS), а не о самом sing-box.
	ErrResolveFailed = errors.New("рантайм не разрешил домен")
)

// Options — параметры клиента.
type Options struct {
	// Addr — «хост:порт» либо готовый базовый URL. Пустой — [DefaultAddr].
	Addr string
	// Secret — `external_controller` sing-box может требовать токен. Наш
	// генератор его не ставит, но клиент не должен ломаться, если поставят.
	Secret string
	// TestURL — цель проверки. Пустая — [DefaultTestURL].
	TestURL string
	// ProbeTimeout — таймаут проверки на стороне sing-box. Ноль — [DefaultProbeTimeout].
	ProbeTimeout time.Duration
	// HTTPClient подменяется в тестах. Пустой — свой с дедлайном по ProbeTimeout.
	HTTPClient *http.Client
}

// Client — клиент Clash API. Безопасен для параллельного использования.
type Client struct {
	base    string
	secret  string
	testURL string
	probe   time.Duration
	// deadline — предел на весь запрос, включая ожидание проверки внутри
	// sing-box. Всегда больше probe, см. requestSlack.
	deadline time.Duration
	hc       *http.Client
}

// New собирает клиент. Ошибок нет намеренно: неверный адрес всплывёт при первом
// запросе как ErrUnavailable, а демон не должен падать на старте из-за того,
// что кто-то поправил адрес Clash API.
func New(opts Options) *Client {
	addr := opts.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	base := addr
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	base = strings.TrimSuffix(base, "/")

	probe := opts.ProbeTimeout
	if probe <= 0 {
		probe = DefaultProbeTimeout
	}
	testURL := opts.TestURL
	if testURL == "" {
		testURL = DefaultTestURL
	}
	// Дедлайн запроса берётся у подставленного клиента, если он задан: иначе
	// тест на «sing-box завис» ждал бы реальные секунды.
	deadline := probe + requestSlack
	hc := opts.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: deadline}
	} else if hc.Timeout > 0 {
		deadline = hc.Timeout
	}
	return &Client{
		base:     base,
		secret:   opts.Secret,
		testURL:  testURL,
		probe:    probe,
		deadline: deadline,
		hc:       hc,
	}
}

// Proxy — состояние прокси в рантайме sing-box.
type Proxy struct {
	Name    string    `json:"name"`
	Type    string    `json:"type"`
	UDP     bool      `json:"udp"`
	History []History `json:"history"`
	// Now — тег участника, выбранного группой (`urltest`, `selector`). У обычного
	// outbound'а пусто: выбирать там нечего.
	Now string `json:"now"`
	// All — теги всех участников группы; у обычного outbound'а пусто.
	All []string `json:"all"`
}

// History — запись журнала проверок Clash API.
type History struct {
	Time  time.Time `json:"time"`
	Delay uint16    `json:"delay"`
}

// Latency отдаёт последнюю измеренную задержку из журнала прокси. Второе
// значение false означает «не проверялся»: нулевая задержка — это измеренный
// ноль, а не отсутствие данных.
func (p Proxy) Latency() (time.Duration, bool) {
	if len(p.History) == 0 {
		return 0, false
	}
	last := p.History[len(p.History)-1]
	if last.Delay == 0 {
		// Clash пишет нулевую задержку неудачной проверке.
		return 0, false
	}
	return time.Duration(last.Delay) * time.Millisecond, true
}

// Delay измеряет задержку до [Options.TestURL] через прокси с тегом tag.
//
// Разбор ответа даёт три разных исхода, и путать их нельзя: [ErrNotFound] —
// туннель не применён, [ErrProbeFailed] — применён, но не работает,
// [ErrUnavailable] — не работает сам sing-box.
func (c *Client) Delay(ctx context.Context, tag string) (time.Duration, error) {
	q := url.Values{
		"url":     {c.testURL},
		"timeout": {strconv.FormatInt(c.probe.Milliseconds(), 10)},
	}
	body, status, err := c.get(ctx, "/proxies/"+url.PathEscape(tag)+"/delay", q)
	if err != nil {
		return 0, err
	}

	switch {
	case status == http.StatusOK:
		var out struct {
			Delay uint16 `json:"delay"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			return 0, fmt.Errorf("%w: задержка туннеля %q: %w", ErrBadResponse, tag, err)
		}
		return time.Duration(out.Delay) * time.Millisecond, nil
	case status == http.StatusNotFound:
		return 0, fmt.Errorf("%w: %s", ErrNotFound, tag)
	case status >= 500:
		// sing-box отвечает 503 и текстом, когда проверка не удалась —
		// это ответ о туннеле, а не сбой API.
		return 0, fmt.Errorf("%w: %s", ErrProbeFailed, detail(body))
	default:
		return 0, fmt.Errorf("%w: код %d, %s", ErrBadResponse, status, detail(body))
	}
}

// Proxy отдаёт состояние одного прокси по тегу.
func (c *Client) Proxy(ctx context.Context, tag string) (Proxy, error) {
	body, status, err := c.get(ctx, "/proxies/"+url.PathEscape(tag), nil)
	if err != nil {
		return Proxy{}, err
	}
	switch status {
	case http.StatusOK:
		var p Proxy
		if err := json.Unmarshal(body, &p); err != nil {
			return Proxy{}, fmt.Errorf("%w: состояние туннеля %q: %w", ErrBadResponse, tag, err)
		}
		return p, nil
	case http.StatusNotFound:
		return Proxy{}, fmt.Errorf("%w: %s", ErrNotFound, tag)
	default:
		return Proxy{}, fmt.Errorf("%w: код %d, %s", ErrBadResponse, status, detail(body))
	}
}

// Proxies отдаёт все прокси рантайма, ключ — тег.
func (c *Client) Proxies(ctx context.Context) (map[string]Proxy, error) {
	body, status, err := c.get(ctx, "/proxies", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: код %d, %s", ErrBadResponse, status, detail(body))
	}
	var out struct {
		Proxies map[string]Proxy `json:"proxies"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: список прокси: %w", ErrBadResponse, err)
	}
	return out.Proxies, nil
}

// Rule — правило маршрутизации в применённом конфиге, как его видит рантайм.
//
// Payload — строковое представление условий (`rule_set=[list-youtube] …`),
// Proxy — действие (`route(tun-…)`, `reject`). Разбирать их — дело
// вызывающего: пакет про наши теги ничего не знает.
type Rule struct {
	Type    string `json:"type"`
	Payload string `json:"payload"`
	Proxy   string `json:"proxy"`
}

// Rules отдаёт правила маршрутизации работающего sing-box в том порядке, в
// каком он их проверяет.
//
// Спрашивается именно рантайм, а не БД: расхождение между применённым конфигом
// и накопленными правками — то самое, ради чего пробник и заводился.
func (c *Client) Rules(ctx context.Context) ([]Rule, error) {
	body, status, err := c.get(ctx, "/rules", nil)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("%w: код %d, %s", ErrBadResponse, status, detail(body))
	}
	var out struct {
		Rules []Rule `json:"rules"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: правила маршрутизации: %w", ErrBadResponse, err)
	}
	return out.Rules, nil
}

// dnsTypeA — тип записи A в ответе `/dns/query`: рантайм отдаёт числовой код.
const dnsTypeA = 1

// Коды ответа резолвера, которые разбираются отдельно.
const (
	rcodeSuccess  = 0 // NOERROR
	rcodeServFail = 2
	rcodeNXDomain = 3
	rcodeRefused  = 5
)

// ResolveError — отказ резолвера с его кодом ответа.
//
// Коды означают разное, и разница существенна для пользователя: NXDOMAIN —
// домена нет у апстрима, REFUSED — запрос отклонён нашим же правилом (правило
// «блокировать» и правило с недоступным туннелем отвечают на DNS отказом,
// ADR 0013). Второе — не «DNS сломан», а «так и задумано», и путать их нельзя.
type ResolveError struct {
	// Code — код ответа (RCODE) из тела `/dns/query`.
	Code int
}

func (e *ResolveError) Error() string {
	switch e.Code {
	case rcodeServFail:
		return "апстрим ответил ошибкой (SERVFAIL)"
	case rcodeNXDomain:
		return "такого домена нет (NXDOMAIN)"
	case rcodeRefused:
		return "запрос отклонён правилом (REFUSED) — так отвечает правило «блокировать» " +
			"и правило с недоступным туннелем"
	default:
		return fmt.Sprintf("резолвер ответил кодом %d", e.Code)
	}
}

// Is связывает отказ с [ErrResolveFailed]: вызывающему, которому важен только
// факт «резолвер не ответил адресом», разбирать код незачем.
func (e *ResolveError) Is(target error) bool { return target == ErrResolveFailed }

// Refused — отклонён ли запрос правилом. Это ответ о маршрутизации, а не о
// поломке DNS, и показывать его надо иначе.
func (e *ResolveError) Refused() bool { return e.Code == rcodeRefused }

// ResolveA спрашивает домен у резолвера работающего sing-box — того самого, к
// которому ходят клиенты, — и отдаёт адреса записей A.
//
// Это единственный способ узнать, поймало ли домен хоть одно правило: домен
// правила с туннелем получает FakeIP, остальные — настоящий адрес с апстрима
// (docs/04-dns-fakeip.md).
func (c *Client) ResolveA(ctx context.Context, name string) ([]netip.Addr, error) {
	q := url.Values{"name": {name}, "type": {"A"}}
	body, status, err := c.get(ctx, "/dns/query", q)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
	case http.StatusBadRequest:
		return nil, fmt.Errorf("%w: домен %q не принят рантаймом: %s", ErrBadResponse, name, detail(body))
	default:
		// Сюда попадает 500: рантайм не смог выполнить запрос вовсе. Обычный
		// отказ резолва так не выглядит — он приезжает кодом 200, см. ниже.
		return nil, fmt.Errorf("%w: %s", ErrResolveFailed, detail(body))
	}

	var out struct {
		Status int `json:"Status"`
		Answer []struct {
			Type uint16 `json:"type"`
			Data string `json:"data"`
		} `json:"Answer"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("%w: ответ резолвера по %q: %w", ErrBadResponse, name, err)
	}
	// Отказ резолва — это `Status` в теле, а не код HTTP: проверено на стенде с
	// sing-box 1.12.25, где несуществующий домен и домен под правилом `reject`
	// оба приезжают с кодом 200. Ветка по коду выше остаётся: 500 означает, что
	// рантайм не дошёл до резолва вовсе.
	if out.Status != rcodeSuccess {
		return nil, &ResolveError{Code: out.Status}
	}
	var addrs []netip.Addr
	for _, a := range out.Answer {
		if a.Type != dnsTypeA {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(a.Data))
		if err != nil {
			continue
		}
		addrs = append(addrs, addr)
	}
	return addrs, nil
}

// Version отдаёт версию работающего рантайма sing-box, без ведущей «v».
//
// Спрашиваем сам процесс, а не запускаем `sing-box version`, по трём причинам,
// и первая — не стилистическая: бинарник на диске может отличаться от того, что
// сейчас в памяти (обновление меняет файл, но пока юнит не перезапущен, работает
// прежний), а вопрос задаётся именно про работающий рантайм. Вторая — запускать
// бинарники ради состояния системы мы не хотим вообще, это то же основание, по
// которому не вызываются `nft`, `ip` и `wg`. Третья — этот клиент уже есть, и
// нового пути наружу не появляется.
//
// Рантайм не отвечает — [ErrUnavailable], как и у остальных методов: это
// состояние демона, а не версия «неизвестна навсегда».
func (c *Client) Version(ctx context.Context) (string, error) {
	body, status, err := c.get(ctx, "/version", nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("%w: код %d, %s", ErrBadResponse, status, detail(body))
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: версия рантайма: %w", ErrBadResponse, err)
	}
	// Живой sing-box отвечает не голой версией, а «sing-box 1.12.25»
	// (проверено на стенде). Имя продукта здесь лишнее: строка подписана
	// «sing-box, рантайм» и сравнивается с версией библиотеки из go.mod,
	// записанной без него.
	v := strings.TrimPrefix(strings.TrimSpace(out.Version), "sing-box")
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return "", fmt.Errorf("%w: версия рантайма пуста", ErrBadResponse)
	}
	return v, nil
}

// get выполняет запрос и возвращает тело с кодом. Ошибка отсюда означает
// недоступный Clash API — коды разбирает вызывающий, у каждого пути они свои.
func (c *Client) get(ctx context.Context, path string, q url.Values) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()

	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: запрос %s не собран: %w", ErrUnavailable, path, err)
	}
	if c.secret != "" {
		req.Header.Set("Authorization", "Bearer "+c.secret)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		var netErr net.Error
		if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout()) {
			return nil, 0, fmt.Errorf("%w: не ответил за %s", ErrUnavailable, c.deadline)
		}
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			// Запрос отменил вызывающий (закрыл вкладку) — это не диагноз.
			return nil, 0, fmt.Errorf("проверка отменена: %w", ctx.Err())
		}
		return nil, 0, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: ответ не дочитан: %w", ErrUnavailable, err)
	}
	return body, resp.StatusCode, nil
}

// detail вынимает из тела ошибки поле message. Clash API кладёт причину именно
// туда; если тела нет — отдаётся сам текст, обрезанный до строки.
func detail(body []byte) string {
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &out); err == nil && out.Message != "" {
		return out.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "без объяснения"
	}
	return s
}
