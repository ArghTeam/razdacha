---
type: spec
tracker: "#168"
---
## TLDR
Новый пакет `internal/geoip`: офлайн-определение страны по IP из встроенной базы
DB-IP Country Lite. Фундамент #167 (страновые пулы), мержится самостоятельно.

## Goal
`geoip.Country(ip net.IP) string` отдаёт ISO-код страны по адресу без сети.

## Domains touched
- [lists](../context/lists/index.md) — geoip используется при наполнении пула для определения страны сервера
- [packaging](../context/packaging/index.md) — embed базы в бинарник, атрибуция DB-IP Lite (CC-BY-4.0)

## Decisions
- 2026-08-17 (ADR 0017): страна — офлайн geo-IP, база в бинарнике через go:embed, чистый Go, без CGO.
- 2026-08-17: база DB-IP Country Lite (mmdb), ридер `oschwald/maxminddb-golang`.

## Acceptance criteria
- [ ] `Country()` отдаёт верную страну на выборке известных IP (тест с фикстурой)
- [ ] Сборка с `CGO_ENABLED=0` проходит, бинарник статический
- [ ] База лежит в репозитории и уезжает в бинарник через embed
- [ ] Атрибуция DB-IP Lite присутствует в бинарнике (лог/About) и в доках/NOTICE
