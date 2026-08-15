package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// poolTunnelsInterval — как часто набор пулов сверяется с БД.
//
// Пулы приходят через REST, и планировщик про них иначе не узнает; та же причина
// и то же значение, что у listSourcesInterval.
const poolTunnelsInterval = 30 * time.Second

// builtinPoolName — имя встроенного пула. Видно пользователю, поэтому по-русски и
// без слова «встроенный»: то, что запись заведена демоном, панель показывает сама.
//
// Протокола в имени нет: он теперь свойство каталога, а не пула (ADR 0015), и
// «Бесплатные VLESS» на shadowsocks-пуле было бы враньём.
const builtinPoolName = "Бесплатные ключи"

// retiredBuiltinPoolName — имя, под которым встроенный пул заводился до ADR 0015.
//
// Переименовывается только оно: имя, которое пользователь поменял сам, — его, и
// трогать его демон не вправе.
const retiredBuiltinPoolName = "Бесплатные VLESS"

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
	typ, err := lists.PoolKeyType(lists.DefaultPoolCatalogURL)
	if err != nil {
		log.Error("у каталога по умолчанию нет драйвера", "каталог",
			lists.DefaultPoolCatalogURL, "ошибка", err)
		return
	}

	res, err := st.EnsureBuiltinPool(ctx, builtinPoolName, lists.DefaultPoolCatalogURL, typ)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("встроенный пул не заведён", "ошибка", err)
		}
		return
	}
	retargetDeadCatalog(ctx, st, res.Tunnel, typ, log)
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

// retargetDeadCatalog переводит встроенный пул с умершего каталога на живой.
//
// Адрес каталога лежит в БД у самого туннеля, поэтому обновление демона его не
// меняет: у всех, кто поставил razdacha до ADR 0015, там остался мёртвый
// vpnkeys.me — обход упирается в несуществующий хост, а пул стоит пустым. Молча
// пустой пул недопустим (issue #153), поэтому адрес и протокол переписываются, а
// состав чистится: в нём ключи чужого протокола с закрывшегося сайта.
//
// Только каталог из списка закрывшихся и только у встроенного пула: адрес, который
// вписал человек, — его решение, и подменять его нельзя. Каталог живого, но
// неизвестного нам сайта останется как есть и получит внятную ошибку при обходе.
//
// Заодно переименовывается запись, если имя осталось прежним, — протокол пула
// перестал быть vless, и «Бесплатные VLESS» на shadowsocks было бы враньём. Имя,
// поменянное пользователем, остаётся его.
func retargetDeadCatalog(ctx context.Context, st *store.Store, t store.Tunnel,
	typ store.TunnelType, log *slog.Logger,
) {
	if !t.Builtin || !lists.PoolCatalogRetired(t.Raw) {
		return
	}
	if err := st.RetargetBuiltinPool(ctx, t.ID, lists.DefaultPoolCatalogURL, typ); err != nil {
		if ctx.Err() == nil {
			log.Warn("каталог встроенного пула не переведён на живой источник",
				"туннель", t.Name, "каталог", t.Raw, "ошибка", err)
		}
		return
	}
	log.Warn("каталог встроенного пула закрылся, пул переведён на новый источник",
		"туннель", t.Name, "было", t.Raw, "стало", lists.DefaultPoolCatalogURL,
		"протокол", typ, "серверов_сброшено", len(t.Pool))

	if t.Name != retiredBuiltinPoolName {
		return
	}
	t.Name, t.Raw, t.Type, t.Pool, t.PoolUpdatedAt = builtinPoolName,
		lists.DefaultPoolCatalogURL, typ, nil, time.Time{}
	if err := st.UpdateTunnel(ctx, t); err != nil {
		// Имя могло быть занято чужим туннелем — это не повод шуметь ошибкой:
		// пул уже переехал, а название осталось прежним.
		if ctx.Err() == nil {
			log.Info("встроенный пул не переименован", "туннель", t.Name, "ошибка", err)
		}
		return
	}
	log.Info("встроенный пул переименован", "было", retiredBuiltinPoolName,
		"стало", builtinPoolName)
}

// startPools поднимает расписание обновления туннелей-пулов.
//
// Каталоги ключей живут на чужих сайтах, поэтому неудача здесь демон не
// останавливает: не обошёлся каталог — расписание повторит само, а пул останется
// с прежним составом серверов. Пустой пул просто выпадает из конфига вместе со
// своими правилами (ADR 0010), исправные туннели это не задевает.
func startPools(ctx context.Context, st *store.Store, log *slog.Logger) *lists.PoolManager {
	m := lists.NewPoolManager(lists.PoolManagerOptions{
		Catalog: &lists.PoolCatalog{Log: log, CheckKey: poolKeyCheck},
		Writer:  st,
		Logger:  log,
	})

	tunnels := poolTunnels(ctx, st, log)
	m.SetTunnels(tunnels)

	// Start возвращается сразу: первый обход уходит в горутину, и старт демона не
	// ждёт чужого сайта.
	m.Start(ctx)
	go syncPoolTunnels(ctx, st, m, tunnels, poolTunnelsInterval, log)
	return m
}

// poolKeyCheck отвергает ключ каталога, который не осилит генератор конфига.
//
// Замыкание от демона, а не вызов внутри слоя lists: слой списков про singbox не
// знает — та же связь, что у `PlainLists` в обратную сторону. Ключ, не прошедший наш
// парсер, в БД не попадает вовсе: в конфиге он занимал бы место участника группы и
// проходил бы как исправный до первой пробы, а генератор всё равно выбросил бы его с
// предупреждением на каждой генерации (issue #153).
func poolKeyCheck(raw string) error {
	res, err := singbox.Parse(raw)
	if err != nil {
		return err
	}
	if res.Outbound == nil {
		// Участники группы `urltest` — только outbound'ы: endpoint (wireguard)
		// в неё не встаёт.
		return fmt.Errorf("ключ разобрался, но outbound из него не вышел")
	}
	return nil
}

// syncPoolTunnels держит набор пулов равным состоянию БД. Появившийся пул
// обходится сразу: ждать двенадцать часов до первого состава серверов незачем.
//
// Свежий набор уезжает в расписание на каждом такте — там лежит и состав серверов, с
// которым сводится обход каталога, а пишет этот состав в БД само расписание. Внеплановый
// же обход запускает только изменение [lists.PoolIdentity]: сравнивать целиком
// [lists.PoolTunnel] нельзя, состав серверов в нём меняется после каждого обхода, и
// каталог обходился бы каждые полминуты вместо двенадцати часов (issue #77).
// Интервал сверки передаётся параметром: в проде это poolTunnelsInterval, в тестах —
// доли секунды, иначе проверка расписания стоила бы полминуты ожидания.
func syncPoolTunnels(ctx context.Context, st *store.Store, m *lists.PoolManager,
	prev []lists.PoolTunnel, interval time.Duration, log *slog.Logger,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		tunnels := poolTunnels(ctx, st, log)
		changed := !lists.SamePoolSet(tunnels, prev)
		prev = tunnels
		m.SetTunnels(tunnels)
		if !changed {
			continue
		}
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
