package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"reflect"
	"time"

	"github.com/ArghTeam/razdacha/internal/api"
	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/restorestate"
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

// startLists starts asynchronous fetches. Failure retains committed list content
// during recovery; a new candidate requiring any missing source is rejected.
func startLists(ctx context.Context, st *store.Store, dbPath string, log *slog.Logger, restore ...*restorestate.Controller) *lists.Manager {
	fetcher, err := lists.NewFetcher(lists.Options{Dir: listsCacheDir(dbPath), Logger: log})
	if err != nil {
		log.Error("кэш списков не открыт, списки не обновляются", "ошибка", err)
		return nil
	}

	var ctl *restorestate.Controller
	if len(restore) > 0 {
		ctl = restore[0]
	}
	m := lists.NewManager(lists.ManagerOptions{Fetcher: fetcher, Logger: log, Allow: ctl.Allowed})
	if settings, err := st.Settings(ctx); err != nil {
		log.Error("чтение настроек для планировщика списков", "ошибка", err)
	} else {
		m.SetInterval(settings.ListUpdateInterval)
	}
	src := listSources(ctx, st, log)
	m.SetSources(src)

	// Start возвращается сразу: первый прогон уходит в горутину, и старт демона
	// не ждёт ни сети, ни чужих серверов.
	if len(restore) == 0 {
		m.Start(ctx)
	}
	// Source reconciliation starts after the apply gate can retain committed sources.
	return m
}

// syncListSources retains applied and pending sources. Pending edits may request
// downloads, but only committed policy decides which content reaches the network.
func syncListSources(ctx context.Context, st *store.Store, m *lists.Manager, prev []lists.Source, applied func() []lists.Source, log *slog.Logger) {
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
		seen := make(map[string]bool, len(src))
		for _, v := range src {
			seen[v.URL] = true
		}
		for _, v := range applied() {
			if !seen[v.URL] {
				src = append(src, v)
				seen[v.URL] = true
			}
		}
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

// plainLists freezes all source contents, including immutable native artifact authority.
func plainLists(m *lists.Manager) singbox.PlainLists {
	if m == nil {
		return nil
	}
	return frozenPlainLists(m.Snapshot())
}

func frozenPlainLists(contents map[string]lists.List) singbox.PlainLists {
	return func(url string) (singbox.PlainList, bool) {
		l, ok := contents[url]
		return singbox.PlainList{Domains: append([]string(nil), l.Domains...), Subnets: append([]string(nil), l.Subnets...), Native: l.Native}, ok
	}
}

// listStates — проводка состояния обновления списков в панель.
//
// Тем же приёмом, что и plainLists: слой api про кэш и расписание не знает, а
// строка правила обязана отличать список, обновившийся сегодня, от списка,
// который не обновился с ошибкой, и от списка, до которого планировщик ещё не
// дошёл (issue #149).
//
// Планировщик не поднялся — замыкание пустое, и панель говорит «неизвестно», а
// не рисует удачное обновление: списки в этом состоянии не обновляются вовсе.
func listStates(m *lists.Manager) func() map[string]api.ListState {
	if m == nil {
		return nil
	}
	return func() map[string]api.ListState {
		src := m.States()
		out := make(map[string]api.ListState, len(src))
		for url, st := range src {
			out[url] = api.ListState{
				UpdatedAt: st.UpdatedAt,
				FailedAt:  st.FailedAt,
				Err:       st.Err,
				Cached:    st.Cached,
			}
		}
		return out
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
