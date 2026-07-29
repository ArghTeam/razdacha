package lists

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Пауза перед повтором после неудачного прогона. На свежем VPS сеть поднимается
// позже демона, и первый прогон стабильно проваливается; Podkop по той же
// причине ходит до 10 раз (podkop/files/usr/bin/podkop:487-533).
const (
	defaultRetry    = 1 * time.Minute
	maxRetryBackoff = 15 * time.Minute
)

// Source — один список, который демон качает сам.
type Source struct {
	URL    string
	Format Format
}

// Sources — какие списки нужны демону при таком состоянии БД.
//
// Домены сюда не попадают: наборы типа remote в конфиге sing-box он обновляет
// сам. Демону списки нужны ради подсетей — их дублируют в nft-сет, потому что
// FakeIP срабатывает, только когда клиент резолвит домен (docs/04-dns-fakeip.md).
func Sources(snap store.Snapshot) []Source {
	seen := make(map[string]bool)
	var out []Source

	add := func(url string) {
		if url == "" || seen[url] {
			return
		}
		seen[url] = true
		out = append(out, Source{URL: url, Format: FormatOf(url)})
	}

	for _, r := range snap.Rules {
		if !r.Enabled {
			continue
		}
		// Правила с любым действием: чтобы sing-box отбросил трафик по
		// action = block, тот сперва должен до него дойти по метке nft.
		// Вторая половина того же сета — ручные подсети правил, и отбор там
		// обязан быть таким же (`netstack.RuleMarksSubnets`); пока он был по
		// одному действию, блокировка по ручной подсети молчала (issue #126).
		// Расхождение стережёт TestRuleSubnetsAgreesWithListSources.
		for _, key := range r.CommunityLists {
			// Только сервисы, у которых в allow-domains есть подсети:
			// у остальных .srs с одними доменами, и качать его незачем.
			if url, ok := CommunitySubnetURL(key); ok {
				add(url)
			}
		}
		for _, url := range r.RemoteLists {
			// Свои списки берём в любом виде: .srs и .json ведёт sing-box,
			// но подсети из них всё равно нужны в nft-сете.
			add(url)
		}
	}
	return out
}

// ManagerOptions — параметры расписания.
type ManagerOptions struct {
	Fetcher  *Fetcher
	Logger   *slog.Logger
	Interval time.Duration // ноль означает дефолт настроек (1 сутки)
	Retry    time.Duration // пауза после неудачного прогона, ноль означает defaultRetry
}

// Manager держит разобранные списки и обновляет их по расписанию.
type Manager struct {
	fetcher *Fetcher
	log     *slog.Logger
	retry   time.Duration

	mu       sync.RWMutex
	interval time.Duration
	sources  []Source
	lists    map[string]List
	last     time.Time

	updates chan struct{}
	wg      sync.WaitGroup
}

// NewManager создаёт расписание. Само по себе оно ничего не качает — нужен Start.
func NewManager(o ManagerOptions) *Manager {
	m := &Manager{
		fetcher:  o.Fetcher,
		log:      o.Logger,
		retry:    o.Retry,
		interval: o.Interval,
		lists:    make(map[string]List),
		updates:  make(chan struct{}, 1),
	}
	if m.log == nil {
		m.log = slog.Default()
	}
	if m.retry <= 0 {
		m.retry = defaultRetry
	}
	if m.interval <= 0 {
		m.interval = store.DefaultSettings().ListUpdateInterval
	}
	return m
}

// SetSources задаёт набор списков. Вызывается при старте и при каждом изменении
// правил; списки, выпавшие из набора, перестают обновляться, но остаются в кэше
// на диске — их вернёт следующее правило с тем же URL.
func (m *Manager) SetSources(src []Source) {
	m.mu.Lock()
	m.sources = append([]Source(nil), src...)
	m.mu.Unlock()
}

// SetInterval меняет интервал обновления. Применяется со следующего срабатывания.
func (m *Manager) SetInterval(d time.Duration) {
	if d <= 0 {
		return
	}
	m.mu.Lock()
	m.interval = d
	m.mu.Unlock()
}

// Start запускает расписание и сразу возвращается: первый прогон идёт в
// горутине, старт демона не ждёт ни сети, ни чужих серверов.
func (m *Manager) Start(ctx context.Context) {
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		m.loop(ctx)
	}()
}

// Wait ждёт остановки расписания после отмены контекста.
func (m *Manager) Wait() { m.wg.Wait() }

// Updates — уведомления об окончании прогона: слой netstack по ним перезаливает
// nft-сет. Канал буферизован на одно значение, отправка не блокирующая, так что
// медленный подписчик расписание не тормозит.
func (m *Manager) Updates() <-chan struct{} { return m.updates }

// loop — само расписание. Неудачный прогон повторяется чаще обычного интервала:
// сутки ждать поднятия сети незачем.
func (m *Manager) loop(ctx context.Context) {
	backoff := m.retry
	for {
		err := m.Refresh(ctx)
		if ctx.Err() != nil {
			return
		}

		wait := m.currentInterval()
		if err != nil {
			m.log.Warn("обновление списков не удалось, повтор позже",
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

func (m *Manager) currentInterval() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.interval
}

// Refresh проходит по всем источникам один раз. Ошибка одного источника не
// отменяет остальные и не выбрасывает его прошлую версию из памяти.
func (m *Manager) Refresh(ctx context.Context) error {
	m.mu.RLock()
	sources := append([]Source(nil), m.sources...)
	m.mu.RUnlock()

	var errs []error
	for _, src := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		list, err := m.fetcher.Parse(ctx, src.URL)
		if err != nil {
			errs = append(errs, err)
			m.log.Warn("список пропущен", "url", src.URL, "err", err)
			continue
		}
		m.mu.Lock()
		m.lists[src.URL] = list
		m.mu.Unlock()
	}

	m.mu.Lock()
	m.last = time.Now().UTC()
	m.mu.Unlock()

	select {
	case m.updates <- struct{}{}:
	default:
	}
	return errors.Join(errs...)
}

// Subnets — все подсети из загруженных списков, отсортированные и без повторов:
// готовый набор элементов nft-сета.
func (m *Manager) Subnets() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var all []string
	for _, l := range m.lists {
		all = append(all, l.Subnets...)
	}
	return dedup(all)
}

// List отдаёт разобранное содержимое одного списка.
func (m *Manager) List(url string) (List, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l, ok := m.lists[url]
	return l, ok
}

// LastRefresh — когда расписание отработало в последний раз. Нулевое время
// означает, что первый прогон ещё не закончился; UI показывает возраст списков.
func (m *Manager) LastRefresh() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.last
}
