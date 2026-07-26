package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// poolRefresher обходит каталог одного пула по требованию. Интерфейс, а не
// *lists.PoolManager: в тестах поднимать расписание и ходить в сеть незачем.
type poolRefresher interface {
	RefreshTunnel(ctx context.Context, id string) (bool, error)
}

// clashProxies — то, что этому файлу нужно от клиента Clash API.
type clashProxies interface {
	Proxies(ctx context.Context) (map[string]clash.Proxy, error)
}

// poolResponse — блок `pool` туннеля в ответе списка.
//
// ServersAlive и Current заполняются из Clash API: живых считает сам `urltest`,
// и вторая собственная проверялка расходилась бы с той, по которой идёт ротация.
// Оба поля указатели: недоступный Clash означает «неизвестно», а не «ноль живых» —
// ноль был бы утверждением о пуле, которого никто не спрашивал.
type poolResponse struct {
	CatalogURL   string             `json:"catalog_url"`
	ServersTotal int                `json:"servers_total"`
	ServersAlive *int               `json:"servers_alive"`
	Current      *poolCurrentServer `json:"current"`
	UpdatedAt    *time.Time         `json:"updated_at"`
	NextUpdateAt *time.Time         `json:"next_update_at"`
}

// poolCurrentServer — сервер, который группа выбрала прямо сейчас.
type poolCurrentServer struct {
	Name      string `json:"name"`
	Country   string `json:"country"`
	LatencyMS *int   `json:"latency_ms"`
}

// newPoolResponse собирает блок из того, что лежит в БД. Живое состояние
// добавляет [Server.withPoolState] — оно требует запроса к sing-box.
func newPoolResponse(t store.Tunnel, interval time.Duration) *poolResponse {
	if t.Source != store.SourcePool {
		return nil
	}
	// В Raw пула лежит URL каталога — см. [store.SourcePool].
	out := &poolResponse{CatalogURL: t.Raw, ServersTotal: len(t.Pool)}
	if !t.PoolUpdatedAt.IsZero() {
		at := t.PoolUpdatedAt.UTC()
		next := at.Add(interval)
		out.UpdatedAt, out.NextUpdateAt = &at, &next
	}
	return out
}

// withPoolState дописывает в блоки пулов живое состояние из Clash API.
//
// Один запрос на весь список, а не по одному на туннель, и только если включённый
// пул в списке есть: недоступный sing-box иначе стоил бы таймаута на каждой
// отрисовке экрана «Туннели». Ошибка наружу не выносится — поля остаются null, и
// карточка показывает пул без статистики вместо ошибки на весь список.
func (s *Server) withPoolState(ctx context.Context, out []tunnelResponse, byID map[string]store.Tunnel) {
	var live bool
	for i := range out {
		if out[i].Pool != nil && out[i].Enabled {
			live = true
			break
		}
	}
	if !live {
		return
	}

	proxies, err := s.proxies().Proxies(ctx)
	if err != nil {
		s.log.Debug("живое состояние пулов недоступно", "ошибка", err)
		return
	}

	for i := range out {
		// Выключенный пул статистики не получает: цифры живых серверов у
		// туннеля, которого нет в конфиге, — ложь, а не ноль.
		if out[i].Pool == nil || !out[i].Enabled {
			continue
		}
		members := singbox.PoolMembers(byID[out[i].ID])

		alive := 0
		for tag := range members {
			if p, ok := proxies[tag]; ok {
				if _, up := p.Latency(); up {
					alive++
				}
			}
		}
		out[i].Pool.ServersAlive = &alive

		group, ok := proxies[singbox.TunnelTag(out[i].ID)]
		if !ok || group.Now == "" {
			continue
		}
		srv, ok := members[group.Now]
		if !ok {
			continue
		}
		cur := &poolCurrentServer{Name: srv.Title, Country: srv.Country}
		if ms, up := proxies[group.Now].Latency(); up {
			v := int(ms / time.Millisecond)
			cur.LatencyMS = &v
		}
		out[i].Pool.Current = cur
	}
}

// handleRefreshPool — `POST /api/tunnels/{id}/refresh`.
//
// Обходит каталог пула и обновляет состав в БД. Конфиг здесь не применяется: как и
// у остальных правок туннелей, применение — отдельный `POST /api/apply`, иначе одна
// кнопка перезапускала бы sing-box в обход общего порядка.
func (s *Server) handleRefreshPool(w http.ResponseWriter, r *http.Request) {
	id, ok := s.idFrom(w, r)
	if !ok {
		return
	}
	t, err := s.store.Tunnel(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	if t.Source != store.SourcePool {
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Обновлять каталог можно только у туннеля-пула")
		return
	}
	if s.pools == nil {
		writeError(w, s.log, http.StatusServiceUnavailable, codeNotReady,
			"Обновление каталога недоступно: расписание не запущено")
		return
	}

	if _, err := s.pools.RefreshTunnel(r.Context(), id); err != nil {
		if errors.Is(err, lists.ErrPoolNotFound) {
			s.storeError(w, store.ErrNotFound, "Туннель не найден")
			return
		}
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Не удалось обойти каталог: "+err.Error())
		return
	}

	// Ответ — сам туннель: карточка обновляется одним ответом, без второго запроса
	// за списком.
	fresh, err := s.store.Tunnel(r.Context(), id)
	if err != nil {
		s.storeError(w, err, "Туннель не найден")
		return
	}
	res := s.withCheck(newTunnelResponse(fresh, s.poolInterval()))
	s.withPoolState(r.Context(), []tunnelResponse{res}, map[string]store.Tunnel{id: fresh})
	writeJSON(w, s.log, http.StatusOK, res)
}

// poolInterval — период обхода каталога, от которого считается next_update_at.
func (s *Server) poolInterval() time.Duration {
	if s.poolEvery > 0 {
		return s.poolEvery
	}
	return lists.DefaultPoolInterval
}

// proxies отдаёт источник живого состояния: настоящий клиент Clash API либо
// подставленный тестом.
func (s *Server) proxies() clashProxies {
	if s.poolProxies != nil {
		return s.poolProxies
	}
	return s.clash
}
