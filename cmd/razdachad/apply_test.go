package main

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// poolServerURL — рабочая ссылка участника пула. Настоящая по форме: генератор
// разбирает её, и без разбора состав в конфиг не попадёт вовсе.
const poolServerURL = "vless://00000000-0000-0000-0000-000000000000@203.0.113.9:443?security=none#сервер"

// fakeApplier запоминает снимки, которые до него доехали. Настоящего рантайма
// и systemd в тестах нет.
type fakeApplier struct {
	mu    sync.Mutex
	snaps []store.Snapshot
	res   singbox.ApplyResult
	err   error
}

func (f *fakeApplier) Apply(_ context.Context, snap store.Snapshot) (singbox.ApplyResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.snaps = append(f.snaps, snap)
	return f.res, f.err
}

func (f *fakeApplier) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.snaps)
}

func (f *fakeApplier) last() store.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.snaps) == 0 {
		return store.Snapshot{}
	}
	return f.snaps[len(f.snaps)-1]
}

// okChecker — `sing-box check`, который всегда доволен: проверяется поведение
// применятеля, а не рантайма.
type okChecker struct{}

func (okChecker) Check(context.Context, string) error { return nil }

// countingReloader считает перезагрузки сервиса. Каждая из них — разрыв
// соединений во всех туннелях сразу, поэтому счёт здесь и проверяется.
type countingReloader struct {
	mu sync.Mutex
	n  int
}

func (r *countingReloader) Reload(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.n++
	return nil
}

func (r *countingReloader) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.n
}

// poolSnapshots собирает пару снимков: применённое состояние и то, что лежит в
// БД сейчас — со свежим составом пула и с правкой правила, которую пользователь
// применять не просил.
func poolSnapshots() (base, fresh store.Snapshot) {
	base = store.Snapshot{
		Tunnels: []store.Tunnel{{
			ID: "t1", Name: "пул", Type: store.TunnelVLESS, Source: store.SourcePool,
			Raw: "https://example.org/protocol/vless", Enabled: true,
			Pool: []store.PoolServer{{URL: "vless://старый@198.51.100.1:443", PingMS: 40}},
		}},
		Rules: []store.Rule{{
			ID: "r1", Name: "правило", Action: store.ActionTunnel, TunnelID: "t1",
			Enabled: true, Domains: []string{"example.com"},
		}},
	}
	fresh = store.Snapshot{
		Tunnels: []store.Tunnel{{
			ID: "t1", Name: "пул", Type: store.TunnelVLESS, Source: store.SourcePool,
			Raw: "https://example.org/protocol/vless", Enabled: true,
			Pool:          []store.PoolServer{{URL: poolServerURL, PingMS: 12}},
			PoolUpdatedAt: time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC),
		}},
		Rules: []store.Rule{{
			// Та же правка правила, что копится до кнопки «Применить».
			ID: "r1", Name: "правило", Action: store.ActionTunnel, TunnelID: "t1",
			Enabled: true, Domains: []string{"example.com", "недоприменённый.example"},
		}},
	}
	return base, fresh
}

// Обновление каталога везёт в конфиг состав пула и ничего кроме него: правки
// правил копятся до кнопки «Применить», и выкатывать их за пользователя
// расписание не вправе (issue #121, issue #125).
func TestApplyPoolsCarriesOnlyPoolServers(t *testing.T) {
	base, fresh := poolSnapshots()
	f := &fakeApplier{res: singbox.ApplyResult{Changed: true, Reloaded: true}}
	g := newApplyGate(f, base, nil)

	if _, err := g.applyPools(context.Background(), fresh); err != nil {
		t.Fatalf("применение состава пула: %v", err)
	}

	got := f.last()
	if len(got.Tunnels) != 1 || len(got.Tunnels[0].Pool) != 1 {
		t.Fatalf("в применённом снимке состав пула %+v", got.Tunnels)
	}
	if got.Tunnels[0].Pool[0].URL != poolServerURL {
		t.Errorf("состав пула не обновился: %q", got.Tunnels[0].Pool[0].URL)
	}
	if got.Tunnels[0].PoolUpdatedAt != fresh.Tunnels[0].PoolUpdatedAt {
		t.Errorf("время обхода каталога не перенесено: %v", got.Tunnels[0].PoolUpdatedAt)
	}
	if len(got.Rules) != 1 || len(got.Rules[0].Domains) != 1 {
		t.Errorf("в конфиг уехала неприменённая правка правила: %+v", got.Rules)
	}

	// Применённое состояние обновилось: следующий обход каталога отсчитывается
	// от него, а не от того, что было до обновления пула.
	if len(g.base.Tunnels[0].Pool) != 1 || g.base.Tunnels[0].Pool[0].URL != poolServerURL {
		t.Errorf("применённое состояние не запомнено: %+v", g.base.Tunnels[0].Pool)
	}
	// Исходный снимок остался нетронутым: он же лежит в кэше применения.
	if base.Tunnels[0].Pool[0].URL == poolServerURL {
		t.Error("применение переписало снимок на месте")
	}
}

// Пул, заведённый, но не применённый, расписание в конфиг не тащит: это такая
// же правка пользователя, как правило рядом.
func TestApplyPoolsIgnoresUnappliedPool(t *testing.T) {
	base, fresh := poolSnapshots()
	base.Tunnels = nil
	f := &fakeApplier{}
	g := newApplyGate(f, base, nil)

	if _, err := g.applyPools(context.Background(), fresh); err != nil {
		t.Fatalf("применение состава пула: %v", err)
	}
	if got := f.last(); len(got.Tunnels) != 0 {
		t.Errorf("неприменённый пул уехал в конфиг: %+v", got.Tunnels)
	}
}

// Кнопка «Применить» применяет снимок целиком и становится новой точкой
// отсчёта: иначе следующее обновление пула откатило бы только что применённое.
func TestApplyRemembersWholeSnapshot(t *testing.T) {
	base, fresh := poolSnapshots()
	f := &fakeApplier{}
	g := newApplyGate(f, base, nil)

	if _, err := g.Apply(context.Background(), fresh); err != nil {
		t.Fatalf("применение снимка: %v", err)
	}
	if _, err := g.applyPools(context.Background(), fresh); err != nil {
		t.Fatalf("применение состава пула: %v", err)
	}
	if got := f.last(); len(got.Rules[0].Domains) != 2 {
		t.Errorf("применённая правка правила откатилась: %+v", got.Rules)
	}
}

// Отказ проверки диск не трогает, поэтому применённым остаётся прежнее
// состояние. Отказ перезагрузки — трогает: конфиг уже лежит на диске, и считать
// его неприменённым значит откатить его следующим же обходом каталога.
func TestApplyGateRemembersByOutcome(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		remember bool
	}{
		{"конфиг отклонён рантаймом", singbox.ErrCheckFailed, false},
		{"сервис не перезагрузился", singbox.ErrReloadFailed, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, fresh := poolSnapshots()
			f := &fakeApplier{err: c.err}
			g := newApplyGate(f, base, nil)

			if _, err := g.applyPools(context.Background(), fresh); !errors.Is(err, c.err) {
				t.Fatalf("ошибка применения: %v", err)
			}
			got := g.base.Tunnels[0].Pool[0].URL == poolServerURL
			if got != c.remember {
				t.Errorf("состояние запомнено: %v, ожидалось %v", got, c.remember)
			}
		})
	}
}

// Обход, не изменивший состав, sing-box не перезапускает. Проверяется на
// настоящем применятеле: молчание держат два рубежа — расписание не будит
// применение зря, а применение сверяет байты с лежащим на диске конфигом.
// Лишний `reload-or-restart` рвёт соединения во всех туннелях сразу (issue #68).
func TestApplyPoolsSameCompositionDoesNotReload(t *testing.T) {
	st, _ := openStore(t)
	tn := createPool(t, st, "пул", "https://example.org/protocol/vless", true)
	servers := []store.PoolServer{{URL: poolServerURL, PingMS: 12}}
	if err := st.UpdateTunnelPool(context.Background(), tn.ID, servers, time.Now().UTC()); err != nil {
		t.Fatalf("запись состава пула: %v", err)
	}

	base, err := st.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}

	reloader := &countingReloader{}
	g := newApplyGate(&singbox.Applier{
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Checker:    okChecker{},
		Reloader:   reloader,
	}, base, nil)

	res, err := g.applyPools(context.Background(), base)
	if err != nil {
		t.Fatalf("первое применение: %v", err)
	}
	if !res.Changed || !res.Reloaded {
		t.Fatalf("первое применение прошло мимо диска: %+v", res)
	}

	res, err = g.applyPools(context.Background(), base)
	if err != nil {
		t.Fatalf("повторное применение: %v", err)
	}
	if res.Changed || res.Reloaded {
		t.Errorf("неизменившийся состав переписал конфиг: %+v", res)
	}
	if got := reloader.count(); got != 1 {
		t.Errorf("sing-box перезагружен %d раза на одном изменении состава", got)
	}
}

// Состав изменился — конфиг применён, без участия человека. До issue #121 у
// [lists.PoolManager.Updates] не было потребителя: свежие ключи ложились в БД и
// оставались там до нажатия «Применить».
func TestWatchPoolsAppliesFreshServers(t *testing.T) {
	st, _ := openStore(t)
	srv, _ := poolCatalogServer(t)
	sink := &logSink{}
	tn := createPool(t, st, "пул", srv.URL+"/protocol/vless", true)

	base, err := st.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("снимок состояния: %v", err)
	}
	if len(base.Tunnels) != 1 || len(base.Tunnels[0].Pool) != 0 {
		t.Fatalf("свежезаведённый пул уже с составом: %+v", base.Tunnels)
	}

	f := &fakeApplier{res: singbox.ApplyResult{Changed: true, Reloaded: true}}
	g := newApplyGate(f, base, sink.logger())

	m := lists.NewPoolManager(lists.PoolManagerOptions{
		Catalog: &lists.PoolCatalog{Client: srv.Client(), Log: sink.logger()},
		Writer:  st,
		Logger:  sink.logger(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go watchPools(ctx, st, m, g, sink.logger())

	changed, err := m.RefreshPool(ctx, lists.PoolTunnelFrom(tn))
	if err != nil {
		t.Fatalf("обход каталога: %v", err)
	}
	if !changed {
		t.Fatal("обход каталога не принёс состава")
	}

	waitFor(t, "состав пула не доехал до применения", func() bool { return f.calls() > 0 })
	got := f.last()
	if len(got.Tunnels) != 1 || len(got.Tunnels[0].Pool) == 0 {
		t.Fatalf("в применённом снимке пул пуст: %+v", got.Tunnels)
	}
	if sink.count("конфиг применён после обновления состава пула") == 0 {
		t.Error("применение конфига не отмечено в журнале")
	}
}
