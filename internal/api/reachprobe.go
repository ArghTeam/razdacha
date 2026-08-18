package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Проба доступности домена: «открывается ли он напрямую, с адреса сервера, или
// отдаёт геоблок». Сосед пробника маршрута (routeprobe.go), но вопрос другой:
// тот говорит, какое правило поймает домен, эта — что ответит сайт, если пойти
// к нему в лоб.
//
// Демон ходит обычным `net/http` со своего адреса. Его резолв идёт мимо
// FakeIP-резолвера sing-box (тот только для клиентов wg0 на 10.8.0.1), поэтому
// настоящий IP берётся сам собой — в конфиг ядра и Clash API лезть незачем.
//
// Вердикт формулируется честно: «напрямую отдаёт 403 — похоже на геоблок», без
// утверждений, что туннель это чинит. Подтверждение через туннель — Фаза 1
// (issue #192), здесь только прямая проба по кнопке.

// Классы доступности домена. Ровно три: сайт ответил по-человечески, сайт
// ответил отказом, похожим на геоблок, либо не ответил вовсе.
const (
	reachClassOK       = "reachable"
	reachClassGeoblock = "geoblock_suspect"
	reachClassDown     = "unreachable"
)

const (
	// reachTimeout — общий бюджет одной пробы. Проба ручная и одиночная, но
	// висеть на неотвечающем домене дольше нельзя: панель ждёт ответа.
	reachTimeout = 9 * time.Second
	// reachMaxBody — сколько тела читаем. Хватает, чтобы поймать «error code:
	// 1020» в теле страницы Cloudflare, и не тянет мегабайты зря.
	reachMaxBody = 64 << 10
	// reachMaxRedirects — редиректы гоняем, но не бесконечно: цепочка редиректов
	// на страницу-заглушку интересна только первыми шагами.
	reachMaxRedirects = 3
	// reachUserAgent — не пустой UA: часть сайтов отдаёт пустому клиенту тот же
	// 403, что и геоблоку, и это исказило бы вердикт.
	reachUserAgent = "Mozilla/5.0 (compatible; razdacha-reachability/1.0)"
)

// reachProber ходит к домену напрямую и возвращает код ответа и начало тела.
// Выделен полем сервера, чтобы хендлер тестировался подменой: настоящая сеть в
// тестах не участвует. status == 0 вместе с ненулевой ошибкой означает, что до
// сайта не дозвонились вовсе.
type reachProber func(ctx context.Context, domain string) (status int, body []byte, err error)

// reachResult — тело `POST /api/domain/reachability`.
type reachResult struct {
	Domain string `json:"domain"`
	// Status — HTTP-код прямого ответа; 0, если соединения не случилось.
	Status int `json:"status"`
	// Class — один из reachClass*: reachable, geoblock_suspect, unreachable.
	Class string `json:"class"`
	// Verdict — тот же вывод словами, на русском. Показывается как есть.
	Verdict string `json:"verdict"`
}

// handleReachability — `POST /api/domain/reachability`.
func (s *Server) handleReachability(w http.ResponseWriter, r *http.Request) {
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

	status, body, err := s.reach(r.Context(), domain)
	if err != nil {
		// Ошибка соединения — не поломка ручки, а сам ответ на вопрос: домен
		// напрямую не отвечает. Классификатор переведёт её в вердикт.
		s.log.Debug("проба доступности: прямого ответа нет", "домен", domain, "ошибка", err)
	}
	class, verdict := classifyReach(status, body, err)

	writeJSON(w, s.log, http.StatusOK, reachResult{
		Domain:  domain,
		Status:  status,
		Class:   class,
		Verdict: verdict,
	})
}

// classifyReach переводит исход пробы в класс и вердикт. Чистая функция: код
// ответа, кусок тела и ошибка соединения на входе — класс и русский текст на
// выходе, без сети. Здесь и живёт правило «не выдумывать пустоту».
func classifyReach(status int, body []byte, connErr error) (class, verdict string) {
	if connErr != nil {
		return reachClassDown, "напрямую не отвечает — блокировка провайдера или сайт недоступен, по коду не различить."
	}
	// Cloudflare 1020 приходит обычно как 403 с этим кодом в теле; ловим его
	// раньше разбора статуса, чтобы уточнить вердикт.
	if bytes.Contains(bytes.ToLower(body), []byte("error code: 1020")) {
		return reachClassGeoblock, "напрямую отдаёт Cloudflare 1020 — похоже на геоблок."
	}
	switch status {
	case http.StatusForbidden, http.StatusUnavailableForLegalReasons:
		return reachClassGeoblock, fmt.Sprintf("напрямую отдаёт %d — похоже на геоблок.", status)
	}
	if status >= 200 && status < 400 {
		return reachClassOK, "напрямую открывается."
	}
	// Любой другой код — сайт всё же ответил, значит канал до него есть: это не
	// молчание и не геоблок (403/451). Но и «открывается» сказать нельзя.
	return reachClassOK, fmt.Sprintf(
		"напрямую отвечает кодом %d — до сайта дозвонились, на геоблок (403/451) не похоже.", status)
}

// probeReachability — настоящий поход к домену. Обычный `net/http` со своего
// адреса: резолв идёт мимо FakeIP-резолвера sing-box, поэтому адрес реальный.
func probeReachability(ctx context.Context, domain string) (int, []byte, error) {
	ctx, cancel := context.WithTimeout(ctx, reachTimeout)
	defer cancel()

	client := &http.Client{
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= reachMaxRedirects {
				// Остановиться на последнем ответе, а не оборвать ошибкой:
				// сама заглушка редиректом ведёт как раз на геоблок-страницу.
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
	if err != nil {
		return 0, nil, fmt.Errorf("проба доступности %q: %w", domain, err)
	}
	req.Header.Set("User-Agent", reachUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("проба доступности %q: %w", domain, err)
	}
	defer resp.Body.Close()

	// Тело читаем ограниченно и его недочит не считаем провалом пробы: код
	// ответа уже получен, а тело нужно лишь для детекта Cloudflare 1020.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, reachMaxBody))
	return resp.StatusCode, body, nil
}
