package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const peerColumns = `id, name, public_key, private_key, preshared_key, address, enabled, created_at, egress_tunnel_id`

// CreatePeer добавляет пира. Пустые ID и CreatedAt заполняются здесь.
func (s *Store) CreatePeer(ctx context.Context, p Peer) (Peer, error) {
	if err := p.validate(); err != nil {
		return Peer{}, err
	}
	if p.ID == "" {
		p.ID = newID()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}

	err := s.tx(ctx, func(tx *sql.Tx) error {
		if err := validatePeerEgress(ctx, tx, p.EgressTunnelID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO peers (`+peerColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.PublicKey, p.PrivateKey, p.PresharedKey, p.Address,
			p.Enabled, p.CreatedAt.Unix(), p.EgressTunnelID)
		if err != nil {
			if isUniqueViolation(err) {
				return peerConflictErr(err, p)
			}
			return fmt.Errorf("добавление пира %q: %w", p.Name, err)
		}
		return nil
	})
	if err != nil {
		return Peer{}, err
	}
	return p, nil
}

// Peer возвращает пира по идентификатору.
func (s *Store) Peer(ctx context.Context, id string) (Peer, error) {
	p, err := scanPeer(s.db.QueryRowContext(ctx,
		`SELECT `+peerColumns+` FROM peers WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Peer{}, fmt.Errorf("пир %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Peer{}, fmt.Errorf("чтение пира %s: %w", id, err)
	}
	return p, nil
}

// Peers возвращает всех пиров в порядке создания.
func (s *Store) Peers(ctx context.Context) ([]Peer, error) {
	return peers(ctx, s.db)
}

func peers(ctx context.Context, q querier) ([]Peer, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT `+peerColumns+` FROM peers ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("чтение пиров: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Peer
	for rows.Next() {
		p, err := scanPeer(rows)
		if err != nil {
			return nil, fmt.Errorf("чтение пиров: %w", err)
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("чтение пиров: %w", err)
	}
	return out, nil
}

// UpdatePeer перезаписывает пира целиком. CreatedAt не меняется.
func (s *Store) UpdatePeer(ctx context.Context, p Peer) error {
	if err := p.validate(); err != nil {
		return err
	}
	if p.ID == "" {
		return fmt.Errorf("%w: у обновляемого пира пустой идентификатор", ErrInvalid)
	}

	return s.tx(ctx, func(tx *sql.Tx) error { return updatePeer(ctx, tx, p) })
}

func updatePeer(ctx context.Context, tx *sql.Tx, p Peer) error {
	if err := validatePeerEgress(ctx, tx, p.EgressTunnelID); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx,
		`UPDATE peers SET name = ?, public_key = ?, private_key = ?, preshared_key = ?,
		 address = ?, enabled = ?, egress_tunnel_id = ? WHERE id = ?`,
		p.Name, p.PublicKey, p.PrivateKey, p.PresharedKey, p.Address, p.Enabled, p.EgressTunnelID, p.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return peerConflictErr(err, p)
		}
		return fmt.Errorf("обновление пира %s: %w", p.ID, err)
	}
	return checkAffected(res, fmt.Sprintf("пир %s", p.ID))
}

// PeerPatch distinguishes an omitted exit from an explicit null (shared rules).
type PeerPatch struct {
	Name            *string
	Enabled         *bool
	SetEgressTunnel bool
	EgressTunnelID  *string
}

// PatchPeer reads and modifies only supplied fields in one transaction. A rename
// or enable toggle cannot overwrite a concurrently saved routing choice.
func (s *Store) PatchPeer(ctx context.Context, id string, patch PeerPatch) (Peer, error) {
	var p Peer
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = scanPeer(tx.QueryRowContext(ctx, `SELECT `+peerColumns+` FROM peers WHERE id = ?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("peer %s: %w", id, ErrNotFound)
		}
		if err != nil {
			return fmt.Errorf("read peer for patch: %w", err)
		}
		if patch.Name != nil {
			p.Name = strings.TrimSpace(*patch.Name)
		}
		if patch.Enabled != nil {
			p.Enabled = *patch.Enabled
		}
		if patch.SetEgressTunnel {
			p.EgressTunnelID = patch.EgressTunnelID
		}
		if err := p.validate(); err != nil {
			return err
		}
		return updatePeer(ctx, tx, p)
	})
	if err != nil {
		return Peer{}, err
	}
	return p, nil
}

// PeerDeletion describes rule changes committed together with a peer deletion.
type PeerDeletion struct {
	AffectedRuleIDs    []string `json:"affected_rule_ids"`
	InactivatedRuleIDs []string `json:"inactivated_rule_ids"`
}

// DeletePeer preserves the compatibility error-only interface.
func (s *Store) DeletePeer(ctx context.Context, id string) error {
	_, err := s.DeletePeerWithEffects(ctx, id)
	return err
}

// DeletePeerWithEffects never broadens selected policy to other clients.
func (s *Store) DeletePeerWithEffects(ctx context.Context, id string) (PeerDeletion, error) {
	effects := PeerDeletion{AffectedRuleIDs: []string{}, InactivatedRuleIDs: []string{}}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM peers WHERE id = ?`, id)
		if err != nil {
			return fmt.Errorf("delete peer %s: %w", id, err)
		}
		if err := checkAffected(res, fmt.Sprintf("peer %s", id)); err != nil {
			return err
		}
		all, err := rules(ctx, tx)
		if err != nil {
			return err
		}
		existing, err := peers(ctx, tx)
		if err != nil {
			return err
		}
		present := make(map[string]bool, len(existing))
		for _, p := range existing {
			present[p.ID] = true
		}
		for _, r := range all {
			if r.PeerScope != ScopeSelected {
				continue
			}
			kept := make([]string, 0, len(r.PeerIDs))
			seen := make(map[string]bool)
			for _, selected := range r.PeerIDs {
				if present[selected] && !seen[selected] {
					kept = append(kept, selected)
					seen[selected] = true
				}
			}
			enabled := r.Enabled && len(kept) > 0
			if len(kept) == len(r.PeerIDs) && enabled == r.Enabled {
				continue
			}
			encoded, err := jsonList(kept)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE rules SET peer_ids = ?, enabled = ? WHERE id = ?`, encoded, enabled, r.ID); err != nil {
				return fmt.Errorf("remove peer from rule %q: %w", r.Name, err)
			}
			effects.AffectedRuleIDs = append(effects.AffectedRuleIDs, r.ID)
			if len(kept) == 0 {
				effects.InactivatedRuleIDs = append(effects.InactivatedRuleIDs, r.ID)
			}
		}
		return nil
	})
	if err != nil {
		return PeerDeletion{}, err
	}
	return effects, nil
}

func scanPeer(sc scanner) (Peer, error) {
	var (
		p         Peer
		createdAt int64
	)
	err := sc.Scan(&p.ID, &p.Name, &p.PublicKey, &p.PrivateKey, &p.PresharedKey,
		&p.Address, &p.Enabled, &createdAt, &p.EgressTunnelID)
	if err != nil {
		return Peer{}, err
	}
	p.CreatedAt = time.Unix(createdAt, 0).UTC()
	return p, nil
}

// peerConflictErr называет колонку, по которой пир не уникален.
func peerConflictErr(err error, p Peer) error {
	if strings.Contains(err.Error(), "peers.address") {
		return fmt.Errorf("%w: адрес %s уже занят другим пиром", ErrInvalid, p.Address)
	}
	return fmt.Errorf("%w: пир с таким публичным ключом уже есть", ErrInvalid)
}

// validatePeerEgress runs in the peer write transaction; disabled targets remain selectable.
func validatePeerEgress(ctx context.Context, tx *sql.Tx, id *string) error {
	if id == nil {
		return nil
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("%w: select an outgoing tunnel or clear the routing override", ErrInvalid)
	}
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tunnels WHERE id = ?)`, *id).Scan(&exists); err != nil {
		return fmt.Errorf("validate peer exit: %w", err)
	}
	if !exists {
		return fmt.Errorf("%w: outgoing tunnel does not exist", ErrInvalid)
	}
	return nil
}
