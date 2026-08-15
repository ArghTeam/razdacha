package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// tunnelResponse — туннель, как его видит UI.
//
// Status, LatencyMS и LastCheck заполняет последняя проверка по Clash API
// (`POST /api/tunnels/{id}/check`, [checkCache]). Пока её не делали — null:
// ноль в latency означал бы «ноль миллисекунд», а «down» — утверждение о
// туннеле, которого никто не проверял.
// Поля `raw` здесь нет намеренно: в нём вставленная ссылка с UUID, а у туннеля
// WARP — целый `.conf` с приватным ключом, который сгенерировал сервер. Список
// перечитывается на каждый опрос экрана «Туннели», то есть ключ оседал бы в кэше
// браузера и в логах всего, что стоит между nginx и клиентом (issue #124). Форме
// правки конфиг отдаётся отдельной ручкой по явному запросу —
// [Server.handleTunnelRaw].
type tunnelResponse struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Type      store.TunnelType   `json:"type"`
	Source    store.TunnelSource `json:"source"`
	Enabled   bool               `json:"enabled"`
	CreatedAt time.Time          `json:"created_at"`

	// Builtin — запись завёл демон, а не пользователь. Панель собирает такой
	// своё меню: встроенное выключают, а не удаляют, и `DELETE` на нём отвечает
	// отказом — прятать кнопку, оставляя разрешение в API, значит расходиться
	// с самим собой.
	Builtin bool `json:"builtin"`

	// Host и Port — адрес туннеля для карточки: у vless/ss/trojan/hysteria2 это
	// сервер и порт, у wireguard и WARP — endpoint первого пира. Два поля, а не
	// строка `host:port`: та же форма, что уже отдаёт `POST /api/tunnels/parse`,
	// и панель складывает их сама — склеенную строку ей пришлось бы разбирать
	// обратно, чтобы замаскировать адрес в отчёте диагностики.
	//
	// Это единственное, что берётся из хранимого `Parsed`. Целиком его отдавать
	// нельзя: там весь конфиг, включая приватный ключ WARP (issue #124).
	// Оба поля `omitempty`: у пула своего адреса нет вовсе (серверы меняются
	// сами, на его месте карточка показывает `pool.catalog_url`), у нечитаемого
	// конфига адреса не нашлось — и в обоих случаях отсутствие поля честнее,
	// чем пустая строка с портом 0.
	Host string `json:"host,omitempty"`
	Port uint16 `json:"port,omitempty"`

	Status    *string    `json:"status"`
	LatencyMS *int       `json:"latency_ms"`
	LastCheck *time.Time `json:"last_check"`

	// LastOK — когда туннель отвечал в последний раз (проверка со статусом `up`
	// или `slow`). Отдельно от LastCheck: у молчащего туннеля LastCheck
	// показывает свежую неудачу и обновляется каждый круг расписания, а «с
	// какого момента он не отвечает» без второй отметки не сказать вовсе —
	// панель на этом различает «упал минуту назад» и «мёртв со вчера»
	// (issue #152). null означает «удачных проверок не записано»: выдуманной
	// даты здесь не будет.
	LastOK *time.Time `json:"last_ok"`

	// Pool — состав и состояние каталога; заполнен только у туннеля-пула
	// (Source = pool). У остальных null, и карточка рисует обычный туннель.
	Pool *poolResponse `json:"pool,omitempty"`
}

func newTunnelResponse(t store.Tunnel, poolEvery time.Duration) tunnelResponse {
	host, port := "", uint16(0)
	// У пула адрес не спрашиваем: серверов там сотня, и меняются они сами.
	if t.Source != store.SourcePool {
		// Ошибка разбора сюда не выносится: туннель с нечитаемым конфигом
		// остаётся в списке без адреса, а не роняет весь ответ. Что именно не
		// разобралось, показывает `POST /api/tunnels/parse` в форме правки.
		host, port = parsedEndpoint(t.Parsed)
	}
	return tunnelResponse{
		Pool:      newPoolResponse(t, poolEvery),
		Host:      host,
		Port:      port,
		ID:        t.ID,
		Name:      t.Name,
		Type:      t.Type,
		Source:    t.Source,
		Enabled:   t.Enabled,
		Builtin:   t.Builtin,
		CreatedAt: t.CreatedAt.UTC(),
	}
}

// handleListTunnels — `GET /api/tunnels`.
func (s *Server) handleListTunnels(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Tunnels(r.Context())
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	out := make([]tunnelResponse, 0, len(list))
	alive := make(map[string]bool, len(list))
	byID := make(map[string]store.Tunnel, len(list))
	for _, t := range list {
		alive[t.ID] = true
		byID[t.ID] = t
		out = append(out, s.withCheck(newTunnelResponse(t, s.poolInterval())))
	}
	s.checks.keep(alive)
	s.withPoolState(r.Context(), out, byID)
	writeJSON(w, s.log, http.StatusOK, out)
}

// tunnelRawResponse — ответ `GET /api/tunnels/{id}/raw`: конфиг туннеля для формы
// правки. Отдельной ручкой, а не полем списка: конфиг нужен одной открытой форме,
// а в списке он ездил бы с каждым опросом экрана вместе с ключом внутри.
//
// Editable — можно ли этот конфиг править руками. У WARP нельзя: ключ выдал
// Cloudflare, пользователь его не вводил и заменять его текстом в поле незачем —
// вместо конфига форма показывает Note и оставляет только имя. Raw у такого
// туннеля пустой: отдавать приватный ключ в поле, которое всё равно только для
// чтения, значит вернуть ту же утечку через другую дверь.
type tunnelRawResponse struct {
	Raw      string `json:"raw"`
	Editable bool   `json:"editable"`
	Note     string `json:"note,omitempty"`
}

// handleTunnelRaw — `GET /api/tunnels/{id}/raw`.
func (s *Server) handleTunnelRaw(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	t, err := s.store.Tunnel(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	if t.Source == store.SourceWARP {
		writeJSON(w, s.log, http.StatusOK, tunnelRawResponse{
			Note: "Конфиг выдал Cloudflare — в нём приватный ключ устройства, " +
				"и править его руками нечего. Поменять можно имя туннеля.",
		})
		return
	}
	writeJSON(w, s.log, http.StatusOK, tunnelRawResponse{Raw: t.Raw, Editable: true})
}

// tunnelRequest — тело создания и изменения туннеля. Тип и разобранный конфиг не
// принимаются: их выдаёт разбор raw, иначе в БД попадёт «vless» с конфигом socks.
type tunnelRequest struct {
	Name    *string `json:"name"`
	Raw     *string `json:"raw"`
	Enabled *bool   `json:"enabled"`
}

// handleCreateTunnel — `POST /api/tunnels`.
func (s *Server) handleCreateTunnel(w http.ResponseWriter, r *http.Request) {
	var req tunnelRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	if req.Raw == nil || strings.TrimSpace(*req.Raw) == "" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Не указан конфиг туннеля")
		return
	}

	res, err := singbox.Parse(*req.Raw)
	if err != nil {
		s.parseError(w, err)
		return
	}
	// Пул в системе один, и его заводит демон ([store.Store.EnsureBuiltinPool]).
	// Второй означал бы второй обход того же каталога и вторую группу urltest в
	// конфиге, поэтому ссылка на каталог здесь — отказ, а не создание. Разбор при
	// этом остаётся: битая ссылка получает свои 400 выше, а не общий отказ.
	if res.Source == store.SourcePool {
		writeError(w, s.log, http.StatusConflict, codeConflict,
			"Пул бесплатных ключей уже есть — включите его в списке туннелей")
		return
	}

	name := res.Name
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		name = strings.TrimSpace(*req.Name)
	}
	if name == "" {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Не указано имя туннеля, а в конфиге его нет")
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	created, err := s.store.CreateTunnel(r.Context(), store.Tunnel{
		Name:      name,
		Type:      res.Type,
		Source:    res.Source,
		Raw:       strings.TrimSpace(*req.Raw),
		Parsed:    res.Parsed,
		Enabled:   enabled,
		CreatedAt: s.now().UTC(),
	})
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	writeJSON(w, s.log, http.StatusCreated, newTunnelResponse(created, s.poolInterval()))
}

// handleUpdateTunnel — `PATCH /api/tunnels/{id}`. Смена raw означает повторный
// разбор: тип и parsed обязаны соответствовать тому, что вставили сейчас.
func (s *Server) handleUpdateTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	var req tunnelRequest
	if !s.decodeBody(w, r, &req) {
		return
	}

	t, err := s.store.Tunnel(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	if req.Raw != nil && strings.TrimSpace(*req.Raw) != t.Raw {
		res, perr := singbox.Parse(*req.Raw)
		if perr != nil {
			s.parseError(w, perr)
			return
		}
		raw := strings.TrimSpace(*req.Raw)
		typ, terr := tunnelType(res, raw)
		if terr != nil {
			writeError(w, s.log, http.StatusBadRequest, codeBadRequest, terr.Error())
			return
		}
		t.Raw = raw
		t.Type = typ
		t.Source = res.Source
		t.Parsed = res.Parsed
	}
	if req.Name != nil {
		t.Name = strings.TrimSpace(*req.Name)
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}

	if err := s.store.UpdateTunnel(r.Context(), t); err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newTunnelResponse(t, s.poolInterval()))
}

// handleDeleteTunnel — `DELETE /api/tunnels/{id}`.
//
// Туннель, на который ссылается правило, store удалить не даёт и называет эти
// правила; это 409 с его текстом, а не 500: пользователю есть что с этим сделать.
//
// У туннеля WARP, заведённого кнопкой, за записью стоит настоящее устройство у
// Cloudflare, и его снимают здесь же. Регистрация читается **до** удаления —
// после строки уже нет (ON DELETE CASCADE), — а запрос наружу идёт только после
// успешного удаления: не удалили локально, значит и устройство ещё нужно.
func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	reg, registered, err := s.store.WARPRegistration(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	if err := s.store.DeleteTunnel(r.Context(), id); err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	if registered {
		// Отказ Cloudflare сюда не долетает: туннеля уже нет, отвечать 502 не о
		// чем. Событие остаётся в логе демона ([Server.unregisterWARP]).
		s.unregisterWARP(r.Context(), id, reg)
	}
	writeJSON(w, s.log, http.StatusOK, map[string]bool{"ok": true})
}

// parsePreview — ответ `POST /api/tunnels/parse`: что демон понял из вставленной
// строки. Форма показывает это до сохранения, чтобы было видно, что вставили не то.
type parsePreview struct {
	Type      store.TunnelType   `json:"type"`
	Source    store.TunnelSource `json:"source"`
	Name      string             `json:"name"`
	Host      string             `json:"host"`
	Port      uint16             `json:"port"`
	Security  string             `json:"security"`
	Transport string             `json:"transport"`
	Warnings  []string           `json:"warnings"`
}

// handleParseTunnel — `POST /api/tunnels/parse`, разбор без сохранения.
func (s *Server) handleParseTunnel(w http.ResponseWriter, r *http.Request) {
	var req tunnelRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	if req.Raw == nil {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, "Не указан конфиг туннеля")
		return
	}
	res, err := singbox.Parse(*req.Raw)
	if err != nil {
		s.parseError(w, err)
		return
	}
	out := preview(res)
	// Тип каталожной ссылки знает драйвер, а не разбор: форма показывает протокол
	// ключей, которые придут из этого каталога, а не выдуманный по схеме ссылки.
	if res.Source == store.SourcePool {
		typ, terr := tunnelType(res, strings.TrimSpace(*req.Raw))
		if terr != nil {
			writeError(w, s.log, http.StatusBadRequest, codeBadRequest, terr.Error())
			return
		}
		out.Type = typ
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// tunnelType — протокол туннеля по разбору конфига.
//
// У каталожной ссылки его выдаёт не разбор, а драйвер каталога: `http(s)://` говорит
// лишь о том, что это каталог, и прошитый vless пережил свой источник (ADR 0015).
// Каталог без драйвера получает отказ здесь — ссылку на неизвестный сайт панель
// принять не может, иначе пул завёлся бы и молча остался пустым (issue #153).
func tunnelType(res singbox.ParseResult, raw string) (store.TunnelType, error) {
	if res.Source != store.SourcePool {
		return res.Type, nil
	}
	typ, err := lists.PoolKeyType(raw)
	if err != nil {
		// Текст показывается пользователю как есть, поэтому идёт наружу целиком:
		// в нём и хост, и причина — закрылся источник или разборщика не было вовсе.
		return "", fmt.Errorf("ключи из этого каталога брать нечем: %w", err)
	}
	return typ, nil
}

// preview вынимает из разобранного конфига то, что показывается в форме.
// Читается сам JSON, а не структуры option: у outbound и endpoint поля разные,
// а формат JSON один и уже нормализован разбором.
func preview(res singbox.ParseResult) parsePreview {
	out := parsePreview{
		Type:     res.Type,
		Source:   res.Source,
		Name:     res.Name,
		Warnings: []string{},
	}

	// У пула нет ни адреса, ни шифра: серверы придут из каталога при первом обходе.
	// Разбирать пустой Parsed незачем — иначе форма показала бы предупреждение о
	// нечитаемом конфиге там, где читать нечего.
	if res.Source == store.SourcePool {
		return out
	}

	var probe endpointProbe
	if err := json.Unmarshal(res.Parsed, &probe); err != nil {
		out.Warnings = append(out.Warnings, "разобранный конфиг не читается обратно")
		return out
	}

	out.Host, out.Port = probe.endpoint()

	switch {
	case probe.TLS != nil && probe.TLS.Reality != nil && probe.TLS.Reality.Enabled:
		out.Security = "reality"
	case probe.TLS != nil && probe.TLS.Enabled:
		out.Security = "tls"
	default:
		out.Security = "none"
	}
	if probe.Transport != nil {
		out.Transport = probe.Transport.Type
	}

	if res.Type == store.TunnelRaw {
		out.Warnings = append(out.Warnings,
			"протокол не распознан, конфиг уйдёт в sing-box как есть")
	}
	if out.Host == "" {
		out.Warnings = append(out.Warnings, "в конфиге не нашёлся адрес сервера")
	}
	return out
}

// endpointProbe — то немногое, что читается из разобранного конфига обоими
// потребителями: превью формы и карточкой в списке. Читается сам JSON, а не
// структуры option: у outbound и endpoint поля разные, а формат JSON один и уже
// нормализован разбором.
type endpointProbe struct {
	Server     string `json:"server"`
	ServerPort uint16 `json:"server_port"`
	TLS        *struct {
		Enabled bool `json:"enabled"`
		Reality *struct {
			Enabled bool `json:"enabled"`
		} `json:"reality"`
	} `json:"tls"`
	Transport *struct {
		Type string `json:"type"`
	} `json:"transport"`
	Peers []struct {
		Address string `json:"address"`
		Port    uint16 `json:"port"`
	} `json:"peers"`
}

// endpoint — адрес туннеля: сервер и порт, а у wireguard и WARP — endpoint
// первого пира, потому что своего `server` у них нет.
func (p endpointProbe) endpoint() (string, uint16) {
	if p.Server != "" {
		return p.Server, p.ServerPort
	}
	if len(p.Peers) > 0 {
		return p.Peers[0].Address, p.Peers[0].Port
	}
	return "", 0
}

// parsedEndpoint достаёт адрес из хранимого `parsed`. Конфига нет или он не
// читается — пусто: адрес показывать нечем, но список из-за этого не падает.
func parsedEndpoint(parsed json.RawMessage) (string, uint16) {
	if len(parsed) == 0 {
		return "", 0
	}
	var probe endpointProbe
	if err := json.Unmarshal(parsed, &probe); err != nil {
		return "", 0
	}
	return probe.endpoint()
}

// parseError отвечает на ошибку разбора вставленной строки. Это ввод
// пользователя, а не сбой: 400 и текст ошибки как есть, он на русском.
func (s *Server) parseError(w http.ResponseWriter, err error) {
	if errors.Is(err, singbox.ErrParse) {
		// Текст оставляется целиком: «не удалось разобрать конфиг туннеля: …» —
		// это и есть объяснение пользователю, что именно не разобралось.
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest, err.Error())
		return
	}
	s.log.Error("разбор конфига туннеля", "ошибка", err)
	writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
}
