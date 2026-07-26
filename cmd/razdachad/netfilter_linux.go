//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

// startNetfilter заливаетnft-правила и маршрутизацию помеченного трафика.
//
// Подсети берутся из правил в БД. Слой lists сюда ещё не подключён: его
// планировщик живёт отдельно от демона, и до его проводки в сет попадает
// только то, что пользователь ввёл руками.
func startNetfilter(ctx context.Context, st *store.Store, log *slog.Logger) (netfilter, error) {
	nft, err := netstack.NewNft()
	if err != nil {
		return netfilter{}, fmt.Errorf("подключение к nftables: %w", err)
	}

	route := netstack.NewRoute()
	wan, err := route.DetectWAN()
	if err != nil {
		return netfilter{}, fmt.Errorf("определение внешнего интерфейса: %w", err)
	}

	subnets, err := ruleSubnets(ctx, st)
	if err != nil {
		return netfilter{}, err
	}

	rs, err := nft.Apply(netstack.NftConfig{WANInterface: wan, Subnets: subnets})
	if err != nil {
		return netfilter{}, fmt.Errorf("заливка правил: %w", err)
	}
	if err := route.Apply(); err != nil {
		return netfilter{}, fmt.Errorf("маршрутизация помеченного трафика: %w", err)
	}

	log.Info("правила залиты", "wan", wan, "подсетей", len(subnets),
		"пропущено", rs.SkippedSubnets)

	// Правила остаются в ядре после остановки демона: прямой трафик клиентов
	// не должен пропадать оттого, что демон перезапускается. Снимает их
	// --reset-network и удаление пакета.
	//
	// Диагностика читает состояние своим соединением ([netstack.DiagNft]), а
	// не тем, которым залиты правила: она ходит из обработчика HTTP, и
	// соединение nftables пришлось бы делить между запросами.
	return netfilter{stop: func() {}, nftState: netstack.DiagNft}, nil
}

// resetNetfilter снимает всё, что демон добавил в сеть: таблицу, правило
// маршрутизации и таблицу 105. Интерфейс wg0 не трогается — он переживает
// сброс, иначе клиенты теряют связь из-за диагностической команды.
func resetNetfilter() error {
	nft, err := netstack.NewNft()
	if err != nil {
		return fmt.Errorf("подключение к nftables: %w", err)
	}
	if err := nft.Clear(); err != nil {
		return fmt.Errorf("снятие правил: %w", err)
	}
	if err := netstack.NewRoute().Clear(); err != nil {
		return fmt.Errorf("снятие маршрутизации: %w", err)
	}
	return nil
}

// ruleSubnets собирает подсети всех включённых правил, ведущих в туннель:
// для них FakeIP не работает, клиент идёт на настоящий адрес, и пометить его
// можно только по совпадению с сетом.
func ruleSubnets(ctx context.Context, st *store.Store) ([]string, error) {
	rules, err := st.Rules(ctx)
	if err != nil {
		return nil, fmt.Errorf("чтение правил: %w", err)
	}
	var out []string
	for _, r := range rules {
		if !r.Enabled || r.Action != store.ActionTunnel {
			continue
		}
		out = append(out, r.Subnets...)
	}
	return out, nil
}
