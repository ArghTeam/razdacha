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
	pool, pool_updated_at`

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

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO tunnels (`+tunnelColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.Name, string(t.Type), string(t.Source), t.Raw, string(t.Parsed),
		t.Enabled, t.CreatedAt.Unix(), pool, unixOrZero(t.PoolUpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return Tunnel{}, fmt.Errorf("%w: туннель с именем %q уже есть", ErrInvalid, t.Name)
		}
		return Tunnel{}, fmt.Errorf("добавление туннеля %q: %w", t.Name, err)
	}
	return t, nil
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

// UpdateTunnel перезаписывает туннель целиком. CreatedAt не меняется.
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
func (s *Store) DeleteTunnel(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
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

// ruleNamesByTunnel отдаёт имена правил, ссылающихся на туннель.
func ruleNamesByTunnel(ctx context.Context, q querier, tunnelID string) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT name FROM rules WHERE tunnel_id = ? ORDER BY priority`, tunnelID)
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
		&pool, &poolUpdate); err != nil {
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
