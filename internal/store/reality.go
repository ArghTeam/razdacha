package store

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strings"
)

// RealityAdmission is policy authority, never a bearer credential.
type RealityAdmission struct {
	PeerID       string `json:"peer_id"`
	CredentialID string `json:"credential_id"`
	Revision     int    `json:"revision"`
}

// RealityIdentity and RealityRuntime are live-only private material. They must
// never be serialized into routing generations or ordinary API responses.
type (
	RealityIdentity struct{ ID, PrivateKey, PublicKey, ShortID string }
	RealityUser     struct{ PeerID, Name, UUID string }
	RealityRuntime  struct {
		LocalAddresses []string
		Identity       *RealityIdentity
		Users          []RealityUser
	}
)

func (v Settings) validateReality() error {
	if !v.RealityEnabled {
		return nil
	}
	if v.RealityListenPort < 1 || v.RealityListenPort > 65535 {
		return fmt.Errorf("%w: REALITY listen port must be 1-65535", ErrInvalid)
	}
	for _, port := range []int{22, 53, 80, 443, 1602, 8080, 9090} {
		if v.RealityListenPort == port {
			return fmt.Errorf("%w: REALITY requires a dedicated non-management TCP port", ErrInvalid)
		}
	}
	if v.RealityHandshakePort < 1 || v.RealityHandshakePort > 65535 {
		return fmt.Errorf("%w: invalid REALITY handshake port", ErrInvalid)
	}
	for name, value := range map[string]string{"server name": v.RealityServerName, "handshake server": v.RealityHandshakeServer} {
		if value == "" || strings.ContainsAny(value, " /\\?#@\t\r\n") {
			return fmt.Errorf("%w: REALITY %s must be a hostname or address", ErrInvalid, name)
		}
		if net.ParseIP(value) == nil {
			for _, c := range value {
				if !strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-", c) {
					return fmt.Errorf("%w: invalid REALITY %s", ErrInvalid, name)
				}
			}
		}
	}
	return nil
}

func ensureRealityIdentity(ctx context.Context, tx *sql.Tx) error {
	var present int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM reality_identity`).Scan(&present); err != nil {
		return fmt.Errorf("read REALITY identity: %w", err)
	}
	if present != 0 {
		return nil
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate REALITY identity: %w", err)
	}
	short := make([]byte, 8)
	if _, err = rand.Read(short); err != nil {
		return fmt.Errorf("generate REALITY short ID: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO reality_identity(singleton,identity_id,private_key,short_id) VALUES(1,?,?,?)`, newID(), base64.RawURLEncoding.EncodeToString(key.Bytes()), hex.EncodeToString(short))
	if err != nil {
		return fmt.Errorf("store REALITY identity: %w", err)
	}
	return nil
}

func realityIdentity(ctx context.Context, q querier) (*RealityIdentity, error) {
	var out RealityIdentity
	err := q.QueryRowContext(ctx, `SELECT identity_id,private_key,short_id FROM reality_identity WHERE singleton=1`).Scan(&out.ID, &out.PrivateKey, &out.ShortID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read REALITY identity: %w", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(out.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("invalid stored REALITY private key")
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid stored REALITY private key")
	}
	out.PublicKey = base64.RawURLEncoding.EncodeToString(key.PublicKey().Bytes())
	return &out, nil
}

func (s *Store) RealityIdentity(ctx context.Context) (*RealityIdentity, error) {
	return realityIdentity(ctx, s.db)
}

func ensurePeerReality(ctx context.Context, tx *sql.Tx, p *Peer) error {
	if p.RealityCredentialID != "" && p.RealityRevision > 0 {
		return nil
	}
	p.RealityCredentialID = newID()
	p.RealityRevision = 1
	if _, err := tx.ExecContext(ctx, `INSERT INTO reality_credentials(peer_id,credential_id,revision,uuid) VALUES(?,?,?,?)`, p.ID, p.RealityCredentialID, p.RealityRevision, newID()); err != nil {
		return fmt.Errorf("create REALITY credential: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE peers SET reality_credential_id=?,reality_revision=? WHERE id=?`, p.RealityCredentialID, p.RealityRevision, p.ID); err != nil {
		return fmt.Errorf("record REALITY admission: %w", err)
	}
	return nil
}

// RotateReality invalidates the old bearer durably before a replacement can be
// admitted by explicit Apply. Its caller owns the ingress publication gate.
func (s *Store) RotateReality(ctx context.Context, id string, expected int) (Peer, error) {
	var p Peer
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = scanPeer(tx.QueryRowContext(ctx, `SELECT `+peerColumns+` FROM peers WHERE id=?`, id))
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if !p.RealityEnabled || p.RealityRevision != expected || expected < 1 {
			return fmt.Errorf("%w: REALITY credential revision changed or access is disabled", ErrConflict)
		}
		p.RealityRevision++
		if _, err = tx.ExecContext(ctx, `UPDATE reality_credentials SET revision=?,uuid=? WHERE peer_id=? AND credential_id=?`, p.RealityRevision, newID(), id, p.RealityCredentialID); err != nil {
			return fmt.Errorf("rotate REALITY credential: %w", err)
		}
		_, err = tx.ExecContext(ctx, `UPDATE peers SET reality_revision=? WHERE id=?`, p.RealityRevision, id)
		return err
	})
	return p, err
}

// RealityRuntime intersects committed admission with live authentication. A stale
// generation cannot recreate deleted/disabled clients or a rotated UUID.
func (s *Store) RealityRuntime(ctx context.Context, snap Snapshot) (*RealityRuntime, error) {
	out := &RealityRuntime{}
	err := s.tx(ctx, func(tx *sql.Tx) error {
		current, err := settings(ctx, tx)
		if err != nil {
			return err
		}
		if !snap.Settings.RealityEnabled || !current.RealityEnabled {
			return nil
		}
		identity, err := realityIdentity(ctx, tx)
		if err != nil {
			return err
		}
		if identity == nil || identity.ID != snap.RealityIdentityID {
			return fmt.Errorf("REALITY identity is missing or no longer admitted")
		}
		out.Identity = identity
		admitted := map[string]RealityAdmission{}
		for _, a := range snap.RealityAdmissions {
			if a.PeerID == "" || a.CredentialID == "" || a.Revision < 1 {
				return fmt.Errorf("invalid REALITY admission")
			}
			if _, ok := admitted[a.PeerID]; ok {
				return fmt.Errorf("duplicate REALITY admission")
			}
			admitted[a.PeerID] = a
		}
		policyPeers := map[string]Peer{}
		for _, p := range snap.Peers {
			policyPeers[p.ID] = p
		}
		rows, err := tx.QueryContext(ctx, `SELECT c.peer_id,c.credential_id,c.revision,c.uuid FROM reality_credentials c JOIN peers p ON p.id=c.peer_id WHERE p.enabled=1 AND p.reality_enabled=1`)
		if err != nil {
			return fmt.Errorf("read live REALITY credentials: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var peerID, credentialID, uuid string
			var revision int
			if err := rows.Scan(&peerID, &credentialID, &revision, &uuid); err != nil {
				return err
			}
			a, ok := admitted[peerID]
			p := policyPeers[peerID]
			if ok && p.Enabled && p.RealityEnabled && a.CredentialID == credentialID && a.Revision == revision {
				out.Users = append(out.Users, RealityUser{PeerID: peerID, Name: RealityUserName(peerID), UUID: uuid})
			}
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func RealityUserName(peerID string) string { return "client-" + peerID }

// invalidateReality increments live revisions without admitting replacement
// credentials. Re-enabling access therefore requires a new explicit Apply.
func invalidateReality(ctx context.Context, tx *sql.Tx, peerID string) error {
	condition := ""
	var args []any
	if peerID != "" {
		condition = " WHERE peer_id=?"
		args = []any{peerID}
	}
	rows, err := tx.QueryContext(ctx, `SELECT peer_id FROM reality_credentials`+condition, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE reality_credentials SET revision=revision+1,uuid=? WHERE peer_id=?`, newID(), id); err != nil {
			return err
		}
	}
	condition = ""
	args = nil
	if peerID != "" {
		condition = " WHERE id=?"
		args = []any{peerID}
	}
	_, err = tx.ExecContext(ctx, `UPDATE peers SET reality_revision=COALESCE((SELECT revision FROM reality_credentials WHERE peer_id=peers.id),0)`+condition, args...)
	return err
}

// ResetRealityAfterRestore prevents a copied backup from activating a cloned
// public identity. WireGuard keys and addresses are preserved unchanged.
func (s *Store) ResetRealityAfterRestore(ctx context.Context) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		for _, query := range []string{`DELETE FROM reality_credentials`, `DELETE FROM reality_identity`, `UPDATE peers SET enabled=CASE WHEN wireguard_disabled=1 THEN 0 ELSE enabled END,reality_enabled=0,reality_credential_id='',reality_revision=0`, `INSERT INTO settings(key,value) VALUES('reality_enabled','false') ON CONFLICT(key) DO UPDATE SET value=excluded.value`} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("invalidate restored REALITY identity: %w", err)
			}
		}
		return nil
	})
}
