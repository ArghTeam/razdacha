package lists

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	// Servers — состав, лежащий в БД сейчас. С ним сверяется свежий: совпал —
	// в БД ничего не пишется и конфиг не перегенерируется.
	Servers []store.PoolServer
}

// PoolTunnels выбирает из снимка туннели-пулы. Выключенные попадают в список, но
// расписание их пропускает: решение «обновлять или нет» принимается в одном месте.
func PoolTunnels(snap store.Snapshot) []PoolTunnel {
	var out []PoolTunnel
	for _, t := range snap.Tunnels {
		if t.Source != store.SourcePool {
			continue
		}
		out = append(out, PoolTunnel{
			ID:         t.ID,
			Name:       t.Name,
			CatalogURL: t.Raw,
			Enabled:    t.Enabled,
			Servers:    t.Pool,
		})
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

// Refresh обходит каталоги всех включённых пулов один раз. Ошибка одного пула не
// отменяет остальные и не выбрасывает его прошлый состав из БД: устаревший список
// серверов лучше пустого.
func (m *PoolManager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	tunnels := append([]PoolTunnel(nil), m.tunnels...)
	m.mu.RUnlock()

	var (
		errs    []error
		changed bool
	)
	for _, t := range tunnels {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Выключенный пул список не обновляет: он не в конфиге, и ходить за него
		// на чужой сайт незачем.
		if !t.Enabled {
			m.log.Debug("пул выключен, каталог не обходим", "туннель", t.Name)
			continue
		}

		ok, err := m.refreshOne(ctx, t)
		if err != nil {
			errs = append(errs, err)
			m.log.Warn("пул не обновлён", "туннель", t.Name, "err", err)
			continue
		}
		if ok {
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

// refreshOne обновляет один пул. Первое значение — изменился ли состав.
func (m *PoolManager) refreshOne(ctx context.Context, t PoolTunnel) (bool, error) {
	servers, err := m.catalog.Servers(ctx, t.CatalogURL)
	if err != nil {
		return false, fmt.Errorf("каталог пула %q: %w", t.Name, err)
	}

	if samePool(t.Servers, servers) {
		m.log.Debug("состав пула не изменился", "туннель", t.Name, "серверов", len(servers))
		return false, nil
	}
	if err := m.writer.UpdateTunnelPool(ctx, t.ID, servers, time.Now().UTC()); err != nil {
		return false, fmt.Errorf("запись состава пула %q: %w", t.Name, err)
	}
	m.log.Info("состав пула обновлён",
		"туннель", t.Name, "было", len(t.Servers), "стало", len(servers))
	return true, nil
}

// samePool отвечает, тот же это набор серверов или другой.
//
// Сравниваются только ссылки. Порядок не важен: генератор всё равно сортирует.
// Пинг, страна и подпись — тоже: пинг в карточке пляшет на каждом обходе, и если
// считать его изменением, то каждое обновление каталога переписывало бы конфиг и
// перезапускало sing-box со разрывом соединений во всех туннелях (ADR 0010).
// Пинги обновляются вместе с составом — там, где перезапуск всё равно случится.
func samePool(a, b []store.PoolServer) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, s := range a {
		seen[s.URL] = true
	}
	for _, s := range b {
		if !seen[s.URL] {
			return false
		}
	}
	return true
}
