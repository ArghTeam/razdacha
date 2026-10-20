package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

func TestDeletePeerCommunicatesInactiveRulesAndRequiresReassignment(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	a := createPeer(t, ts, cookie, "ClientA")
	b := createPeer(t, ts, cookie, "ClientB")
	r, err := ts.st.CreateRule(context.Background(), store.Rule{Name: "Private rule", Action: store.ActionBlock, Enabled: true, PeerScope: store.ScopeSelected, PeerIDs: []string{a.ID}, Domains: []string{"private.example"}})
	if err != nil {
		t.Fatal(err)
	}
	res := ts.auth(t, cookie, http.MethodDelete, "/api/peers/"+a.ID, "")
	requireCode(t, res, http.StatusOK)
	var deleted struct {
		OK bool `json:"ok"`
		store.PeerDeletion
	}
	decodeJSONBody(t, res, &deleted)
	if !deleted.OK || len(deleted.AffectedRuleIDs) != 1 || deleted.AffectedRuleIDs[0] != r.ID || len(deleted.InactivatedRuleIDs) != 1 || deleted.InactivatedRuleIDs[0] != r.ID {
		t.Fatalf("missing effects: %+v", deleted)
	}
	var rules []ruleResponse
	res = ts.auth(t, cookie, http.MethodGet, "/api/rules", "")
	requireCode(t, res, http.StatusOK)
	decodeJSONBody(t, res, &rules)
	if len(rules) != 1 || rules[0].Enabled || rules[0].PeerScope != store.ScopeSelected || rules[0].InactiveReason != "no_selected_peers" {
		t.Fatalf("orphan response: %+v", rules)
	}
	res = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+r.ID, `{"name":"Edited orphan"}`)
	requireCode(t, res, http.StatusOK)
	res = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+r.ID, `{"enabled":true}`)
	requireCode(t, res, http.StatusBadRequest)
	body, _ := json.Marshal(map[string]any{"peer_ids": []string{a.ID}, "enabled": true})
	res = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+r.ID, string(body))
	requireCode(t, res, http.StatusBadRequest)
	body, _ = json.Marshal(map[string]any{"peer_ids": []string{b.ID}})
	res = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+r.ID, string(body))
	requireCode(t, res, http.StatusOK)
	assigned, _ := ts.st.Rule(context.Background(), r.ID)
	if assigned.Enabled {
		t.Fatal("assignment enabled orphan")
	}
	res = ts.auth(t, cookie, http.MethodPatch, "/api/rules/"+r.ID, `{"enabled":true}`)
	requireCode(t, res, http.StatusOK)
}
