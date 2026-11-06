package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

type settingsPatchPublisher interface {
	PatchSettings(context.Context, *store.Store, func(store.Settings) store.Settings) (store.Settings, store.Settings, error)
}

// settingsResponse — настройки для UI.
//
// `list_update_interval` уходит в секундах, а не наносекундами time.Duration:
// наносекунды в JSON — деталь представления Go, а не контракт API.
//
// `server_public_key` — публичный ключ wg0. Хранимой настройкой он не является и
// изменению не подлежит, но клиентскому конфигу необходим, а больше его взять
// неоткуда; поле только на чтение и `null`, пока интерфейс не поднят.
type settingsResponse struct {
	RealityEnabled         bool    `json:"reality_enabled"`
	RealityListenPort      int     `json:"reality_listen_port"`
	RealityServerName      string  `json:"reality_server_name"`
	RealityHandshakeServer string  `json:"reality_handshake_server"`
	RealityHandshakePort   int     `json:"reality_handshake_port"`
	RealityPublicKey       string  `json:"reality_public_key"`
	RealityShortID         string  `json:"reality_short_id"`
	WGListenPort           int     `json:"wg_listen_port"`
	WGPool                 string  `json:"wg_pool"`
	WGServerAddress        string  `json:"wg_server_address"`
	EndpointHost           string  `json:"endpoint_host"`
	ClientMTU              int     `json:"client_mtu"`
	DNSUpstream            string  `json:"dns_upstream"`
	DNSType                string  `json:"dns_type"`
	WANInterface           string  `json:"wan_interface"`
	ListUpdateInterval     int     `json:"list_update_interval"`
	PoolUpdateInterval     int     `json:"pool_update_interval"`
	TunnelCheckInterval    int     `json:"tunnel_check_interval"`
	LogLevel               string  `json:"log_level"`
	ServerPublicKey        *string `json:"server_public_key"`

	// PoolCountryBlocklist — ISO-коды стран, ноды которых не берутся в пул
	// (ADR 0020). Всегда массив, пустой означает «не отбраковывать по стране».
	PoolCountryBlocklist []string `json:"pool_country_blocklist"`

	// RequiresClientReconfig ставится только в ответе на изменение и означает,
	// что клиентам нужно перевыдать конфиги.
	RequiresClientReconfig bool `json:"requires_client_reconfig,omitempty"`
}

func newSettingsResponse(v store.Settings, serverKey string) settingsResponse {
	out := settingsResponse{
		RealityEnabled:         v.RealityEnabled,
		RealityListenPort:      v.RealityListenPort,
		RealityServerName:      v.RealityServerName,
		RealityHandshakeServer: v.RealityHandshakeServer,
		RealityHandshakePort:   v.RealityHandshakePort,

		WGListenPort:        v.WGListenPort,
		WGPool:              v.WGPool,
		WGServerAddress:     v.WGServerAddress,
		EndpointHost:        v.EndpointHost,
		ClientMTU:           v.ClientMTU,
		DNSUpstream:         v.DNSUpstream,
		DNSType:             v.DNSType,
		WANInterface:        v.WANInterface,
		ListUpdateInterval:  int(v.ListUpdateInterval / time.Second),
		PoolUpdateInterval:  int(v.PoolUpdateInterval / time.Second),
		TunnelCheckInterval: int(v.TunnelCheckInterval / time.Second),
		LogLevel:            v.LogLevel,
		// Не nil: `null` в этом поле панель прочитала бы как «фильтра нет», а
		// пустой список и отсутствие ключа — разные вещи (ADR 0020).
		PoolCountryBlocklist: append([]string{}, v.PoolCountryBlocklist...),
	}
	if serverKey != "" {
		out.ServerPublicKey = &serverKey
	}
	return out
}

// settingsRequest — тело `PATCH /api/settings`. Указатели отличают «не прислали»
// от «прислали пустое»: обнулять MTU молчанием нельзя.
type settingsRequest struct {
	RealityEnabled         *bool   `json:"reality_enabled"`
	RealityListenPort      *int    `json:"reality_listen_port"`
	RealityServerName      *string `json:"reality_server_name"`
	RealityHandshakeServer *string `json:"reality_handshake_server"`
	RealityHandshakePort   *int    `json:"reality_handshake_port"`

	WGListenPort        *int    `json:"wg_listen_port"`
	WGPool              *string `json:"wg_pool"`
	WGServerAddress     *string `json:"wg_server_address"`
	EndpointHost        *string `json:"endpoint_host"`
	ClientMTU           *int    `json:"client_mtu"`
	DNSUpstream         *string `json:"dns_upstream"`
	DNSType             *string `json:"dns_type"`
	WANInterface        *string `json:"wan_interface"`
	ListUpdateInterval  *int    `json:"list_update_interval"`
	PoolUpdateInterval  *int    `json:"pool_update_interval"`
	TunnelCheckInterval *int    `json:"tunnel_check_interval"`
	LogLevel            *string `json:"log_level"`
	// Указатель на срез, а не срез: «не прислали» обязано отличаться от
	// «прислали пустой список», иначе любой PATCH соседнего поля снимал бы
	// чёрный список стран целиком (ADR 0020).
	PoolCountryBlocklist *[]string `json:"pool_country_blocklist"`
}

// apply накладывает присланные поля на текущие настройки.
func (req settingsRequest) apply(v store.Settings) store.Settings {
	if req.RealityEnabled != nil {
		v.RealityEnabled = *req.RealityEnabled
	}
	if req.RealityListenPort != nil {
		v.RealityListenPort = *req.RealityListenPort
	}
	if req.RealityServerName != nil {
		v.RealityServerName = *req.RealityServerName
	}
	if req.RealityHandshakeServer != nil {
		v.RealityHandshakeServer = *req.RealityHandshakeServer
	}
	if req.RealityHandshakePort != nil {
		v.RealityHandshakePort = *req.RealityHandshakePort
	}

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
	if req.PoolUpdateInterval != nil {
		v.PoolUpdateInterval = time.Duration(*req.PoolUpdateInterval) * time.Second
	}
	if req.TunnelCheckInterval != nil {
		v.TunnelCheckInterval = time.Duration(*req.TunnelCheckInterval) * time.Second
	}
	if req.LogLevel != nil {
		v.LogLevel = *req.LogLevel
	}
	if req.PoolCountryBlocklist != nil {
		v.PoolCountryBlocklist = normalizeCountryCodes(*req.PoolCountryBlocklist)
	}
	return v
}

// normalizeCountryCodes приводит присланные коды к тому виду, в каком они хранятся:
// верхний регистр, без пробелов и пустых элементов. Проверку на «две латинские буквы»
// делает store — отказ обязан приходить оттуда же, откуда и остальные (ErrInvalid).
func normalizeCountryCodes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, code := range in {
		if v := strings.ToUpper(strings.TrimSpace(code)); v != "" {
			out = append(out, v)
		}
	}
	return out
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
	out := newSettingsResponse(v, key)
	if identity, err := s.store.RealityIdentity(r.Context()); err == nil && identity != nil {
		out.RealityPublicKey = identity.PublicKey
		out.RealityShortID = identity.ShortID
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// handleUpdateSettings — `PATCH /api/settings`.
func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var req settingsRequest
	if !s.decodeBody(w, r, &req) {
		return
	}
	var current, next store.Settings
	var err error
	if publisher, ok := s.applier.(settingsPatchPublisher); ok {
		current, next, err = publisher.PatchSettings(r.Context(), s.store, req.apply)
	} else if _, ok := s.applier.(RealityPublisher); ok {
		// An older publisher cannot merge safely outside its gate.
		err = ErrNetworkGuarded
	} else {
		current, next, err = s.store.UpdateSettings(r.Context(), req.apply)
	}
	if err != nil {
		s.storeError(w, err, "Settings update failed")
		return
	}
	key, err := s.serverPublicKey(r)
	if err != nil {
		s.log.Error("публичный ключ сервера", "ошибка", err)
		writeError(w, s.log, http.StatusInternalServerError, codeInternal, "Внутренняя ошибка")
		return
	}

	out := newSettingsResponse(next, key)
	if identity, err := s.store.RealityIdentity(r.Context()); err == nil && identity != nil {
		out.RealityPublicKey = identity.PublicKey
		out.RealityShortID = identity.ShortID
	}
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
