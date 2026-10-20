---
type: invariants
title: Ui
description: Static HTML/CSS/ES modules embedded from ui/dist; defaults instead of options
---

## Invariants
- Интерфейс — статика без сборщика в `ui/dist`, уезжает в бинарник через `go:embed`. Никакого npm и node в цепочке сборки демона.
- Внешних ссылок в статике нет вовсе: ни CDN, ни шрифтов, ни картинок. Всё, что нужно панели, лежит рядом — иначе оно не попадёт в `go:embed`. Единственный внешний запрос — проверка свежего релиза у GitHub, и её делает **браузер**, а не демон: сервер наружу не ходит, панель работает и без исходящего интернета, а бейдж просто не появляется (#114).
- Пустота не заполняется выдумкой: нет данных — так и говорим («не проверялся», «данные появятся позже»), а не показываем ноль.
- A current-session authenticated `401` returns to the login gate; successful login
  returns to the selected screen. A response from a previous session cannot dismiss
  a newer login. Incorrect-password login retains its local error handling.
- Высота элементов с прокруткой задаётся им самим, а не цепочкой flex-родителей: цепочка рвётся молча (см. `<details>` в каталоге списков).
- Меню строк живёт в `body`: внутри `.card` с `overflow: hidden` его обрезает.
- Отметка строки в сетке красит фон и рамку, но не меняет метрику текста: `font-weight` по `:checked` расширяет название, строка переносится, и сетка пересобирается под курсором.
- Пул узнаётся по `source`, а не по `type`: тип означает протокол, и какой он — решает драйвер каталога (ADR 0015), а не форма конфига. Пинга у источника может не быть вовсе: `ping_ms = null` показывается словами «пинг неизвестен», а не нулём.
- Выключенный пул не показывает ни живых, ни выбранный сервер: цифры у туннеля, которого нет в конфиге, это ложь, а не ноль. Число серверов подписано «известно N» — список в БД больше того, что каталог отдаёт за раз.
- Четыре экрана, не больше (`docs/00-vision.md`). Экран входа — гейт, а не пятый раздел.

## Key entry points
- `ui/dist/js/api.js` — единственный слой доступа к API: экраны не зовут `fetch` сами.
- `ui/dist/js/shell.js` — тосты, модальные окна, меню, общее состояние.
- `ui/ui_test.go` — сторож: баланс скобок в css и ссылки из `index.html`.

- Deleting a client names affected/orphaned rules before confirmation, reloads rules after success and explains explicit reassignment, activation and Apply. Selected empty disabled rules remain editable and show the inactive reason. Assignment alone does not enable a rule (ADR 0026).

- Client configuration text, filename, QR pixels and download object URLs belong only to
  the current modal. Closing or replacing it clears these copies and invalidates both
  successful and failed pending configuration requests, including peer creation.
  Logout invalidates the modal before waiting for the server; session expiry shares
  the same cleanup. This removes application references, not JavaScript heap contents.
- Tunnel editor raw text, pending debounce and last parse state share that modal
  lifetime. Late raw/parse responses cannot refill closed or replaced editors; newer
  input wins over older parse responses. Session checks returning 401 invalidate the
  modal as other authenticated API calls do; incorrect-password login stays local.
- Session revisions distinguish ordinary modal closure from logout/expiry. A completed
  peer creation still updates pending-change state after ordinary close/replacement;
  completion from a previous session cannot repopulate UI state. A late old-session
  401 cannot trigger frontend dismissal of a configuration opened after a new login.
  Backend invalid-session responses still expire cookies; late cookie processing can
  require another login. Browser stale-401 evidence without Set-Cookie proves only
  notification/rendering lifetime, not cookie delivery timing.
- Logout clears secrets immediately and prevents a new login until its response
  settles, including failure. This prevents a late logout cookie from deleting a
  newly issued session cookie; form submission by Enter obeys the same pending gate.

## Configuration lifetime verification boundary

The synthetic actual-API fixture verifies frontend lifetime, not backend cookie
delivery or installed deployment. Cleanup removes application references without
promising heap erasure or immunity to delayed cookie expiry. A dataset marker alone
does not prove visible rendering. Keep case-specific coverage and inconclusive
observations explicit in the [completed specification](../../tasks/done/216-clear-ui-config-secrets.md).
