//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/ArghTeam/razdacha/internal/lists"
	"github.com/ArghTeam/razdacha/internal/netstack"
	"github.com/ArghTeam/razdacha/internal/store"
)

// startNetfilter заливает nft-правила и маршрутизацию помеченного трафика.
//
// Подсети берутся из правил в БД и из списков, которые качает планировщик
// (`mgr` может быть nil — тогда только ручные). После каждого прогона
// планировщика таблица перезаливается заново: сет — часть таблицы, отдельно от
// неё он не обновляется.
//
// Та же заливка уезжает в слой api замыканием (`netfilter.applyNft`): по
// `POST /api/apply` она перезаливает таблицу вместе с конфигом sing-box, иначе
// дописанные руками подсети ждали бы планового прогона списков до суток
// (issue #119), а при неподнявшемся планировщике — до перезапуска демона.
func startNetfilter(ctx context.Context, st *store.Store, mgr *lists.Manager,
	log *slog.Logger,
) (netfilter, error) {
	nft, err := netstack.NewNft()
	if err != nil {
		return netfilter{}, fmt.Errorf("подключение к nftables: %w", err)
	}

	route := netstack.NewRoute()
	wan, err := route.DetectWAN()
	if err != nil {
		return netfilter{}, fmt.Errorf("определение внешнего интерфейса: %w", err)
	}

	// Соединение с nftables одно на все заливки, а зовут их теперь двое —
	// планировщик списков и обработчик `POST /api/apply`. Мьютекс сериализует
	// их: делить netlink-соединение между одновременными транзакциями нельзя,
	// а заливка редкая, и очередь из двух ожидающих здесь ничего не стоит.
	var mu sync.Mutex
	apply := func(ctx context.Context) (int, error) {
		mu.Lock()
		defer mu.Unlock()

		// Маршрутизация заводится до nft и на каждом прогоне (issue #120).
		//
		// До — потому что метку ставит nft, а разбирает её policy routing: с
		// обратным порядком между заливкой и правилом остаётся окно, в котором
		// пакеты уже помечены, а таблицы 105 для них ещё нет. И отказ здесь
		// не оставляет систему в состоянии «nft залит, маршрутизации нет» —
		// прежняя таблица остаётся в ядре нетронутой.
		//
		// На каждом — потому что правило снимается не только нами: `ip rule
		// flush` чужого софта или сброс сети уносят его молча, а перезаливка
		// идёт при каждом изменении конфигурации. [netstack.Route.Apply]
		// идемпотентен, дублей от повторных вызовов не появляется.
		if err := route.Apply(); err != nil {
			return 0, fmt.Errorf("маршрутизация помеченного трафика: %w", err)
		}

		rules, err := st.Rules(ctx)
		if err != nil {
			return 0, fmt.Errorf("чтение правил: %w", err)
		}
		subnets := nftSubnets(ruleSubnets(rules), mgr)
		// Адрес резолвера читается на каждой заливке: на него DNAT-ит перехват
		// DNS, и разъехаться с адресом, который слушает sing-box, он не должен.
		settings, err := st.Settings(ctx)
		if err != nil {
			return 0, fmt.Errorf("чтение настроек: %w", err)
		}
		// Заливка идёт одной транзакцией (инвариант слоя netstack): окна без
		// правил не возникает, и обновление сета не рвёт живые соединения.
		rs, err := nft.Apply(netstack.NftConfig{
			WANInterface: wan,
			ResolverAddr: settings.WGServerAddress,
			Subnets:      subnets,
		})
		if err != nil {
			return 0, fmt.Errorf("заливка правил: %w", err)
		}
		if rs.SkippedSubnets > 0 {
			log.Warn("часть подсетей не разобрана", "пропущено", rs.SkippedSubnets)
		}
		return len(subnets), nil
	}

	count, err := apply(ctx)
	if err != nil {
		return netfilter{}, err
	}
	log.Info("правила залиты", "wan", wan, "подсетей", count)

	if mgr != nil {
		go watchLists(ctx, mgr, apply, log)
	}

	// Правила остаются в ядре после остановки демона: прямой трафик клиентов
	// не должен пропадать оттого, что демон перезапускается. Снимает их
	// --reset-network и удаление пакета.
	//
	// Диагностика читает состояние своим соединением ([netstack.DiagNft]), а
	// не тем, которым залиты правила: она ходит из обработчика HTTP, и
	// соединение nftables пришлось бы делить между запросами.
	return netfilter{
		stop:       func() {},
		nftState:   netstack.DiagNft,
		routeState: netstack.DiagRoute,
		applyNft:   apply,
	}, nil
}

// watchLists перезаливает правила после каждого прогона планировщика.
//
// Неудачная перезаливка только пишется в лог: в ядре остаётся прежняя таблица
// (пакет изменений откатывается целиком), клиенты продолжают работать, а
// следующий прогон списков попробует снова.
func watchLists(ctx context.Context, mgr *lists.Manager, apply func(context.Context) (int, error), log *slog.Logger) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-mgr.Updates():
		}
		count, err := apply(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("перезаливка правил после обновления списков", "ошибка", err)
			continue
		}
		log.Info("правила перезалиты после обновления списков", "подсетей", count)
	}
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
