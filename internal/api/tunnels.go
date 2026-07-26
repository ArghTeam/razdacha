package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// tunnelResponse — туннель, как его видит UI.
//
// Status, LatencyMS и LastCheck заполняет последняя проверка по Clash API
// (`POST /api/tunnels/{id}/check`, [checkCache]). Пока её не делали — null:
// ноль в latency означал бы «ноль миллисекунд», а «down» — утверждение о
// туннеле, которого никто не проверял.
type tunnelResponse struct {
	ID        string             `json:"id"`
	Name      string             `json:"name"`
	Type      store.TunnelType   `json:"type"`
	Source    store.TunnelSource `json:"source"`
	Raw       string             `json:"raw"`
	Enabled   bool               `json:"enabled"`
	CreatedAt time.Time          `json:"created_at"`

	// Builtin — запись завёл демон, а не пользователь. Панель собирает такой
	// своё меню: встроенное выключают, а не удаляют, и `DELETE` на нём отвечает
	// отказом — прятать кнопку, оставляя разрешение в API, значит расходиться
	// с самим собой.
	Builtin bool `json:"builtin"`

	Status    *string    `json:"status"`
	LatencyMS *int       `json:"latency_ms"`
	LastCheck *time.Time `json:"last_check"`

	// Pool — состав и состояние каталога; заполнен только у туннеля-пула
	// (Source = pool). У остальных null, и карточка рисует обычный туннель.
	Pool *poolResponse `json:"pool,omitempty"`
}

func newTunnelResponse(t store.Tunnel, poolEvery time.Duration) tunnelResponse {
	return tunnelResponse{
		Pool:      newPoolResponse(t, poolEvery),
		ID:        t.ID,
		Name:      t.Name,
		Type:      t.Type,
		Source:    t.Source,
		Raw:       t.Raw,
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
		t.Raw = strings.TrimSpace(*req.Raw)
		t.Type = res.Type
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
func (s *Server) handleDeleteTunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteTunnel(r.Context(), id); err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
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
	writeJSON(w, s.log, http.StatusOK, preview(res))
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

	var probe struct {
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
	if err := json.Unmarshal(res.Parsed, &probe); err != nil {
		out.Warnings = append(out.Warnings, "разобранный конфиг не читается обратно")
		return out
	}

	out.Host, out.Port = probe.Server, probe.ServerPort
	if out.Host == "" && len(probe.Peers) > 0 {
		// WireGuard: адрес сервера лежит в первом пире endpoint'а.
		out.Host, out.Port = probe.Peers[0].Address, probe.Peers[0].Port
	}

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
