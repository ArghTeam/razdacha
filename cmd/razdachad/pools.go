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

// igareckCatalogURL — источник ключей встроенного общего пула (ADR 0018). Пул один,
// без раскладки по странам: драйвер слоя lists отдаёт все прошедшие парсер ключи
// целиком. Базовый адрес — зеркало CDN githack поверх репозитория
// igareck/vpn-configs-for-russia; точные файлы и разбор — за драйвером, хранилищу
// достаточно строки, чтобы пул был засеян.
const igareckCatalogURL = "https://raw.githack.com/igareck/vpn-configs-for-russia/main/"

// poolCatalogType — тип встроенного пула. Косметичен: участники группы urltest
// разбираются каждый по своей ссылке (ADR 0010/0015), настоящий протокол ключа
// приходит из каталога. Разумный дефолт — vless: конфиги igareck в основном vless.
const poolCatalogType = store.TunnelVLESS

// ensureBuiltinPool приводит БД к состоянию «встроенный общий пул есть» (ADR 0018).
// Заведён он выключенным: свежая установка не должна сама ходить на чужой сайт за
// ключами. После апгрейда с версии со странами (ADR 0017) семь страновых пулов
// сворачиваются в один, а ссылавшиеся на удалённые правила отвязываются — про каждое
// пишется в лог, потому что снаружи такая правка БД ничем себя не проявляет.
//
// Неудача демон не останавливает: без пула панель работает, а сообщение в логе
// объясняет, почему его нет (например, имя пула занято туннелем пользователя).
func ensureBuiltinPool(ctx context.Context, st *store.Store, log *slog.Logger) {
	res, err := st.EnsureBuiltinPool(ctx, igareckCatalogURL, poolCatalogType)
	if err != nil {
		if ctx.Err() == nil {
			log.Warn("встроенный пул не заведён", "ошибка", err)
		}
		return
	}
	if res.Created {
		log.Info("заведён встроенный общий пул", "каталог", igareckCatalogURL)
	}
	for _, name := range res.RemovedExtra {
		log.Warn("страновой встроенный пул свёрнут в общий (ADR 0018)", "туннель", name)
	}
	for _, r := range res.DetachedRules {
		if r.Disabled {
			log.Warn("правило ссылалось на свёрнутый пул и выключено", "правило", r.Name)
		} else {
			log.Warn("у правила отвязано второе звено цепи на свёрнутый пул", "правило", r.Name)
		}
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
