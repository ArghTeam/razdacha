// Command razdachad — демон селективного VPN-шлюза razdacha.
package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
)

// version подставляется линкером при сборке, см. Makefile.
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "показать версию и выйти")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.Error("демон ещё не реализован", "версия", version)
	os.Exit(1)
}
