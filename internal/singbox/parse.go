// Package singbox отвечает за конфиг sing-box: разбор пользовательского ввода в
// структуры option, генерацию конфига из состояния БД, проверку и перезапуск.
package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/sagernet/sing-box/option"

	"github.com/ArghTeam/razdacha/internal/store"
)

// ErrParse — общая обёртка ошибок разбора. Сообщения адресованы пользователю и
// показываются в UI как есть, поэтому они на русском.
var ErrParse = errors.New("не удалось разобрать конфиг туннеля")

// ParseResult — итог разбора одной пользовательской строки. Ложится в поля туннеля
// (см. docs/02-data-model.md): Type, Source и Parsed.
//
// Заполнено ровно одно из Outbound и Endpoint: WireGuard по [ADR 0002] живёт в секции
// endpoints (userspace), все остальные протоколы — обычные outbound'ы.
//
// [ADR 0002]: docs/decisions/0002-userspace-wireguard-outbound.md
type ParseResult struct {
	// Type — протокол туннеля, как он записывается в БД.
	Type store.TunnelType
	// Source — в каком виде пользователь ввёл конфиг.
	Source store.TunnelSource
	// Name — имя, предложенное самим конфигом (фрагмент URL). Может быть пустым.
	Name string
	// Outbound — разобранный outbound sing-box; nil для WireGuard.
	Outbound *option.Outbound
	// Endpoint — разобранный endpoint sing-box; заполнен только для WireGuard.
	Endpoint *option.Endpoint
	// Parsed — JSON без тега, готовый к вклейке в конфиг. Тег проставляет генератор.
	Parsed json.RawMessage
}

// Parse определяет форму пользовательского ввода и разбирает его.
//
// Распознаются три формы: URL с известной схемой, INI-файл WireGuard с секцией
// [Interface] и произвольный JSON-объект.
func Parse(raw string) (ParseResult, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ParseResult{}, fmt.Errorf("%w: конфиг пустой", ErrParse)
	}

	switch {
	case strings.HasPrefix(s, "{"):
		return parseJSON(s)
	case hasInterfaceSection(s):
		return parseWireGuardConf(s)
	case schemeOf(s) != "":
		return parseProxyURL(s)
	default:
		return ParseResult{}, fmt.Errorf(
			"%w: не похоже ни на ссылку proxy, ни на .conf WireGuard, ни на JSON", ErrParse)
	}
}

// parseJSON принимает готовый фрагмент конфига sing-box и вставляет его как есть —
// это запасной путь для протоколов, у которых нет своего парсера.
func parseJSON(s string) (ParseResult, error) {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(s), &probe); err != nil {
		return ParseResult{}, fmt.Errorf("%w: JSON не разбирается: %w", ErrParse, err)
	}
	if probe.Type == "" {
		return ParseResult{}, fmt.Errorf("%w: в JSON нет поля \"type\"", ErrParse)
	}

	// json.Compact заодно нормализует отступы, чтобы в БД не попадал ввод «как в буфере».
	compact, err := compactJSON(s)
	if err != nil {
		return ParseResult{}, err
	}

	return ParseResult{
		Type:   tunnelTypeByJSONType(probe.Type),
		Source: store.SourceJSON,
		Parsed: compact,
	}, nil
}

// tunnelTypeByJSONType переводит поле "type" фрагмента sing-box в тип туннеля.
// Всё, чего нет в модели данных, считается «raw»: конфиг всё равно вставляется как есть.
func tunnelTypeByJSONType(t string) store.TunnelType {
	switch t {
	case "vless":
		return store.TunnelVLESS
	case "shadowsocks":
		return store.TunnelShadowsocks
	case "trojan":
		return store.TunnelTrojan
	case "hysteria2":
		return store.TunnelHysteria2
	case "socks":
		return store.TunnelSOCKS
	case "wireguard":
		return store.TunnelWireGuard
	default:
		return store.TunnelRaw
	}
}

// compactJSON убирает лишние пробелы, сохраняя порядок ключей.
func compactJSON(s string) (json.RawMessage, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, []byte(s)); err != nil {
		return nil, fmt.Errorf("%w: JSON не разбирается: %w", ErrParse, err)
	}
	return json.RawMessage(buf.Bytes()), nil
}

// marshalOutbound сериализует outbound. option.Outbound складывает общие поля с
// протокольными только через MarshalJSONContext, обычный json.Marshal теряет Options.
func marshalOutbound(o *option.Outbound) (json.RawMessage, error) {
	b, err := o.MarshalJSONContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("%w: сборка outbound %q: %w", ErrParse, o.Type, err)
	}
	return b, nil
}

// marshalEndpoint сериализует endpoint — по тем же причинам, что и marshalOutbound.
func marshalEndpoint(e *option.Endpoint) (json.RawMessage, error) {
	b, err := e.MarshalJSONContext(context.Background())
	if err != nil {
		return nil, fmt.Errorf("%w: сборка endpoint %q: %w", ErrParse, e.Type, err)
	}
	return b, nil
}
