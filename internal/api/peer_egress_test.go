package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestPeerEgressPatchNullableAndReferences(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	peer := createPeer(t, ts, cookie, "Client")
	res := ts.auth(t, cookie, http.MethodPost, "/api/tunnels", `{"name":"Test exit","raw":"`+vlessKeyLink+`"}`)
	requireCode(t, res, http.StatusCreated)
	var target tunnelResponse
	decodeJSONBody(t, res, &target)
	patch := func(body string, status int) peerResponse {
		t.Helper()
		res := ts.auth(t, cookie, http.MethodPatch, "/api/peers/"+peer.ID, body)
		requireCode(t, res, status)
		var p peerResponse
		if status == http.StatusOK {
			decodeJSONBody(t, res, &p)
		}
		return p
	}
	got := patch(`{"egress_tunnel_id":"`+target.ID+`"}`, http.StatusOK)
	if got.EgressTunnelID == nil || *got.EgressTunnelID != target.ID {
		t.Fatal("choice absent in API")
	}
	got = patch(`{"name":"Renamed"}`, http.StatusOK)
	if got.EgressTunnelID == nil {
		t.Fatal("omitted patch cleared choice")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+target.ID, ""), http.StatusConflict)
	for _, bad := range []string{`{"egress_tunnel_id":"missing"}`, `{"egress_tunnel_id":""}`, `{"egress_tunnel_id":true}`, `{"egress_tunnel_id":{}}`} {
		patch(bad, http.StatusBadRequest)
	}
	got = patch(`{"egress_tunnel_id":null}`, http.StatusOK)
	if got.EgressTunnelID != nil {
		t.Fatal("explicit null did not clear choice")
	}
	requireCode(t, ts.auth(t, cookie, http.MethodDelete, "/api/tunnels/"+target.ID, ""), http.StatusOK)
	patch(`{"egress_tunnel_id":"`+target.ID+`"}`, http.StatusBadRequest)
}

type pendingFakeApplier struct {
	fakeApplier
	dirty      bool
	pendingErr error
}

func (a *pendingFakeApplier) Pending(context.Context, *store.Store) (bool, error) {
	return a.dirty, a.pendingErr
}

func TestApplyStatusAuthenticatedAndReadOnly(t *testing.T) {
	ap := &pendingFakeApplier{}
	ts := withApplier(newTestServer(t), ap)
	requireCode(t, ts.do(t, request{method: http.MethodGet, path: "/api/apply/status"}), http.StatusUnauthorized)
	cookie := ts.login(t)
	for _, want := range []bool{false, true, false} {
		ap.dirty = want
		res := ts.auth(t, cookie, http.MethodGet, "/api/apply/status", "")
		requireCode(t, res, http.StatusOK)
		var status map[string]bool
		decodeJSONBody(t, res, &status)
		if status["dirty"] != want || ap.calls != 0 {
			t.Fatal("status is incorrect or published changes")
		}
	}
	ap.pendingErr = errors.New("fixture status failure")
	requireCode(t, ts.auth(t, cookie, http.MethodGet, "/api/apply/status", ""), http.StatusInternalServerError)
}
