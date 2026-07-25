package singbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// maxToolOutput — сколько байт вывода чужого процесса попадает в текст ошибки.
// Ошибка уходит в UI как есть, и простыня на экране хуже, чем первые строки.
const maxToolOutput = 2 << 10

// BinaryChecker проверяет конфиг настоящим рантаймом: `sing-box check -c <файл>`.
// Никакой свой валидатор не заменит рантайм — принимает конфиг именно он.
type BinaryChecker struct {
	// Binary — путь или имя бинарника; пустое означает [DefaultBinary] в PATH.
	Binary string
}

// Check возвращает [ErrCheckFailed] с выводом рантайма в тексте: пользователю
// нужна причина отказа, а не код возврата.
func (c BinaryChecker) Check(ctx context.Context, path string) error {
	bin := c.Binary
	if bin == "" {
		bin = DefaultBinary
	}
	out, err := exec.CommandContext(ctx, bin, "check", "-c", path).CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := trimOutput(out); detail != "" {
		return fmt.Errorf("%w: %s", ErrCheckFailed, detail)
	}
	return fmt.Errorf("%w: %w", ErrCheckFailed, err)
}

// SystemctlReloader перезагружает штатный юнит sing-box.
//
// Вызов бинарника здесь не спорит с инвариантом «netlink вместо бинарников»: тот
// про nft/ip/wg, где у нас есть свои интерфейсы к ядру. Здесь мы управляем чужим
// сервисом, и systemctl — его единственный публичный интерфейс.
type SystemctlReloader struct {
	// Service — имя юнита; пустое означает [DefaultService].
	Service string
}

// Reload делает `systemctl reload <юнит>`.
func (r SystemctlReloader) Reload(ctx context.Context) error {
	unit := r.Service
	if unit == "" {
		unit = DefaultService
	}
	out, err := exec.CommandContext(ctx, "systemctl", "reload", unit).CombinedOutput()
	if err == nil {
		return nil
	}
	if detail := trimOutput(out); detail != "" {
		return fmt.Errorf("%w: %s", ErrReloadFailed, detail)
	}
	return fmt.Errorf("%w: %w", ErrReloadFailed, err)
}

// trimOutput приводит вывод процесса к одной пригодной для показа строке.
func trimOutput(out []byte) string {
	if len(out) > maxToolOutput {
		out = out[:maxToolOutput]
	}
	return strings.TrimSpace(string(out))
}
