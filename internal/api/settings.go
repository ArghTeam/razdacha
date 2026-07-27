package api

import (
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// settingsResponse — настройки для UI.
//
// `list_update_interval` уходит в секундах, а не наносекундами time.Duration:
// наносекунды в JSON — деталь представления Go, а не контракт API.
//
// `server_public_key` — публичный ключ wg0. Хранимой настройкой он не является и
// изменению не подлежит, но клиентскому конфигу необходим, а больше его взять
// неоткуда; поле только на чтение и `null`, пока интерфейс не поднят.
type settingsResponse struct {
	WGListenPort        int     `json:"wg_listen_port"`
	WGPool              string  `json:"wg_pool"`
	WGServerAddress     string  `json:"wg_server_address"`
	EndpointHost        string  `json:"endpoint_host"`
	ClientMTU           int     `json:"client_mtu"`
	DNSUpstream         string  `json:"dns_upstream"`
	DNSType             string  `json:"dns_type"`
	WANInterface        string  `json:"wan_interface"`
	ListUpdateInterval  int     `json:"list_update_interval"`
	TunnelCheckInterval int     `json:"tunnel_check_interval"`
	LogLevel            string  `json:"log_level"`
	ServerPublicKey     *string `json:"server_public_key"`

	// RequiresClientReconfig ставится только в ответе на изменение и означает,
	// что клиентам нужно перевыдать конфиги.
	RequiresClientReconfig bool `json:"requires_client_reconfig,omitempty"`
}

func newSettingsResponse(v store.Settings, serverKey string) settingsResponse {
	out := settingsResponse{
		WGListenPort:        v.WGListenPort,
		WGPool:              v.WGPool,
		WGServerAddress:     v.WGServerAddress,
		EndpointHost:        v.EndpointHost,
		ClientMTU:           v.ClientMTU,
		DNSUpstream:         v.DNSUpstream,
		DNSType:             v.DNSType,
		WANInterface:        v.WANInterface,
		ListUpdateInterval:  int(v.ListUpdateInterval / time.Second),
		TunnelCheckInterval: int(v.TunnelCheckInterval / time.Second),
		LogLevel:            v.LogLevel,
	}
	if serverKey != "" {
		out.ServerPublicKey = &serverKey
	}
	return out
}

// settingsRequest — тело `PATCH /api/settings`. Указатели отличают «не прислали»
// от «прислали пустое»: обнулять MTU молчанием нельзя.
type settingsRequest struct {
	WGListenPort        *int    `json:"wg_listen_port"`
	WGPool              *string `json:"wg_pool"`
	WGServerAddress     *string `json:"wg_server_address"`
	EndpointHost        *string `json:"endpoint_host"`
	ClientMTU           *int    `json:"client_mtu"`
	DNSUpstream         *string `json:"dns_upstream"`
	DNSType             *string `json:"dns_type"`
	WANInterface        *string `json:"wan_interface"`
	ListUpdateInterval  *int    `json:"list_update_interval"`
	TunnelCheckInterval *int    `json:"tunnel_check_interval"`
	LogLevel            *string `json:"log_level"`
}

// apply накладывает присланные поля на текущие настройки.
func (req settingsRequest) apply(v store.Settings) store.Settings {
	if req.WGListenPort != nil {
		v.WGListenPort = *req.WGListenPort
	}
	if req.WGPool != nil {
		v.WGPool = *req.WGPool
	}
	if req.WGServerAddress != nil {
		v.WGServerAddress = *req.WGServerAddress
	}
	if req.EndpointHost != nil {
		v.EndpointHost = *req.EndpointHost
	}
	if req.ClientMTU != nil {
		v.ClientMTU = *req.ClientMTU
	}
	if req.DNSUpstream != nil {
		v.DNSUpstream = *req.DNSUpstream
	}
	if req.DNSType != nil {
		v.DNSType = *req.DNSType
	}
	if req.WANInterface != nil {
		v.WANInterface = *req.WANInterface
	}
	if req.ListUpdateInterval != nil {
		v.ListUpdateInterval = time.Duration(*req.ListUpdateInterval) * time.Second
	}
	if req.TunnelCheckInterval != nil {
		v.TunnelCheckInterval = time.Duration(*req.TunnelCheckInterval) * time.Second
	}
	if req.LogLevel != nil {
		v.LogLevel = *req.LogLevel
	}
	return v
}

// handleSettings — `GET /api/settings`. Хеш пароля панели сюда не попадает:
// его в store.Settings нет вовсе.
func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.store.Settings(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки не найдены")
		return
	}
	key, err := s.serverPublicKey(r)
	if err != nil {
		s.log.Error("публичный ключ сервера", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}
	writeJSON(w, s.log, http.StatusOK, newSettingsResponse(v, key))
}

// handleUpdateSettings — `PATCH /api/settings`.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	current, err := s.store.Settings(r.Context())
	if err != nil {
		s.storeError(w, err, "Настройки не найдены")
		return
	}

	next := req.apply(current)
	if err := s.store.SaveSettings(r.Context(), next); err != nil {
		s.storeError(w, err, "Настройки не найдены")
		return
	}
	key, err := s.serverPublicKey(r)
	if err != nil {
		s.log.Error("публичный ключ сервера", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}

	out := newSettingsResponse(next, key)
	out.RequiresClientReconfig = requiresClientReconfig(current, next)
	writeJSON(w, s.log, http.StatusOK, out)
}

// requiresClientReconfig — изменилось ли что-то, что записано в клиентских
// конфигах: порт и адрес подключения, MTU, пул адресов и адрес DNS-сервера.
func requiresClientReconfig(before, after store.Settings) bool {
	return before.WGListenPort != after.WGListenPort ||
		before.WGPool != after.WGPool ||
		before.WGServerAddress != after.WGServerAddress ||
		before.EndpointHost != after.EndpointHost ||
		before.ClientMTU != after.ClientMTU
}
