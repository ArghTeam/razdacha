//go:build linux

package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ArghTeam/razdacha/internal/netstack"
)

// resetNetwork возвращает сеть в исходное состояние: снимает wg0, таблицу
// `inet razdacha`, правило маршрутизации и таблицу 105.
//
// Порядок обратный установке: сначала интерфейс, потом правила — так между
// шагами не остаётся окна, в котором правила ссылаются на исчезнувший
// интерфейс.
//
// Ошибки собираются, а не прерывают проход: удаление обязано доходить до конца.
// Половина снятого хуже, чем ничего — именно на этом Podkop и спотыкается при
// переустановке (docs/08-install-upgrade.md).
func resetNetwork(_ context.Context, log *slog.Logger) error {
	var errs []error

	if err := deleteWG(log); err != nil {
		errs = append(errs, err)
	}

	nft, err := netstack.NewNft()
	if err != nil {
		errs = append(errs, err)
	} else if err := nft.Clear(); err != nil {
		errs = append(errs, err)
	} else {
		log.Info("nft-правила сняты")
	}

	if err := netstack.NewRoute().Clear(); err != nil {
		errs = append(errs, err)
	} else {
		log.Info("маршрутизация помеченного трафика снята")
	}

	return errors.Join(errs...)
}

// deleteWG снимает интерфейс. Хранилище ключей менеджеру не нужно: удаление
// интерфейса ключей не читает, а БД к этому моменту может быть уже удалена.
func deleteWG(log *slog.Logger) error {
	wg, err := netstack.NewWGManager(netstack.WGConfig{Name: netstack.DefaultWGInterface}, nil, log)
	if err != nil {
		return err
	}
	defer func() { _ = wg.Close() }()
	return wg.Down(context.Background())
}
