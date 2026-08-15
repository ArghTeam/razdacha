package singbox

import (
	"strings"
	"testing"

	"github.com/ArghTeam/razdacha/internal/store"
)

// Каталожная ссылка опознаётся как пул, но протокол разбор не назначает: его знает
// драйвер каталога, а не схема ссылки (ADR 0015). Прошитый здесь vless пережил свой
// источник — у outlinekeys раздел outline отдаёт shadowsocks.
func TestParseCatalogLeavesTypeToDriver(t *testing.T) {
	for _, raw := range []string{
		"https://outlinekeys.com/protocols/outline/",
		"http://vpnkeys.me/protocol/vless",
	} {
		res, err := Parse(raw)
		if err != nil {
			t.Fatalf("%s: Parse: %v", raw, err)
		}
		if res.Source != store.SourcePool {
			t.Errorf("%s: source = %q, ожидался пул", raw, res.Source)
		}
		if res.Type != "" {
			t.Errorf("%s: разбор назначил тип %q, а его сообщает драйвер каталога",
				raw, res.Type)
		}
		if res.Outbound != nil || res.Endpoint != nil {
			t.Errorf("%s: у пула появился конфиг, хотя серверов ещё нет", raw)
		}
	}
}

// Ссылка с логином и паролем — не каталог, а перепутанный proxy-URL.
func TestParseCatalogRejectsCredentials(t *testing.T) {
	_, err := Parse("https://user:pass@outlinekeys.com/protocols/outline/")
	if err == nil {
		t.Fatal("каталог с логином и паролем принят")
	}
	if !strings.Contains(err.Error(), "логин") {
		t.Errorf("ошибка не объясняет, что не так: %v", err)
	}
}
