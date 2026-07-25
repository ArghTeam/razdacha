# API

REST поверх HTTP + WebSocket для живых данных. Слушает `10.8.0.1:8080` — доступ только
из VPN, поэтому без аутентификации и TLS ([архитектура](01-architecture.md#модель-привилегий)).

Все ответы — JSON. Ошибки: `{"error": "человекочитаемое сообщение", "code": "slug"}`
с соответствующим HTTP-статусом. Сообщения на русском, они показываются пользователю
как есть.

## Peers

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/peers` | список с живой статистикой |
| `POST` | `/api/peers` | создать; тело `{name}`, ключи и адрес генерируются |
| `PATCH` | `/api/peers/{id}` | `{name?, enabled?}` |
| `DELETE` | `/api/peers/{id}` | удалить пира и его ссылки в правилах |
| `GET` | `/api/peers/{id}/config` | клиентский `.conf`, `text/plain` |
| `GET` | `/api/peers/{id}/qr` | тот же конфиг QR-кодом, `image/png` |

```jsonc
// GET /api/peers
[{
  "id": "...", "name": "iPhone Ромы", "address": "10.8.0.5",
  "enabled": true, "online": true,
  "last_handshake": "2026-07-25T12:03:11Z",
  "rx_bytes": 184320000, "tx_bytes": 12800000,
  "endpoint": "203.0.113.44:41233"
}]
```

## Tunnels

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/tunnels` | список со статусом и latency |
| `POST` | `/api/tunnels` | `{name, raw}` — тип определяется автоматически |
| `PATCH` | `/api/tunnels/{id}` | `{name?, raw?, enabled?}` |
| `DELETE` | `/api/tunnels/{id}` | `409`, если на туннель ссылается правило |
| `POST` | `/api/tunnels/parse` | разбор без сохранения — для превью в форме |
| `POST` | `/api/tunnels/{id}/check` | измерить latency сейчас |

`POST /api/tunnels/parse` возвращает `{type, host, port, security, transport, warnings[]}`
либо `400` с указанием, что именно в строке не разобралось. Форма показывает результат
до сохранения, чтобы пользователь видел, что вставил не то.

## Rules

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/rules` | по возрастанию `priority` |
| `POST` | `/api/rules` | создать; `priority` — в конец |
| `PATCH` | `/api/rules/{id}` | частичное обновление |
| `DELETE` | `/api/rules/{id}` | |
| `PUT` | `/api/rules/order` | `{ids: [...]}` — новый порядок целиком |
| `GET` | `/api/lists/community` | доступные сервисы: `{key, title, has_domains, has_subnets}` |

Переупорядочивание отдельным эндпоинтом, а не через `PATCH` каждого правила: порядок
меняется целиком и атомарно, промежуточных состояний с дублирующимися приоритетами быть
не должно.

## Settings

| Метод | Путь |
|---|---|
| `GET` | `/api/settings` |
| `PATCH` | `/api/settings` |

Изменение `wg_listen_port`, `wg_pool` или `client_mtu` требует переподключения клиентов —
ответ содержит `{"requires_client_reconfig": true}`, UI показывает предупреждение.

## Diagnostics

| Метод | Путь | Описание |
|---|---|---|
| `GET` | `/api/diag` | все проверки |
| `POST` | `/api/diag/run` | перезапустить проверки |
| `GET` | `/api/diag/singbox-config` | сгенерированный конфиг, для багрепортов |
| `GET` | `/api/logs?source=razdachad\|sing-box&lines=200` | последние строки |

```jsonc
// GET /api/diag
{
  "checks": [
    {"id": "wg",       "title": "WireGuard",        "status": "ok",
     "detail": "wg0 поднят, 3 пира, 2 онлайн"},
    {"id": "singbox",  "title": "sing-box",          "status": "ok",
     "detail": "1.12.4, запущен 4 ч назад"},
    {"id": "nft",      "title": "Правила nftables",  "status": "ok",
     "detail": "таблица razdacha, 1284 подсети в сете"},
    {"id": "tunnels",  "title": "Туннели",           "status": "warn",
     "detail": "«Нидерланды» не отвечает"},
    {"id": "lists",    "title": "Списки",            "status": "ok",
     "detail": "обновлены 3 ч назад"},
    {"id": "forward",  "title": "IP forwarding",     "status": "ok"},
    {"id": "mtu",      "title": "Path MTU",          "status": "ok",
     "detail": "1500 до всех туннелей"}
  ],
  "overall": "warn"
}
```

Статусы: `ok` | `warn` | `error` | `unknown`. `overall` — худший из них.

## Applying

| Метод | Путь | Описание |
|---|---|---|
| `POST` | `/api/apply` | пересобрать конфиг sing-box и nft, применить |
| `GET` | `/api/apply/status` | есть ли несохранённые изменения |

Изменения через REST пишутся в БД **сразу**, но применяются пакетно по `/api/apply`.
Причина: правка правил — многошаговая операция, и перегенерировать конфиг sing-box после
каждого нажатия дорого и вызывает разрывы соединений. UI показывает плашку «есть
непримененные изменения» с кнопкой.

Ошибка `sing-box check` возвращает `422` с текстом ошибки; предыдущий конфиг остаётся
активным.

## WebSocket

`GET /api/ws` — один канал, сообщения вида `{"type": "...", "data": {...}}`.

| Тип | Частота | Содержимое |
|---|---|---|
| `peers` | 2 с | статистика и онлайн-статус пиров |
| `traffic` | 1 с | суммарные rx/tx для графика |
| `diag` | по событию | обновление статуса проверок |
| `log` | по событию | новые строки лога с уровнем `error`/`fatal` |
| `apply` | по событию | ход применения конфигурации |

Одно соединение вместо канала на сущность: клиентов мало, данных мало, мультиплексирование
не окупает сложности. Опрос ставится на паузу по `visibilitychange` на стороне UI.
