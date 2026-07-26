package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// maxToolOutput — сколько байт чужого вывода попадает в текст ошибки. Простыня
// на экране хуже, чем первые строки.
const maxToolOutput = 2 << 10

// nginxUnit — юнит nginx. Имена своих юнитов живут в internal/packaging, этот
// принадлежит пакету дистрибутива.
const nginxUnit = "nginx.service"

// systemctlRunner — вызов systemd. Отдельный тип нужен ради тестов: настоящий
// systemctl на машине разработчика запускать нечем, а порядок и набор команд —
// ровно то, что ломалось при обновлении (issue #80).
type systemctlRunner func(ctx context.Context, args ...string) error

// systemctl вызывает systemd.
//
// Это не спорит с инвариантом «netlink вместо бинарников»: тот про `nft`, `ip`
// и `wg`, где у нас есть свои интерфейсы к ядру. У systemd публичный интерфейс
// один — `systemctl`.
func systemctl(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := trimOutput(out); detail != "" {
		return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), detail)
	}
	return fmt.Errorf("systemctl %s: %w", strings.Join(args, " "), err)
}

// systemctlQuiet — вызов, неудача которого не повод останавливаться: юнита
// может не быть вовсе. Возвращает, удался ли вызов.
func systemctlQuiet(ctx context.Context, args ...string) bool {
	return exec.CommandContext(ctx, "systemctl", args...).Run() == nil
}

func trimOutput(out []byte) string {
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput]
	}
	return strings.TrimSpace(string(out))
}
