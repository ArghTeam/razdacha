//go:build !linux

package main

import (
	"context"
	"errors"
	"log/slog"
)

// errNotLinux — интерфейс, nftables и таблицы маршрутизации есть только в
// Linux. CLI собирается и на macOS ради разработки, но снимать там нечего.
var errNotLinux = errors.New("снятие интерфейса и правил доступно только в Linux")

func resetNetwork(context.Context, *slog.Logger) error { return errNotLinux }
