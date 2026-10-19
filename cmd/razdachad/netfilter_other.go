//go:build !linux

package main

import (
	"context"
	"errors"
)

// errNotLinux — nftables и таблицы маршрутизации есть только в Linux. Демон
// собирается и на macOS ради разработки, но работать там ему нечем.
var errNotLinux = errors.New("правила и маршрутизация доступны только в Linux")

func startNetfilter(context.Context) (netfilter, error) {
	return netfilter{}, errNotLinux
}

func resetNetfilter() error { return errNotLinux }
