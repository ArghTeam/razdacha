package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// applyGate — единственная дверь к конфигу sing-box.
//
// Дверей стало две: кнопка «Применить» в панели и расписание пулов, которое
// приносит свежие ключи само (ADR 0010). Обе ведут в один файл и один
// `reload-or-restart`, поэтому идут через один мьютекс: параллельные применения
// разошлись бы на `sameOnDisk` и перезапустили бы sing-box дважды подряд.
//
// Второе назначение — помнить применённое состояние. Правки через REST ложатся
// в БД сразу, а применяются пакетно по кнопке (docs/05-api.md), то есть в БД
// рядом со свежим составом пула лежат правки правил, которых пользователь ещё
// не применял. Обновление каталога не повод выкатывать их за него, поэтому
// расписание применяет не снимок БД целиком, а состав пулов поверх снимка,
// который уже в работе.
type applyGate struct {
	inner api.Applier
	log   *slog.Logger

	mu sync.Mutex
	// base — состояние, из которого собран конфиг, лежащий на диске. Начальное
	// значение — снимок БД на старте демона: конфиг собран из неё же, если
	// только пользователь не оставил правки неприменёнными перед перезапуском.
	// В этом случае первое же обновление пула их применит — ровно то, что
	// сделала бы кнопка, и не хуже.
	base store.Snapshot
}

// newApplyGate собирает дверь поверх готового применятеля. Применятель
// интерфейсом, а не *singbox.Applier: в тестах ни рантайма, ни systemd нет.
func newApplyGate(inner api.Applier, base store.Snapshot, log *slog.Logger) *applyGate {
	if log == nil {
		log = slog.Default()
	}
	return &applyGate{inner: inner, base: base, log: log}
}

// Apply применяет снимок целиком — это то, что делает `POST /api/apply`.
func (g *applyGate) Apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.apply(ctx, snap)
}

// applyPools применяет свежий состав пулов и ничего кроме него: всё остальное
// берётся из состояния, которое уже в работе.
func (g *applyGate) applyPools(ctx context.Context, fresh store.Snapshot) (singbox.ApplyResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.apply(ctx, withPoolServers(g.base, fresh))
}

// apply вызывает применятель и запоминает применённое состояние.
//
// Запоминается оно и при отказе перезагрузки: конфиг в этот момент уже лежит на
// диске и его подхватит следующий старт сервиса ([singbox.Applier.Apply]).
// Считать его неприменённым значило бы собрать следующий конфиг пула из
// состояния, которого на диске давно нет, — то есть откатить чужие правки.
// Отказ проверки и отказ генерации диск не трогают, там прежнее состояние и
// остаётся применённым.
func (g *applyGate) apply(ctx context.Context, snap store.Snapshot) (singbox.ApplyResult, error) {
	res, err := g.inner.Apply(ctx, snap)
	if err == nil || errors.Is(err, singbox.ErrReloadFailed) {
		g.base = snap
	}
	return res, err
}

// withPoolServers переносит состав пулов из свежего снимка в применённый.
//
// Переносится только состав и время обхода, и только у пулов, которые в
// применённом состоянии уже есть: заведённый, но не применённый пул — такая же
// правка пользователя, как правило, и выкатывать её за него нечего.
func withPoolServers(base, fresh store.Snapshot) store.Snapshot {
	pools := make(map[string]store.Tunnel, len(fresh.Tunnels))
	for _, t := range fresh.Tunnels {
		if t.Source == store.SourcePool {
			pools[t.ID] = t
		}
	}

	out := base
	out.Tunnels = append([]store.Tunnel(nil), base.Tunnels...)
	for i, t := range out.Tunnels {
		if t.Source != store.SourcePool {
			continue
		}
		f, ok := pools[t.ID]
		if !ok {
			continue
		}
		out.Tunnels[i].Pool = f.Pool
		out.Tunnels[i].PoolUpdatedAt = f.PoolUpdatedAt
	}
	return out
}

// startApply собирает применятель конфига и подписывает его на обновления
// состава пулов.
//
// До этой подписки у [lists.PoolManager.Updates] не было ни одного потребителя:
// расписание писало свежие ключи в БД, а работающий sing-box жил со старыми,
// пока кто-нибудь не нажимал «Применить». Пока в окне конфига оставался хоть
// один живой ключ, это скрывал `urltest`; когда умирали все, правила пула
// начинали отказывать (ADR 0013) и лежали так до ручного вмешательства —
// при том, что рабочие ключи уже были в БД (issue #121).
func startApply(ctx context.Context, st *store.Store, m *lists.PoolManager,
	plain singbox.PlainLists, log *slog.Logger,
) (*applyGate, error) {
	base, err := st.Snapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("чтение состояния для применения конфига: %w", err)
	}

	a := singbox.NewApplier()
	a.PlainLists = plain
	a.Log = log

	g := newApplyGate(a, base, log)
	if m != nil {
		go watchPools(ctx, st, m, g, log)
	}
	return g, nil
}

// watchPools применяет конфиг после каждого обхода, изменившего состав пула.
//
// Обход, ничего не изменивший, до сюда не доходит: [lists.PoolManager] молчит,
// когда состав тот же (ADR 0010, issue #68). Второй рубеж — сравнение с
// лежащим на диске конфигом внутри применятеля; лишний
// `systemctl reload-or-restart` рвёт соединения во всех туннелях сразу, а не
// только в обновившемся пуле.
//
// Таблица nft здесь не перезаливается: состав пула не меняет ни подсетей
// правил, ни адреса резолвера, а перезаливка забрала бы в сет подсети правил,
// которых пользователь ещё не применял.
func watchPools(ctx context.Context, st *store.Store, m *lists.PoolManager,
	g *applyGate, log *slog.Logger,
) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.Updates():
		}

		snap, err := st.Snapshot(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("чтение состояния после обновления пула", "ошибка", err)
			continue
		}

		res, err := g.applyPools(ctx, snap)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			// Прежний конфиг остаётся в силе, а пул — со старыми ключами до
			// следующего обхода. Ронять демон не за что: остальные туннели
			// работают.
			log.Error("конфиг не применён после обновления пула", "ошибка", err)
			continue
		}
		if !res.Changed {
			log.Debug("состав пула обновлён, конфиг тот же", "путь", res.Path)
			continue
		}
		log.Info("конфиг применён после обновления состава пула",
			"путь", res.Path, "перезагружен", res.Reloaded)
	}
}
