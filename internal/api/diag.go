package api

import (
	"fmt"
	"net/http"

	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// Статусы проверки из docs/05-api.md.
const (
	statusOK      = "ok"
	statusWarn    = "warn"
	statusError   = "error"
	statusUnknown = "unknown"
)

// check — одна строка диагностики.
type check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// diagResponse — `GET /api/diag`.
type diagResponse struct {
	Checks  []check `json:"checks"`
	Overall string  `json:"overall"`
}

// notReady — детализация проверки, источника которой ещё нет в коде. Проверка не
// пропускается и не подделывается под «ok»: «неизвестно» это состояние, а не
// отсутствие строки в списке.
func notReady(layer string) string {
	return "данных нет: слой " + layer + " ещё не написан"
}

// handleDiag — `GET /api/diag`.
//
// По-настоящему проверяется пока одно — собирается ли конфиг sing-box из текущего
// состояния БД: это чистая функция, ей не нужны ни root, ни сеть. Остальное
// (wg0, nftables, forwarding, path MTU, доступность туннелей, свежесть списков)
// требует netstack, lists-планировщика и клиента Clash API и отдаётся как unknown.
func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	snap, err := s.store.Snapshot(r.Context())
	if err != nil {
		s.storeError(w, err, "Состояние не прочитано")
		return
	}

	checks := []check{
		{ID: "wg", Title: "WireGuard", Status: statusUnknown, Detail: notReady("netstack")},
		singboxCheck(snap),
		{ID: "nft", Title: "Правила nftables", Status: statusUnknown, Detail: notReady("netstack")},
		{
			ID: "tunnels", Title: "Туннели", Status: statusUnknown,
			Detail: "проверка доступности появится с клиентом Clash API",
		},
		{
			ID: "lists", Title: "Списки", Status: statusUnknown,
			Detail: "планировщик обновления списков ещё не подключён к демону",
		},
		{ID: "forward", Title: "IP forwarding", Status: statusUnknown, Detail: notReady("netstack")},
		{ID: "mtu", Title: "Path MTU", Status: statusUnknown, Detail: notReady("netstack")},
	}

	writeJSON(w, s.log, http.StatusOK, diagResponse{Checks: checks, Overall: overall(checks)})
}

// singboxCheck собирает конфиг из снимка состояния. Ошибка здесь означает, что
// применить конфигурацию не удастся — например, правило ссылается на туннель,
// которого нет.
func singboxCheck(snap store.Snapshot) check {
	c := check{ID: "singbox", Title: "sing-box"}
	opts, err := singbox.Generate(snap)
	if err != nil {
		c.Status = statusError
		c.Detail = err.Error()
		return c
	}
	if _, err := singbox.Marshal(opts); err != nil {
		c.Status = statusError
		c.Detail = err.Error()
		return c
	}
	c.Status = statusOK
	c.Detail = fmt.Sprintf("конфиг собирается: туннелей %d, правил %d",
		len(snap.Tunnels), len(snap.Rules))
	return c
}

// overall — худший из статусов. «Неизвестно» хуже «ok», но лучше предупреждения:
// это не поломка, а невыполненная проверка.
func overall(checks []check) string {
	rank := map[string]int{statusOK: 0, statusUnknown: 1, statusWarn: 2, statusError: 3}
	worst := statusOK
	for _, c := range checks {
		if rank[c.Status] > rank[worst] {
			worst = c.Status
		}
	}
	return worst
}
