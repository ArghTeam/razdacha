package store

import (
	"encoding/json"
	"fmt"
	"slices"
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
	// SourceWARP — WireGuard-туннель Cloudflare WARP. Протокол тот же, что у
	// SourceWGConf, меняется только происхождение ключей: их выдал Cloudflare
	// по запросу демона либо пользователь вставил готовый .conf, у которого
	// endpoint на cloudflareclient.com.
	//
	// Признак хранится, а не выводится из содержимого конфига: он решает, годится
	// ли туннель вторым звеном цепи (ADR 0012), и вывод по ключам, диапазонам или
	// MTU был бы угадыванием чужого формата.
	SourceWARP TunnelSource = "warp"
)

// RuleAction — что делать с трафиком, попавшим под правило.
type RuleAction string

// Действия правила маршрутизации.
const (
	ActionTunnel RuleAction = "tunnel"
	ActionDirect RuleAction = "direct"
	ActionBlock  RuleAction = "block"
)

// RuleActions — все действия правила, какие бывают.
//
// Список ведётся одним местом и им же проверяется ввод (`Rule.validate`):
// обход всех действий в тестах должен ломаться при добавлении нового, иначе
// новое действие тихо остаётся непроверенным. Ровно на этом разъехались отбор
// подсетей для nft-сета и отбор списков в слое lists (issue #126).
func RuleActions() []RuleAction {
	return []RuleAction{ActionTunnel, ActionDirect, ActionBlock}
}

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
//
// Порядок серверов в [Tunnel.Pool] значим: это приоритет отбора в конфиг. Ведёт его
// слой lists при обходе каталога, слой singbox читает как есть (ADR 0010).
type PoolServer struct {
	URL     string `json:"url"`
	Country string `json:"country,omitempty"`
	Title   string `json:"title,omitempty"`
	PingMS  int    `json:"ping_ms,omitempty"`
	// Misses — сколько обходов каталога подряд ссылка не попадалась. Нужно, чтобы
	// карточка, отданная сайтом без ссылки на одном запросе, не считалась
	// исчезнувшим сервером: за такой ошибкой следует смена состава конфига и
	// перезапуск sing-box. Ноль у всех присутствующих в последнем обходе.
	Misses int `json:"misses,omitempty"`
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
	ID       string     `json:"id"`
	Name     string     `json:"name"`
	Action   RuleAction `json:"action"`
	TunnelID string     `json:"tunnel_id,omitempty"`
	// ViaTunnelID — второе звено цепи: туннель, которым трафик выходит наружу
	// после первого (ADR 0012). Пусто — цепи нет, и правило работает ровно как
	// раньше, одним туннелем. Годится только туннель с Source = SourceWARP, но
	// проверяет это слой api: правилу одному туннели не видны.
	ViaTunnelID    string    `json:"via_tunnel_id,omitempty"`
	Priority       int       `json:"priority"`
	Enabled        bool      `json:"enabled"`
	CommunityLists []string  `json:"community_lists"`
	Domains        []string  `json:"domains"`
	Subnets        []string  `json:"subnets"`
	RemoteLists    []string  `json:"remote_lists"`
	PeerScope      PeerScope `json:"peer_scope"`
	PeerIDs        []string  `json:"peer_ids"`
	ResolveRealIP  bool      `json:"resolve_real_ip"`
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
	case SourceWARP:
		// WARP — это происхождение ключей, а не протокол: наружу он всё тот же
		// WireGuard-endpoint в userspace (ADR 0002, ADR 0012).
		if t.Type != TunnelWireGuard {
			return fmt.Errorf("%w: туннель WARP %q бывает только типа wireguard, а не %q",
				ErrInvalid, t.Name, t.Type)
		}
		if len(t.Pool) > 0 {
			return fmt.Errorf("%w: у туннеля %q задан список серверов, но он не пул",
				ErrInvalid, t.Name)
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
	if !slices.Contains(RuleActions(), r.Action) {
		return fmt.Errorf("%w: неизвестное действие правила %q", ErrInvalid, r.Action)
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
	}
	// resolve_real_ip выключает выдачу FakeIP (docs/04-dns-fakeip.md), а без
	// FakeIP клиент идёт на настоящий адрес, и пометить этот трафик для tproxy
	// можно только совпадением с подсетью в nft-сете. Подсети приезжают в сет из
	// двух мест: свои `subnets` правила и подсети скачанных списков
	// (`cmd/razdachad/lists.go`, `nftSubnets`). Ни одного источника — маркировать
	// нечего, правило молча отдаёт свой трафик напрямую (аудит от 2026-07, пункт 8).
	//
	// Отказ, а не предупреждение: правило, которое провально не маршрутизирует
	// ничего, — это утечка, а утечки в этой системе видимы отказом (ADR 0013).
	// Довод «флаг могли поставить заранее» не выдерживает: флаг без подсетей не
	// значит ничего, порядок «сначала подсети, потом флаг» ничего не стоит, а в
	// UI флага нет вовсе — ставят его через API, где 400 с объяснением полезнее
	// молчаливого сохранения.
	if r.ResolveRealIP && r.Action == ActionTunnel &&
		len(r.Subnets) == 0 && len(r.RemoteLists) == 0 && len(r.CommunityLists) == 0 {
		return fmt.Errorf(
			"%w: правило %q резолвит настоящий IP, и маршрутизировать его нечем: "+
				"нужны подсети — свои или из списка", ErrInvalid, r.Name)
	}
	// Правило без единого условия совпадения совпадало бы со всем, поэтому
	// генератор пропускает его целиком — единственный законный пропуск,
	// оставшийся после ADR 0013. Пропущенное правило отдаёт свой трафик в
	// route.final = direct-out, то есть напрямую с адреса сервера, а в панели
	// выглядит работающим. Диагностика называет это ошибкой (#123) — сохранение
	// теперь тоже (#142).
	//
	// Проверка не зависит от действия: правилу с `block` и `direct` условия
	// нужны так же, как правилу в туннель. Выбранные пиры условием совпадения не
	// считаются: они сужают источник, а не назначение.
	//
	// Отказ только на запись: уже лежащие в БД пустые правила читаются как
	// прежде и остаются видны в панели — иначе их нельзя было бы ни починить,
	// ни удалить.
	if len(r.CommunityLists) == 0 && len(r.RemoteLists) == 0 &&
		len(r.Domains) == 0 && len(r.Subnets) == 0 {
		return fmt.Errorf(
			"%w: у правила %q нет ни одного условия совпадения: нужны списки, домены "+
				"или подсети — иначе правило совпадает со всем, в конфиг не попадает, "+
				"и его трафик уходит напрямую", ErrInvalid, r.Name)
	}
	if r.ViaTunnelID != "" && r.Action != ActionTunnel {
		return fmt.Errorf("%w: правило %q с действием %q не выходит в туннель, второму звену цепи взяться неоткуда",
			ErrInvalid, r.Name, r.Action)
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
