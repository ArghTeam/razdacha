package lists

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// DefaultPoolInterval — как часто обходится каталог ключей.
//
// Двенадцать часов, потому что обновление дорого: изменившийся набор серверов
// переписывает конфиг, а перезагрузка идёт через `systemctl reload-or-restart`,
// то есть перезапускает sing-box и рвёт соединения во всех туннелях. Ротация внутри
// пула к этому отношения не имеет — её ведёт `urltest` внутри процесса каждые
// несколько минут (ADR 0010). Сам сайт объявляет тот же период в заголовке
// `profile-update-interval: 12`.
const DefaultPoolInterval = 12 * time.Hour

// PoolWriter — то, что расписание пула умеет менять в БД. Интерфейс, а не *store.Store:
// в тестах записи проверяются без SQLite, а слой lists и без того не владеет БД.
type PoolWriter interface {
	UpdateTunnelPool(ctx context.Context, id string, servers []store.PoolServer, at time.Time) error
}

// PoolTunnel — туннель-пул в том виде, в каком он нужен расписанию.
type PoolTunnel struct {
	ID         string
	Name       string
	CatalogURL string
	Enabled    bool
	// Servers — состав, лежащий в БД сейчас, в порядке приоритета отбора. С ним
	// сводится свежий обход ([MergePool]): ничего не изменилось — в БД не пишем и
	// конфиг не перегенерируем.
	Servers []store.PoolServer
}

// PoolIdentity — то, что делает набор пулов набором: какой это пул, какой каталог он
// обходит и обходит ли вообще. Состава серверов здесь нет намеренно.
//
// Нужна для сверки набора с БД: изменился набор — каталоги надо обойти сразу, не дожидаясь
// [DefaultPoolInterval]. Состав же серверов шевелится после каждого обхода (счётчики
// пропусков, новые карточки), и сверка, которая его учитывает, объявляет набор
// изменившимся всегда — каталог обходится на каждом такте сверки вместо раза в 12 часов
// (issue #77).
//
// Тип отдельный, а не сравнение полей на месте: он перечисляет участвующие поля явно и
// сравнивается через `==`, поэтому новое поле [PoolTunnel] попадает в сверку только когда
// его сюда вписали руками. Полноту набора полей стережёт `TestPoolTunnelFieldsGuard`.
type PoolIdentity struct {
	ID         string
	CatalogURL string
	Enabled    bool
}

// Identity — признаки, по которым пул опознаётся в наборе. Имя сюда не входит:
// переименование туннеля каталог не меняет, обходить его заново незачем.
func (t PoolTunnel) Identity() PoolIdentity {
	return PoolIdentity{
		ID:         t.ID,
		CatalogURL: t.CatalogURL,
		Enabled:    t.Enabled,
	}
}

// SamePoolSet отвечает, тот же это набор пулов или другой — с точностью до [PoolIdentity]
// и порядка. Совпал — внеплановый обход каталогов не нужен.
func SamePoolSet(a, b []PoolTunnel) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Identity() != b[i].Identity() {
			return false
		}
	}
	return true
}

// PoolTunnelFrom переводит туннель из БД в то, что нужно расписанию. Отдельно от
// [PoolTunnels]: обход по требованию идёт по одному туннелю, прочитанному ручкой API,
// и снимка всего состояния ради него не берут.
func PoolTunnelFrom(t store.Tunnel) PoolTunnel {
	return PoolTunnel{
		ID:         t.ID,
		Name:       t.Name,
		CatalogURL: t.Raw,
		Enabled:    t.Enabled,
		Servers:    t.Pool,
	}
}

// PoolTunnels выбирает из снимка туннели-пулы. Выключенные попадают в список, но
// расписание их пропускает: решение «обновлять или нет» принимается в одном месте.
func PoolTunnels(snap store.Snapshot) []PoolTunnel {
	var out []PoolTunnel
	for _, t := range snap.Tunnels {
		if t.Source != store.SourcePool {
			continue
		}
		out = append(out, PoolTunnelFrom(t))
	}
	return out
}

// PoolManagerOptions — параметры расписания пулов.
type PoolManagerOptions struct {
	Catalog  *PoolCatalog
	Writer   PoolWriter
	Logger   *slog.Logger
	Interval time.Duration // ноль означает DefaultPoolInterval
	Retry    time.Duration // пауза после неудачного прогона, ноль означает defaultRetry
}

// PoolManager обновляет состав туннелей-пулов по расписанию.
//
// Устроен как [Manager]: набор задаётся снаружи через [PoolManager.SetTunnels],
// первый прогон уходит в горутину, неудача повторяется чаще обычного интервала.
type PoolManager struct {
	catalog *PoolCatalog
	writer  PoolWriter
	log     *slog.Logger
	retry   time.Duration

	mu       sync.RWMutex
	interval time.Duration
	tunnels  []PoolTunnel
	last     time.Time

	updates chan struct{}
	wg      sync.WaitGroup
}

// NewPoolManager создаёт расписание. Само по себе оно ничего не качает — нужен Start.
func NewPoolManager(o PoolManagerOptions) *PoolManager {
	m := &PoolManager{
		catalog:  o.Catalog,
		writer:   o.Writer,
		log:      o.Logger,
		retry:    o.Retry,
		interval: o.Interval,
		updates:  make(chan struct{}, 1),
	}
	if m.catalog == nil {
		m.catalog = &PoolCatalog{Log: m.log}
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.retry <= 0 {
		m.retry = defaultRetry
	}
	if m.interval <= 0 {
		m.interval = DefaultPoolInterval
	}
	return m
}

// SetTunnels задаёт набор пулов. Вызывается при старте и при каждом изменении
// туннелей.
func (m *PoolManager) SetTunnels(list []PoolTunnel) {
	m.mu.Lock()
	m.tunnels = append([]PoolTunnel(nil), list...)
	m.mu.Unlock()
}

// SetInterval меняет интервал обновления. Применяется со следующего срабатывания.
func (m *PoolManager) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	m.interval = d
	m.mu.Unlock()
}

// Start запускает расписание и сразу возвращается: старт демона не ждёт чужого сайта.
func (m *PoolManager) Start(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.loop(ctx)
	}()
}

// Wait ждёт остановки расписания после отмены контекста.
func (m *PoolManager) Wait() { m.wg.Wait() }

// Updates — уведомления о том, что состав какого-то пула изменился и конфиг надо
// перегенерировать. Прогон, ничего не изменивший, тут молчит: перегенерация ведёт к
// перезапуску sing-box, и делать его на неизменившемся наборе незачем.
func (m *PoolManager) Updates() <-chan struct{} { return m.updates }

// LastRefresh — когда расписание отработало в последний раз.
func (m *PoolManager) LastRefresh() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.last
}

func (m *PoolManager) loop(ctx context.Context) {
	backoff := m.retry
	for {
		err := m.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}

		wait := m.currentInterval()
		if err != nil {
			m.log.Warn("обновление пулов не удалось, повтор позже",
				"retry_in", backoff, "err", err)
			if backoff < wait {
				wait = backoff
			}
			if backoff < maxRetryBackoff {
				backoff *= 2
			}
		} else {
			backoff = m.retry
		}

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (m *PoolManager) currentInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

// Refresh обходит каталоги всех включённых пулов.
//
// Пулы с общим каталогом обходятся один раз на группу, а не по разу на пул: обойти
// один и тот же файл дважды нельзя ни по вежливости к источнику, ни по устройству
// дедупа обхода — `beginCrawl` отдал бы ключи только первому пулу. Встроенный пул
// один (ADR 0018), но группировка остаётся общей защитой от совпавших обходов одного
// каталога. Ошибка одной группы не отменяет остальные и не выбрасывает прошлый состав
// пула из БД: устаревший список лучше пустого.
func (m *PoolManager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	tunnels := append([]PoolTunnel(nil), m.tunnels...)
	m.mu.RUnlock()

	// Включённые пулы группируются по каталогу; порядок каталогов и порядок пулов
	// внутри группы сохраняются — от них зависит и предсказуемость лога, и порядок в
	// панели. Выключенный пул в группу не входит: он не в конфиге, ходить за него на
	// чужой сайт незачем.
	var order []string
	groups := make(map[string][]PoolTunnel)
	for _, t := range tunnels {
		if !t.Enabled {
			m.log.Debug("пул выключен, каталог не обходим", "туннель", t.Name)
			continue
		}
		if _, ok := groups[t.CatalogURL]; !ok {
			order = append(order, t.CatalogURL)
		}
		groups[t.CatalogURL] = append(groups[t.CatalogURL], t)
	}

	var (
		errs    []error
		changed bool
	)
	for _, catalogURL := range order {
		if err := ctx.Err(); err != nil {
			return err
		}
		ok, err := m.refreshGroup(ctx, groups[catalogURL])
		switch {
		case errors.Is(err, ErrPoolCrawlBusy):
			// Каталог уже обходит кто-то другой — чаще всего кнопка «Обновить» в
			// панели, нажатая в ту же секунду. Это не неудача: уходить в ретраи
			// незачем, свежий состав запишет идущий обход (issue #156).
			m.log.Info("каталог пула уже обходится, такт расписания пропущен",
				"каталог", catalogURL, "пулов", len(groups[catalogURL]))
		case err != nil:
			errs = append(errs, err)
			m.log.Warn("каталог пулов обойдён не полностью", "каталог", catalogURL, "err", err)
		case ok:
			changed = true
		}
	}

	m.mu.Lock()
	m.last = time.Now().UTC()
	m.mu.Unlock()

	if changed {
		select {
		case m.updates <- struct{}{}:
		default:
		}
	}
	return errors.Join(errs...)
}

// RefreshPool обходит каталог одного пула по требованию — за кнопкой «Обновить» в
// панели. Первое значение — изменился ли состав серверов.
//
// Пул передаётся целиком, а не идентификатором: набор пулов менеджер сверяет с БД раз
// в полминуты, и поиск по этому набору отказывал на только что появившемся пуле, хотя
// в БД он уже был (issue #74). Тот, кто нажал кнопку, пул из БД и прочитал — значит,
// и состав серверов для слияния у него свежее, чем в наборе расписания.
//
// Выключенный пул обновляется и здесь: в отличие от расписания, за него попросил
// человек, и отказ выглядел бы поломкой кнопки. В конфиг он всё равно не попадёт,
// пока выключен.
//
// Идущий обход того же каталога отказывает [ErrPoolCrawlBusy] — сразу, а не через две
// минуты ожидания: результат у обоих обходов один и тот же, и его запишет идущий.
func (m *PoolManager) RefreshPool(ctx context.Context, t PoolTunnel) (bool, error) {
	changed, err := m.refreshOne(ctx, t)
	if err != nil {
		return false, err
	}

	m.mu.Lock()
	m.last = time.Now().UTC()
	m.mu.Unlock()

	if changed {
		select {
		case m.updates <- struct{}{}:
		default:
		}
	}
	return changed, nil
}

// refreshGroup обходит общий каталог группы один раз и применяет выдачу к каждому её
// пулу целиком (ADR 0018). Первое значение — изменился ли состав хоть у одного пула
// группы. Ошибка обхода — общая для группы; ошибка записи одного пула не мешает остальным.
func (m *PoolManager) refreshGroup(ctx context.Context, group []PoolTunnel) (bool, error) {
	servers, err := m.catalog.Servers(ctx, group[0].CatalogURL)
	if err != nil {
		return false, fmt.Errorf("каталог пулов %q: %w", group[0].CatalogURL, err)
	}

	var (
		errs    []error
		changed bool
	)
	for _, t := range group {
		ok, werr := m.applyServers(ctx, t, servers)
		if werr != nil {
			errs = append(errs, werr)
			m.log.Warn("пул не обновлён", "туннель", t.Name, "err", werr)
			continue
		}
		if ok {
			changed = true
		}
	}
	return changed, errors.Join(errs...)
}

// refreshOne обходит каталог одного пула. Первое значение — изменился ли состав.
// Используется обходом по требованию ([RefreshPool]): один пул — один обход.
func (m *PoolManager) refreshOne(ctx context.Context, t PoolTunnel) (bool, error) {
	servers, err := m.catalog.Servers(ctx, t.CatalogURL)
	if err != nil {
		return false, fmt.Errorf("каталог пула %q: %w", t.Name, err)
	}
	return m.applyServers(ctx, t, servers)
}

// applyServers сводит переданный состав с тем, что лежит в БД, и пишет, если он
// изменился. Первое значение — изменился ли состав.
func (m *PoolManager) applyServers(ctx context.Context, t PoolTunnel, servers []store.PoolServer) (bool, error) {
	merged, changed := MergePool(t.Servers, servers)
	if !changed {
		m.log.Debug("состав пула не изменился", "туннель", t.Name, "серверов", len(servers))
		return false, nil
	}
	if err := m.writer.UpdateTunnelPool(ctx, t.ID, merged, time.Now().UTC()); err != nil {
		return false, fmt.Errorf("запись состава пула %q: %w", t.Name, err)
	}
	m.log.Info("состав пула обновлён",
		"туннель", t.Name, "было", len(t.Servers), "стало", len(merged),
		"в_каталоге", len(servers))
	return true, nil
}

// poolMissesBeforeDrop — сколько обходов подряд ссылка должна не попасться, чтобы
// сервер считался исчезнувшим и уступил своё место.
//
// Не один: каталог отдаёт часть карточек без ссылки на ключ почти на каждом запросе
// (наблюдалось 96–99 карточек со ссылкой из 100 на первой странице), и одиночный
// пропуск — свойство чужой страницы, а не смерть сервера. Считать его исчезновением
// значило бы менять состав конфига и перезапускать sing-box в четверти обходов
// (issue #68). Мёртвый ключ до третьего обхода из конфига не уходит, но и не мешает:
// трафик с него уводит `urltest` внутри процесса.
const poolMissesBeforeDrop = 3

// PoolConfigServers — сколько первых серверов списка уходит в конфиг sing-box.
//
// Держится равным `poolMaxServers` слоя singbox: слияние обязано знать границу окна,
// иначе выбывший участник сдвинул бы позиции остальных, а позиция — это тег в конфиге.
// Импортировать singbox слой lists не может (генератор читает списки, не наоборот),
// поэтому число объявлено здесь и сверяется тестом `TestPoolConfigWindowAgrees`.
const PoolConfigServers = 16

// MergePool сводит состав пула, лежащий в БД, со свежим обходом каталога и говорит,
// изменился ли он. Возвращённый список идёт в БД как есть.
//
// Порядок в списке значим: первые [PoolConfigServers] штук — это окно конфига, и слой
// singbox раздаёт им теги по позиции. Отсюда правила слияния:
//
//   - Известный сервер остаётся на своём месте, и его запись не перезаписывается:
//     пинг в карточке пляшет на каждом обходе, и отбор, ведомый этим шумом, менял бы
//     участников конфига чуть не при каждом обновлении каталога. Пинг чужой карточки —
//     грубая оценка на момент первой встречи, не больше.
//   - Сервер, не попавшийся poolMissesBeforeDrop обходов подряд, освобождает место.
//   - Освободившееся место в окне занимает лучший по пингу из остальных — из хвоста
//     списка либо из новых карточек. Место занимается ровно то, что освободилось:
//     сдвиг окна означал бы смену тегов у выживших, то есть другой конфиг.
//   - Хвост за окном выстраивается по пингу. Первый обход — тот же случай: окна ещё
//     нет, новы все, и список выходит по пингу. Занять освободившееся место нечем —
//     окно сужается; это пул из считанных серверов, где позиции сдвинутся и так.
//
// Тем самым уже отобранные в конфиг серверы остаются отобранными, пока они есть в
// каталоге, а конфиг меняется только когда участник действительно пропал: смена
// байтов конфига стоит перезапуска sing-box и разрыва соединений во всех туннелях
// (ADR 0010).
func MergePool(stored, fresh []store.PoolServer) ([]store.PoolServer, bool) {
	inFresh := make(map[string]bool, len(fresh))
	freshByURL := make(map[string]store.PoolServer, len(fresh))
	for _, s := range fresh {
		if s.URL != "" {
			inFresh[s.URL] = true
			if _, ok := freshByURL[s.URL]; !ok {
				freshByURL[s.URL] = s
			}
		}
	}

	// Окно конфига разбирается на слоты: nil — место освободилось и будет занято на
	// этой же позиции. Всё, что за окном, идёт в очередь кандидатов.
	var (
		window     []*store.PoolServer
		candidates []store.PoolServer
	)
	known := make(map[string]bool, len(stored))
	for _, s := range stored {
		if s.URL == "" || known[s.URL] {
			continue
		}
		known[s.URL] = true

		alive := true
		if inFresh[s.URL] {
			s.Misses = 0
			// Каталожная подпись — источник истины у свежего обхода. Старый состав
			// мог лечь без неё (до #189 Title у igareck не заполнялся), поэтому
			// присутствующему в каталоге серверу подтягиваем имя и страну заново.
			// На конфиг sing-box и порядок окна это не влияет — поля косметические.
			f := freshByURL[s.URL]
			s.Title = f.Title
			s.Country = f.Country
		} else {
			s.Misses++
			alive = s.Misses < poolMissesBeforeDrop
		}

		if len(window) < PoolConfigServers {
			if alive {
				kept := s
				window = append(window, &kept)
			} else {
				window = append(window, nil)
			}
			continue
		}
		if alive {
			candidates = append(candidates, s)
		}
	}

	seen := make(map[string]bool, len(fresh))
	for _, s := range fresh {
		if s.URL == "" || known[s.URL] || seen[s.URL] {
			continue
		}
		seen[s.URL] = true
		s.Misses = 0
		candidates = append(candidates, s)
	}
	// Кандидаты выстраиваются по пингу, при равенстве — по ссылке: иначе порядок
	// зависел бы от порядка страниц каталога, а он у сайта свой.
	sort.Slice(candidates, func(i, j int) bool {
		a, b := poolPingRank(candidates[i].PingMS), poolPingRank(candidates[j].PingMS)
		if a != b {
			return a < b
		}
		return candidates[i].URL < candidates[j].URL
	})

	next := 0
	merged := make([]store.PoolServer, 0, len(window)+len(candidates))
	for _, s := range window {
		if s != nil {
			merged = append(merged, *s)
			continue
		}
		if next < len(candidates) {
			merged = append(merged, candidates[next])
			next++
		}
	}
	merged = append(merged, candidates[next:]...)

	return merged, !samePool(stored, merged)
}

// poolPingRank переводит пинг в ключ сортировки: неизвестный пинг хуже любого
// известного, но сервер не выбрасывается — каталог показывает его не всегда.
func poolPingRank(ping int) int {
	if ping <= 0 {
		return 1 << 30
	}
	return ping
}

// samePool отвечает, тот же это список серверов или другой — с точностью до записи
// и её позиции. Совпал — в БД не пишем и перегенерацию конфига не будим.
func samePool(a, b []store.PoolServer) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
