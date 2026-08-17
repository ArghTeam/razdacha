package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/ArghTeam/razdacha/internal/clash"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// poolRefresher обходит каталог одного пула по требованию. Интерфейс, а не
// *lists.PoolManager: в тестах поднимать расписание и ходить в сеть незачем.
//
// Пул передаётся целиком: ручка уже прочитала его из БД, и обход по требованию не
// должен зависеть от того, успело ли расписание сверить свой набор с БД (issue #74).
type poolRefresher interface {
	RefreshPool(ctx context.Context, t lists.PoolTunnel) (bool, error)
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
		cur := &poolCurrentServer{Name: srv.Title}
		if ms, up := proxies[group.Now].Latency(); up {
			v := int(ms / time.Millisecond)
			cur.LatencyMS = &v
		}
		out[i].Pool.Current = cur
	}
}

// poolServersResponse — ответ `GET /api/tunnels/{id}/pool`: состав каталога по одному
// серверу. Отдельной ручкой, а не полем списка туннелей: каталог — это сотня с лишним
// записей, и возить их в каждом ответе экрана «Туннели» незачем.
type poolServersResponse struct {
	CatalogURL string               `json:"catalog_url"`
	UpdatedAt  *time.Time           `json:"updated_at"`
	Servers    []poolServerResponse `json:"servers"`
}

// poolServerResponse — один сервер каталога.
//
// Ссылки на серверы здесь нет намеренно: в ней UUID или пароль ключа, а на экране от
// неё пользы нет. Alive и LatencyMS — указатели, потому что осмысленны они только для
// серверов в ротации: остальных никто не проверял, и false был бы утверждением о
// непроверенном сервере, а ноль — «ноль миллисекунд».
//
// PingMS — тоже указатель: пинг перестал быть обязательным. Прежний каталог показывал
// его на карточке, outlinekeys не измеряет задержку вовсе (ADR 0015), и ноль в этом
// поле читался бы как «ноль миллисекунд», а не «неизвестно».
type poolServerResponse struct {
	Title      string `json:"title"`
	PingMS     *int   `json:"ping_ms"`
	InRotation bool   `json:"in_rotation"`
	Alive      *bool  `json:"alive"`
	LatencyMS  *int   `json:"latency_ms"`
	Current    bool   `json:"current"`
}

// handlePoolServers — `GET /api/tunnels/{id}/pool`.
func (s *Server) handlePoolServers(w http.ResponseWriter, r *http.Request) {
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
			"Список серверов есть только у туннеля-пула")
		return
	}

	out := poolServersResponse{CatalogURL: t.Raw, Servers: s.poolServers(r.Context(), t)}
	if !t.PoolUpdatedAt.IsZero() {
		at := t.PoolUpdatedAt.UTC()
		out.UpdatedAt = &at
	}
	writeJSON(w, s.log, http.StatusOK, out)
}

// poolServers переводит состав пула из БД в ответ и дописывает живое состояние
// участников ротации из Clash API.
//
// Ротацию считает не этот файл: набор участников и их теги даёт [singbox.PoolMembers] —
// тот же отбор, что попал в конфиг. Свой второй отбор разошёлся бы с генератором.
func (s *Server) poolServers(ctx context.Context, t store.Tunnel) []poolServerResponse {
	tagByURL := make(map[string]string, len(t.Pool))
	for tag, srv := range singbox.PoolMembers(t) {
		tagByURL[srv.URL] = tag
	}

	// Выключенный пул живого состояния не получает: его нет в конфиге, и цифры о
	// нём были бы ложью, а не нулём. Ошибка Clash API тоже оставляет поля пустыми.
	var (
		proxies map[string]clash.Proxy
		current string
	)
	if t.Enabled && len(tagByURL) > 0 {
		got, err := s.proxies().Proxies(ctx)
		if err != nil {
			s.log.Debug("живое состояние серверов пула недоступно", "ошибка", err)
		} else {
			proxies = got
			if group, ok := proxies[singbox.TunnelTag(t.ID)]; ok {
				current = group.Now
			}
		}
	}

	out := make([]poolServerResponse, 0, len(t.Pool))
	seen := make(map[string]bool, len(t.Pool))
	for _, srv := range t.Pool {
		if srv.URL == "" || seen[srv.URL] {
			continue
		}
		seen[srv.URL] = true

		tag, inRotation := tagByURL[srv.URL]
		item := poolServerResponse{
			Title:      srv.Title,
			InRotation: inRotation,
			Current:    inRotation && tag == current,
		}
		// Нулевой пинг в БД означает «источник его не дал», а не «ноль миллисекунд».
		if srv.PingMS > 0 {
			ping := srv.PingMS
			item.PingMS = &ping
		}
		if p, ok := proxies[tag]; inRotation && ok {
			ms, up := p.Latency()
			item.Alive = &up
			if up {
				v := int(ms / time.Millisecond)
				item.LatencyMS = &v
			}
		}
		out = append(out, item)
	}

	sortPoolServers(out)
	return out
}

// sortPoolServers ставит ротацию впереди: сначала она по измеренной задержке, затем
// остальные по пингу карточки. Порядок задаёт сервер, а не панель — иначе каждая
// отрисовка пересобирала бы его своей сортировкой.
func sortPoolServers(v []poolServerResponse) {
	sort.SliceStable(v, func(i, j int) bool {
		a, b := v[i], v[j]
		if a.InRotation != b.InRotation {
			return a.InRotation
		}
		if a.InRotation {
			if la, lb := poolLatencyRank(a), poolLatencyRank(b); la != lb {
				return la < lb
			}
		}
		if pa, pb := poolPingRank(a.PingMS), poolPingRank(b.PingMS); pa != pb {
			return pa < pb
		}
		return a.Title < b.Title
	})
}

// poolLatencyRank: измеренная задержка идёт первой, непроверенный — за живыми,
// не ответивший — последним. Мёртвый сервер в начале списка сбивал бы с толку.
func poolLatencyRank(s poolServerResponse) int {
	switch {
	case s.LatencyMS != nil:
		return *s.LatencyMS
	case s.Alive == nil:
		return 1 << 20
	default:
		return 1 << 21
	}
}

// poolPingRank: пинг карточки, где отсутствие означает «источник его не даёт».
func poolPingRank(ping *int) int {
	if ping == nil || *ping <= 0 {
		return 1 << 20
	}
	return *ping
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

	// Пул передаётся расписанию целиком: искать его в наборе, который сверяется с
	// БД раз в полминуты, значит отвечать отказом на пуле, который только что
	// появился, — и отказ этот выглядел бы как «туннеля нет» (issue #74).
	if _, err := s.pools.RefreshPool(r.Context(), lists.PoolTunnelFrom(t)); err != nil {
		s.poolCrawlError(w, err)
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

// poolCrawlError переводит отказ обхода каталога в код ответа.
//
// Устроен как [Server.storeError]: сентинелы означают решение пользователя, всё
// остальное — сбой. Каталог без драйвера — это адрес, который вписал человек, и
// `502 internal` про него врал про сбой демона; запись того же адреса отвечает `400`
// (см. `tunnelType`), и два пути к одной ошибке обязаны сходиться (issue #156). Текст
// при этом идёт наружу как есть — он и раньше был верным.
func (s *Server) poolCrawlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, lists.ErrPoolCrawlBusy):
		// Свой текст, а не сырая ошибка: пользователю важно не то, что каталог
		// занят, а то, что делать ничего не нужно — состав обновится сам.
		writeError(w, s.log, http.StatusConflict, codeConflict,
			"Каталог уже обходится — состав пула обновится сам")
	case errors.Is(err, lists.ErrNoPoolDriver), errors.Is(err, store.ErrInvalid):
		writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
			"Не удалось обойти каталог: "+err.Error())
	default:
		writeError(w, s.log, http.StatusBadGateway, codeInternal,
			"Не удалось обойти каталог: "+err.Error())
	}
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
