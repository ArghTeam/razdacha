package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const ruleColumns = `id, name, action, tunnel_id, via_tunnel_id, priority, enabled,
	community_lists, domains, subnets, remote_lists, peer_scope, peer_ids, resolve_real_ip`

// CreateRule добавляет правило в конец списка: приоритет назначает слой хранения,
// иначе уникальный индекс rules_priority ловил бы пользовательский ввод.
func (s *Store) CreateRule(ctx context.Context, r Rule) (Rule, error) {
	if err := r.validate(); err != nil {
		return Rule{}, err
	}
	if r.ID == "" {
		r.ID = newID()
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		var maxPriority sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(priority) FROM rules`).Scan(&maxPriority); err != nil {
			return fmt.Errorf("добавление правила %q: %w", r.Name, err)
		}
		r.Priority = 0
		if maxPriority.Valid {
			r.Priority = int(maxPriority.Int64) + 1
		}

		args, err := ruleArgs(r)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO rules (`+ruleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			append([]any{r.ID}, args...)...)
		if err != nil {
			return wrapRuleWriteErr(err, r, "добавление")
		}
		return nil
	})
	if err != nil {
		return Rule{}, err
	}
	return r, nil
}

// Rule возвращает правило по идентификатору.
func (s *Store) Rule(ctx context.Context, id string) (Rule, error) {
	r, err := scanRule(s.db.QueryRowContext(ctx,
		`SELECT `+ruleColumns+` FROM rules WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, fmt.Errorf("правило %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Rule{}, fmt.Errorf("чтение правила %s: %w", id, err)
	}
	return r, nil
}

// Rules возвращает правила в порядке проверки: меньший приоритет — раньше.
func (s *Store) Rules(ctx context.Context) ([]Rule, error) {
	return rules(ctx, s.db)
}

func rules(ctx context.Context, q querier) ([]Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+ruleColumns+` FROM rules ORDER BY priority`)
	if err != nil {
		return nil, fmt.Errorf("чтение правил: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Rule
	for rows.Next() {
		r, err := scanRule(rows)
		if err != nil {
			return nil, fmt.Errorf("чтение правил: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение правил: %w", err)
	}
	return out, nil
}

// UpdateRule перезаписывает правило, кроме приоритета: порядок меняет [Store.ReorderRules].
func (s *Store) UpdateRule(ctx context.Context, r Rule) error {
	if err := r.validate(); err != nil {
		return err
	}
	if r.ID == "" {
		return fmt.Errorf("%w: у обновляемого правила пустой идентификатор", ErrInvalid)
	}

	args, err := ruleArgs(r)
	if err != nil {
		return err
	}
	// Приоритет из аргументов выкидываем — колонка не обновляется.
	set := `name = ?, action = ?, tunnel_id = ?, via_tunnel_id = ?, enabled = ?,
		community_lists = ?, domains = ?, subnets = ?, remote_lists = ?, peer_scope = ?,
		peer_ids = ?, resolve_real_ip = ?`
	values := make([]any, 0, len(args))
	values = append(values, args[:4]...) // name, action, tunnel_id, via_tunnel_id
	values = append(values, args[5:]...) // всё после priority
	values = append(values, r.ID)

	res, err := s.db.ExecContext(ctx, `UPDATE rules SET `+set+` WHERE id = ?`, values...)
	if err != nil {
		return wrapRuleWriteErr(err, r, "обновление")
	}
	return checkAffected(res, fmt.Sprintf("правило %s", r.ID))
}

// DeleteRule удаляет правило и сжимает приоритеты оставшихся, чтобы в списке не
// оставалось дыр.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM rules WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("удаление правила %s: %w", id, err)
		}
		if err := checkAffected(res, fmt.Sprintf("правило %s", id)); err != nil {
			return err
		}

		order, err := ruleIDsByPriority(ctx, tx)
		if err != nil {
			return err
		}
		return applyOrder(ctx, tx, order)
	})
}

// ReorderRules задаёт новый порядок проверки правил. ids — полный список
// идентификаторов в нужном порядке: частичная перестановка сделала бы порядок
// зависимым от прежних значений приоритета.
func (s *Store) ReorderRules(ctx context.Context, ids []string) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		current, err := ruleIDsByPriority(ctx, tx)
		if err != nil {
			return err
		}
		if err := sameSet(current, ids); err != nil {
			return err
		}
		return applyOrder(ctx, tx, ids)
	})
}

// applyOrder расставляет приоритеты 0..N-1 в порядке ids. Приоритеты сначала уходят в
// отрицательный диапазон: SQLite проверяет уникальный индекс построчно, и прямая
// перестановка столкнулась бы на первом же промежуточном значении.
func applyOrder(ctx context.Context, tx *sql.Tx, ids []string) error {
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE rules SET priority = ? WHERE id = ?`, -(i + 1), id); err != nil {
			return fmt.Errorf("перестановка правил: %w", err)
		}
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(ctx,
			`UPDATE rules SET priority = ? WHERE id = ?`, i, id); err != nil {
			return fmt.Errorf("перестановка правил: %w", err)
		}
	}
	return nil
}

// ruleIDsByPriority отдаёт идентификаторы правил в текущем порядке проверки.
func ruleIDsByPriority(ctx context.Context, q querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM rules ORDER BY priority`)
	if err != nil {
		return nil, fmt.Errorf("чтение порядка правил: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("чтение порядка правил: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение порядка правил: %w", err)
	}
	return ids, nil
}

// sameSet проверяет, что новый порядок — перестановка текущего набора правил.
func sameSet(current, next []string) error {
	if len(current) != len(next) {
		return fmt.Errorf("%w: в новом порядке %d правил, а всего их %d",
			ErrInvalid, len(next), len(current))
	}
	have := make(map[string]bool, len(current))
	for _, id := range current {
		have[id] = true
	}
	seen := make(map[string]bool, len(next))
	for _, id := range next {
		if !have[id] {
			return fmt.Errorf("правило %s: %w", id, ErrNotFound)
		}
		if seen[id] {
			return fmt.Errorf("%w: правило %s указано в новом порядке дважды", ErrInvalid, id)
		}
		seen[id] = true
	}
	return nil
}

// ruleArgs собирает значения колонок правила в порядке ruleColumns без id.
func ruleArgs(r Rule) ([]any, error) {
	lists := [...][]string{r.CommunityLists, r.Domains, r.Subnets, r.RemoteLists, r.PeerIDs}
	encoded := make([]any, len(lists))
	for i, l := range lists {
		v, err := jsonList(l)
		if err != nil {
			return nil, fmt.Errorf("правило %q: %w", r.Name, err)
		}
		encoded[i] = v
	}

	tunnelID := sql.NullString{String: r.TunnelID, Valid: r.TunnelID != ""}
	viaTunnelID := sql.NullString{String: r.ViaTunnelID, Valid: r.ViaTunnelID != ""}
	return []any{
		r.Name, string(r.Action), tunnelID, viaTunnelID, r.Priority, r.Enabled,
		encoded[0], encoded[1], encoded[2], encoded[3],
		string(r.PeerScope), encoded[4], r.ResolveRealIP,
	}, nil
}

func scanRule(sc scanner) (Rule, error) {
	var (
		r                                        Rule
		action, scope                            string
		tunnelID, viaTunnelID                    sql.NullString
		lists, domains, subnets, remote, peerIDs string
	)
	err := sc.Scan(&r.ID, &r.Name, &action, &tunnelID, &viaTunnelID, &r.Priority, &r.Enabled,
		&lists, &domains, &subnets, &remote, &scope, &peerIDs, &r.ResolveRealIP)
	if err != nil {
		return Rule{}, err
	}

	r.Action = RuleAction(action)
	r.PeerScope = PeerScope(scope)
	r.TunnelID = tunnelID.String
	r.ViaTunnelID = viaTunnelID.String
	for _, f := range []struct {
		raw string
		dst *[]string
	}{
		{lists, &r.CommunityLists},
		{domains, &r.Domains},
		{subnets, &r.Subnets},
		{remote, &r.RemoteLists},
		{peerIDs, &r.PeerIDs},
	} {
		if err := parseList(f.raw, f.dst); err != nil {
			return Rule{}, err
		}
	}
	return r, nil
}

// wrapRuleWriteErr превращает ошибки внешнего ключа в сообщение про туннель:
// «FOREIGN KEY constraint failed» пользователю ничего не говорит. Какое из двух
// звеньев не нашлось, SQLite не сообщает, поэтому называются оба заданных.
func wrapRuleWriteErr(err error, r Rule, what string) error {
	if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		ids := r.TunnelID
		if r.ViaTunnelID != "" {
			ids += ", " + r.ViaTunnelID
		}
		return fmt.Errorf("правило %q ссылается на туннель %s: %w", r.Name, ids, ErrNotFound)
	}
	return fmt.Errorf("%s правила %q: %w", what, r.Name, err)
}
