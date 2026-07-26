# Модель данных

Четыре сущности. Podkop смешивает туннель и списки в одной сущности `section`; здесь они
разделены — см. [ADR 0003](decisions/0003-tunnel-separate-from-rule.md).

```
Peer ──────┐
           ├──→ Rule ──→ Tunnel
Settings   │      │
           │      └──→ Lists (community / domains / subnets)
```

## Tunnel

Исходящий канал. Один туннель = один тег, на который ссылаются правила: outbound, endpoint
либо группа `urltest` ([ADR 0010](decisions/0010-tunnel-pool-urltest.md)).

| Поле | Тип | Описание |
|---|---|---|
| `id` | uuid | |
| `name` | string | отображаемое имя, уникально |
| `type` | enum | `wireguard` \| `vless` \| `shadowsocks` \| `trojan` \| `hysteria2` \| `socks` \| `raw` |
| `source` | enum | `url` \| `wg_conf` \| `json` \| `pool` — в каком виде пользователь ввёл конфиг |
| `raw` | text | то, что вставил пользователь (URL, `.conf`, JSON или URL каталога при `pool`) |
| `parsed` | json | результат разбора, готовый к вклейке в конфиг sing-box; при `pool` пуст |
| `pool` | json | серверы пула, снятые с каталога; пуст у остальных форм |
| `pool_updated_at` | ts | когда каталог обходили в последний раз; ноль — ни разу |
| `enabled` | bool | |
| `created_at` | ts | |

**`source = pool`** — туннель, собранный из каталога бесплатных ключей: `raw` хранит URL
каталога, `pool` — снятые с него серверы (URL, страна, подпись, пинг карточки). Такой
туннель разворачивается в N vless-outbound'ов плюс `urltest` под тегом туннеля; ротацию и
health-check ведёт sing-box ([ADR 0010](decisions/0010-tunnel-pool-urltest.md)). Выключение
пула — штатный `enabled`.

Производные поля (не хранятся, отдаются API): `status` (up/down/unknown), `latency_ms`,
`last_check`.

**Как разбирается `raw`:**
- `vless://`, `ss://`, `trojan://`, `hysteria2://`, `hy2://`, `socks5://` → парсер
  proxy-URL, портируется из Podkop `sing_box_config_facade.sh`
- `[Interface] … [Peer]` (INI WireGuard) → `endpoints[].type = wireguard` sing-box,
  userspace-режим ([ADR 0002](decisions/0002-userspace-wireguard-outbound.md))
- произвольный JSON → вставляется как есть, для протоколов без парсера

**Тестовый корпус для парсера:** `podkop/String-example.md` — 118 строк примеров URI всех
поддерживаемых протоколов и транспортов. Использовать как фикстуры.

## Rule

Правило маршрутизации: «эти ресурсы — в этот туннель».

| Поле | Тип | Описание |
|---|---|---|
| `id` | uuid | |
| `name` | string | «YouTube и Google», «Соцсети» |
| `action` | enum | `tunnel` \| `direct` \| `block` |
| `tunnel_id` | uuid? | обязателен при `action = tunnel` |
| `priority` | int | порядок проверки, меньше = раньше |
| `enabled` | bool | |
| `community_lists` | []string | ключи сервисов из `allow-domains` |
| `domains` | []string | свои домены (совпадение по суффиксу) |
| `subnets` | []cidr | свои подсети |
| `remote_lists` | []url | внешние списки (`.srs`, `.json`, plain) |
| `peer_scope` | enum | `all` \| `selected` |
| `peer_ids` | []uuid | при `peer_scope = selected` |
| `resolve_real_ip` | bool | резолвить настоящий IP вместо FakeIP (редкие случаи) |

**`action = direct`** нужен для исключений: правило с высоким приоритетом, которое
выводит подсеть из-под более общего правила ниже. **`action = block`** — просто отбросить
(реклама, трекеры).

**Приоритет обязателен и виден в UI.** Правила маршрутизации sing-box проверяются по
порядку, первое совпадение выигрывает. Podkop порядок не выставляет, и при пересекающихся
списках результат неочевиден. В UI — перетаскивание, в API — целое число с
переупорядочиванием при сохранении.

## Peer

Клиентское устройство.

| Поле | Тип | Описание |
|---|---|---|
| `id` | uuid | |
| `name` | string | «iPhone Ромы» |
| `public_key` | string | |
| `private_key` | string | хранится, чтобы можно было перевыдать конфиг |
| `preshared_key` | string | генерируется всегда |
| `address` | ip | из пула, `/32` |
| `enabled` | bool | выключенный пир удаляется из wg0, но остаётся в БД |
| `created_at` | ts | |

Производные: `last_handshake`, `rx_bytes`, `tx_bytes`, `endpoint` (текущий внешний адрес),
`online` (хендшейк < 3 мин назад).

Привязка к правилам — со стороны `Rule.peer_ids`, не отсюда. Причина: типичный сценарий —
«это правило только для рабочего ноутбука», а не «у этого пира свой набор правил».

## Settings

Одна запись.

| Поле | Дефолт | Описание |
|---|---|---|
| `wg_listen_port` | `51820` | |
| `wg_pool` | `10.8.0.0/24` | |
| `wg_server_address` | `10.8.0.1` | |
| `endpoint_host` | автодетект | что попадёт в `Endpoint` клиентских конфигов |
| `client_mtu` | `1280` | [ADR 0004](decisions/0004-client-mtu-1280.md) |
| `dns_upstream` | `1.1.1.1` | апстрим для sing-box |
| `dns_type` | `udp` | `udp` \| `dot` \| `doh` |
| `wan_interface` | автодетект | для masquerade |
| `list_update_interval` | `1d` | |
| `log_level` | `warn` | |

Всё, что можно вывести автоматически — выводится. В UI показываются только
`wg_listen_port`, `endpoint_host`, `client_mtu`, `dns_upstream`,
`list_update_interval`; остальное — в `config.yaml` для тех, кому надо.

## Отображение в конфиг sing-box

| Сущность | Куда попадает |
|---|---|
| `Tunnel` (wireguard) | `endpoints[]`, tag = `tun-<id>` |
| `Tunnel` (прочее) | `outbounds[]`, tag = `tun-<id>` |
| `Rule` (tunnel) | `route.rules[]` → `outbound: tun-<id>` + `dns.rules[]` для FakeIP |
| `Rule` (direct) | `route.rules[]` → `outbound: direct-out` |
| `Rule` (block) | `route.rules[]` → `action: reject` |
| `Rule.community_lists` | `route.rule_set[]` типа `remote`, URL `.srs` из allow-domains |
| `Rule.domains` | `rule_set` типа `inline`, `domain_suffix` |
| `Rule.subnets` | тот же `inline`-набор + дублирование в nft-сет `razdacha_subnets` |
| `Rule.remote_lists` | `route.rule_set[]` типа `remote` — только `.srs` и `.json` |
| `Rule.peer_ids` | `source_ip_cidr` в правиле |
| `Rule.priority` | порядок элементов в `route.rules[]` |
| `Settings.dns_*` | `dns.servers[]` |

**Наборы `inline`, а не `local`.** Тип `local` держит условия в отдельных файлах рядом с
конфигом; тогда состояние на диске перестаёт быть одним артефактом, и файлы приходится
удерживать в согласии с конфигом при каждой правке. `inline` кладёт условия внутрь
конфига — он и остаётся единственным, что генератор пишет на диск.

Домены и подсети правила — **две записи** одного набора: внутри записи условия
складываются по «и», а нужно «или».

Правило не попадает в конфиг, если его туннель выключен, если у него не осталось ни
одного условия совпадения (такое правило поймало бы весь трафик) или если выключены все
выбранные им пиры (пустой `source_ip_cidr` означает «для всех» — противоположность
заданному). Ссылка на **несуществующий** туннель — ошибка генерации, а не пропуск:
состояние повреждено, и трафик ушёл бы мимо туннеля молча.

Конфиг **всегда генерируется целиком** из состояния БД, никогда не патчится частично.
Перед применением — `sing-box check`; при ошибке старый конфиг остаётся в силе, ошибка
уходит в UI. Podkop делает так же и сравнивает md5, чтобы не перезапускать зря — стоит
повторить.

## Схема SQLite (набросок)

```sql
CREATE TABLE tunnels (
  id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, type TEXT NOT NULL,
  source TEXT NOT NULL, raw TEXT NOT NULL, parsed TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL,
  pool TEXT NOT NULL DEFAULT '[]', pool_updated_at INTEGER NOT NULL DEFAULT 0);

CREATE TABLE rules (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, action TEXT NOT NULL,
  tunnel_id TEXT REFERENCES tunnels(id) ON DELETE RESTRICT,
  priority INTEGER NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  community_lists TEXT NOT NULL DEFAULT '[]', domains TEXT NOT NULL DEFAULT '[]',
  subnets TEXT NOT NULL DEFAULT '[]', remote_lists TEXT NOT NULL DEFAULT '[]',
  peer_scope TEXT NOT NULL DEFAULT 'all', peer_ids TEXT NOT NULL DEFAULT '[]',
  resolve_real_ip INTEGER NOT NULL DEFAULT 0);
CREATE UNIQUE INDEX rules_priority ON rules(priority);

CREATE TABLE peers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, public_key TEXT NOT NULL UNIQUE,
  private_key TEXT NOT NULL, preshared_key TEXT NOT NULL,
  address TEXT NOT NULL UNIQUE, enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY, created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL);
CREATE INDEX sessions_expires_at ON sessions(expires_at);
```

`ON DELETE RESTRICT` для `tunnel_id` намеренно: удаление туннеля, на который ссылается
правило, должно быть отклонено с внятным сообщением, а не тихо сломать маршрутизацию.
Внешние ключи в SQLite выключены по умолчанию — `PRAGMA foreign_keys = ON` ставится на
каждом соединении, иначе `RESTRICT` не действует.

**Приоритет назначает слой хранения, а не вызывающий.** Правило добавляется в конец
списка, удаление сжимает приоритеты оставшихся, порядок меняется отдельной операцией
переупорядочивания, которой передаётся полный список идентификаторов. Иначе уникальный
индекс `rules_priority` ловил бы пользовательский ввод, а перестановка «в лоб»
сталкивалась бы на промежуточном значении: SQLite проверяет уникальность построчно.

**Ссылки на пиров (`Rule.peer_ids`) — JSON-список, внешнего ключа на них нет.** Поэтому
удаление пира чистит списки правил само; правило, оставшееся без пиров, переводится на
`peer_scope = all`, а не остаётся молча неработающим.

**`settings` — key/value.** Добавление настройки не требует миграции, отсутствующий ключ
читается как дефолт из таблицы выше, неизвестный ключ игнорируется (откат на прежнюю
версию демона не должен ронять чтение). `list_update_interval` хранится в секундах:
`1d` из таблицы — форма записи для человека, не формат хранения.

**Пароль панели — ключ `auth_password_hash` в той же таблице**, значение — хеш argon2id
вместе с параметрами (формат PHC). В настройки, которые отдаёт и принимает API, ключ не
входит: сохранение настроек из UI его не затирает, а `GET /api/settings` не выносит
наружу даже хеш.

**`sessions` — выданные сессии панели** ([ADR 0009](decisions/0009-public-panel-access.md)):
хеш токена, а не сам токен, поэтому доступ к файлу БД не даёт готовой cookie. Смена
пароля удаляет все строки, истёкшие убираются по расписанию.
