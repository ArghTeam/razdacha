//go:build !linux

package main

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ArghTeam/razdacha/internal/store"
)

// errNotLinux — nftables и таблицы маршрутизации есть только в Linux. Демон
// собирается и на macOS ради разработки, но работать там ему нечем.
var errNotLinux = errors.New("правила и маршрутизация доступны только в Linux")

func startNetfilter(context.Context, *store.Store, *slog.Logger) (func(), error) {
	return nil, errNotLinux
}

func resetNetfilter() error { return errNotLinux }
