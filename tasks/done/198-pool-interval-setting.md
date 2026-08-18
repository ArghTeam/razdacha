# 198 — интервал обновления пула настройкой в панели

## Цель
Вывести интервал обхода каталога пула в настройки (как у списков `ListUpdateInterval`),
дефолт 1 ч, нижний кламп 30 мин. Сейчас это захардкоженная константа `DefaultPoolInterval`.

## Образец для копирования
`ListUpdateInterval` уже сделан по всей вертикали — иди по нему:
- `internal/store/settings.go` — поле, ключ, дефолт, парс/сериализация, валидация.
- `internal/api/settings.go` — отдача поля наружу.
- `cmd/razdachad/lists.go:50,78` — `m.SetInterval(settings.ListUpdateInterval)` на старте и при смене.
- `ui/dist/js/screens/settings.js` — контрол интервала списков.

## Что сделать
1. `internal/store/settings.go`: `PoolUpdateInterval time.Duration` + ключ `pool_update_interval`
   + дефолт **1 ч** (= текущему `lists.DefaultPoolInterval`). Валидация: **floor 30 мин**
   (значение ниже — либо ошибка, либо кламп к 30 мин; выбери как у списков поступают с 0/малым).
   Верхнюю границу — как у `ListUpdateInterval`.
2. `internal/api/settings.go`: добавить поле в ответ/приём настроек по образцу `ListUpdateInterval`.
3. `cmd/razdachad/pools.go` (`startPools`, `syncPoolTunnels` или где уместно, по образцу
   `cmd/razdachad/lists.go`): читать настройки, звать `m.SetInterval(settings.PoolUpdateInterval)`
   на старте и переприменять при изменении настроек — без перезапуска демона. `PoolManager.SetInterval`
   уже существует (`internal/lists/pool.go`).
4. `ui/dist/js/screens/settings.js`: контрол «интервал обновления пула» рядом с интервалом списков,
   тот же формат ввода. Метод в `ui/dist/js/api.js`, если требуется.
5. Docs: `docs/05-api.md` (поле настроек), `docs/06-ui-screens.md` (контрол).

## Тонкости
- **`poolMissesBeforeDrop` НЕ трогать.** Floor 30 мин держит выселение ≥ 1.5 ч — churn не растёт.
- Дефолт остаётся 1 ч: свежая установка и апгрейд ведут себя как сейчас.
- `DefaultPoolInterval` можно оставить как значение дефолта настройки (не удалять смысл, а
  переиспользовать), чтобы код и настройка не разъезжались.

## Инварианты
- Клиентские инварианты (MTU/DNS/FakeIP) не трогать.
- `CGO_ENABLED=0`, netlink/генератор не затрагиваются.
- Миграция БД настроек — как у `ListUpdateInterval` (key/value, без отдельной миграции, если так у него).

## Критерии приёмки
- В панели есть контрол интервала пула, дефолт 1 ч, < 30 мин отбивается/клампится.
- Смена значения меняет реальный интервал без перезапуска демона.
- `go build ./...`, `go test ./...` зелёные; gofmt чисто; CI зелёный.
- Стенд (оркестратор): 30 мин → `next_update_at` через 30 мин; < 30 → отказ/кламп.

## Слои
store (поле настройки), api (отдача), ui (контрол), + проводка в cmd/razdachad.
