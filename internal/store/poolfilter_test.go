package store

import (
	"reflect"
	"testing"
)

// blocklist — фильтр с дефолтным чёрным списком (RU, BY).
func blocklist() PoolFilter { return PoolFilter{Countries: DefaultPoolCountryBlocklist()} }

// Хорошая ссылка: шифрование включено, страна нейтральная.
const okKey = "vless://uuid@nl.example.com:443?security=reality&pbk=abc"

// Страна берётся из подписи карточки: флаг, название словами или голый код.
// Отбраковка идёт по любому из трёх — источник пишет подпись как ему вздумается.
func TestPoolFilterCountryFromTitle(t *testing.T) {
	cases := []struct {
		name    string
		title   string
		blocked bool
	}{
		{"флаг РФ", "🇷🇺 Россия, Москва", true},
		{"только флаг РФ", "🇷🇺", true},
		{"флаг Беларуси", "🇧🇾 Minsk", true},
		{"название по-русски", "Россия, Санкт-Петербург", true},
		{"название по-английски", "Russia, Moscow", true},
		{"сокращение РФ", "Хостинг РФ, Москва", true},
		{"голый код в верхнем регистре", "AEZA RU 01", true},
		{"нейтральная страна", "🇳🇱 Нидерланды, Амстердам", false},
		{"английское by словом", "Frankfurt by AEZA", false},
		{"страна внутри другого слова", "Prussia, Berlin", false},
		{"подписи нет вовсе", "", false},
	}
	f := blocklist()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := f.Exclusion(PoolServer{URL: okKey, Title: c.title})
			if (got != "") != c.blocked {
				t.Fatalf("подпись %q дала %q, ожидалось blocked=%v", c.title, got, c.blocked)
			}
		})
	}
}

// Карточка с несколькими флагами отсеивается по любому совпадению: у anycast часть
// выходов в РФ, и какая именно — из подписи не видно (ADR 0020).
func TestPoolFilterMultiFlagAnycast(t *testing.T) {
	f := blocklist()

	for _, title := range []string{"🇷🇺 🇺🇸 anycast", "🇺🇸 🇷🇺 anycast", "🇩🇪🇷🇺🇺🇸"} {
		if f.Allows(PoolServer{URL: okKey, Title: title}) {
			t.Errorf("карточка %q прошла фильтр, хотя один из флагов из чёрного списка", title)
		}
	}
	// Соседние флаги не должны склеиваться в мнимую пару: 🇺🇸🇩🇪 это US и DE, а не «SD».
	if !f.Allows(PoolServer{URL: okKey, Title: "🇺🇸🇩🇪 anycast"}) {
		t.Error("нейтральная мультифлаговая карточка отбракована")
	}
}

// Ключ без шифрования транспорта в пул не идёт ни при какой стране: открытый текст
// через чужой сервер хуже прямого выхода (ADR 0020).
func TestPoolFilterUnencryptedKey(t *testing.T) {
	cases := []struct {
		name    string
		url     string
		blocked bool
	}{
		{"vless security=none", "vless://uuid@1.2.3.4:443?type=tcp&security=none", true},
		{"vless без security", "vless://uuid@1.2.3.4:443?type=ws&host=a.example", true},
		{"trojan security=none", "trojan://pass@1.2.3.4:443?security=none", true},
		{"trojan без security", "trojan://pass@1.2.3.4:443", true},
		{"vless security=tls", "vless://uuid@1.2.3.4:443?security=tls&sni=a.example", false},
		{"vless security=reality", okKey, false},
		{"shadowsocks", "ss://YWVzLTI1Ni1nY206cGFzcw==@1.2.3.4:8388", false},
		{"hysteria2", "hysteria2://pass@1.2.3.4:443", false},
	}
	// Чёрный список пуст: отбраковать может только отсутствие шифрования.
	f := PoolFilter{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := f.Exclusion(PoolServer{URL: c.url, Title: "🇳🇱 Нидерланды"})
			if (got != "") != c.blocked {
				t.Fatalf("ключ %q дал %q, ожидалось blocked=%v", c.url, got, c.blocked)
			}
		})
	}
}

// Пустой чёрный список — законный выбор: по стране не отбраковываем, шифрование
// требуем всё равно.
func TestPoolFilterEmptyBlocklistKeepsCountries(t *testing.T) {
	f := PoolFilter{}
	if !f.Allows(PoolServer{URL: okKey, Title: "🇷🇺 Россия, Москва"}) {
		t.Error("с пустым чёрным списком российская нода всё равно отбракована")
	}
	if f.Allows(PoolServer{URL: "vless://uuid@1.2.3.4:443", Title: "🇳🇱 Нидерланды"}) {
		t.Error("ключ без шифрования прошёл при пустом чёрном списке")
	}
}

// Причина отбраковки называет страну: она уходит в панель как есть.
func TestPoolFilterReasonNamesCountry(t *testing.T) {
	got := blocklist().Exclusion(PoolServer{URL: okKey, Title: "🇧🇾 Минск"})
	if got != poolReasonCountry+"BY" {
		t.Fatalf("причина %q, ожидалась про страну BY", got)
	}
}

// SplitPool делит состав на пул и отбракованных, сохраняя порядок обоих.
func TestSplitPool(t *testing.T) {
	pool := []PoolServer{
		{URL: okKey, Title: "🇳🇱 NL-1"},
		{URL: okKey + "&x=2", Title: "🇷🇺 Россия, Москва"},
		{URL: "vless://uuid@1.2.3.4:443", Title: "🇩🇪 DE-1"},
		{URL: okKey + "&x=4", Title: "🇩🇪 DE-2"},
	}
	keep, excluded := SplitPool(pool, blocklist())

	if want := []string{"🇳🇱 NL-1", "🇩🇪 DE-2"}; !reflect.DeepEqual(titles(keep), want) {
		t.Errorf("в пуле %v, ожидалось %v", titles(keep), want)
	}
	if len(excluded) != 2 {
		t.Fatalf("отбраковано %d, ожидалось 2: %+v", len(excluded), excluded)
	}
	if excluded[0].Reason != poolReasonCountry+"RU" {
		t.Errorf("причина первого %q, ожидалась про страну", excluded[0].Reason)
	}
	if excluded[1].Reason != poolReasonNoEncryption {
		t.Errorf("причина второго %q, ожидалась про шифрование", excluded[1].Reason)
	}
}

func titles(v []PoolServer) []string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		out = append(out, s.Title)
	}
	return out
}
