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
