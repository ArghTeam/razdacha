package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// querier — общее подмножество *sql.DB и *sql.Tx, чтобы читать одинаково внутри
// транзакции и вне её.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const tunnelColumns = `id, name, type, source, raw, parsed, enabled, created_at,
	pool, pool_updated_at, builtin, country`

// CreateTunnel добавляет туннель. Пустые ID и CreatedAt заполняются здесь.
func (s *Store) CreateTunnel(ctx context.Context, t Tunnel) (Tunnel, error) {
	if err := t.validate(); err != nil {
		return Tunnel{}, err
	}
	if t.ID == "" {
		t.ID = newID()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	pool, err := marshalPool(t.Pool)
	if err != nil {
		return Tunnel{}, err
	}

	_, err = s.db.ExecContext(ctx, insertTunnelSQL, tunnelArgs(t, pool)...)
	if err != nil {
		if isUniqueViolation(err) {
			return Tunnel{}, fmt.Errorf("%w: туннель с именем %q уже есть", ErrInvalid, t.Name)
		}
		return Tunnel{}, fmt.Errorf("добавление туннеля %q: %w", t.Name, err)
	}
	return t, nil
}

// insertTunnelSQL и tunnelArgs — одна вставка для [Store.CreateTunnel] и
// [Store.EnsureBuiltinPool]: второй нужна та же запись, но внутри транзакции.
const insertTunnelSQL = `INSERT INTO tunnels (` + tunnelColumns +
	`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func tunnelArgs(t Tunnel, pool string) []any {
	return []any{
		t.ID, t.Name, string(t.Type), string(t.Source), t.Raw, string(t.Parsed),
		t.Enabled, t.CreatedAt.Unix(), pool, unixOrZero(t.PoolUpdatedAt), t.Builtin,
		t.Country,
	}
}

// Tunnel возвращает туннель по идентификатору.
func (s *Store) Tunnel(ctx context.Context, id string) (Tunnel, error) {
	t, err := scanTunnel(s.db.QueryRowContext(ctx,
		`SELECT `+tunnelColumns+` FROM tunnels WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Tunnel{}, fmt.Errorf("туннель %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Tunnel{}, fmt.Errorf("чтение туннеля %s: %w", id, err)
	}
	return t, nil
}

// Tunnels возвращает все туннели в порядке создания.
func (s *Store) Tunnels(ctx context.Context) ([]Tunnel, error) {
	return tunnels(ctx, s.db)
}

func tunnels(ctx context.Context, q querier) ([]Tunnel, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+tunnelColumns+` FROM tunnels ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("чтение туннелей: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Tunnel
	for rows.Next() {
		t, err := scanTunnel(rows)
		if err != nil {
			return nil, fmt.Errorf("чтение туннелей: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение туннелей: %w", err)
	}
	return out, nil
}

// UpdateTunnel перезаписывает туннель целиком. CreatedAt, Builtin и Country не
// меняются: первое — история записи, второе — то, кем она заведена, и снимать флаг
// правкой через панель означало бы обходить запрет на удаление встроенного; третье —
// колонка, не используемая с ADR 0018 и остающаяся пустой. Все три в списке SET ниже
// отсутствуют намеренно и потому переживают обновление.
func (s *Store) UpdateTunnel(ctx context.Context, t Tunnel) error {
	if err := t.validate(); err != nil {
		return err
	}
	if t.ID == "" {
		return fmt.Errorf("%w: у обновляемого туннеля пустой идентификатор", ErrInvalid)
	}
	pool, err := marshalPool(t.Pool)
	if err != nil {
		return err
	}

	res, err := s.db.ExecContext(ctx,
		`UPDATE tunnels SET name = ?, type = ?, source = ?, raw = ?, parsed = ?, enabled = ?,
		 pool = ?, pool_updated_at = ?
		 WHERE id = ?`,
		t.Name, string(t.Type), string(t.Source), t.Raw, string(t.Parsed), t.Enabled,
		pool, unixOrZero(t.PoolUpdatedAt), t.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("%w: туннель с именем %q уже есть", ErrInvalid, t.Name)
		}
		return fmt.Errorf("обновление туннеля %s: %w", t.ID, err)
	}
	return checkAffected(res, fmt.Sprintf("туннель %s", t.ID))
}

// DeleteTunnel удаляет туннель. Если на него ссылается правило, удаление отклоняется:
// иначе маршрутизация ломается молча (ON DELETE RESTRICT в схеме — та же защита уровнем
// ниже, здесь она превращается во внятное сообщение).
//
// Встроенную запись удалить нельзя вовсе: её заводит демон, и на следующем старте она
// появилась бы снова. Такую выключают.
func (s *Store) DeleteTunnel(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		var builtin bool
		err := tx.QueryRowContext(ctx, `SELECT builtin FROM tunnels WHERE id = ?`, id).Scan(&builtin)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("туннель %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("чтение туннеля %s: %w", id, err)
		}
		if builtin {
			return fmt.Errorf("%w: встроенный пул бесплатных ключей не удаляется — его можно выключить",
				ErrInUse)
		}

		names, err := ruleNamesByTunnel(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(names) > 0 {
			return fmt.Errorf("%w: на туннель ссылаются правила: %s — сначала измените или удалите их",
				ErrInUse, strings.Join(quoteAll(names), ", "))
		}

		res, err := tx.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("удаление туннеля %s: %w", id, err)
		}
		return checkAffected(res, fmt.Sprintf("туннель %s", id))
	})
}

// builtinPoolName — имя встроенного общего пула (ADR 0018). Видно пользователю,
// поэтому по-русски; уникально — колонка tunnels.name под UNIQUE.
const builtinPoolName = "Общий пул"

// BuiltinPoolResult — что сделал [Store.EnsureBuiltinPool], для лога демона.
// Заведение пула, сворачивание лишних встроенных пулов и отвязку их правил разводим
// по полям: снаружи правку БД ничем иначе не увидеть, а молча удалять чужие ссылки
// нельзя.
type BuiltinPoolResult struct {
	// Created — завели ли встроенный пул прямо сейчас. false на повторном старте.
	Created bool
	// RemovedExtra — имена свёрнутых лишних встроенных пулов (семь страновых после
	// апгрейда с ADR 0017; обычно пусто).
	RemovedExtra []string
	// DetachedRules — правила, которые ссылались на свёрнутый пул и потому отвязаны.
	DetachedRules []DetachedRule
}

// DetachedRule — правило, отвязанное от свёрнутого встроенного пула.
type DetachedRule struct {
	// Name — имя правила для лога.
	Name string
	// Disabled — правило ссылалось на пул первым (основным) звеном, поэтому вместе
	// с обнулением ссылки выключено: правило с action=tunnel и пустым туннелем
	// генератор конфига считает повреждением состояния и роняет всю генерацию, а
	// выключенное — просто пропускает. Если false, отвязано лишь второе звено цепи
	// (via_tunnel_id), а само правило осталось включённым и маршрутизирует первым.
	Disabled bool
}

// EnsureBuiltinPool приводит БД к состоянию «ровно один встроенный пул» (ADR 0018):
// source=pool, builtin=1, выключенным (свежая установка не должна сама пойти на
// чужой сайт за ключами — включение через PATCH), каталог — catalogURL.
//
// Идемпотентно по (builtin, source=pool): встроенный пул, который уже есть, не
// трогается и не дублируется — признак флаг, а не имя (пользователь переименовывает)
// и не каталог (адрес меняется между релизами).
//
// Апгрейд с версии со странами (ADR 0017) сворачивает семь страновых пулов в один:
// первый по порядку заведения оставляется, остальные удаляются. Удаление молчаливым
// быть не может: на пул могли ссылаться правила, а схема держит их `ON DELETE
// RESTRICT`. Поэтому сначала правила отвязываются (ссылки в NULL, правило первого
// звена ещё и выключается) по ADR 0013, о каждом сообщается в [BuiltinPoolResult], и
// только потом пул удаляется — всё в одной транзакции, чтобы прерывание не оставило
// пул без правил или наоборот. Выживший пул нормализуется: имя — [builtinPoolName],
// колонка country очищается (наследие ADR 0017, с 0018 не используется).
//
// Тип пула косметичен (участники группы urltest разбираются каждый по своей ссылке,
// ADR 0010/0015) и передаётся, а не берётся константой.
func (s *Store) EnsureBuiltinPool(ctx context.Context, catalogURL string, typ TunnelType,
) (BuiltinPoolResult, error) {
	if catalogURL == "" {
		return BuiltinPoolResult{}, fmt.Errorf("%w: пустой адрес каталога для встроенного пула", ErrInvalid)
	}
	var out BuiltinPoolResult
	err := s.tx(ctx, func(tx *sql.Tx) error {
		pools, err := builtinPools(ctx, tx)
		if err != nil {
			return err
		}
		if len(pools) == 0 {
			t := Tunnel{
				ID:        newID(),
				Name:      builtinPoolName,
				Type:      typ,
				Source:    SourcePool,
				Raw:       catalogURL,
				Enabled:   false,
				Builtin:   true,
				CreatedAt: time.Now(),
			}
			if err := t.validate(); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, insertTunnelSQL, tunnelArgs(t, "[]")...); err != nil {
				if isUniqueViolation(err) {
					return fmt.Errorf("%w: имя %q занято другим туннелем — освободите его, "+
						"чтобы демон завёл встроенный пул", ErrInvalid, t.Name)
				}
				return fmt.Errorf("заведение встроенного пула %q: %w", t.Name, err)
			}
			out.Created = true
			return nil
		}

		return collapseBuiltinPools(ctx, tx, pools, &out)
	})
	if err != nil {
		return BuiltinPoolResult{}, err
	}
	return out, nil
}

// builtinPools отдаёт встроенные пулы в порядке заведения. Порядок значим:
// [collapseBuiltinPools] оставляет первый, а остальные сворачивает.
func builtinPools(ctx context.Context, tx *sql.Tx) ([]Tunnel, error) {
	all, err := tunnels(ctx, tx)
	if err != nil {
		return nil, err
	}
	var pools []Tunnel
	for _, t := range all {
		if t.Builtin && t.Source == SourcePool {
			pools = append(pools, t)
		}
	}
	return pools, nil
}

// collapseBuiltinPools сворачивает набор встроенных пулов в один: первый остаётся,
// остальные удаляются с отвязкой правил по ADR 0013. Выживший нормализуется —
// пустеет колонка country, имя приводится к [builtinPoolName], если оно свободно.
// Работает внутри транзакции [Store.EnsureBuiltinPool].
func collapseBuiltinPools(ctx context.Context, tx *sql.Tx, pools []Tunnel, out *BuiltinPoolResult) error {
	survivor := pools[0]
	for _, t := range pools[1:] {
		if err := detachRulesFromTunnel(ctx, tx, t.ID, out); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tunnels WHERE id = ?`, t.ID); err != nil {
			return fmt.Errorf("сворачивание встроенного пула %q: %w", t.Name, err)
		}
		out.RemovedExtra = append(out.RemovedExtra, t.Name)
	}
	if survivor.Country != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE tunnels SET country = '' WHERE id = ?`, survivor.ID); err != nil {
			return fmt.Errorf("очистка страны встроенного пула %q: %w", survivor.Name, err)
		}
	}
	// Переименовываем только при реальном сворачивании (были лишние пулы): иначе
	// единственный пул, который пользователь переименовал сам, на каждом старте
	// возвращал бы имя [builtinPoolName]. Имя занято туннелем пользователя —
	// оставляем прежнее: признак пула флаг, а не имя, идемпотентности расхождение не
	// мешает.
	if len(out.RemovedExtra) > 0 && survivor.Name != builtinPoolName {
		if _, err := tx.ExecContext(ctx, `UPDATE tunnels SET name = ? WHERE id = ?`,
			builtinPoolName, survivor.ID); err != nil && !isUniqueViolation(err) {
			return fmt.Errorf("переименование встроенного пула %q: %w", survivor.Name, err)
		}
	}
	return nil
}

// detachRulesFromTunnel обнуляет ссылки правил на туннель, обходя `ON DELETE
// RESTRICT`: без этого удалить пул нельзя. Правило, ссылавшееся первым звеном,
// выключается — иначе оно осталось бы action=tunnel с пустым туннелем и уронило бы
// генерацию конфига (ADR 0013 закрывает недоступный туннель отказом, но пустой
// tunnel_id для генератора — не «недоступен», а «состояние повреждено»). Правило,
// ссылавшееся лишь вторым звеном цепи, теряет цепь, но остаётся включённым и
// маршрутизирует первым звеном.
func detachRulesFromTunnel(ctx context.Context, tx *sql.Tx, tunnelID string, out *BuiltinPoolResult) error {
	// Сбор ссылок и обнуление разнесены намеренно: курсор чтения обязан закрыться
	// до UPDATE'ов — SQLite не пишет, пока по той же транзакции открыт read-курсор.
	// Поэтому чтение живёт в отдельной функции с `defer rows.Close()`, а изменения
	// идут уже по собранному срезу.
	refs, err := ruleRefsForTunnel(ctx, tx, tunnelID)
	if err != nil {
		return err
	}

	for _, r := range refs {
		if r.primary {
			// Первое звено: обнуляем ссылку (и цепь, если она вела на тот же пул) и
			// выключаем правило.
			if _, err := tx.ExecContext(ctx,
				`UPDATE rules SET tunnel_id = NULL, enabled = 0,
				 via_tunnel_id = CASE WHEN via_tunnel_id = ? THEN NULL ELSE via_tunnel_id END
				 WHERE id = ?`, tunnelID, r.id); err != nil {
				return fmt.Errorf("отвязка правила %q от пула: %w", r.name, err)
			}
			out.DetachedRules = append(out.DetachedRules, DetachedRule{Name: r.name, Disabled: true})
			continue
		}
		// Только второе звено: цепь оборвалась, правило остаётся первым звеном.
		if _, err := tx.ExecContext(ctx,
			`UPDATE rules SET via_tunnel_id = NULL WHERE id = ?`, r.id); err != nil {
			return fmt.Errorf("отвязка второго звена правила %q: %w", r.name, err)
		}
		out.DetachedRules = append(out.DetachedRules, DetachedRule{Name: r.name, Disabled: false})
	}
	return nil
}

// ruleRef — ссылка правила на туннель: каким звеном оно за него держится.
type ruleRef struct {
	id, name string
	// primary — правило ссылается первым звеном (tunnel_id). Иначе оно держится
	// только вторым звеном цепи (via_tunnel_id).
	primary bool
}

// ruleRefsForTunnel собирает правила, ссылающиеся на туннель любым звеном, и
// закрывает курсор до возврата: вызывающий пишет по той же транзакции, а SQLite
// не допускает запись при открытом read-курсоре.
func ruleRefsForTunnel(ctx context.Context, tx *sql.Tx, tunnelID string) ([]ruleRef, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, name, tunnel_id FROM rules
		 WHERE tunnel_id = ? OR via_tunnel_id = ? ORDER BY priority`,
		tunnelID, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("поиск правил пула %s: %w", tunnelID, err)
	}
	defer func() { _ = rows.Close() }()

	var refs []ruleRef
	for rows.Next() {
		var (
			id, name string
			tid      sql.NullString
		)
		if err := rows.Scan(&id, &name, &tid); err != nil {
			return nil, fmt.Errorf("поиск правил пула %s: %w", tunnelID, err)
		}
		refs = append(refs, ruleRef{
			id: id, name: name,
			primary: tid.Valid && tid.String == tunnelID,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("поиск правил пула %s: %w", tunnelID, err)
	}
	return refs, nil
}

// ruleNamesByTunnel отдаёт имена правил, ссылающихся на туннель — любым из двух
// звеньев цепи (ADR 0012): вторым звеном туннель держится так же, как первым.
func ruleNamesByTunnel(ctx context.Context, q querier, tunnelID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name FROM rules WHERE tunnel_id = ? OR via_tunnel_id = ? ORDER BY priority`,
		tunnelID, tunnelID)
	if err != nil {
		return nil, fmt.Errorf("поиск правил туннеля %s: %w", tunnelID, err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("поиск правил туннеля %s: %w", tunnelID, err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("поиск правил туннеля %s: %w", tunnelID, err)
	}
	return names, nil
}

// scanner — общее подмножество *sql.Row и *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanTunnel(sc scanner) (Tunnel, error) {
	var (
		t          Tunnel
		typ, src   string
		parsed     string
		pool       string
		createdAt  int64
		poolUpdate int64
	)
	if err := sc.Scan(&t.ID, &t.Name, &typ, &src, &t.Raw, &parsed, &t.Enabled, &createdAt,
		&pool, &poolUpdate, &t.Builtin, &t.Country); err != nil {
		return Tunnel{}, err
	}
	t.Type = TunnelType(typ)
	t.Source = TunnelSource(src)
	if parsed != "" {
		t.Parsed = []byte(parsed)
	}
	if err := parsePool(pool, &t.Pool); err != nil {
		return Tunnel{}, err
	}
	t.CreatedAt = time.Unix(createdAt, 0).UTC()
	if poolUpdate != 0 {
		t.PoolUpdatedAt = time.Unix(poolUpdate, 0).UTC()
	}
	return t, nil
}

// UpdateTunnelPool записывает свежий состав пула, не трогая остального туннеля:
// расписание обновления каталога знает только про серверы, а имя, тип и enabled
// правит пользователь через панель.
func (s *Store) UpdateTunnelPool(ctx context.Context, id string, servers []PoolServer, at time.Time) error {
	pool, err := marshalPool(servers)
	if err != nil {
		return err
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE tunnels SET pool = ?, pool_updated_at = ? WHERE id = ? AND source = ?`,
		pool, unixOrZero(at), id, string(SourcePool))
	if err != nil {
		return fmt.Errorf("запись серверов пула %s: %w", id, err)
	}
	return checkAffected(res, fmt.Sprintf("туннель-пул %s", id))
}

// marshalPool сериализует серверы пула для колонки TEXT. nil хранится как пустой
// список: колонка объявлена NOT NULL DEFAULT '[]'.
func marshalPool(v []PoolServer) (string, error) {
	if len(v) == 0 {
		return "[]", nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("сериализация серверов пула: %w", err)
	}
	return string(b), nil
}

// parsePool разбирает колонку с серверами пула.
func parsePool(s string, dst *[]PoolServer) error {
	if s == "" || s == "[]" {
		*dst = nil
		return nil
	}
	if err := json.Unmarshal([]byte(s), dst); err != nil {
		return fmt.Errorf("разбор серверов пула %q: %w", s, err)
	}
	return nil
}

// unixOrZero переводит время в колонку INTEGER: нулевое время хранится нулём, а не
// отрицательной эпохой.
func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// checkAffected превращает «ноль изменённых строк» в ErrNotFound.
func checkAffected(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if n == 0 {
		return fmt.Errorf("%s: %w", what, ErrNotFound)
	}
	return nil
}

// isUniqueViolation отличает нарушение UNIQUE от прочих ошибок драйвера. Драйвер
// modernc не даёт кода ошибки через errors.As, остаётся текст.
func isUniqueViolation(err error) bool {
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}

func quoteAll(v []string) []string {
	out := make([]string, len(v))
	for i, s := range v {
		out[i] = fmt.Sprintf("%q", s)
	}
	return out
}
