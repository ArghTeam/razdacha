package api

import (
	"regexp"
	"strings"
	"testing"
)

// reTunnelName — регулярка клиента WireGuard для Android: имя туннеля берётся
// из имени файла без расширения. Всё, что мимо неё, отбивается на импорте.
var reTunnelName = regexp.MustCompile(`^[a-zA-Z0-9_=+.-]{1,15}$`)

func TestConfFileName(t *testing.T) {
	cases := []struct {
		name string
		peer string
		want string
	}{
		{"латиница как есть", "phone", "phone.conf"},
		{"регистр вниз", "MacBook", "macbook.conf"},
		{"пробелы в дефис", "pizel 4", "pizel-4.conf"},
		{"кириллица транслитом", "Телефон", "telefon.conf"},
		{"мягкий знак не даёт дефиса", "Русь", "rus.conf"},
		{"щ и ю не теряются", "Щука Юля", "schuka-yulya.conf"},
		{"разрешённые символы остаются", "a_b=c+d.e-f", "a_b=c+d.e-f.conf"},
		{"подряд идущий мусор схлопывается", "a !? b", "a-b.conf"},
		{"длинное режется", "телефон рабочий", "telefon-rabochi.conf"},
		{"обрезка не оставляет дефис на конце", "abcdefghijklmn qq", "abcdefghijklmn.conf"},
		{"пустое имя", "", "peer.conf"},
		{"имя из одного мусора", "!!!", "peer.conf"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := confFileName(c.peer)
			if got != c.want {
				t.Fatalf("confFileName(%q) = %q, ожидалось %q", c.peer, got, c.want)
			}
			tunnel := strings.TrimSuffix(got, ".conf")
			if !reTunnelName.MatchString(tunnel) {
				t.Fatalf("имя туннеля %q не проходит валидацию клиента", tunnel)
			}
		})
	}
}

// Имя пира вводит пользователь, поэтому проверка идёт не только по подобранным
// случаям: что бы ни пришло, имя туннеля обязано укладываться в регулярку
// клиента — иначе конфиг не импортируется, а разбираться будет пользователь с
// телефоном в руках.
func TestConfFileNameAlwaysImportable(t *testing.T) {
	peers := []string{
		"", " ", "—", "Телефон Ромы", "Pixel 4 XL (рабочий)", "ЪЬ",
		strings.Repeat("я", 40), strings.Repeat("-", 20), "....",
		"192.168.0.1", "\n\t", "Ёлка", "AAAAAAAAAAAAAAAAAAAAAAAA",
	}
	for _, p := range peers {
		tunnel := strings.TrimSuffix(confFileName(p), ".conf")
		if !reTunnelName.MatchString(tunnel) {
			t.Errorf("пир %q дал имя туннеля %q — клиент такое не примет", p, tunnel)
		}
	}
}
