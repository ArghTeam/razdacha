package qr

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTerminalShape — размеры вывода: ширина в символах равна стороне матрицы с
// зоной тишины, высота вдвое меньше, потому что строка текста несёт две строки
// модулей.
func TestTerminalShape(t *testing.T) {
	m, err := Encode("razdacha")
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	out := m.Terminal(false)
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")

	wantWidth := m.Size() + 2*QuietZone
	wantHeight := (wantWidth + 1) / 2
	if len(lines) != wantHeight {
		t.Fatalf("строк %d, ожидалось %d", len(lines), wantHeight)
	}
	for i, line := range lines {
		if got := utf8.RuneCountInString(line); got != wantWidth {
			t.Fatalf("строка %d шириной %d символов, ожидалось %d", i, got, wantWidth)
		}
	}
}

// TestTerminalQuietZone — верхние и нижние строки вывода пустые: без поля
// сканер не находит границу кода.
func TestTerminalQuietZone(t *testing.T) {
	m, err := Encode("razdacha")
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(m.Terminal(false), "\n"), "\n")
	// Зона тишины — четыре строки модулей сверху и снизу, то есть две строки текста.
	for _, i := range []int{0, 1, len(lines) - 1} {
		if strings.TrimSpace(lines[i]) != "" {
			t.Fatalf("строка %d зоны тишины не пуста: %q", i, lines[i])
		}
	}
}

// TestTerminalHalfBlocks — каждая половина символа соответствует своему модулю.
func TestTerminalHalfBlocks(t *testing.T) {
	m, err := Encode("razdacha")
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(m.Terminal(false), "\n"), "\n")
	for i, line := range lines {
		row := i*2 - QuietZone
		for j, r := range []rune(line) {
			col := j - QuietZone
			upper, lower := m.Dark(row, col), m.Dark(row+1, col)
			var want rune
			switch {
			case upper && lower:
				want = blockBoth
			case upper:
				want = blockUpper
			case lower:
				want = blockLower
			default:
				want = blockNeither
			}
			if r != want {
				t.Fatalf("символ (%d,%d) = %q, ожидался %q", i, j, r, want)
			}
		}
	}
}

// TestTerminalColor — раскраска обязательна для тёмной темы: инвертированный
// код читает не всякий сканер, поэтому фон и текст задаются явно.
func TestTerminalColor(t *testing.T) {
	out, err := Terminal("razdacha", true)
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	if !strings.Contains(out, ansiOn) || !strings.Contains(out, ansiOff) {
		t.Fatal("в цветном выводе нет управляющих последовательностей")
	}
	plain, err := Terminal("razdacha", false)
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	if strings.Contains(plain, "\x1b") {
		t.Fatal("в бесцветном выводе оказалась управляющая последовательность")
	}
}
