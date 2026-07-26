package main

import (
	"context"
	"log/slog"
	"reflect"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/store"
)

// poolTunnelsInterval — как часто набор пулов сверяется с БД.
//
// Пулы приходят через REST, и планировщик про них иначе не узнает; та же причина
// и то же значение, что у listSourcesInterval.
const poolTunnelsInterval = 30 * time.Second

// builtinPoolName — имя встроенного пула. Видно пользователю, поэтому по-русски и
// без слова «встроенный»: то, что запись заведена демоном, панель показывает сама.
const builtinPoolName = "Бесплатные VLESS"

// ensureBuiltinPool приводит БД к состоянию «встроенный пул бесплатных ключей есть».
//
// Заведён он будет выключенным: свежая установка не должна сама начинать ходить на
// чужой сайт за ключами. Пул, заведённый руками до этой версии, признаётся встроенным,
// а не дублируется вторым — про это и пишется в лог, потому что снаружи такая правка
// БД ничем себя не проявляет.
//
// Неудача демон не останавливает: без пула панель работает, а сообщение в логе
// объясняет, почему его нет (например, имя занято туннелем пользователя).
func ensureBuiltinPool(ctx context.Context, st *store.Store, log *slog.Logger) {
	res, err := st.EnsureBuiltinPool(ctx, builtinPoolName, lists.DefaultPoolCatalogURL)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("встроенный пул не заведён", "ошибка", err)
		}
		return
	}
	switch {
	case res.Created:
		log.Info("заведён встроенный пул бесплатных ключей",
			"туннель", res.Tunnel.Name, "каталог", res.Tunnel.Raw, "включён", res.Tunnel.Enabled)
	case res.Adopted:
		log.Info("существующий пул признан встроенным",
			"туннель", res.Tunnel.Name, "каталог", res.Tunnel.Raw)
	}
	// Пул в системе один. Остальные — след ручной правки БД или старой установки:
	// они остаются обычными туннелями и работают как работали, но молчать о том,
	// что встроенным стал один из нескольких, нельзя.
	if res.OtherPools > 0 {
		log.Warn("туннелей-пулов в БД больше одного, встроенным считается один",
			"встроенный", res.Tunnel.Name, "остальных", res.OtherPools)
	}
}

// startPools поднимает расписание обновления туннелей-пулов.
//
// Каталоги ключей живут на чужих сайтах, поэтому неудача здесь демон не
// останавливает: не обошёлся каталог — расписание повторит само, а пул останется
// с прежним составом серверов. Пустой пул просто выпадает из конфига вместе со
// своими правилами (ADR 0010), исправные туннели это не задевает.
func startPools(ctx context.Context, st *store.Store, log *slog.Logger) *lists.PoolManager {
	m := lists.NewPoolManager(lists.PoolManagerOptions{
		Catalog: &lists.PoolCatalog{Log: log},
		Writer:  st,
		Logger:  log,
	})

	tunnels := poolTunnels(ctx, st, log)
	m.SetTunnels(tunnels)

	// Start возвращается сразу: первый обход уходит в горутину, и старт демона не
	// ждёт чужого сайта.
	m.Start(ctx)
	go syncPoolTunnels(ctx, st, m, tunnels, log)
	return m
}

// syncPoolTunnels держит набор пулов равным состоянию БД. Появившийся пул
// обходится сразу: ждать двенадцать часов до первого состава серверов незачем.
func syncPoolTunnels(ctx context.Context, st *store.Store, m *lists.PoolManager,
	prev []lists.PoolTunnel, log *slog.Logger,
) {
	ticker := time.NewTicker(poolTunnelsInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		tunnels := poolTunnels(ctx, st, log)
		if reflect.DeepEqual(tunnels, prev) {
			continue
		}
		prev = tunnels
		m.SetTunnels(tunnels)
		log.Info("набор пулов изменился", "пулов", len(tunnels))
		if err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
			log.Warn("каталоги пулов обойдены не полностью", "ошибка", err)
		}
	}
}

// poolTunnels читает состояние и переводит его в набор пулов. Ошибка чтения —
// пустой набор: на следующем такте прочитаем снова, а ронять демон из-за
// неудачного запроса к БД нельзя.
func poolTunnels(ctx context.Context, st *store.Store, log *slog.Logger) []lists.PoolTunnel {
	snap, err := st.Snapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("чтение состояния для расписания пулов", "ошибка", err)
		}
		return nil
	}
	return lists.PoolTunnels(snap)
}
