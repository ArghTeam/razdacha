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
	pool, pool_updated_at, builtin`

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
	`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

func tunnelArgs(t Tunnel, pool string) []any {
	return []any{
		t.ID, t.Name, string(t.Type), string(t.Source), t.Raw, string(t.Parsed),
		t.Enabled, t.CreatedAt.Unix(), pool, unixOrZero(t.PoolUpdatedAt), t.Builtin,
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

// UpdateTunnel перезаписывает туннель целиком. CreatedAt и Builtin не меняются:
// первое — история записи, второе — то, кем она заведена, и снимать флаг правкой
// через панель означало бы обходить запрет на удаление встроенного.
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

// BuiltinPoolResult — чем закончилось [Store.EnsureBuiltinPool]. Три исхода вместо
// одного флага: заведение, признание существующего пула встроенным и «и так был» —
// это разные события, и в логе они читаются по-разному.
type BuiltinPoolResult struct {
	// Tunnel — встроенный пул, каким он лежит в БД после вызова.
	Tunnel Tunnel
	// Created — запись завели прямо сейчас.
	Created bool
	// Adopted — встроенным пометили пул, который уже был в БД.
	Adopted bool
	// OtherPools — сколько туннелей-пулов осталось в БД помимо встроенного.
	// Штатно ноль: пул в системе один. Больше нуля — след ручной правки или
	// старой установки, и это повод для записи в лог, а не для отказа.
	OtherPools int
}

// EnsureBuiltinPool приводит БД к состоянию «встроенный пул есть, и он один».
//
// Пул в системе единственный и встроенный: через API его не создают, поэтому весь
// разбор случаев живёт здесь.
//
//  1. Есть запись с `builtin = 1` — берётся она.
//  2. Иначе есть туннель-пул — встроенным помечается он. Установка, где пул завели
//     руками до этой версии, получает один пул, а не два: второй означал бы второй
//     обход того же каталога и вторую группу urltest в конфиге.
//  3. Иначе пул заводится — выключенным: включённый на свежей установке сразу пошёл
//     бы на чужой сайт за ключами, о чём пользователь не просил. Включение — PATCH.
//
// Идемпотентность держится на признаке в колонке, а не на имени и не на каталоге:
// имя пользователь может переименовать, каталог — сменить, и по ним повторный старт
// завёл бы второй пул.
// Тип пула передаётся, а не берётся константой: протокол ключей знает драйвер
// каталога, а не хранилище (ADR 0015).
func (s *Store) EnsureBuiltinPool(ctx context.Context, name, catalogURL string, typ TunnelType) (
	BuiltinPoolResult, error,
) {
	var out BuiltinPoolResult
	err := s.tx(ctx, func(tx *sql.Tx) error {
		found, err := adoptBuiltinPool(ctx, tx, &out)
		if err != nil {
			return err
		}
		if !found {
			t := Tunnel{
				ID:        newID(),
				Name:      name,
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
					return fmt.Errorf("%w: имя %q занято другим туннелем — переименуйте его, "+
						"чтобы получить пул бесплатных ключей", ErrInvalid, t.Name)
				}
				return fmt.Errorf("заведение встроенного пула %q: %w", t.Name, err)
			}
			out.Tunnel, out.Created = t, true
		}

		var others int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(1) FROM tunnels WHERE source = ? AND id <> ?`,
			string(SourcePool), out.Tunnel.ID).Scan(&others); err != nil {
			return fmt.Errorf("подсчёт туннелей-пулов: %w", err)
		}
		out.OtherPools = others
		return nil
	})
	if err != nil {
		return BuiltinPoolResult{}, err
	}
	return out, nil
}

// RetargetBuiltinPool переводит встроенный пул на другой каталог: адрес, протокол
// ключей и — обязательно — пустой состав серверов.
//
// Нужен установкам, которые пережили смерть источника: адрес каталога лежит в БД у
// самого туннеля, и у всех, кто поставил razdacha до ADR 0015, там мёртвый
// vpnkeys.me. Такой пул обязан переехать, а не остаться молча пустым (issue #153).
//
// Состав чистится вместе с адресом, а не остаётся «пока не обойдём новый каталог»:
// в нём ключи чужого протокола с умершего сайта, и до первого обхода они уезжали бы
// в конфиг под видом состава нового каталога.
//
// Только встроенный пул: каталог, вписанный человеком, — его решение, и подменять
// его молча нельзя. Переезд обязан быть виден снаружи, поэтому вызывающий пишет о
// нём в лог.
func (s *Store) RetargetBuiltinPool(ctx context.Context, id, catalogURL string, typ TunnelType) error {
	if catalogURL == "" {
		return fmt.Errorf("%w: у пула %s пустой адрес каталога", ErrInvalid, id)
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE tunnels SET raw = ?, type = ?, pool = '[]', pool_updated_at = 0
		 WHERE id = ? AND source = ? AND builtin = 1`,
		catalogURL, string(typ), id, string(SourcePool))
	if err != nil {
		return fmt.Errorf("смена каталога пула %s: %w", id, err)
	}
	return checkAffected(res, fmt.Sprintf("встроенный туннель-пул %s", id))
}

// adoptBuiltinPool ищет в БД встроенный пул, а если его нет — самый старый обычный
// пул, и помечает встроенным его. Отвечает, нашёлся ли пул вообще.
//
// Самый старый, а не любой: выбор обязан быть одинаковым на каждом старте, иначе
// признак встроенного перескакивал бы с записи на запись.
func adoptBuiltinPool(ctx context.Context, tx *sql.Tx, out *BuiltinPoolResult) (bool, error) {
	existing, err := scanTunnel(tx.QueryRowContext(ctx,
		`SELECT `+tunnelColumns+` FROM tunnels WHERE builtin = 1 ORDER BY created_at, id LIMIT 1`))
	if err == nil {
		out.Tunnel = existing
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("поиск встроенного пула: %w", err)
	}

	existing, err = scanTunnel(tx.QueryRowContext(ctx,
		`SELECT `+tunnelColumns+` FROM tunnels WHERE source = ? ORDER BY created_at, id LIMIT 1`,
		string(SourcePool)))
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("поиск туннеля-пула: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE tunnels SET builtin = 1 WHERE id = ?`, existing.ID); err != nil {
		return false, fmt.Errorf("признание пула %q встроенным: %w", existing.Name, err)
	}
	existing.Builtin = true
	out.Tunnel, out.Adopted = existing, true
	return true, nil
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
		&pool, &poolUpdate, &t.Builtin); err != nil {
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
