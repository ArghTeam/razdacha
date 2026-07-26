package qr

import "strings"

// QuietZone — поле вокруг кода. Четыре модуля — минимум стандарта; без него
// сканер не находит границу символа на пёстром фоне терминала.
const QuietZone = 4

// Полублоки: один символ терминала выше, чем шире, примерно вдвое. Пара строк
// модулей в одну строку текста даёт квадратные модули на экране и вдвое
// уменьшает высоту вывода — конфиг WireGuard кодируется версией 13–15, а это
// под семьдесят строк модулей, целиком в экран они не помещаются.
const (
	blockBoth    = '█' // █ тёмные обе половины
	blockUpper   = '▀' // ▀ тёмная верхняя
	blockLower   = '▄' // ▄ тёмная нижняя
	blockNeither = ' '
)

// Цвета выставляются явно: в тёмной теме терминала «тёмный модуль» иначе
// оказался бы светлее фона, а инвертированный код читает не всякий сканер.
const (
	ansiOn  = "\x1b[47;30m" // белый фон, чёрный текст
	ansiOff = "\x1b[0m"
)

// Terminal рисует код полублоками. При color=true каждая строка обрамляется
// управляющими последовательностями, задающими белый фон и чёрный текст;
// при false выводится чистый текст — так вывод не портит лог установки,
// перенаправленный в файл.
func (m Matrix) Terminal(color bool) string {
	var b strings.Builder
	top := -QuietZone
	bottom := m.size + QuietZone
	left := -QuietZone
	right := m.size + QuietZone

	// Шаг две строки модулей: верхняя половина символа — row, нижняя — row+1.
	for row := top; row < bottom; row += 2 {
		if color {
			b.WriteString(ansiOn)
		}
		for col := left; col < right; col++ {
			switch upper, lower := m.Dark(row, col), m.Dark(row+1, col); {
			case upper && lower:
				b.WriteRune(blockBoth)
			case upper:
				b.WriteRune(blockUpper)
			case lower:
				b.WriteRune(blockLower)
			default:
				b.WriteRune(blockNeither)
			}
		}
		if color {
			b.WriteString(ansiOff)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// Terminal кодирует текст и сразу рисует его для терминала.
func Terminal(text string, color bool) (string, error) {
	m, err := Encode(text)
	if err != nil {
		return "", err
	}
	return m.Terminal(color), nil
}
