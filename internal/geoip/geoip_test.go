package geoip

import (
	"net"
	"testing"
)

// TestCountryKnownIPs проверяет определение страны на стабильных адресах из
// базы DB-IP Country Lite. Пары зафиксированы по фактическому выводу базы —
// если база в репозитории обновится и вывод разъедется, тест это поймает.
func TestCountryKnownIPs(t *testing.T) {
	cases := map[string]string{
		"1.1.1.1":     "AU", // Cloudflare, Австралия
		"8.8.8.8":     "US", // Google, США
		"77.88.55.88": "RU", // Яндекс, Россия
		"217.10.60.1": "DE", // Германия
	}
	for ip, want := range cases {
		got := Country(net.ParseIP(ip))
		if got != want {
			t.Errorf("Country(%s) = %q, ожидалось %q", ip, got, want)
		}
	}
}

// TestCountryUnknown — адреса, для которых страны быть не должно: приватная
// сеть, nil и некорректный ввод. Ожидается пустая строка, а не паника.
func TestCountryUnknown(t *testing.T) {
	cases := []net.IP{
		net.ParseIP("10.0.0.1"),    // приватная сеть
		net.ParseIP("192.168.1.1"), // приватная сеть
		nil,                        // nil-адрес
	}
	for _, ip := range cases {
		if got := Country(ip); got != "" {
			t.Errorf("Country(%v) = %q, ожидалась пустая строка", ip, got)
		}
	}
}

// TestEmbeddedDatabase убеждается, что база вшита в бинарник и открывается.
func TestEmbeddedDatabase(t *testing.T) {
	if len(database) == 0 {
		t.Fatal("встроенная база пуста — go:embed не сработал")
	}
	load()
	if reader == nil {
		t.Fatal("ридер не инициализировался из встроенной базы")
	}
	if reader.Metadata.DatabaseType != "DBIP-Country-Lite" {
		t.Errorf("тип базы = %q, ожидался DBIP-Country-Lite", reader.Metadata.DatabaseType)
	}
}
