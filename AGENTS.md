# razdacha — конституция

## Pitch

Селективный VPN-шлюз для Debian/Ubuntu: сервер поднимает WireGuard для клиентов, весь их
трафик приходит на `wg0`, выбранные списки уходят в настроенные туннели, остальное —
напрямую. Клиенты используют штатный WireGuard без дополнительного ПО.

**Статус: кода нет, только документация.** Проект в стадии проектирования.

## Stack

- Go ≥ 1.23 (зависимость `sing-box/option`), в системе 1.26.5; `CGO_ENABLED=0`
- `modernc.org/sqlite`, `google/nftables`, `vishvananda/netlink`, `wgctrl`, `sing-box/option`
- SvelteKit + Tailwind в `ui/`, сборка в `ui/build`, встраивание через `go:embed`
- Модуль `github.com/ArghTeam/razdacha`, remote `git@github.com:ArghTeam/razdacha.git`

Планируемая раскладка:

```
cmd/razdachad/          точка входа демона
cmd/razdacha/           CLI (update, uninstall, --reset-network)
internal/store/         SQLite, миграции
internal/wg/            wg0 через wgctrl
internal/nft/           правила через google/nftables
internal/route/         ip rule + таблица 105
internal/singbox/       генерация option.Options, управление процессом
internal/lists/         загрузка .srs и plain-списков
internal/clash/         клиент Clash API sing-box
internal/diag/          проверки
internal/api/           REST + WS + go:embed для SPA
ui/                     SvelteKit, собирается в ui/build
packaging/              nfpm, systemd-юнит, install.sh
```

**Первое, что стоит писать — `internal/singbox`.** Генератор конфига есть чистая функция
«состояние → `option.Options`»: тестируется без сети и root, и это то место, где Podkop
слабее всего (1508 строк конструирования JSON через `jq`).

Соглашения: ошибки оборачиваются `fmt.Errorf("...: %w", err)`; текст ошибок, доходящих
до UI, — на русском, он показывается пользователю как есть; логи — `log/slog`.

## Layers

Пакеты `internal/*` группируются в семь слоёв памяти (`context/<слой>/`):

- **store** — SQLite: схема четырёх сущностей, миграции, хранение ключей пиров
- **singbox** — генерация `option.Options` из состояния БД, парсеры proxy-URL, check и reload
- **netstack** — `wg0`, nft-правила и маршрутизация через netlink; обратимость изменений системы
  (`internal/wg`, `internal/nft`, `internal/route` — меняются всегда вместе)
- **lists** — загрузка и кэш `.srs`/plain-списков, расписание, дублирование подсетей в nft-сет
- **api** — REST + WS, встроенная SPA, диагностика и клиент Clash API (`internal/api`,
  `internal/diag`, `internal/clash`)
- **ui** — SvelteKit-интерфейс: четыре экрана, дефолты вместо опций
- **packaging** — systemd-юнит, nfpm, install/uninstall, матрица поддерживаемых ОС

## Порядок работы

1. **Перед любым архитектурным предложением читай `docs/decisions/`.** Семь решений уже
   приняты с обоснованием. Если предложение противоречит принятому решению — это не
   ошибка сама по себе, но нужно явно сказать, какое решение оно заменяет, и почему.
2. Решения не переписываются. Меняется — заводится новое ADR со статусом
   «заменяет NNNN», старое получает «заменено». Индекс — `docs/decisions/README.md`.
3. Документация ведёт код, а не наоборот. Меняется поведение — сначала документ.

`docs/` остаётся источником решений; инварианты слоя ссылаются на ADR, а не пересказывают их.

## podkop — источник, только для чтения

`~/Web/podkop` — репозиторий Podkop (OpenWrt), откуда взята архитектура. Разбор с
готовыми ссылками `file:line` — `docs/research/podkop-analysis.md`.

- **Никогда не редактируй и не коммить в `~/Web/podkop`.** Это чужой проект, у нас он
  только читается.
- Ссылки `file:line` на podkop **обязательно проверять грепом** перед тем, как записать
  их в документ. Строки уже разъезжались один раз.
- Заимствование кода разрешено: оба проекта под GPL-2.0
  (`docs/decisions/0007-license-gplv2.md`).

## Инварианты, которые код обязан соблюдать

Взяты из ADR, нарушение — баг, а не стилистика:

- **MTU клиентов 1280**, тот же MTU на `wg0` сервера. Не 1420, не «по умолчанию».
- **`strategy: ipv4_only`** в DNS sing-box, FakeIP только `inet4_range`. IPv6 для
  клиентов отключён тремя слоями — DNS, `AllowedIPs ::/0` без v6-адреса, nft `reject`.
- **Исходящие WireGuard-туннели — userspace**, через `endpoints` sing-box. Никаких
  kernel-интерфейсов `wg1`/`wg2` и `bind_interface`.
- **Конфиг sing-box генерируется целиком** из состояния БД, никогда не патчится частично.
  Перед применением — `sing-box check`; при ошибке остаётся прежний конфиг.
- **Netlink вместо вызова бинарников.** Не вызываем `nft`, `ip`, `wg`, `wg-quick` —
  работаем через `google/nftables`, `netlink`, `wgctrl`. Это условие широкой матрицы ОС.
- **`CGO_ENABLED=0`.** Статический бинарник, `modernc.org/sqlite`, не `mattn`.
- Клиентские конфиги всегда содержат `DNS = 10.8.0.1`, `MTU`, `AllowedIPs = 0.0.0.0/0, ::/0`,
  `PersistentKeepalive = 25`. Ничего из этого не настраивается per-peer.

## Команды

```
make build      сборка демона
make test       go test ./...
make lint       golangci-lint run
make ui         сборка SPA
```

`golangci-lint`, `gofumpt` и `nfpm` в системе не установлены. `node` через nvm не
поднимается в неинтерактивном shell — для сборки UI понадобится починить окружение.

## Brand

не задан — визуальной идентичности пока нет

## Скиллы проекта

- `/adr` — завести новое решение в `docs/decisions/` по принятому формату.
- `/podkop-source` — найти, как что-то устроено в Podkop, с проверенными ссылками
  `file:line`.
