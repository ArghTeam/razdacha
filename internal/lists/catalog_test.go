package lists

import "testing"

// TestCatalog — каталог непустой, без дублей, и признак подсетей в нём совпадает
// с тем, по которому планировщик решает, что качать: разъехавшись, они дали бы
// галочку «есть подсети» у списка, который никто не скачает.
func TestCatalog(t *testing.T) {
	got := Catalog()
	if len(got) == 0 {
		t.Fatal("каталог пуст")
	}

	seen := make(map[string]bool, len(got))
	withSubnets := 0
	for _, s := range got {
		if s.Key == "" || s.Title == "" {
			t.Errorf("запись без ключа или названия: %+v", s)
		}
		if seen[s.Key] {
			t.Errorf("ключ %q встречается дважды", s.Key)
		}
		seen[s.Key] = true

		if !s.HasDomains {
			t.Errorf("у сервиса %q нет доменов, хотя .srs собирается на каждый ключ", s.Key)
		}
		_, ok := CommunitySubnetURL(s.Key)
		if s.HasSubnets != ok {
			t.Errorf("сервис %q: has_subnets = %v, а адрес подсетей = %v", s.Key, s.HasSubnets, ok)
		}
		if ok {
			withSubnets++
		}
	}

	// Обратная сверка: сервис с подсетями, которого нет в каталоге, качался бы
	// без возможности его выбрать.
	if withSubnets != len(communitySubnets) {
		t.Errorf("в каталоге %d сервисов с подсетями, всего их %d", withSubnets, len(communitySubnets))
	}
}

// regionalKeys и allowedWithRussiaInside — ограничения Podkop, повторённые
// здесь ровно затем, чтобы состав пресета от них не отъехал.
// Источник: luci-app-podkop/htdocs/luci-static/resources/view/podkop/main.js:875
// и :880 (снятие несовместимых — section.js:344).
var (
	regionalKeys = []string{"russia_inside", "russia_outside", "ukraine_inside"}

	allowedWithRussiaInside = map[string]bool{
		"russia_inside": true, "meta": true, "twitter": true, "discord": true,
		"telegram": true, "cloudflare": true, "google_ai": true, "google_play": true,
		"hetzner": true, "ovh": true, "hodca": true, "roblox": true,
		"digitalocean": true, "cloudfront": true,
	}
)

// TestPreset — состав стартового пресета. Проверяется не «тот же список, что
// написан выше», а три свойства, нарушение которых даёт пользователю
// противоречивую раскладку: ключ вне каталога панель не покажет вовсе,
// два региональных списка рядом Podkop считает несовместимыми, а сосед
// `russia_inside`, которого нет в `ALLOWED_WITH_RUSSIA_INSIDE`, уже лежит
// внутри него — то есть это дубль.
func TestPreset(t *testing.T) {
	preset := Preset()
	if len(preset) == 0 {
		t.Fatal("пресет пуст — кнопке на пустом экране нечего предлагать")
	}

	known := make(map[string]bool)
	for _, s := range Catalog() {
		known[s.Key] = true
	}

	seen := make(map[string]bool, len(preset))
	regional := 0
	for _, key := range preset {
		if !known[key] {
			t.Errorf("ключ %q есть в пресете, но не в каталоге", key)
		}
		if seen[key] {
			t.Errorf("ключ %q встречается в пресете дважды", key)
		}
		seen[key] = true
		for _, r := range regionalKeys {
			if key == r {
				regional++
			}
		}
	}
	if regional > 1 {
		t.Errorf("в пресете %d региональных списков — Podkop считает их несовместимыми", regional)
	}

	if seen["russia_inside"] {
		for _, key := range preset {
			if !allowedWithRussiaInside[key] {
				t.Errorf("ключ %q уже внутри russia_inside (нет в ALLOWED_WITH_RUSSIA_INSIDE) — в пресете это дубль", key)
			}
		}
	}

	// Каталог обязан нести признак: панель берёт состав оттуда, своего списка
	// ключей у неё нет.
	marked := 0
	for _, s := range Catalog() {
		if s.InPreset {
			marked++
			if !seen[s.Key] {
				t.Errorf("сервис %q помечен in_preset, но в пресете его нет", s.Key)
			}
		}
	}
	if marked != len(preset) {
		t.Errorf("в каталоге %d помеченных сервисов, в пресете %d", marked, len(preset))
	}
}

// TestPresetIsCopy — правка возвращённого среза не должна менять пресет:
// caller получает копию, а не внутренний список.
func TestPresetIsCopy(t *testing.T) {
	got := Preset()
	got[0] = "сломано"
	if Preset()[0] == "сломано" {
		t.Error("Preset() отдаёт внутренний срез — его правка меняет каталог")
	}
}
