package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"
)

const keyOnboarding = "onboarding_state"

var ErrOnboardingConflict = errors.New("setup conflicts with current policy; review pending changes first")

// OnboardingState is installation metadata, separate from editable Settings.
type OnboardingState struct {
	CandidateCount int    `json:"candidate_count"`
	CatalogOutcome string `json:"catalog_outcome"`
	PoolEnabled    bool   `json:"pool_enabled"`
	Pending        bool   `json:"pending"`
	PeerID         string `json:"peer_id,omitempty"`
	RuleID         string `json:"rule_id,omitempty"`
	ReadOnly       bool   `json:"read_only"`
	Mode           string `json:"mode,omitempty"`
	Country        string `json:"country,omitempty"`
	Prepared       bool   `json:"prepared"`
	Applied        bool   `json:"applied"`
	Expected       string `json:"expected,omitempty"`
}

func onboardingState(ctx context.Context, q querier) (OnboardingState, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, keyOnboarding).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return OnboardingState{}, nil
	}
	if err != nil {
		return OnboardingState{}, err
	}
	var state OnboardingState
	if json.Unmarshal([]byte(raw), &state) != nil {
		return state, errors.New("invalid setup metadata")
	}
	return state, nil
}

func saveOnboarding(ctx context.Context, q querier, state OnboardingState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, keyOnboarding, string(raw))
	return err
}

func (s *Store) Onboarding(ctx context.Context) (OnboardingState, error) {
	var state OnboardingState
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = onboardingState(ctx, tx)
		if err != nil {
			return err
		}
		snap, err := onboardingSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		onboardingPoolObservation(&state, snap)
		return nil
	})
	return state, err
}

func onboardingPoolObservation(state *OnboardingState, snap Snapshot) {
	state.CandidateCount = 0
	state.PoolEnabled = false
	state.CatalogOutcome = "not_configured"
	for _, tunnel := range snap.Tunnels {
		if !tunnel.Builtin || tunnel.Source != SourcePool {
			continue
		}
		eligible, _ := SplitPool(tunnel.Pool, PoolFilterFrom(snap.Settings))
		state.CandidateCount = len(eligible)
		state.PoolEnabled = tunnel.Enabled
		switch {
		case !tunnel.Enabled:
			state.CatalogOutcome = "disabled"
		case len(eligible) == 0:
			state.CatalogOutcome = "no_candidates"
		default:
			state.CatalogOutcome = "candidates_available"
		}
		return
	}
}

// InitOnboarding is called only by genuinely fresh setup after creating its client.
func (s *Store) InitOnboarding(ctx context.Context, peerID, ruleID, country string) error {
	state := OnboardingState{Pending: true, PeerID: peerID, RuleID: ruleID, Country: country}
	if ruleID != "" {
		state.Mode = "free"
	}
	return saveOnboarding(ctx, s.db, state)
}

// PolicyFingerprint ignores only refreshable pool inventory, never routing policy.
func PolicyFingerprint(snap Snapshot) (string, error) {
	snap.Tunnels = append([]Tunnel(nil), snap.Tunnels...)
	for i := range snap.Tunnels {
		if snap.Tunnels[i].Source == SourcePool {
			snap.Tunnels[i].Pool = nil
			snap.Tunnels[i].PoolUpdatedAt = time.Time{}
		}
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func onboardingSnapshot(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	var snap Snapshot
	var err error
	if snap.Settings, err = settings(ctx, tx); err != nil {
		return snap, err
	}
	if snap.Tunnels, err = tunnels(ctx, tx); err != nil {
		return snap, err
	}
	if snap.Rules, err = rules(ctx, tx); err != nil {
		return snap, err
	}
	snap.Peers, err = peers(ctx, tx)
	return snap, err
}

// ReopenOnboarding changes only metadata. Legacy configured policy is read-only.
func (s *Store) ReopenOnboarding(ctx context.Context, peerID string) (OnboardingState, error) {
	var state OnboardingState
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = onboardingState(ctx, tx)
		if err != nil {
			return err
		}
		snap, err := onboardingSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		onboardingPoolObservation(&state, snap)
		if state.PeerID != "" && peerID != "" && peerID != state.PeerID {
			return ErrOnboardingConflict
		}
		if peerID == "" {
			peerID = state.PeerID
		}
		if peerID == "" && len(snap.Peers) > 0 {
			peerID = snap.Peers[0].ID
		}
		found := false
		for _, peer := range snap.Peers {
			if peer.ID == peerID {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("%w: select an existing client before opening setup", ErrInvalid)
		}
		if state.Pending && state.PeerID == peerID {
			return nil
		}
		if state.PeerID == "" {
			state.ReadOnly = len(snap.Rules) > 0
			for _, tunnel := range snap.Tunnels {
				if !tunnel.Builtin {
					state.ReadOnly = true
				}
			}
		}
		state.Pending = true
		state.PeerID = peerID
		state.Prepared = false
		state.Applied = false
		state.Expected = ""
		return saveOnboarding(ctx, tx, state)
	})
	return state, err
}

// ConfigureOnboarding atomically checks the base and edits only owned starter policy.
func (s *Store) ConfigureOnboarding(ctx context.Context, expected, mode, country, catalog string, members []PoolServer, preset []string) (OnboardingState, error) {
	var state OnboardingState
	err := s.tx(ctx, func(tx *sql.Tx) error {
		var err error
		state, err = onboardingState(ctx, tx)
		if err != nil {
			return err
		}
		if !state.Pending || state.PeerID == "" {
			return fmt.Errorf("%w: setup is not pending", ErrInvalid)
		}
		snap, err := onboardingSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		actual, err := PolicyFingerprint(snap)
		if err != nil {
			return err
		}
		if actual != expected {
			return ErrOnboardingConflict
		}
		found := false
		for _, peer := range snap.Peers {
			if peer.ID == state.PeerID {
				found = true
				if peer.EgressTunnelID != nil && mode == "free" {
					return ErrOnboardingConflict
				}
			}
		}
		if !found {
			return fmt.Errorf("%w: initial client no longer exists", ErrInvalid)
		}
		if mode != "free" && mode != "skip" {
			return fmt.Errorf("%w: invalid setup choice", ErrInvalid)
		}
		if mode == "free" {
			if state.ReadOnly {
				return fmt.Errorf("%w: existing policy is read-only in setup; keep it or manage tunnels separately", ErrInvalid)
			}
			var pool Tunnel
			for _, tunnel := range snap.Tunnels {
				if tunnel.Builtin && tunnel.Source == SourcePool {
					if pool.ID != "" {
						return ErrOnboardingConflict
					}
					pool = tunnel
				}
			}
			if pool.ID == "" {
				return fmt.Errorf("%w: built-in pool unavailable", ErrInvalid)
			}
			var owned Rule
			for _, rule := range snap.Rules {
				if rule.ID != state.RuleID {
					return ErrOnboardingConflict
				}
				if rule.TunnelID != pool.ID || rule.Action != ActionTunnel || rule.PeerScope != ScopeSelected || !reflect.DeepEqual(rule.PeerIDs, []string{state.PeerID}) || !reflect.DeepEqual(rule.CommunityLists, preset) || len(rule.Domains)+len(rule.Subnets)+len(rule.RemoteLists) > 0 || rule.ViaTunnelID != "" || rule.ResolveRealIP {
					return ErrOnboardingConflict
				}
				owned = rule
			}
			for _, peer := range snap.Peers {
				if peer.EgressTunnelID != nil && *peer.EgressTunnelID == pool.ID {
					return ErrOnboardingConflict
				}
			}
			pool.Raw = catalog
			pool.Enabled = true
			pool.Pool = members
			pool.PoolUpdatedAt = time.Time{}
			if len(members) > 0 {
				pool.PoolUpdatedAt = time.Now().UTC()
			}
			encoded, err := marshalPool(pool.Pool)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE tunnels SET raw=?,enabled=1,pool=?,pool_updated_at=? WHERE id=?`, pool.Raw, encoded, unixOrZero(pool.PoolUpdatedAt), pool.ID); err != nil {
				return err
			}
			if owned.ID == "" {
				owned = Rule{ID: newID(), Name: "Free start", Action: ActionTunnel, TunnelID: pool.ID, Enabled: true, PeerScope: ScopeSelected, PeerIDs: []string{state.PeerID}, CommunityLists: preset}
				if err := validateRulePeers(ctx, tx, &owned); err != nil {
					return err
				}
				args, err := ruleArgs(owned)
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO rules (`+ruleColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, append([]any{owned.ID}, args...)...); err != nil {
					return err
				}
				state.RuleID = owned.ID
			} else {
				if _, err := tx.ExecContext(ctx, `UPDATE rules SET enabled=1 WHERE id=?`, owned.ID); err != nil {
					return err
				}
			}
		}
		snap, err = onboardingSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		state.Expected, err = PolicyFingerprint(snap)
		if err != nil {
			return err
		}
		state.Mode = mode
		onboardingPoolObservation(&state, snap)
		state.Country = country
		state.Prepared = true
		state.Applied = false
		return saveOnboarding(ctx, tx, state)
	})
	return state, err
}

func (s *Store) AcceptOnboarding(ctx context.Context, expected string, finish bool) error {
	return s.tx(ctx, func(tx *sql.Tx) error {
		state, err := onboardingState(ctx, tx)
		if err != nil {
			return err
		}
		snap, err := onboardingSnapshot(ctx, tx)
		if err != nil {
			return err
		}
		actual, err := PolicyFingerprint(snap)
		if err != nil {
			return err
		}
		if !state.Pending || !state.Prepared || state.Expected != expected || actual != expected || (finish && !state.Applied) {
			return ErrOnboardingConflict
		}
		state.Applied = true
		if finish {
			state.Pending = false
		}
		return saveOnboarding(ctx, tx, state)
	})
}
