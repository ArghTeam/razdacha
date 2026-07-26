package qr

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// vector — один golden-случай: текст и ожидаемая матрица построчно, где «1» —
// тёмный модуль.
type vector struct {
	Name string   `json:"name"`
	Text string   `json:"text"`
	Size int      `json:"size"`
	Rows []string `json:"rows"`
}

func load(t *testing.T, path string) []vector {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("чтение %s: %v", path, err)
	}
	var out []vector
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("разбор %s: %v", path, err)
	}
	if len(out) == 0 {
		t.Fatalf("%s пуст", path)
	}
	return out
}

func rows(m Matrix) []string {
	out := make([]string, m.Size())
	for r := 0; r < m.Size(); r++ {
		var b strings.Builder
		for c := 0; c < m.Size(); c++ {
			if m.Dark(r, c) {
				b.WriteByte('1')
			} else {
				b.WriteByte('0')
			}
		}
		out[r] = b.String()
	}
	return out
}

func diff(got, want []string) string {
	for i := range want {
		if i >= len(got) {
			return "не хватает строк"
		}
		if got[i] != want[i] {
			return "строка " + string(rune('0'+i/10)) + string(rune('0'+i%10)) +
				":\n получено " + got[i] + "\n ожидалось " + want[i]
		}
	}
	return ""
}

// TestEncodeGolden сверяет вывод с кодером панели (`ui/dist/js/qr.js`):
// векторы в testdata сняты с него, а его вывод проверен сканером.
func TestEncodeGolden(t *testing.T) {
	for _, v := range load(t, "testdata/vectors.json") {
		t.Run(v.Name, func(t *testing.T) {
			m, err := Encode(v.Text)
			if err != nil {
				t.Fatalf("кодирование: %v", err)
			}
			if m.Size() != v.Size {
				t.Fatalf("сторона %d, ожидалась %d", m.Size(), v.Size)
			}
			if d := diff(rows(m), v.Rows); d != "" {
				t.Fatalf("матрица разошлась, %s", d)
			}
		})
	}
}

// TestEncodeMatchesQrencode — сверка с независимой реализацией.
//
// Полного совпадения здесь не требуется, и это не послабление: маска выбирается
// штрафной функцией, а libqrencode считает правило 3 (узор 1:1:3:1:1) иначе, чем
// написано в стандарте, и на части входов выбирает другую маску. Обе картинки
// при этом валидны — маска влияет на читаемость, а не на содержимое. Проверяется
// то, что от расхождения не зависит: если хоть одна из восьми масок даёт матрицу
// байт в байт как у qrencode, значит совпали и версия, и поток данных, и
// коррекция Рида — Соломона, и раскладка по зигзагу.
func TestEncodeMatchesQrencode(t *testing.T) {
	for _, v := range load(t, "testdata/qrencode.json") {
		t.Run(v.Name, func(t *testing.T) {
			version, err := pickVersion(len(v.Text))
			if err != nil {
				t.Fatalf("выбор версии: %v", err)
			}
			if got := version*4 + 17; got != v.Size {
				t.Fatalf("версия %d даёт сторону %d, у qrencode %d", version, got, v.Size)
			}
			stream := bitStream(v.Text, version)
			for mask := 0; mask < 8; mask++ {
				if diff(rows(place(version, stream, mask)), v.Rows) == "" {
					return
				}
			}
			t.Fatal("ни одна из восьми масок не воспроизвела матрицу qrencode")
		})
	}
}

// TestEncodeStructure проверяет то, за что отвечает скелет: поисковые узоры по
// трём углам, синхроузоры и всегда тёмный модуль.
func TestEncodeStructure(t *testing.T) {
	m, err := Encode("razdacha")
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	size := m.Size()
	corners := [][2]int{{0, 0}, {0, size - 7}, {size - 7, 0}}
	for _, c := range corners {
		for r := 0; r < 7; r++ {
			for col := 0; col < 7; col++ {
				want := r == 0 || r == 6 || col == 0 || col == 6 ||
					(r >= 2 && r <= 4 && col >= 2 && col <= 4)
				if got := m.Dark(c[0]+r, c[1]+col); got != want {
					t.Fatalf("поисковый узор в (%d,%d): модуль (%d,%d) = %v, ожидался %v",
						c[0], c[1], r, col, got, want)
				}
			}
		}
	}
	for i := 8; i < size-8; i++ {
		if m.Dark(6, i) != (i%2 == 0) || m.Dark(i, 6) != (i%2 == 0) {
			t.Fatalf("синхроузор разошёлся на %d", i)
		}
	}
	if !m.Dark(size-8, 8) {
		t.Fatal("тёмный модуль (size-8, 8) оказался светлым")
	}
}

// TestDarkOutside — координаты вне матрицы считаются светлыми: на этом держится
// отрисовка зоны тишины.
func TestDarkOutside(t *testing.T) {
	m, err := Encode("razdacha")
	if err != nil {
		t.Fatalf("кодирование: %v", err)
	}
	for _, c := range [][2]int{{-1, 0}, {0, -1}, {m.Size(), 0}, {0, m.Size()}, {-4, -4}} {
		if m.Dark(c[0], c[1]) {
			t.Fatalf("модуль (%d,%d) вне матрицы оказался тёмным", c[0], c[1])
		}
	}
}

// TestEncodeTooLong — в версию 40 на уровне L помещается 2953 байта.
func TestEncodeTooLong(t *testing.T) {
	if _, err := Encode(strings.Repeat("x", 2953)); err != nil {
		t.Fatalf("2953 байта должны кодироваться: %v", err)
	}
	if _, err := Encode(strings.Repeat("x", 2954)); err == nil {
		t.Fatal("2954 байта закодировались, ожидалась ошибка")
	}
}
