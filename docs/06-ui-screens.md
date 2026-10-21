# Экраны UI

Четыре экрана. Любая новая настройка должна либо вытеснить существующую, либо быть
отклонена — см. [принципы](00-vision.md#ключевые-продуктовые-принципы).

The panel uses HTML, CSS and ES modules in `ui/dist`, embedded in the daemon with
`go:embed` and no frontend build step. Dark mode follows the system preference.
The [task #22 specification](../tasks/done/22-ui.md#decisions) records why Svelte + Vite
was rejected.

## 1. Clients

The default inventory shows device identity/address, enabled state, handshake activity,
traffic since gateway-interface startup and configured routing. Each row has one
**Details** action. Routing summaries distinguish shared rules from a selected exit;
missing, disabled and empty-pool exits remain visibly blocked without direct fallback.
Neither a healthy exit nor global pending status proves a client's active route.

**Add client** asks only for a device name. Address, keys, MTU and DNS are assigned
automatically. Enter and pointer submission share one in-flight gate. A successful
creation opens the same Details dialog and reveals its expanded WireGuard loading,
ready or retry section, including on narrow screens. A
same-session creation finishing after ordinary close still updates inventory and
pending changes, without reopening the dialog.

Details contains:

- **Device:** name and enabled state.
- **Internet routing:** explicit Shared rules or Whole Internet through a tunnel.
  Shared rules may tunnel, send directly or block selected resources; unmatched
  traffic goes direct. Whole Internet supports IPv4 TCP/UDP and DNS, blocks IPv6,
  ping/other protocols/private forwarding, retains gateway access and has no direct
  fallback (ADR 0028). An unavailable saved/selected exit is retained rather than
  silently replaced. Tunnel loading/retry does not discard the current selection.
- **WireGuard configuration:** an independently loaded disclosure with QR, Copy,
  Download and optional configuration text. All configuration and filenames come
  from the daemon. Delivery uses saved settings; unsaved name edits disable Copy
  and Download with an adjacent Save-the-name explanation until saved or reverted. Saved rename invalidates the old text, QR and filename,
  including in another open Details interaction for the same client. Configuration
  errors offer Retry while the device/routing draft remains editable.
- **Delete device:** a separate inline confirmation names affected and orphaned
  selected-client rules. Keep client returns to the preserved draft. Orphaned rules
  require reassignment and explicit activation; rule publication still needs Apply
  (ADR 0026).

**Save changes** sends one transactional PATCH containing only changed fields. Writes
for the same client are sequenced across replacement dialogs; a stale untouched form
snapshot cannot revert an earlier accepted change. Failures retain the draft and a
persistent inline error. Successful same-session saves update inventory and pending
state after ordinary close; they cannot replace a newer editor or its draft. Previous
session callbacks cannot change new-session state. Close, header close, Escape and
backdrop discard unsaved edits; the persistent footer states this explicitly.

Saving routing is distinct from Apply. Enabled changes and deletion may affect
WireGuard access during the daemon's next 10-second synchronization, before Apply.
The UI does not promise a per-device applied state or an exact revocation deadline.

Configuration text, filename, QR pixels and download object URLs belong to the modal
and are cleared on close/replacement/logout/session expiry. Late responses cannot
repopulate another modal. Copy captures the delivered text and guards asynchronous
feedback; clipboard content deliberately leaves the application and is not erased
by modal cleanup. Removing application references does not promise heap erasure.

Client addresses, expiry, traffic limits and per-client resource-list selection are
not configurable here. Rules assign selected clients from the Rules screen. Handshake
status and traffic counters remain observations, not claims of successful Internet
connectivity or lifetime usage.

## 2. Туннели

```
┌──────────────────────────────────────────────────────────────┐
│  Туннели                                    [+ Добавить]     │
├──────────────────────────────────────────────────────────────┤
│  ● Нидерланды       vless    nl.example.com     42 мс   [⋮]  │
│  ● Домашний wg      wireguard  198.51.100.7     18 мс   [⋮]  │
│  ⚠ Резервный        hysteria2  hy.example.net   нет ответа   │
└──────────────────────────────────────────────────────────────┘
```

Добавление — одно поле `textarea` и имя:

```
┌─ Новый туннель ──────────────────────────────────────────────┐
│  Имя:  [ Нидерланды                                       ]  │
│                                                              │
│  Вставьте ссылку или конфиг WireGuard:                       │
│  ┌────────────────────────────────────────────────────────┐  │
│  │ vless://uuid@nl.example.com:443?security=reality&...    │  │
│  └────────────────────────────────────────────────────────┘  │
│                                                              │
│  ✓ Распознан VLESS · REALITY · TCP · nl.example.com:443      │
│                                             [Отмена] [Сохранить] │
└──────────────────────────────────────────────────────────────┘
```

Разбор идёт по мере ввода (`POST /api/tunnels/parse`), результат показывается строкой
подтверждения или внятной ошибкой. Пользователь видит, что вставил, **до** сохранения —
это снимает основной класс обращений в поддержку.

Поддерживаются: `vless://`, `ss://`, `trojan://`, `hysteria2://`, `hy2://`, `socks5://`,
INI-конфиг WireGuard, произвольный JSON outbound.

При правке существующего туннеля конфиг подгружается в поле отдельным запросом
(`GET /api/tunnels/{id}/raw`), а не берётся из списка: в списке его нет вовсе — там
ключ, а список перечитывается на каждый опрос экрана ([API](05-api.md#tunnels)). Пока
конфиг не приехал, поле заблокировано: пустое активное поле читалось бы как «конфига
нет», и сохранение стёрло бы настоящий. У туннеля WARP поля конфига нет совсем — ключ
выдал Cloudflare, править его руками нечего, — вместо него объяснение, и меняется
только имя.

### Пул бесплатных ключей

Пул — та же строка списка, но заводит его демон, а не пользователь, и в системе он
один (`builtin`), поэтому меню у него своё: **«Детали»** и **«Включить/Выключить»**.
«Изменить» нет — править нечего, кроме каталога; «Удалить» нет — встроенное
выключают, и API удалить его тоже не даёт. Наборов меню ровно два: этот у пула и
обычный («Изменить», «Включить/Выключить», «Удалить») у остальных туннелей.

«Детали» открывают состав каталога (`GET /api/tunnels/{id}/pool`):

```
┌─ Пул «Общий пул» ────────────────────────────────────────────┐
│  https://raw.githack.com/igareck/vpn-configs-for-russia/main/ │
│  каталог обновлён 26.07.2026, 15:32 (2 часа назад)           │
│  известно 148 серверов                                       │
│                                                              │
│  В ротации 13                                                │
│   ● VLESS 39712                                36 мс          │
│   ● VLESS 39701                             34 мс  сейчас     │
│  ▸ Не отвечают 3                                             │
│  ▸ Остальные — вне ротации 121                               │
│                                    [Обновить каталог] [Закрыть] │
└──────────────────────────────────────────────────────────────┘
```

В ротации — те, кто попал в конфиг (лучшие по пингу каталога, ADR 0010); выбранный
сейчас помечен. Мёртвые и остальные свёрнуты и раскрываются по клику. Страну сервера
панель не показывает: geo-IP убран вместе со страновыми пулами
([ADR 0018](decisions/0018-unified-pool.md)), а метка каталога врёт (issue #161).
У серверов вне ротации живость не показывается вовсе — их никто не проверял, и «не
отвечает» было бы неправдой.

Число серверов подписано «известно N», а не «N в каталоге»: список копит известные
серверы между обходами и выбрасывает сервер только после трёх пропусков подряд, поэтому
он обычно больше того, что каталог отдаёт за раз (на стенде 137 против 125–128). Та же
подпись на карточке в списке туннелей.

## 3. Правила

```
┌──────────────────────────────────────────────────────────────┐
│  Правила                                    [+ Добавить]     │
│  Проверяются сверху вниз, первое совпадение выигрывает.      │
├──────────────────────────────────────────────────────────────┤
│  ⠿  1  Банки и госуслуги    → напрямую          вкл  [⋮]     │
│  ⠿  2  YouTube и Google     → Нидерланды        вкл  [⋮]     │
│  ⠿  3  Соцсети              → Нидерланды        вкл  [⋮]     │
│  ⠿  4  Реклама              → блокировать       выкл [⋮]     │
└──────────────────────────────────────────────────────────────┘
```

`⠿` — перетаскивание. Подпись про порядок проверки стоит прямо в шапке: это единственная
концепция, которую пользователь обязан понять, и её нельзя спрятать.

**Отказ правила виден в строке.** Правило, чей туннель выключен или чей пул остался без
живых серверов, отбрасывает трафик, а не пускает его напрямую
([ADR 0013](decisions/0013-unavailable-tunnel-rejects.md)) — и снаружи это неотличимо от
«всё сломалось». Поэтому цель такого правила красится тревожным цветом прямо в строке, а
под чипами стоит фраза о том, что происходит. Без наведения и без клика: строка правила —
единственное место, где это увидят. Пул, про который ядро молчит (`servers_alive: null`,
Clash API недоступен), не красится: «не знаем» — не то же самое, что «нет живых».

**Молчащий канал — третье состояние строки, не второй повод покрасить её красным.**
Туннель включён и применён, а последняя проверка вернула `status: "down"`: правило при
этом работает, остаётся в конфиге обычным маршрутом и отправляет трафик — тот просто не
доходит. Отказа здесь нет, `reject` не наступает, и фраза «трафик отбрасывается» была бы
ложью. Строка красится жёлтым, а не красным, бейдж несёт `◌` вместо `⚠`, и подпись
называет время последнего удачного ответа (`last_ok`): «упал минуту назад» и «мёртв со
вчера» — разные новости. Времени нет — так и написано, выдуманная дата туда не ставится.
`status: null` (проверку не делали, Clash API недоступен) не красит ничего, ровно как
`servers_alive: null`.

**У чипа списка — состояние его источника:** когда обновился, не обновился ли с ошибкой
или не обновлялся ни разу; текст ошибки источника — строкой под чипами. Список, который
перестал обновляться, тихо перестаёт ловить домены, и раньше выглядел ровно как рабочий.
Состояния приходят полем `lists_status` вместе с правилом ([API](05-api.md#rules)).

**Пустой экран предлагает стартовое правило.** Правил нет — весь трафик идёт напрямую, и
это законное состояние: так выглядит и свежая установка, и сервер, где правила удалили
осознанно. Поэтому пустота не молчит и не давит:

```
┌──────────────────────────────────────────────────────────────┐
│  Правил пока нет — весь трафик идёт напрямую, с адреса        │
│  сервера. Так и задумано, пока вы не решите иначе.            │
│                                                               │
│  Стартовый набор отправит в туннель: Россия — внутренние      │
│  сервисы, Google AI, Discord, Meta (Facebook, Instagram),     │
│  Twitter (X). Форма откроется заполненной — туннель           │
│  выбираете вы, лишнее снимаете, название меняете.             │
│                    [ Собрать стартовое правило ]              │
└──────────────────────────────────────────────────────────────┘
```

Кнопка не создаёт правило сама: она открывает обычную форму нового правила с отмеченными
списками пресета ([ADR 0014](decisions/0014-rule-preset-offered-not-applied.md)). Дальше
всё как у правила, заведённого руками, — «Сохранить», потом «Применить». Пометки
«системное» у получившегося правила нет, оно правится и удаляется наравне с остальными.

Состав пресета приходит из каталога полем `in_preset` ([API](05-api.md#rules)) — панель
не хранит свой список ключей. Каталог недоступен — предложения нет вовсе, вместо него
строка о том, что каталог не пришёл: выдумывать состав панель не станет.

**Туннелей нет — кнопка ведёт к ним, а не создаёт правило без цели.** Правило со ссылкой
на несуществующий туннель — ошибка генерации конфига, а правило с выключенным туннелем
отбрасывает трафик ([ADR 0013](decisions/0013-unavailable-tunnel-rejects.md)). Поэтому,
пока нет ни одного включённого туннеля, на месте кнопки стоит «Перейти к туннелям» и
фраза о том, что направлять пока некуда.

**Пробник маршрута** — поле «домен» и две кнопки над списком правил:

```
┌──────────────────────────────────────────────────────────────────────┐
│  Проверить домен  [ youtube.com  ]  [Куда уйдёт] [Доступность напрямую]│
│  Куда уйдёт — отвечает sing-box; Доступность напрямую — демон сам      │
│  идёт к домену с адреса сервера.                                       │
│  ───────────────────────────────────────────────────────────────────  │
│  youtube.com                                                          │
│  (YouTube и Google)  route(tun-…)                                     │
│  Домен ловит правило «YouTube и Google»…                              │
│  198.18.0.9 — FakeIP                                                  │
│  ───────────────────────────────────────────────────────────────────  │
│  chatgpt.com — доступность напрямую  HTTP 403                         │
│  напрямую отдаёт 403 — похоже на геоблок.                             │
└──────────────────────────────────────────────────────────────────────┘
```

Пятого экрана он не заводит: ответ нужен там же, где стоит список правил. Обе пробы делят
одно поле домена. **Куда уйдёт** спрашивает работающее ядро, а не базу, — накопленные и не
применённые правки в ответ не попадают, и расхождение называется прямо; ядро не ответило —
на месте ответа стоит объяснение, а не пустота (пустой результат читался бы как «ни одно
правило не сработает»). **Доступность напрямую** — демон сам делает `GET https://<домен>/`
со своего адреса и говорит, открывается ли тот в лоб или отдаёт геоблок (`403`/`451`,
Cloudflare 1020); вердикт красится по классу и приходит с сервера как есть — про починку
туннелем панель не додумывает (это Фаза 1). Соединение оборвалось — «напрямую не отвечает».

### Rule editor

The dialog follows one reading order: **Name → Clients → Resources → Action**.
Its body scrolls while Cancel, Save changes and the Apply reminder remain available.
The compact priority line identifies the actual position; optional Ordering help
explains first-match precedence and moving rules in the inventory.

- **Clients:** All clients or Selected clients. Selection has search and a persistent
  selected count. Full names wrap; disabled and whole-Internet status appear separately.
  Whole-Internet exits precede shared rules. Disabled clients do not gain access from
  selection. An empty selected disabled orphan stays editable and disabled; assignment
  does not enable it (ADR 0026).
- **Resources:** Community source counts remain visible while selected-source review
  and catalog browsing use separate disclosures. Search filters the catalog without
  changing selection. IDs absent from a partial/unavailable catalog stay selected,
  with an explicit retained-source warning and deliberate removal controls.
  Existing API-managed URLs are escaped inert text outside catalog disclosures and
  are omitted from PATCH. URL-only rules remain editable. Domain and IPv4 fields
  retain bounded textareas, examples and live line validation; optional help explains
  matching and DNS without adding a DNS setting.
- **Action:** Tunnel, Direct or Block. A separate associated Tunnel label stays beside
  its selector. New rules require a deliberate target; empty, missing, disabled and
  empty-pool targets never select a replacement. Blocked and failed-health guidance
  distinguishes rejection from an unhealthy route, with no direct fallback. Optional
  Advanced WARP chain controls open automatically for an existing second link,
  including an unavailable reference. WARP remains the final, distinct link (ADR 0012).

Overlap warnings consider intersecting enabled shared-rule clients and resources,
not total shadowing for every client. Save validation and server errors remain in the
dialog with the draft preserved. Save records pending routing changes; Apply activates
them. Same-session successful mutations still mark pending changes after ordinary
closure, while modal/session/request revisions prevent delayed success or failure from
closing or repopulating a replacement editor. The editor does not change API, DNS,
chain or routing semantics; the backend remains authoritative, including nonempty
TunnelID validation for disabled rules.

## 4. Диагностика

```
┌──────────────────────────────────────────────────────────────┐
│  Диагностика                        [Проверить заново]       │
├──────────────────────────────────────────────────────────────┤
│  ✓  WireGuard        wg0 поднят, 3 пира, 2 онлайн            │
│  ✓  sing-box         1.12.4, запущен 4 часа назад            │
│  ✓  Правила nftables таблица razdacha, 1284 подсети          │
│  ⚠  Туннели          «Резервный» не отвечает                 │
│  ✓  Списки           обновлены 3 часа назад                  │
│  ✓  IP forwarding    включён                                 │
│  ✓  Path MTU         1500 до всех туннелей                   │
├──────────────────────────────────────────────────────────────┤
│  [Скачать отчёт]   [Логи]   [Конфиг sing-box]                │
└──────────────────────────────────────────────────────────────┘
```

«Скачать отчёт» собирает всё в один текстовый файл для багрепорта — с замаскированными
ключами, IP-адресами эндпоинтов и UUID. Маскирование обязательно: пользователи вставляют
такие отчёты в публичные чаты. Функция `maskIP` берётся из `fe-app-podkop/src/helpers/`.

## Общие элементы

**Плашка непримененных изменений** — фиксирована внизу, появляется после правки правил
или настроек:

```
  Есть непримененные изменения                    [Применить]
```

Причина отложенного применения — в [API](05-api.md#applying).

**Settings** open from the header gear as one dialog with Network, Schedules,
Telegram and Backup navigation. Each section has its own Save action, draft indicator
and persistent inline feedback. Switching sections preserves drafts; closing discards
only unsaved edits. Network saves validate port, MTU and required DNS before any
request and require global Apply. Changed client connection settings require updated
client configurations. Schedules save only list/pool/check intervals and are picked up
automatically. Use global Apply to update the active configuration record as well.
Unknown current interval values remain selected instead of silently
resetting to a preset. Pool refresh has a 30-minute minimum.

Network and Schedules PATCH only changed fields, preserving hidden settings such as
the pool country blocklist and untouched values loaded before an earlier save finishes.
Settings, Telegram and Backup endpoints perform full-record read/modify/write, so
the panel serializes all section saves across sections and replacement dialogs.
Telegram and Backup also omit unchanged ordinary fields so an older replacement
form cannot revert a preceding save or secret rotation. Queued sections show a waiting
status and retain their draft; success or failure releases the next request. Queued
intent from a previous session is canceled before dispatch. Successful responses confirm
only their own section's values, avoiding stale cross-section response overwrites. Telegram and Backup save only through their own endpoints and take
effect immediately. No action silently saves another section. Failed requests retain
the draft and explain that saving could not be confirmed; other sections were not saved.
Settings are persisted by the daemon; advanced options use the authenticated settings
API, not a configuration file or direct database edits.

Telegram token, backup passphrase and interval controls have persistent labels. Saved
secret presence is separate from the empty secret field. Blank secret fields are omitted
from requests, preserving existing values. Modal cleanup drops drafts and clears secret
fields; late callbacks cannot refill replacement dialogs or update a later session.

Test saved channel and Send saved backup now use saved configuration and never save
first. Unsaved edits disable the section's delivery action until saved or reverted.
Delivery is allowed with notifications or backup scheduling disabled, but needs a saved
Telegram channel and, for backups, an encryption passphrase. Delivery failures leave
saved settings unchanged and remain visible in the section. Backup sends refresh only
delivery history after success or failure, without replacing form values. History-read
failures explicitly mark that metadata unavailable.

Download full backup uses saved state without saving the form. Its adjacent warning
states that the unencrypted file contains all client private keys and access to the
entire VPN. Telegram delivery is encrypted. The passphrase warning explains that a
forgotten phrase makes encrypted backups unrecoverable and must be stored separately
from the delivery chat ([ADR 0016](decisions/0016-backup-full-and-encrypted.md)).

## Что не делаем

- Графики трафика по каждому пиру с историей — суммарный график на экране клиентов
  достаточен, хранение временных рядов тянет за собой отдельную подсистему.
- Редактор JSON-конфига sing-box в UI — только просмотр. Кто умеет править — правит
  через `raw` туннеля.
- Мастер первичной настройки. Установщик доводит систему до рабочего состояния сам,
  визарду нечего спрашивать.

## Deleted-client rules (ADR 0026)

Client deletion confirmation lists affected rules and those losing their final existing
client. The latter remain visible as disabled selected rules. The rules screen shows
"No selected clients" and instructs the operator to assign clients, explicitly enable
and Apply. Editing an inactive rule does not force activation or a change to all clients.
The panel reloads rules after deletion; it does not predict the committed effects from
its pre-confirmation cache alone. Operators should audit historical all-client rules,
which cannot be automatically distinguished from the previous deletion bug.
