package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"time"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/singbox"
	"github.com/ArghTeam/razdacha/internal/store"
)

// listSourcesInterval — как часто набор качаемых списков сверяется с БД.
//
// Правила приходят через REST, а планировщик про них узнать иначе не может:
// событийная перевыдача приедет вместе с применением конфига (issue #30). До
// неё периодическая сверка — способ не заставлять пользователя перезапускать
// демон после добавления правила со списком.
const listSourcesInterval = 30 * time.Second

// listsCacheDir — каталог кэша списков рядом с файлом состояния: в проде это
// /var/lib/razdacha/lists, в тестах и при -db во временном каталоге — там же,
// где БД, и отдельного флага для этого не нужно.
func listsCacheDir(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "lists")
}

// startLists поднимает планировщик списков.
//
// Списки качаются с чужих серверов, поэтому ни одна неудача здесь демон не
// останавливает: не открылся кэш — возвращается nil, и демон работает без
// списков; не скачался список — планировщик повторит сам с нарастающей паузой.
// Единственное следствие — в nft-сет не попадут подсети, а трафик к ним пойдёт
// напрямую, и это лучше, чем не поднявшийся шлюз.
func startLists(ctx context.Context, st *store.Store, dbPath string, log *slog.Logger) *lists.Manager {
	fetcher, err := lists.NewFetcher(lists.Options{Dir: listsCacheDir(dbPath), Logger: log})
	if err != nil {
		log.Error("кэш списков не открыт, списки не обновляются", "ошибка", err)
		return nil
	}

	m := lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: log})
	if settings, err := st.Settings(ctx); err != nil {
		log.Error("чтение настроек для планировщика списков", "ошибка", err)
	} else {
		m.SetInterval(settings.ListUpdateInterval)
	}
	src := listSources(ctx, st, log)
	m.SetSources(src)

	// Start возвращается сразу: первый прогон уходит в горутину, и старт демона
	// не ждёт ни сети, ни чужих серверов.
	m.Start(ctx)
	go syncListSources(ctx, st, m, src, log)
	return m
}

// syncListSources держит набор источников и интервал равными состоянию БД.
// Изменившийся набор дозагружается сразу: ждать сутки до следующего прогона
// после добавления правила незачем.
func syncListSources(ctx context.Context, st *store.Store, m *lists.Manager, prev []lists.Source, log *slog.Logger) {
	ticker := time.NewTicker(listSourcesInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		if settings, err := st.Settings(ctx); err != nil {
			log.Error("чтение настроек для планировщика списков", "ошибка", err)
		} else {
			m.SetInterval(settings.ListUpdateInterval)
		}

		src := listSources(ctx, st, log)
		if reflect.DeepEqual(src, prev) {
			continue
		}
		prev = src
		m.SetSources(src)
		log.Info("набор списков изменился", "источников", len(src))
		if err := m.Refresh(ctx); err != nil && ctx.Err() == nil {
			log.Warn("новые списки скачаны не полностью", "ошибка", err)
		}
	}
}

// listSources читает состояние и переводит его в набор качаемых списков.
// Ошибка чтения — пустой набор: на следующем такте прочитаем снова, а ронять
// демон из-за неудачного запроса к БД нельзя.
func listSources(ctx context.Context, st *store.Store, log *slog.Logger) []lists.Source {
	snap, err := st.Snapshot(ctx)
	if err != nil {
		if ctx.Err() == nil {
			log.Error("чтение состояния для планировщика списков", "ошибка", err)
		}
		return nil
	}
	return lists.Sources(snap)
}

// plainLists — проводка разобранных plain-списков в генератор конфига.
//
// Списки, которые sing-box не читает сам (не .srs и не .json), качает и
// разбирает планировщик, а генератор кладёт их домены и подсети в inline-набор
// правила. Без этой проводки домены пропадали бы молча, а правило, у которого
// такой список единственный, выпадало бы из конфига — и его трафик уходил бы
// напрямую по route.final (issue #125).
//
// Планировщик может быть не поднят: тогда замыкание пустое, и генератор
// собирает конфиг без таких наборов.
func plainLists(m *lists.Manager) singbox.PlainLists {
	if m == nil {
		return nil
	}
	return func(url string) (singbox.PlainList, bool) {
		l, ok := m.List(url)
		if !ok {
			return singbox.PlainList{}, false
		}
		return singbox.PlainList{Domains: l.Domains, Subnets: l.Subnets}, true
	}
}

// ruleSubnets собирает подсети правил, которым нужна метка nft. Отбор —
// [netstack.RuleMarksSubnets], там же записано, почему действие правила в нём
// не участвует (issue #126).
//
// Функция лежит рядом с nftSubnets, а не в netfilter_linux.go: она про
// содержимое сета, а не про netlink, и на платформе без nftables она обязана
// собираться — иначе её тесты не идут нигде, кроме Linux.
func ruleSubnets(rules []store.Rule) []string {
	var out []string
	for _, r := range rules {
		if !netstack.RuleMarksSubnets(r) {
			continue
		}
		out = append(out, r.Subnets...)
	}
	return out
}

// nftSubnets — что уходит в сет razdacha_subnets: подсети, введённые руками, и
// подсети из загруженных списков. Дубли снимет слой netstack при разборе.
//
// Планировщик может быть не поднят (startLists вернул nil) или ещё ничего не
// успеть скачать — тогда остаются одни ручные подсети, и это рабочее состояние,
// а не ошибка.
func nftSubnets(ruleSubnets []string, m *lists.Manager) []string {
	out := append([]string(nil), ruleSubnets...)
	if m == nil {
		return out
	}
	return append(out, m.Subnets()...)
}
