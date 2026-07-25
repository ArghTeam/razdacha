// Command razdachad — демон селективного VPN-шлюза razdacha.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net/netip"
	"os"

	"github.com/ArghTeam/razdacha/internal/packaging"
)

// version подставляется линкером при сборке, см. Makefile.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	listen := flag.String("listen", packaging.DefaultUpstream,
		"адрес HTTP-сервера панели; только loopback — наружу его открывает nginx")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	if err := checkListen(*listen); err != nil {
		slog.Error("некорректный адрес панели", "ошибка", err)
		os.Exit(2)
	}

	slog.Error("демон ещё не реализован", "версия", version, "адрес", *listen)
	os.Exit(1)
}

// checkListen не пускает демон на адрес, видимый снаружи loopback. Панель
// открывает nginx на адресе wg0 (ADR 0008), сам демон не виден ни из
// интернета, ни из VPN.
func checkListen(addr string) error {
	ap, err := netip.ParseAddrPort(addr)
	if err != nil {
		return fmt.Errorf("адрес %q не разобран: %w", addr, err)
	}
	if !ap.Addr().IsLoopback() {
		return fmt.Errorf("%s не является loopback; панель открывает nginx, см. ADR 0008", ap.Addr())
	}
	return nil
}
