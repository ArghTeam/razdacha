# 189 — имя члена пула из фрагмента ссылки

## Цель
Убрать прочерк вместо названия у серверов единого пула. Заполнять `store.PoolServer.Title`
из человекочитаемого фрагмента ссылки-ключа.

## Контекст
- Единый пул igareck (ADR 0018) собирает ключи в `internal/lists/pool_igareck.go`.
  Сейчас: `out = append(out, store.PoolServer{URL: line})` — `Title` пуст.
- Фрагмент ссылки несёт имя: `…#%F0%9F%87%A7%F0%9F%87%AC%20Bulgaria%2C%20Sofia%20%7C%20%5BBL%5D`
  → `🇧🇬 Bulgaria, Sofia | [BL]`. Флаг + страна + город.
- UI: `ui/dist/js/screens/tunnels.js:113` показывает `s.title || '—'`; «Сейчас» —
  `internal/api/tunnels_pool.go:117` `poolCurrentServer{Name: srv.Title}`.
- `Title` персистится в БД (`store.PoolServer`), переживает мерж составов
  (`internal/lists/pool.go`).

## Что сделать
1. В `pool_igareck.go` завести помощник `igareckTitle(line string) string`:
   - взять часть после первого `#`;
   - percent-decode (`url.PathUnescape`, при ошибке — сырой фрагмент);
   - пустой фрагмент → fallback: хост:порт из `url.Parse` (best-effort), иначе `""`.
2. При сборке члена: `store.PoolServer{URL: line, Title: igareckTitle(line)}`.
3. Ссылку в лог не тащить (пароль ключа, #124) — тут её и так нет.
4. Тест в `internal/lists/`: имя извлекается из фрагмента с эмодзи и `%20`,
   fallback на хост при пустом фрагменте.

## Инварианты
- Title — только отображение: конфиг sing-box не меняется, тег члена по-прежнему
  синтетический `poolMemberTag` (ADR 0010/0015). Не трогать генерацию outbound'ов.
- Netlink/CGO/парсер не затрагиваются.

## Критерии приёмки
- `make test` зелёный, `make build` собирается.
- В деталях пула и в «Сейчас» — страна/город, не прочерк.
- Проверка на стенде 186.246.29.11 отдельным шагом оркестратором.

## Слои
lists (заполнение Title), ui — только следствие, правок не требует.
