package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// ClientEvidenceAuthority is private metadata, never serialized as an HTTP payload.
// Peer is a narrow RouteEligibility read; it contains no private keys or UUIDs.
type ClientEvidenceAuthority struct {
	Epoch       string
	SourceEpoch string
	State       string
	Reason      string
	Peer        store.Peer
}

type ClientExitPlan struct {
	Authority ClientEvidenceAuthority
	Policy    singbox.PolicyExplanation
	Tag       string
	Pool      bool
}

// ClientEvidencePublisher binds observations to checked publication and live admission.
type ClientEvidencePublisher interface {
	ClientEvidenceAuthority(context.Context, *store.Store, string, string) (ClientEvidenceAuthority, error)
	ClientExitPlan(context.Context, *store.Store, singbox.ExplainRequest) (ClientExitPlan, error)
}

type clientObserveRequest struct {
	PeerID    string `json:"peer_id"`
	Transport string `json:"transport"`
}

type clientWGObservation struct {
	RxBytes       *int64     `json:"rx_bytes"`
	TxBytes       *int64     `json:"tx_bytes"`
	LastHandshake *time.Time `json:"last_handshake"`
}

type clientRealityObservation struct {
	OpenFlows         *int   `json:"open_flows"`
	OpenUploadBytes   *int64 `json:"open_upload_bytes"`
	OpenDownloadBytes *int64 `json:"open_download_bytes"`
}

type clientObservation struct {
	Scope      string                    `json:"scope"`
	PeerID     string                    `json:"peer_id"`
	Transport  string                    `json:"transport"`
	State      string                    `json:"state"`
	Reason     string                    `json:"reason"`
	Epoch      *string                   `json:"epoch"`
	SampledAt  time.Time                 `json:"sampled_at"`
	ObservedAt *time.Time                `json:"observed_at"`
	AgeMS      *int64                    `json:"age_ms"`
	Fresh      bool                      `json:"fresh"`
	Source     string                    `json:"source"`
	WG         *clientWGObservation      `json:"wg"`
	Reality    *clientRealityObservation `json:"reality"`
}

const (
	clientEvidenceFreshness       = 30 * time.Second
	maxEvidenceCounter      int64 = 1<<53 - 1
)

func safeEvidenceCounter(n int64) *int64 {
	if n < 0 || n > maxEvidenceCounter {
		return nil
	}
	return &n
}

func evidenceTime(at, now time.Time, limit time.Duration) (*time.Time, *int64, bool) {
	if at.IsZero() {
		return nil, nil, false
	}
	at = at.UTC()
	age := now.Sub(at)
	ms := age.Milliseconds()
	return &at, &ms, age >= 0 && age <= limit
}

func decodeEvidenceRequest(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(dst) != nil {
		return false
	}
	return d.Decode(new(any)) == io.EOF
}

func validEvidenceClient(id, transport string) bool {
	return id != "" && len(id) <= 128 && (transport == "wg" || transport == "reality")
}

// A single synchronous source job holds its slot until the device read returns.
// Canceled waiters cannot release it or start detached replacement source work.
type wgEvidenceSample struct {
	epoch string
	at    time.Time
	stats map[string]netstack.WGPeerStat
}

type wgEvidenceCache struct {
	mu     sync.Mutex
	sample wgEvidenceSample
	until  time.Time
	flight chan struct{}
}

func (c *wgEvidenceCache) observe(ctx context.Context, epoch string, now func() time.Time, load func(context.Context) (map[string]netstack.WGPeerStat, error)) wgEvidenceSample {
	for {
		if ctx.Err() != nil {
			return wgEvidenceSample{}
		}
		c.mu.Lock()
		if c.sample.epoch == epoch && now().Before(c.until) {
			out := c.sample
			c.mu.Unlock()
			return out
		}
		if c.flight != nil {
			done := c.flight
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return wgEvidenceSample{}
			case <-done:
				continue
			}
		}
		done := make(chan struct{})
		c.flight = done
		c.mu.Unlock()
		// WG device APIs may ignore context. Keep the one slot until actual completion.
		stats, err := load(context.WithoutCancel(ctx))
		out := wgEvidenceSample{epoch: epoch, at: now()}
		if err == nil {
			out.stats = stats
		}
		c.mu.Lock()
		c.sample = out
		c.until = out.at.Add(realityActivityInterval)
		c.flight = nil
		close(done)
		c.mu.Unlock()
		return out
	}
}

func (s *Server) evidenceAuthority(ctx context.Context, id, transport string) (ClientEvidenceAuthority, error) {
	if pub, ok := s.applier.(ClientEvidencePublisher); ok {
		return pub.ClientEvidenceAuthority(ctx, s.store, id, transport)
	}
	_, err := s.store.RouteEligibility(ctx, id)
	return ClientEvidenceAuthority{State: "unknown", Reason: "authority_unavailable"}, err
}

func (s *Server) handleClientObserve(w http.ResponseWriter, r *http.Request) {
	var req clientObserveRequest
	if !decodeEvidenceRequest(w, r, &req) || !validEvidenceClient(req.PeerID, req.Transport) {
		writeError(w, s.log, 400, codeBadRequest, "Choose a client and wg or reality ingress; no other fields are accepted.")
		return
	}
	before, err := s.evidenceAuthority(r.Context(), req.PeerID, req.Transport)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, s.log, 404, codeNotFound, "Client no longer exists.")
		return
	}
	out := clientObservation{Scope: "client_ingress", PeerID: req.PeerID, Transport: req.Transport, State: "unknown", Reason: "authority_unavailable", SampledAt: s.now().UTC(), Source: "wireguard_kernel"}
	if req.Transport == "reality" {
		out.Source = "authenticated_open_flows"
	}
	if err != nil || before.Epoch == "" {
		if err == nil {
			out.State = before.State
			out.Reason = before.Reason
		}
		writeJSON(w, s.log, 200, out)
		return
	}
	out.Epoch = &before.Epoch
	out.Reason = "source_unavailable"
	if req.Transport == "wg" && s.peerStat != nil {
		sample := s.wgEvidence.observe(r.Context(), before.SourceEpoch, s.now, s.peerStat)
		if sample.epoch == before.SourceEpoch && sample.stats != nil {
			out.ObservedAt, out.AgeMS, out.Fresh = evidenceTime(sample.at, s.now(), clientEvidenceFreshness)
			if stat, ok := sample.stats[before.Peer.PublicKey]; ok {
				wg := clientWGObservation{RxBytes: safeEvidenceCounter(stat.RxBytes), TxBytes: safeEvidenceCounter(stat.TxBytes)}
				if !stat.LastHandshake.IsZero() && !stat.LastHandshake.After(sample.at) {
					at := stat.LastHandshake.UTC()
					wg.LastHandshake = &at
				}
				out.WG = &wg
				out.State = "observed"
				out.Reason = "aggregate_peer_counters"
				if wg.RxBytes == nil || wg.TxBytes == nil {
					out.Reason = "unsafe_counter"
				}
				if stat.LastHandshake.After(sample.at) {
					out.Reason = "future_handshake"
				}
			} else {
				out.Reason = "peer_not_in_source"
			}
		}
	} else if req.Transport == "reality" {
		activity := s.observeReality(r.Context(), []store.Peer{before.Peer})[req.PeerID]
		if activity.State == "observed" && activity.ObservedAt != nil {
			out.State = "observed"
			out.Reason = "current_open_flows"
			out.ObservedAt, out.AgeMS, out.Fresh = evidenceTime(*activity.ObservedAt, s.now(), clientEvidenceFreshness)
			values := clientRealityObservation{OpenFlows: activity.OpenFlows}
			if activity.OpenUploadBytes != nil {
				values.OpenUploadBytes = safeEvidenceCounter(*activity.OpenUploadBytes)
			}
			if activity.OpenDownloadBytes != nil {
				values.OpenDownloadBytes = safeEvidenceCounter(*activity.OpenDownloadBytes)
			}
			if values.OpenFlows != nil && (*values.OpenFlows < 0 || int64(*values.OpenFlows) > maxEvidenceCounter) {
				values.OpenFlows = nil
			}
			if values.OpenFlows == nil || values.OpenUploadBytes == nil || values.OpenDownloadBytes == nil {
				out.Reason = "partial_counters"
			}
			out.Reality = &values
		}
	}
	after, err := s.evidenceAuthority(r.Context(), req.PeerID, req.Transport)
	out.SampledAt = s.now().UTC()
	if err != nil || after.Epoch != before.Epoch || r.Context().Err() != nil {
		out.State = "unknown"
		out.Reason = "authority_changed"
		out.Epoch = nil
		out.WG = nil
		out.Reality = nil
		out.ObservedAt = nil
		out.AgeMS = nil
		out.Fresh = false
	} else if out.ObservedAt != nil {
		out.ObservedAt, out.AgeMS, out.Fresh = evidenceTime(*out.ObservedAt, out.SampledAt, clientEvidenceFreshness)
		if !out.Fresh {
			out.State = "unknown"
			out.Reason = "stale_source"
			out.WG = nil
			out.Reality = nil
		}
	}
	writeJSON(w, s.log, 200, out)
}

type clientHTTPSObservation struct {
	State      string     `json:"state"`
	Reason     string     `json:"reason"`
	Source     *string    `json:"source"`
	Scope      string     `json:"scope"`
	ObservedAt *time.Time `json:"observed_at"`
	AgeMS      *int64     `json:"age_ms"`
	Fresh      bool       `json:"fresh"`
	DelayMS    *int64     `json:"delay_ms"`
}

type clientExitResponse struct {
	Scope     string                    `json:"scope"`
	PeerID    string                    `json:"peer_id"`
	Transport string                    `json:"transport"`
	Domain    string                    `json:"domain"`
	Epoch     *string                   `json:"epoch"`
	SampledAt time.Time                 `json:"sampled_at"`
	Policy    singbox.PolicyExplanation `json:"policy"`
	HTTPS     clientHTTPSObservation    `json:"https"`
}

func (s *Server) handleClientCheckExit(w http.ResponseWriter, r *http.Request) {
	var req singbox.ExplainRequest
	if !decodeEvidenceRequest(w, r, &req) || !validEvidenceClient(req.PeerID, req.Transport) {
		writeError(w, s.log, 400, codeBadRequest, "Choose a client, ingress and domain only.")
		return
	}
	domain, err := singbox.NormalizeExplainDomain(req.Domain)
	if err != nil {
		writeError(w, s.log, 400, codeBadRequest, "Enter a domain without URL, IP, port, path or CIDR.")
		return
	}
	req.Domain = domain
	// Separate from passive source reads. A canceled probe still owns its broker lease.
	select {
	case s.exitEvidenceSlot <- struct{}{}:
		defer func() { <-s.exitEvidenceSlot }()
	default:
		writeError(w, s.log, 429, codeTooMany, "An exit observation is already running.")
		return
	}
	out := clientExitResponse{Scope: "gateway_exit_https", PeerID: req.PeerID, Transport: req.Transport, Domain: domain, SampledAt: s.now().UTC(), Policy: singbox.UnknownExplanation(req, "authority_unavailable"), HTTPS: clientHTTPSObservation{State: "not_run", Reason: "authority_unavailable", Scope: "gateway_fixed_target_https"}}
	pub, ok := s.applier.(ClientEvidencePublisher)
	if !ok {
		if _, err = s.store.RouteEligibility(r.Context(), req.PeerID); errors.Is(err, store.ErrNotFound) {
			writeError(w, s.log, 404, codeNotFound, "Client no longer exists.")
			return
		}
		writeJSON(w, s.log, 200, out)
		return
	}
	plan, err := pub.ClientExitPlan(r.Context(), s.store, req)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, s.log, 404, codeNotFound, "Client no longer exists.")
		return
	}
	if err != nil {
		writeJSON(w, s.log, 200, out)
		return
	}
	out.Policy = plan.Policy
	if plan.Authority.Epoch == "" {
		out.HTTPS.Reason = plan.Authority.Reason
		writeJSON(w, s.log, 200, out)
		return
	}
	out.Epoch = &plan.Authority.Epoch
	if plan.Tag == "" {
		out.HTTPS.Reason = "route_not_known"
		if plan.Policy.Routing.Action == "reject" || plan.Policy.GatewayA.Action == "reject" {
			out.HTTPS.Reason = "policy_reject"
		}
		writeJSON(w, s.log, 200, out)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	// Check immediately before dispatch as compiler work can overlap publication.
	before, err := s.evidenceAuthority(r.Context(), req.PeerID, req.Transport)
	if err != nil || before.Epoch != plan.Authority.Epoch {
		out.Epoch = nil
		out.Policy = singbox.UnknownExplanation(req, "authority_changed")
		out.HTTPS.Reason = "authority_changed"
		writeJSON(w, s.log, 200, out)
		return
	}
	// Keep monotonic clock data private. Only jobs admitted after this final
	// coherent authority check can be attributed to the captured lifetime.
	probeFence := time.Now()
	if plan.Pool {
		out.HTTPS.Scope = "runtime_pool_history"
		source := "pool_history"
		out.HTTPS.Source = &source
		out.HTTPS.State = "unknown"
		out.HTTPS.Reason = "no_pool_history"
		proxies, e := s.proxies().Proxies(r.Context())
		if e == nil {
			if p, exists := proxies[plan.Tag]; exists && len(p.History) > 0 {
				h := p.History[len(p.History)-1]
				out.HTTPS.ObservedAt, out.HTTPS.AgeMS, out.HTTPS.Fresh = evidenceTime(h.Time, s.now(), 5*time.Minute)
				out.HTTPS.Reason = "probe_unconfirmed"
				if !out.HTTPS.Fresh {
					out.HTTPS.Reason = "stale_source"
				} else if h.Delay > 0 {
					ms := int64(h.Delay)
					out.HTTPS.DelayMS = &ms
					out.HTTPS.State = "observed"
					out.HTTPS.Reason = "pool_history_only"
				}
			}
		}
	} else {
		source := "active_probe"
		out.HTTPS.Source = &source
		out.HTTPS.State = "unknown"
		out.HTTPS.Reason = "probe_unconfirmed"
		delay, started, at, e := s.clash.DelayObserved(r.Context(), plan.Tag)
		out.HTTPS.ObservedAt, out.HTTPS.AgeMS, out.HTTPS.Fresh = evidenceTime(at, s.now(), clientEvidenceFreshness)
		if !started.After(probeFence) {
			out.HTTPS.ObservedAt = nil
			out.HTTPS.AgeMS = nil
			out.HTTPS.Fresh = false
			out.HTTPS.Reason = "source_lifetime_unknown"
		} else if e == nil && out.HTTPS.Fresh {
			ms := delay.Milliseconds()
			out.HTTPS.DelayMS = &ms
			out.HTTPS.State = "observed"
			out.HTTPS.Reason = "fixed_target_succeeded"
		}
	}
	after, err := s.evidenceAuthority(r.Context(), req.PeerID, req.Transport)
	out.SampledAt = s.now().UTC()
	if err != nil || after.Epoch != before.Epoch || r.Context().Err() != nil {
		out.Epoch = nil
		out.Policy = singbox.UnknownExplanation(req, "authority_changed")
		out.HTTPS = clientHTTPSObservation{State: "unknown", Reason: "authority_changed", Scope: "gateway_fixed_target_https"}
	} else if out.HTTPS.ObservedAt != nil {
		limit := clientEvidenceFreshness
		if plan.Pool {
			limit = 5 * time.Minute
		}
		out.HTTPS.ObservedAt, out.HTTPS.AgeMS, out.HTTPS.Fresh = evidenceTime(*out.HTTPS.ObservedAt, out.SampledAt, limit)
		if !out.HTTPS.Fresh {
			out.HTTPS.State = "unknown"
			out.HTTPS.DelayMS = nil
			out.HTTPS.Reason = "stale_source"
		}
	}
	writeJSON(w, s.log, 200, out)
}
