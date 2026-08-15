package singbox

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/ArghTeam/razdacha/internal/store"
)

// isCatalogURL — похож ли ввод на ссылку http(s) на каталог ключей.
//
// Проверяется только схема: остальное разбирает parseCatalogURL, чтобы битая
// ссылка получила внятную ошибку, а не «не похоже ни на что».
func isCatalogURL(s string) bool {
	switch strings.ToLower(schemeOf(s)) {
	case "http", "https":
		return true
	default:
		return false
	}
}

// parseCatalogURL разбирает ссылку на каталог ключей в туннель-пул.
//
// Ни Outbound, ни Endpoint здесь не заполняются: конкретных серверов на момент
// разбора нет, каталог обходит расписание, а группу `urltest` собирает генератор из
// состава в БД (ADR 0010).
//
// Type остаётся пустым намеренно. `http(s)://` говорит только о том, что это каталог;
// какие в нём ключи — знает драйвер каталога, а не разбор ссылки (ADR 0015). Прежний
// прошитый здесь vless пережил свой источник и вводил в заблуждение: у outlinekeys
// раздел outline отдаёт shadowsocks. Проставляет тип тот, кто умеет спросить драйвер, —
// слой api (`lists.PoolKeyType`).
//
// Сеть здесь не трогается: разбор обязан быть быстрым и работать без интернета, а
// содержимое каталога проверяется при первом обходе — [lists.PoolCatalog.Servers]
// отвергает страницу, не давшую ни одного ключа.
func parseCatalogURL(s string) (ParseResult, error) {
	u, err := url.Parse(s)
	if err != nil {
		return ParseResult{}, fmt.Errorf("%w: ссылка на каталог не разобрана: %w", ErrParse, err)
	}
	if u.Host == "" {
		return ParseResult{}, fmt.Errorf("%w: в ссылке на каталог не указан адрес", ErrParse)
	}
	if u.User != nil {
		// Ссылка с логином и паролем — почти наверняка не каталог, а перепутанный
		// proxy-URL: у наших каталогов авторизации нет.
		return ParseResult{}, fmt.Errorf(
			"%w: ссылка на каталог не может содержать логин и пароль", ErrParse)
	}

	return ParseResult{
		Source: store.SourcePool,
		Name:   u.Host,
	}, nil
}
