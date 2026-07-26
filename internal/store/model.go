package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// TunnelType — протокол исходящего канала, см. docs/02-data-model.md.
type TunnelType string

// Поддерживаемые протоколы туннелей.
const (
	TunnelWireGuard   TunnelType = "wireguard"
	TunnelVLESS       TunnelType = "vless"
	TunnelShadowsocks TunnelType = "shadowsocks"
	TunnelTrojan      TunnelType = "trojan"
	TunnelHysteria2   TunnelType = "hysteria2"
	TunnelSOCKS       TunnelType = "socks"
	TunnelRaw         TunnelType = "raw"
)

// TunnelSource — в каком виде пользователь ввёл конфиг туннеля.
type TunnelSource string

// Формы ввода конфига туннеля.
const (
	SourceURL    TunnelSource = "url"
	SourceWGConf TunnelSource = "wg_conf"
	SourceJSON   TunnelSource = "json"
	// SourcePool — туннель-пул: в Raw лежит URL каталога ключей, а серверы
	// снимаются с него по расписанию (ADR 0010).
	SourcePool TunnelSource = "pool"
)

// RuleAction — что делать с трафиком, попавшим под правило.
type RuleAction string

// Действия правила маршрутизации.
const (
	ActionTunnel RuleAction = "tunnel"
	ActionDirect RuleAction = "direct"
	ActionBlock  RuleAction = "block"
)

// PeerScope — на кого распространяется правило.
type PeerScope string

// Область действия правила по пирам.
const (
	ScopeAll      PeerScope = "all"
	ScopeSelected PeerScope = "selected"
)

// PoolServer — один сервер туннеля-пула, снятый с карточки каталога. Ссылка
// разбирается на слое singbox при генерации конфига, здесь она хранится как есть;
// остальные поля нужны отбору по пингу и панели.
type PoolServer struct {
	URL     string `json:"url"`
	Country string `json:"country,omitempty"`
	Title   string `json:"title,omitempty"`
	PingMS  int    `json:"ping_ms,omitempty"`
}

// Tunnel — исходящий канал: один тег в конфиге sing-box, на который ссылаются
// правила. За тегом стоит outbound, endpoint либо группа urltest — последнее у
// туннеля-пула (ADR 0010).
type Tunnel struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Type      TunnelType      `json:"type"`
	Source    TunnelSource    `json:"source"`
	Raw       string          `json:"raw"`
	Parsed    json.RawMessage `json:"parsed"`
	Enabled   bool            `json:"enabled"`
	CreatedAt time.Time       `json:"created_at"`

	// Pool — серверы пула; заполнен только при Source = SourcePool.
	Pool []PoolServer `json:"pool,omitempty"`
	// PoolUpdatedAt — когда каталог обходили в последний раз. Нулевое время
	// означает, что обхода ещё не было и пул пока пуст.
	PoolUpdatedAt time.Time `json:"pool_updated_at,omitempty"`

	// Builtin — запись завёл демон, а не пользователь ([Store.EnsureBuiltinPool]).
	// Такую не удаляют, а выключают: см. [Store.DeleteTunnel]. Ставится один раз
	// при заведении, [Store.UpdateTunnel] его не трогает.
	Builtin bool `json:"builtin"`
}

// Rule — правило маршрутизации: «эти ресурсы — в этот туннель».
// Priority выставляет сам слой хранения, вручную его не задают: порядок меняется
// через [Store.ReorderRules].
type Rule struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	Action         RuleAction `json:"action"`
	TunnelID       string     `json:"tunnel_id,omitempty"`
	Priority       int        `json:"priority"`
	Enabled        bool       `json:"enabled"`
	CommunityLists []string   `json:"community_lists"`
	Domains        []string   `json:"domains"`
	Subnets        []string   `json:"subnets"`
	RemoteLists    []string   `json:"remote_lists"`
	PeerScope      PeerScope  `json:"peer_scope"`
	PeerIDs        []string   `json:"peer_ids"`
	ResolveRealIP  bool       `json:"resolve_real_ip"`
}

// Peer — клиентское устройство. Приватный и pre-shared ключи хранятся, чтобы конфиг
// можно было перевыдать; отсюда права 0600 на файл БД.
type Peer struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	PublicKey    string    `json:"public_key"`
	PrivateKey   string    `json:"private_key"`
	PresharedKey string    `json:"preshared_key"`
	Address      string    `json:"address"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"created_at"`
}

// Snapshot — полное состояние демона на один момент времени. Конфиг sing-box
// генерируется целиком из него, поэтому снимок читается в одной транзакции.
type Snapshot struct {
	Settings Settings `json:"settings"`
	Tunnels  []Tunnel `json:"tunnels"`
	Rules    []Rule   `json:"rules"`
	Peers    []Peer   `json:"peers"`
}

// validate проверяет туннель до записи в БД.
func (t *Tunnel) validate() error {
	if t.Name == "" {
		return fmt.Errorf("%w: у туннеля пустое имя", ErrInvalid)
	}
	switch t.Type {
	case TunnelWireGuard, TunnelVLESS, TunnelShadowsocks, TunnelTrojan,
		TunnelHysteria2, TunnelSOCKS, TunnelRaw:
	default:
		return fmt.Errorf("%w: неизвестный тип туннеля %q", ErrInvalid, t.Type)
	}
	switch t.Source {
	case SourcePool:
		// Каталог разбирается только для vless: разборщик страницы снимает
		// именно эти ключи, и участники группы собираются как vless-outbound'ы.
		if t.Type != TunnelVLESS {
			return fmt.Errorf("%w: туннель-пул %q бывает только типа vless, а не %q",
				ErrInvalid, t.Name, t.Type)
		}
		if len(t.Parsed) > 0 {
			return fmt.Errorf("%w: у туннеля-пула %q нет своего конфига, серверы берутся из каталога",
				ErrInvalid, t.Name)
		}
		for _, s := range t.Pool {
			if s.URL == "" {
				return fmt.Errorf("%w: у сервера пула %q пустая ссылка", ErrInvalid, t.Name)
			}
		}
	case SourceURL, SourceWGConf, SourceJSON:
		if len(t.Pool) > 0 {
			return fmt.Errorf("%w: у туннеля %q задан список серверов, но он не пул",
				ErrInvalid, t.Name)
		}
	default:
		return fmt.Errorf("%w: неизвестная форма конфига %q", ErrInvalid, t.Source)
	}
	// Встроенная разновидность пока одна — пул бесплатных ключей. У остальных
	// форм флаг означал бы неудаляемый туннель, которого никто не заводил.
	if t.Builtin && t.Source != SourcePool {
		return fmt.Errorf("%w: встроенным бывает только туннель-пул, а не форма %q",
			ErrInvalid, t.Source)
	}
	if t.Raw == "" {
		return fmt.Errorf("%w: у туннеля %q пустой конфиг", ErrInvalid, t.Name)
	}
	if len(t.Parsed) > 0 && !json.Valid(t.Parsed) {
		return fmt.Errorf("%w: разобранный конфиг туннеля %q — не JSON", ErrInvalid, t.Name)
	}
	return nil
}

// validate проверяет правило до записи в БД.
func (r *Rule) validate() error {
	if r.Name == "" {
		return fmt.Errorf("%w: у правила пустое имя", ErrInvalid)
	}
	switch r.Action {
	case ActionTunnel:
		if r.TunnelID == "" {
			return fmt.Errorf("%w: правило %q с действием «tunnel» без туннеля", ErrInvalid, r.Name)
		}
	case ActionDirect, ActionBlock:
		if r.TunnelID != "" {
			return fmt.Errorf("%w: правило %q с действием %q не может ссылаться на туннель",
				ErrInvalid, r.Name, r.Action)
		}
	default:
		return fmt.Errorf("%w: неизвестное действие правила %q", ErrInvalid, r.Action)
	}
	switch r.PeerScope {
	case ScopeAll:
		if len(r.PeerIDs) > 0 {
			return fmt.Errorf("%w: правило %q действует на всех пиров, список пиров лишний",
				ErrInvalid, r.Name)
		}
	case ScopeSelected:
		if len(r.PeerIDs) == 0 {
			return fmt.Errorf("%w: у правила %q выбраны отдельные пиры, но список пуст",
				ErrInvalid, r.Name)
		}
	default:
		return fmt.Errorf("%w: неизвестная область действия правила %q", ErrInvalid, r.PeerScope)
	}
	return nil
}

// validate проверяет пира до записи в БД.
func (p *Peer) validate() error {
	if p.Name == "" {
		return fmt.Errorf("%w: у пира пустое имя", ErrInvalid)
	}
	if p.PublicKey == "" || p.PrivateKey == "" || p.PresharedKey == "" {
		return fmt.Errorf("%w: у пира %q заполнены не все ключи", ErrInvalid, p.Name)
	}
	if p.Address == "" {
		return fmt.Errorf("%w: у пира %q не задан адрес", ErrInvalid, p.Name)
	}
	return nil
}

// newID выдаёт идентификатор новой записи.
func newID() string { return uuid.NewString() }

// jsonList сериализует список строк для колонки TEXT. nil хранится как пустой список,
// а не как NULL: колонки объявлены NOT NULL DEFAULT '[]'.
func jsonList(v []string) (string, error) {
	if v == nil {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("сериализация списка: %w", err)
	}
	return string(b), nil
}

// parseList разбирает колонку TEXT со списком строк.
func parseList(s string, dst *[]string) error {
	if s == "" {
		*dst = nil
		return nil
	}
	if err := json.Unmarshal([]byte(s), dst); err != nil {
		return fmt.Errorf("разбор списка %q: %w", s, err)
	}
	return nil
}
