package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/ArghTeam/razdacha/internal/netstack"
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

// diagMTU — MTU клиентов и wg0 (ADR 0004). Расхождение — баг, а не настройка:
// у клиента нет способа узнать, что сервер поднял интерфейс с другим MTU.
const diagMTU = 1280

// diagStaleFactor — во сколько интервалов обновления списки считаются
// протухшими. Одного интервала мало: прогон занимает время, и проверка сразу
// после срока показывала бы предупреждение на исправной системе.
const diagStaleFactor = 2

// errDiagNoSource — источника данных проверки в демоне нет. Отдельная ошибка,
// а не пустой снимок: «unknown» обязан объяснять, чего не хватает.
var errDiagNoSource = errors.New("источник данных не подключён к демону")

// check — одна строка диагностики.
type check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// diagResponse — `GET /api/diag`.
//
// Overall — худший из вернувшихся статусов. При запуске одной проверки
// (`POST /api/diag/run?check=…`) в ответе она одна, и сводка по ней же:
// собрать общий статус из семи проверок в этом случае может только тот, кто
// их все и запускал, — панель.
type diagResponse struct {
	Checks []check `json:"checks"`
	// CheckedAt — когда проверки отработали. Панель показывает время
	// последней проверки, и брать его с часов браузера нельзя: свежим
	// выглядел бы и снимок часовой давности.
	CheckedAt time.Time `json:"checked_at"`
	Overall   string    `json:"overall"`
}

// Идентификаторы проверок. Порядок фиксирован: в нём проверки идут в сводке и
// в нём же панель прогоняет их по одной, показывая ход.
const (
	checkWG      = "wg"
	checkSingbox = "singbox"
	checkNft     = "nft"
	checkTunnels = "tunnels"
	checkLists   = "lists"
	checkForward = "forward"
	checkMTU     = "mtu"
)

// diagCheckIDs — полный набор проверок в порядке показа.
var diagCheckIDs = []string{
	checkWG, checkSingbox, checkNft, checkTunnels, checkLists, checkForward, checkMTU,
}

// DiagLists — свежесть кэша списков глазами диагностики. Заполняет
// планировщик слоя lists; пока он не подключён к демону, источника нет.
type DiagLists struct {
	// Sources — сколько списков требуют правила.
	Sources int
	// Loaded — сколько из них лежит в кэше разобранными.
	Loaded int
	// LastRefresh — когда расписание отработало в последний раз. Нулевое
	// время означает, что первый прогон ещё не закончился.
	LastRefresh time.Time
}

// DiagSources — источники данных диагностики.
//
// Поля независимы: пустое означает «источника нет», и проверка отвечает
// unknown с объяснением, а не пропадает из сводки (docs/05-api.md). Функции, а
// не один интерфейс, потому что подключаются они порознь: wg0 живёт в демоне
// на любой платформе, nftables — только под Linux, планировщик списков
// приезжает отдельной задачей.
type DiagSources struct {
	// WG — состояние интерфейса клиентов, [netstack.WGManager.DiagState].
	WG func(context.Context) (netstack.DiagWGState, error)
	// Nft — состояние таблицы `inet razdacha`, [netstack.DiagNft].
	Nft func(context.Context) (netstack.DiagNftState, error)
	// IPForward — net.ipv4.ip_forward, [netstack.DiagIPForward].
	IPForward func(context.Context) (bool, error)
	// Lists — свежесть кэша списков.
	Lists func(context.Context) (DiagLists, error)
}

// handleDiag — `GET /api/diag` и `POST /api/diag/run[?check=<id>]`.
//
// Каждая проверка читает свой источник и отвечает по существу. Источник
// недоступен — проверка остаётся в сводке со статусом unknown и причиной в
// detail: молчаливое «данных нет» скрывает и поломку, и неподключённый слой.
//
// `?check=<id>` прогоняет одну проверку. Панель пользуется этим, чтобы
// показывать ход перезапуска построчно: пока проверка не ответила, строка
// говорит «проверяется», а не выдаёт прошлый результат за новый.
func (s *Server) handleDiag(w http.ResponseWriter, r *http.Request) {
	ids := diagCheckIDs
	if one := r.URL.Query().Get("check"); one != "" {
		if !diagKnownCheck(one) {
			writeError(w, s.log, http.StatusBadRequest, codeBadRequest,
				fmt.Sprintf("Проверки %q нет. Доступны: %s", one, strings.Join(diagCheckIDs, ", ")))
			return
		}
		ids = []string{one}
	}

	ctx := r.Context()
	snap, err := s.store.Snapshot(ctx)
	if err != nil {
		s.storeError(w, err, "Состояние не прочитано")
		return
	}

	in := s.diagRead(ctx, snap, ids)
	checks := make([]check, 0, len(ids))
	for _, id := range ids {
		checks = append(checks, s.diagCheck(id, in))
	}

	writeJSON(w, s.log, http.StatusOK, diagResponse{
		Checks:    checks,
		CheckedAt: s.now().UTC(),
		Overall:   overall(checks),
	})
}

// diagKnownCheck — есть ли такая проверка. Неизвестный идентификатор лучше
// отвергнуть: пустая сводка в ответ на опечатку выглядит как «всё хорошо».
func diagKnownCheck(id string) bool {
	return slices.Contains(diagCheckIDs, id)
}

// diagInput — прочитанные источники. Отдельный тип, потому что источник
// читается один раз на запрос: wg нужен и «WireGuard», и «Path MTU», nft —
// и «Правилам nftables», и «IP forwarding».
type diagInput struct {
	snap store.Snapshot

	wg    netstack.DiagWGState
	wgErr error

	nft    netstack.DiagNftState
	nftErr error

	forward    bool
	forwardErr error

	lists    DiagLists
	listsErr error
}

// diagRead читает ровно те источники, которые нужны перечисленным проверкам:
// прогон одной проверки не должен дёргать netlink за остальные шесть.
func (s *Server) diagRead(ctx context.Context, snap store.Snapshot, ids []string) diagInput {
	in := diagInput{snap: snap}
	want := func(need ...string) bool {
		return slices.ContainsFunc(ids, func(id string) bool {
			return slices.Contains(need, id)
		})
	}

	if want(checkWG, checkMTU) {
		in.wg, in.wgErr = diagSource(ctx, s.diag.WG)
	}
	if want(checkNft, checkForward) {
		in.nft, in.nftErr = diagSource(ctx, s.diag.Nft)
	}
	if want(checkForward) {
		in.forward, in.forwardErr = diagSource(ctx, s.diag.IPForward)
	}
	if want(checkLists) {
		in.lists, in.listsErr = diagSource(ctx, s.diag.Lists)
	}
	return in
}

// diagCheck собирает одну строку сводки из уже прочитанных источников.
func (s *Server) diagCheck(id string, in diagInput) check {
	switch id {
	case checkWG:
		return wgCheck(in.wg, in.wgErr, in.snap)
	case checkSingbox:
		return singboxCheck(in.snap)
	case checkNft:
		return nftCheck(in.nft, in.nftErr)
	case checkTunnels:
		return tunnelsCheck(in.snap.Tunnels, s.checks)
	case checkLists:
		return listsCheck(in.lists, in.listsErr, in.snap.Settings, s.now())
	case checkForward:
		return forwardCheck(in.forward, in.forwardErr, in.nft, in.nftErr)
	case checkMTU:
		return mtuCheck(in.wg, in.wgErr, in.snap.Settings)
	}
	// Недостижимо: идентификатор проверен в handleDiag. Молча пропускать
	// строку нельзя — сводка недосчиталась бы проверки.
	return check{ID: id, Title: id, Status: statusUnknown, Detail: "проверка не реализована"}
}

// diagSource читает источник, подставляя [errDiagNoSource] вместо неподключённого.
func diagSource[T any](ctx context.Context, src func(context.Context) (T, error)) (T, error) {
	var zero T
	if src == nil {
		return zero, errDiagNoSource
	}
	v, err := src(ctx)
	if err != nil {
		return zero, err
	}
	return v, nil
}

// diagUnknown — единственный способ выдать unknown: причина всегда в detail.
func diagUnknown(c check, what string, err error) check {
	c.Status = statusUnknown
	c.Detail = what + ": " + err.Error()
	return c
}

// wgCheck — интерфейс поднят, слушает настроенный порт, и на нём столько же
// пиров, сколько включено в БД.
func wgCheck(st netstack.DiagWGState, err error, snap store.Snapshot) check {
	c := check{ID: "wg", Title: "WireGuard"}
	if err != nil {
		return diagUnknown(c, "состояние интерфейса не прочитано", err)
	}

	name := st.Name
	if name == "" {
		name = netstack.DefaultWGInterface
	}
	if !st.Exists {
		c.Status = statusError
		c.Detail = fmt.Sprintf("интерфейса %s нет: демон его не поднял", name)
		return c
	}
	if !st.Up {
		c.Status = statusError
		c.Detail = fmt.Sprintf("интерфейс %s есть, но не поднят: клиенты не подключатся", name)
		return c
	}
	if want := snap.Settings.WGListenPort; st.ListenPort != want {
		c.Status = statusError
		c.Detail = fmt.Sprintf("%s слушает порт %d, в настройках %d: клиенты идут не туда",
			name, st.ListenPort, want)
		return c
	}

	enabled := diagEnabledPeers(snap.Peers)
	if st.Peers != enabled {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("на %s пиров %d, включённых в базе %d: синхронизация ещё не прошла",
			name, st.Peers, enabled)
		return c
	}
	c.Status = statusOK
	c.Detail = fmt.Sprintf("%s поднят, порт %d, пиров %d", name, st.ListenPort, st.Peers)
	return c
}

// diagEnabledPeers — сколько пиров должно быть на интерфейсе: выключенный
// остаётся в БД, но с интерфейса снимается.
func diagEnabledPeers(peers []store.Peer) int {
	n := 0
	for _, p := range peers {
		if p.Enabled {
			n++
		}
	}
	return n
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

// tunnelsCheck — сводка по проверкам туннелей, сделанным с запуска демона.
//
// Сама проверка тут не запускается: она ходит в сеть через sing-box и делается
// по кнопке в панели (`POST /api/tunnels/{id}/check`). Диагностика показывает
// то, что уже измерено, а «не проверялось» называет своими словами — молча
// выдавать это за «ok» нельзя.
func tunnelsCheck(tunnels []store.Tunnel, cache *checkCache) check {
	c := check{ID: "tunnels", Title: "Туннели"}

	var enabled, checked int
	var bad []string
	for _, t := range tunnels {
		if !t.Enabled {
			continue
		}
		enabled++
		res, ok := cache.get(t.ID)
		if !ok {
			continue
		}
		checked++
		if res.Status != tunnelUp {
			bad = append(bad, t.Name)
		}
	}

	switch {
	case enabled == 0:
		c.Status = statusOK
		c.Detail = "включённых туннелей нет"
	case checked == 0:
		c.Status = statusUnknown
		c.Detail = fmt.Sprintf(
			"туннели (%d) с запуска демона не проверялись: проверка запускается из панели", enabled)
	case len(bad) > 0:
		c.Status = statusWarn
		c.Detail = "не отвечают: " + strings.Join(bad, ", ")
	default:
		c.Status = statusOK
		c.Detail = fmt.Sprintf("проверено %d из %d, все отвечают", checked, enabled)
	}
	return c
}

// nftCheck — таблица на месте, цепочки и сеты созданы.
func nftCheck(st netstack.DiagNftState, err error) check {
	c := check{ID: "nft", Title: "Правила nftables"}
	if err != nil {
		return diagUnknown(c, "состояние nftables не прочитано", err)
	}

	table := st.Table
	if table == "" {
		table = netstack.NftTable
	}
	if !st.Exists {
		c.Status = statusError
		c.Detail = fmt.Sprintf("таблицы inet %s нет: правила не залиты, трафик идёт мимо туннелей",
			table)
		return c
	}
	if chains, sets := st.DiagMissing(); len(chains) > 0 || len(sets) > 0 {
		c.Status = statusError
		c.Detail = fmt.Sprintf("таблица inet %s залита не целиком: %s",
			table, diagMissingText(chains, sets))
		return c
	}
	c.Status = statusOK
	c.Detail = fmt.Sprintf("таблица inet %s на месте: цепочек %d, сетов %d, подсетей в сете %d",
		table, len(st.Chains), len(st.Sets), st.SubnetIntervals)
	return c
}

// diagMissingText перечисляет недостающие объекты таблицы.
func diagMissingText(chains, sets []string) string {
	var parts []string
	if len(chains) > 0 {
		parts = append(parts, "нет цепочек "+strings.Join(chains, ", "))
	}
	if len(sets) > 0 {
		parts = append(parts, "нет сетов "+strings.Join(sets, ", "))
	}
	return strings.Join(parts, "; ")
}

// forwardCheck — форвардинг включён и обратный путь есть: без masquerade
// ответы на трафик клиентов не вернутся, сколько бы ip_forward ни стоял.
func forwardCheck(forward bool, forwardErr error, nft netstack.DiagNftState, nftErr error) check {
	c := check{ID: "forward", Title: "IP forwarding"}
	if forwardErr != nil {
		return diagUnknown(c, "net.ipv4.ip_forward не прочитан", forwardErr)
	}
	if !forward {
		c.Status = statusError
		c.Detail = "net.ipv4.ip_forward выключен: трафик клиентов дальше wg0 не уходит"
		return c
	}
	if nftErr != nil {
		return diagUnknown(c,
			"ip_forward включён; masquerade проверить нечем, состояние nftables не прочитано",
			nftErr)
	}
	if !nft.Exists {
		c.Status = statusError
		c.Detail = "ip_forward включён, но таблицы nft нет: masquerade не настроен"
		return c
	}
	if !nft.Masquerade {
		c.Status = statusError
		c.Detail = fmt.Sprintf(
			"ip_forward включён, но в цепочке %s нет masquerade: ответы клиентам не вернутся",
			netstack.ChainPostrouting)
		return c
	}
	c.Status = statusOK
	c.Detail = "ip_forward включён, masquerade на " + diagOIf(nft.MasqueradeOIf)
	return c
}

// diagOIf — как назвать интерфейс masquerade в сводке.
func diagOIf(name string) string {
	if name == "" {
		return "всём исходящем трафике"
	}
	return name
}

// mtuCheck — 1280 и на wg0, и в том, что уходит клиентам (ADR 0004).
//
// Проверяются оба конца: одинаково неверны и интерфейс с чужим MTU, и
// настройка, из которой клиентам выдаётся не 1280.
func mtuCheck(st netstack.DiagWGState, err error, settings store.Settings) check {
	c := check{ID: "mtu", Title: "Path MTU"}

	if settings.ClientMTU != diagMTU {
		c.Status = statusError
		c.Detail = fmt.Sprintf("клиентам выдаётся MTU %d, ADR 0004 требует %d",
			settings.ClientMTU, diagMTU)
		return c
	}
	if err != nil {
		return diagUnknown(c,
			fmt.Sprintf("клиентам выдаётся MTU %d; MTU интерфейса не прочитан", diagMTU), err)
	}

	name := st.Name
	if name == "" {
		name = netstack.DefaultWGInterface
	}
	if !st.Exists {
		c.Status = statusUnknown
		c.Detail = fmt.Sprintf("клиентам выдаётся MTU %d; интерфейса %s нет, сверять не с чем",
			diagMTU, name)
		return c
	}
	if st.MTU != diagMTU {
		c.Status = statusError
		c.Detail = fmt.Sprintf("MTU %s равен %d, клиентам выдаётся %d: пакеты будут рваться",
			name, st.MTU, diagMTU)
		return c
	}
	c.Status = statusOK
	c.Detail = fmt.Sprintf("%d и на %s, и в клиентских конфигах", diagMTU, name)
	return c
}

// listsCheck — свежесть кэша списков.
func listsCheck(l DiagLists, err error, settings store.Settings, now time.Time) check {
	c := check{ID: "lists", Title: "Списки"}
	if errors.Is(err, errDiagNoSource) {
		c.Status = statusUnknown
		c.Detail = "планировщик обновления списков к демону не подключён"
		return c
	}
	if err != nil {
		return diagUnknown(c, "состояние кэша списков не прочитано", err)
	}

	if l.Sources == 0 {
		c.Status = statusOK
		c.Detail = "правила не ссылаются на списки — обновлять нечего"
		return c
	}
	if l.LastRefresh.IsZero() {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("списки ещё ни разу не обновлялись: источников %d", l.Sources)
		return c
	}

	age := now.Sub(l.LastRefresh)
	if limit := diagStaleFactor * settings.ListUpdateInterval; limit > 0 && age > limit {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("кэш протух: обновлялся %s назад при интервале %s",
			diagAge(age), diagAge(settings.ListUpdateInterval))
		return c
	}
	if l.Loaded < l.Sources {
		c.Status = statusWarn
		c.Detail = fmt.Sprintf("загружено %d списков из %d: остальные не скачались",
			l.Loaded, l.Sources)
		return c
	}
	c.Status = statusOK
	c.Detail = fmt.Sprintf("обновлены %s назад: списков %d", diagAge(age), l.Loaded)
	return c
}

// diagAge — возраст по-русски: строка уходит в UI как есть.
func diagAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "меньше минуты"
	case d < time.Hour:
		return fmt.Sprintf("%d мин", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн", int(d.Hours()/24))
	}
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
