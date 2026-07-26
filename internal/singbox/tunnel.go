package singbox

import (
	"encoding/json"
	"fmt"
	"log/slog"

	C "github.com/sagernet/sing-box/constant"
	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// singboxTypes — соответствие типов туннеля протоколам sing-box. Тип raw сюда не
// входит: там протокол берётся из самого конфига.
var singboxTypes = map[store.TunnelType]string{
	store.TunnelWireGuard:   C.TypeWireGuard,
	store.TunnelVLESS:       C.TypeVLESS,
	store.TunnelShadowsocks: C.TypeShadowsocks,
	store.TunnelTrojan:      C.TypeTrojan,
	store.TunnelHysteria2:   C.TypeHysteria2,
	store.TunnelSOCKS:       C.TypeSOCKS,
}

// buildTunnels раскладывает включённые туннели по endpoints и outbounds.
//
// WireGuard уходит в endpoints и работает в userspace (ADR 0002): никаких
// kernel-интерфейсов wg1/wg2 и bind_interface. Остальные протоколы — обычные
// outbounds. Туннель-пул разворачивается в группу: N vless-outbound'ов плюс
// `urltest` под тегом туннеля (ADR 0010).
//
// Третье значение — идентификаторы туннелей, которые в конфиг не попали, хотя
// включены: пока это только пул без пригодных серверов. Правила на них
// пропускаются так же, как правила на выключенный туннель, иначе они ссылались бы
// на несуществующий тег и `sing-box check` отверг бы конфиг целиком.
func buildTunnels(list []store.Tunnel, log *slog.Logger) (
	[]option.Endpoint, []option.Outbound, map[string]bool, error,
) {
	var (
		endpoints []option.Endpoint
		outbounds []option.Outbound
	)
	skipped := make(map[string]bool)
	for _, t := range list {
		if !t.Enabled {
			continue
		}
		if t.Source == store.SourcePool {
			group, ok := buildPool(t, log)
			if !ok {
				skipped[t.ID] = true
				continue
			}
			outbounds = append(outbounds, group...)
			continue
		}
		typ, opts, err := tunnelOptions(t)
		if err != nil {
			return nil, nil, nil, err
		}
		if typ == C.TypeWireGuard {
			endpoints = append(endpoints, option.Endpoint{
				Type: typ, Tag: TunnelTag(t.ID), Options: opts,
			})
			continue
		}
		outbounds = append(outbounds, option.Outbound{
			Type: typ, Tag: TunnelTag(t.ID), Options: opts,
		})
	}
	return endpoints, outbounds, skipped, nil
}

// tunnelOptions отдаёт протокол sing-box и тело конфига туннеля.
//
// Поля type и tag выбрасываются: их выставляет генератор, а совпадение ключей при
// слиянии объектов перебило бы наш тег чужим.
func tunnelOptions(t store.Tunnel) (string, json.RawMessage, error) {
	if len(t.Parsed) == 0 {
		return "", nil, fmt.Errorf("туннель %q не разобран: пустой parsed", t.Name)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(t.Parsed, &fields); err != nil {
		return "", nil, fmt.Errorf("разбор конфига туннеля %q: %w", t.Name, err)
	}

	typ, err := singboxType(t, fields)
	if err != nil {
		return "", nil, err
	}
	delete(fields, "type")
	delete(fields, "tag")

	body, err := json.Marshal(fields)
	if err != nil {
		return "", nil, fmt.Errorf("сборка конфига туннеля %q: %w", t.Name, err)
	}
	return typ, body, nil
}

// singboxType выбирает протокол sing-box по типу туннеля, а для raw — по полю
// type в конфиге, который пользователь вставил как есть.
func singboxType(t store.Tunnel, fields map[string]json.RawMessage) (string, error) {
	if t.Type != store.TunnelRaw {
		typ, ok := singboxTypes[t.Type]
		if !ok {
			return "", fmt.Errorf("туннель %q: неизвестный тип %q", t.Name, t.Type)
		}
		return typ, nil
	}
	rawType, ok := fields["type"]
	if !ok {
		return "", fmt.Errorf("туннель %q: в конфиге не задан type", t.Name)
	}
	var typ string
	if err := json.Unmarshal(rawType, &typ); err != nil {
		return "", fmt.Errorf("туннель %q: поле type не строка: %w", t.Name, err)
	}
	if typ == "" {
		return "", fmt.Errorf("туннель %q: в конфиге пустой type", t.Name)
	}
	return typ, nil
}
