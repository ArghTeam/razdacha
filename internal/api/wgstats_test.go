package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
)

// TestPeerDerivedFieldsFromNetstack — замеры с wg0 доезжают до UI. Пир, которого
// на интерфейсе нет, остаётся с null: «нет данных» и «ноль байт» — разное.
func TestPeerDerivedFieldsFromNetstack(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	online := createPeer(t, ts, cookie, "iPhone")
	createPeer(t, ts, cookie, "MacBook")

	handshake := ts.clock().Add(-time.Minute)
	ts.peerStat = func(context.Context) (map[string]netstack.WGPeerStat, error) {
		return map[string]netstack.WGPeerStat{
			online.PublicKey: {
				PublicKey:     online.PublicKey,
				LastHandshake: handshake,
				RxBytes:       4096,
				TxBytes:       2048,
				Endpoint:      "203.0.113.7:44100",
			},
		}, nil
	}

	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	requireCode(t, resp, http.StatusOK)
	var list []peerResponse
	decodeJSONBody(t, resp, &list)
	if len(list) != 2 {
		t.Fatalf("в списке %d пиров, ожидалось 2", len(list))
	}

	got := list[0]
	if got.ID != online.ID {
		got = list[1]
	}
	if got.Online == nil || !*got.Online {
		t.Error("пир с хендшейком минуту назад показан не онлайн")
	}
	if got.RxBytes == nil || *got.RxBytes != 4096 || got.TxBytes == nil || *got.TxBytes != 2048 {
		t.Errorf("счётчики не доехали: rx=%v tx=%v", got.RxBytes, got.TxBytes)
	}
	if got.Endpoint == nil || *got.Endpoint != "203.0.113.7:44100" {
		t.Errorf("endpoint = %v", got.Endpoint)
	}
	if got.LastHandshake == nil || !got.LastHandshake.Equal(handshake) {
		t.Errorf("last_handshake = %v, ожидался %v", got.LastHandshake, handshake)
	}

	other := list[0]
	if other.ID == online.ID {
		other = list[1]
	}
	if other.Online != nil || other.RxBytes != nil || other.Endpoint != nil {
		t.Errorf("пира нет на интерфейсе, а поля заполнены: %+v", other)
	}
}

// TestPeerStatsErrorDoesNotBreakList — интерфейс не отвечает: список пиров
// показывается без статистики, а не превращается в ошибку.
func TestPeerStatsErrorDoesNotBreakList(t *testing.T) {
	ts := newTestServer(t)
	cookie := ts.login(t)
	createPeer(t, ts, cookie, "iPhone")
	ts.peerStat = func(context.Context) (map[string]netstack.WGPeerStat, error) {
		return nil, netstack.ErrWGNoDevice
	}

	resp := ts.auth(t, cookie, http.MethodGet, "/api/peers", "")
	requireCode(t, resp, http.StatusOK)
	var raw []map[string]json.RawMessage
	decodeJSONBody(t, resp, &raw)
	if len(raw) != 1 || string(raw[0]["online"]) != "null" {
		t.Errorf("ожидался список с null в производных полях: %v", raw)
	}
}
