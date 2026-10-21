/* Экран «Туннели». Добавление — одно поле: тип определяется по вставленному
   конфигу, руками его не выбирают. Разбор идёт на сервере
   (`POST /api/tunnels/parse`) — свой парсер в интерфейсе означал бы второй,
   расходящийся с настоящим. */

import * as api from '../api.js';
import {
  state, tunnelById, toast, toastError, openModal, closeModal, modalShell,
  openMenu, notImplemented, refresh, markDirty,
  currentModalRevision, isCurrentModal, onModalCleanup,
} from '../shell.js';
import {
  $, esc, plural, since, until, stamp, TUNNEL_LABEL, tunnelLabel, tunnelEndpoint, tunnelPool,
  isWarp,
} from '../util.js';

export const title = 'Туннели';

let inventoryRevision = 0;
let inventoryLoad = null;
const operations = new Map();

/** Shared publication token for screen and startup tunnel inventory readers. */
export function beginInventoryRead() {
  const currentSession = api.session.guard();
  const revision = ++inventoryRevision;
  const current = () => currentSession() && revision === inventoryRevision;
  return {
    current,
    accept(tunnels) {
      if (!current()) return false;
      state.tunnels = tunnels;
      return true;
    },
  };
}

/** Coalesce unchanged reads; accepted mutations invalidate older snapshots. */
export async function load() {
  if (inventoryLoad?.read.current()) return inventoryLoad.pending;
  const read = beginInventoryRead();
  const entry = { read, pending: null };
  entry.pending = (async () => {
    try {
      const tunnels = await api.tunnels.list();
      read.accept(tunnels);
    } catch (err) {
      if (read.current()) throw err;
    } finally {
      if (inventoryLoad === entry) inventoryLoad = null;
    }
  })();
  inventoryLoad = entry;
  return entry.pending;
}

// A new login never waits behind an operation from the previous session.
function beginOperation(key, button, modal = false) {
  if (operations.get(key)?.currentSession()) {
    toast('A tunnel operation is already in progress. Wait for it to finish before retrying.', 'err');
    return null;
  }
  const ctx = {
    key, button, label: button?.textContent, modal, revision: currentModalRevision(),
    currentSession: api.session.guard(), screen: state.screen, screenNode: $('#screen')?.firstElementChild,
  };
  ctx.current = () => ctx.currentSession() && ctx.revision === currentModalRevision()
    && (ctx.modal ? isCurrentModal(ctx.revision)
      : ctx.screen === state.screen && ctx.screenNode === $('#screen')?.firstElementChild);
  operations.set(key, ctx);
  if (button) button.disabled = true;
  return ctx;
}

function finishOperation(ctx) {
  const owned = operations.get(ctx.key) === ctx;
  if (owned) operations.delete(ctx.key);
  // Restoring our connected control is independent of obsolete modal feedback.
  if (owned && ctx.currentSession() && ctx.button?.isConnected) {
    ctx.button.disabled = false;
    ctx.button.textContent = ctx.label;
  }
}

function operationError(ctx, err, fallback) {
  if (ctx.current()) toastError(err, fallback);
}

/** The inventory never keeps editor raw configuration copies. */
function acceptTunnel(result, fallback = {}) {
  inventoryRevision++;
  const safe = { ...fallback, ...(result || {}) };
  delete safe.raw;
  if (!safe.id) return;
  const tunnel = tunnelById(safe.id);
  if (tunnel) Object.assign(tunnel, safe);
  else state.tunnels.push(safe);
}

function acceptPool(id, result) {
  inventoryRevision++;
  const tunnel = tunnelById(id);
  if (!tunnel || !result) return tunnel;
  if (result.pool) {
    const safe = { ...result };
    delete safe.raw;
    Object.assign(tunnel, safe);
  } else tunnel.pool = { ...(tunnel.pool || {}), ...result };
  return tunnel;
}

async function reloadAfterMutation(ctx, success) {
  if (!ctx.currentSession()) return;
  try {
    await load();
  } catch (err) {
    if (!ctx.currentSession()) return;
    if (ctx.current()) toast(`${success}. Inventory refresh failed: ${err.message}. The change was saved; reload the Tunnels screen instead of repeating it.`, 'err');
    refresh();
    return;
  }
  if (!ctx.currentSession()) return;
  if (ctx.current()) toast(success);
  refresh();
}

/* Кнопка «+ WARP» показывается, пока WARP не заведён: второй раз она отвечает
   409, а кнопка, которая только отказывает, — обещание несуществующего. Прячется
   именно кнопка, а не возможность: несколько WARP-туннелей законны, второй
   добавляется вставкой `.conf` через «+ Добавить». Отказ бережёт Cloudflare —
   каждая регистрация заводит там настоящее устройство.

   Признак — `source`, а не имя: WARP, вставленный руками, узнаётся разбором
   конфига и тоже прячет кнопку (ADR 0012). */
const head = () => `
  <div class="screen-head">
    <h1>Туннели</h1>
    <div class="spacer"></div>
    ${state.tunnels.some(isWarp) ? '' : '<button class="btn" data-act="add-warp">+ WARP</button>'}
    <button class="btn btn-primary" data-act="add-tunnel">+ Добавить</button>
  </div>`;

/* --- пул ------------------------------------------------------------------
   Пул — тот же айтем списка, не отдельный экран и не отдельная сущность:
   правило навешивается на него штатно (спека tasks/63-pool-tunnel-card.md).
   Отличается он выделением строки и своим набором данных: каталог вместо
   адреса, сколько серверов живо, какой выбран сейчас, когда обновлялся.

   Выключенный пул не показывает живых серверов и текущий выбор: цифры от
   выключенного туннеля — ложь, а не ноль. */

/** Сколько серверов известно и сколько из них живы.

    Именно «известно», а не «в каталоге»: список копит серверы между обходами и
    выбрасывает сервер только после трёх пропусков подряд (issue #68), поэтому он
    обычно больше того, что каталог отдаёт за раз — на стенде 137 против 125–128.
    Подписать это «в каталоге» значило бы соврать в цифре, которую видно. */
function poolServers(pool, off) {
  const total = Number(pool.servers_total) || 0;
  if (!total) return '';
  // Живых не знаем (поля нет) или знать нечего (пул выключен) — говорим только
  // про размер списка, а не подставляем ноль.
  if (off || typeof pool.servers_alive !== 'number') {
    return `<span class="badge">известно ${total} ${plural(total, 'сервер', 'сервера', 'серверов')}</span>`;
  }
  const alive = pool.servers_alive;
  return `<span class="badge ${alive ? 'ok' : 'err'}">живых ${alive} из ${total}</span>`;
}

/** Сколько нод выброшено фильтром пула (ADR 0020).

    Отдельным бейджем, а не молчанием: пул, похудевший на семнадцать нод, иначе
    выглядит обеднённым каталогом. Ноль не показываем — это не новость. */
function poolExcludedBadge(pool) {
  const n = Number(pool.servers_excluded) || 0;
  if (!n) return '';
  return `<span class="badge">исключено ${n}</span>`;
}

/** Сервер, через который пул идёт прямо сейчас. */
function poolCurrent(pool, off) {
  const cur = pool.current || {};
  if (off || !cur.name) return '';
  const parts = [esc(String(cur.name))];
  if (cur.latency_ms != null) parts.push(`${esc(cur.latency_ms)} мс`);
  return `<div class="row-meta pool-now">Сейчас: ${parts.join(' · ')}</div>`;
}

/** Расписание каталога. Следующего обновления у выключенного пула не будет —
    обещать его нечем.

    Время обхода показывается точно, а относительное «2 часа назад» идёт рядом
    в скобках: на вопрос «когда именно обходили» относительная подпись не отвечает. */
function poolSchedule(pool, off) {
  const parts = [];
  if (pool.updated_at) {
    const abs = stamp(pool.updated_at);
    parts.push(abs
      ? `каталог обновлён ${abs} (${since(pool.updated_at)})`
      : `каталог обновлён ${since(pool.updated_at)}`);
  } else {
    parts.push('каталог ещё не загружался');
  }
  if (!off) {
    // У следующего обхода абсолютной даты нет намеренно: точное «когда обходили»
    // нужно, а точное «когда обойдёт» — нет, и строка меты и без него длинная.
    const next = until(pool.next_update_at);
    if (next) parts.push(`дальше ${next}`);
  }
  return ' · ' + parts.join(' · ');
}

/* --- модалка «Детали» -----------------------------------------------------
   У пула нет формы конфига: править в нём нечего, кроме каталога, а сам пул не
   правят и не удаляют вовсе — его выключают. Вместо формы показывается то, что
   у пула меняется само: состав серверов.

   Список приходит отдельным запросом (`GET /api/tunnels/{id}/pool`), а не в
   ответе экрана: сотня с лишним серверов нужна только открытой модалке.

   Живость осмысленна только для тех, кто попал в конфиг: в ротацию идут лучшие
   по пингу карточки (ADR 0010), остальных никто не проверял — у них `alive` и
   `latency_ms` приходят null, и вместо выдуманного «не отвечает» говорится
   «не проверялся». */

/** Один сервер строкой: имя · задержка. */
function poolServerRow(s, { current } = {}) {
  const parts = [`<span class="pool-srv-name">${esc(s.title || '—')}</span>`];

  let dot = 'off';
  let badge = '';
  if (s.alive === true) {
    dot = 'on';
    badge = s.latency_ms != null
      ? `<span class="badge ok">${esc(s.latency_ms)} мс</span>`
      : '<span class="badge ok">отвечает</span>';
  } else if (s.alive === false) {
    dot = 'bad';
    badge = '<span class="badge err">нет ответа</span>';
  } else if (s.in_rotation) {
    badge = '<span class="badge">не проверялся</span>';
  } else if (s.ping_ms != null) {
    // Вне ротации своей задержки нет: показываем пинг карточки каталога и
    // подписываем, откуда он, чтобы его не спутали с измеренным.
    badge = `<span class="badge">${esc(s.ping_ms)} мс по каталогу</span>`;
  } else {
    // Пинга у каталога может не быть вовсе: outlinekeys задержку не измеряет
    // (ADR 0015). Ноль здесь читался бы как «ноль миллисекунд», поэтому так и
    // говорим — неизвестно.
    badge = '<span class="badge">пинг неизвестен</span>';
  }

  return `<div class="pool-srv${current ? ' current' : ''}">
      <span class="dot ${dot}"></span>
      <div class="pool-srv-main">${parts.join(' · ')}</div>
      ${badge}
      ${current ? '<span class="badge accent">сейчас</span>' : ''}
    </div>`;
}

/** Свёрнутый блок: раскрывается по клику, закрыт по умолчанию — мёртвые и
    непопавшие в ротацию нужны редко, а места занимают больше всего. */
function poolFold(title, n, body) {
  if (!n) return '';
  return `<details class="pool-fold">
      <summary>${esc(title)} <span class="pool-fold-count">${n}</span></summary>
      <div class="pool-fold-body">${body}</div>
    </details>`;
}

/** Отбракованный фильтром сервер: подпись каталога и причина.

    Живости у него нет и быть не может — в конфиг он не попал, никто его не
    проверял. Поэтому строка без точки состояния и без задержки: показывается
    ровно то, что известно. */
function poolExcludedRow(s) {
  return `<div class="pool-srv">
      <div class="pool-srv-main"><span class="pool-srv-name">${esc(s.title || '—')}</span></div>
      <span class="badge err">${esc(s.reason || 'исключён фильтром')}</span>
    </div>`;
}

/** Блок «Исключены фильтром». Свёрнут, как и прочие вторичные списки, но виден
    всегда, когда исключённые есть: молча исчезнувшая нода читалась бы как
    поломка каталога, а не как работа фильтра (ADR 0020). */
function poolExcludedSection(excluded) {
  if (!excluded.length) return '';
  return poolFold('Исключены фильтром', excluded.length,
    `<div class="hint">Эти ключи в пул не идут: страна из чёрного списка либо транспорт
       без шифрования. Страна — со слов каталога, по IP она не проверяется.</div>
     <div class="pool-list">${excluded.map(poolExcludedRow).join('')}</div>`);
}

function poolDetailsHTML(t, data) {
  const servers = data.servers || [];
  const excluded = data.excluded || [];
  if (!servers.length) {
    if (excluded.length) {
      return `<div class="parse-result idle">Все ${excluded.length} известных ключей
        отбракованы фильтром — в пул не попал ни один.</div>
        ${poolExcludedSection(excluded)}`;
    }
    return '<div class="parse-result idle">Каталог ещё не обходился — серверов пока нет. '
      + 'Нажмите «Обновить каталог».</div>';
  }

  const rotation = servers.filter((s) => s.in_rotation);
  const dead = rotation.filter((s) => s.alive === false);
  const live = rotation.filter((s) => s.alive !== false);
  const rest = servers.filter((s) => !s.in_rotation);

  const when = data.updated_at
    ? `каталог обновлён ${stamp(data.updated_at)} (${since(data.updated_at)})`
    : 'каталог ещё не обновлялся';
  const off = t.enabled === false
    ? '<div class="parse-result idle">Пул выключен: серверы не проверяются, '
      + 'поэтому про живость каждого сказать нечего.</div>'
    : '';

  return `
    <div class="row-meta">${esc(when)} · известно ${servers.length} ${
      plural(servers.length, 'сервер', 'сервера', 'серверов')}</div>
    ${off}
    <div class="pool-sect">
      <div class="pool-sect-head">В ротации <span class="pool-fold-count">${live.length}</span></div>
      <div class="pool-list">${live.map((s) => poolServerRow(s, { current: s.current })).join('')
        || '<div class="rule-none">ни один сервер не отвечает</div>'}</div>
    </div>
    ${poolFold('Не отвечают', dead.length,
    `<div class="pool-list">${dead.map((s) => poolServerRow(s)).join('')}</div>`)}
    ${poolFold('Остальные — вне ротации', rest.length,
    `<div class="hint">Проверяются только серверы в ротации — в конфиг попадают лучшие
       по пингу каталога. Об остальных известно лишь то, что было на карточке.</div>
     <div class="pool-list">${rest.map((s) => poolServerRow(s)).join('')}</div>`)}
    ${poolExcludedSection(excluded)}`;
}

function modalPoolDetails(id) {
  const t = tunnelById(id);
  if (!t) return;
  openModal(modalShell(`Пул «${t.name}»`, `
    <div class="row-meta mono pool-catalog-url">${esc(tunnelEndpoint(t))}</div>
    <div class="pool-details" id="pool-details">
      <div class="parse-result idle">Загружаю список серверов…</div>
    </div>`,
  `<button class="btn" data-act="refresh-pool-details" data-id="${esc(id)}">Обновить каталог</button>
     <div class="spacer" style="flex:1"></div>
     <button class="btn btn-primary" data-act="close-modal">Закрыть</button>`),
  () => loadPoolDetails(id));
}

let poolDetailsRevision = 0;
async function loadPoolDetails(id) {
  const box = $('#pool-details');
  if (!box) return false;
  const currentSession = api.session.guard(), modalRevision = currentModalRevision();
  const revision = ++poolDetailsRevision;
  const current = () => currentSession() && isCurrentModal(modalRevision)
    && revision === poolDetailsRevision && box === $('#pool-details');
  try {
    const data = await api.tunnels.poolServers(id);
    if (!current()) return false;
    const tunnel = tunnelById(id);
    if (!tunnel) return false;
    box.innerHTML = poolDetailsHTML(tunnel, data);
    return true;
  } catch (err) {
    if (!current()) return false;
    box.innerHTML = `<div class="parse-result ${err.missing ? 'idle' : 'err'}"></div>`;
    box.firstElementChild.textContent = err.missing
      ? 'Server details are unavailable: GET /api/tunnels/{id}/pool is not implemented.' : err.message;
    return false;
  }
}

/** Одна строка списка туннелей — общая для встроенного пула и своих туннелей. */
function tunnelRow(t) {
  // Правило считается использующим туннель обоими звеньями цепи: вторым
  // звеном туннель держится так же, и удалить его сервер не даст (ADR 0012).
  const used = state.rules.filter((r) => r.tunnel_id === t.id || r.via_tunnel_id === t.id).length;
  const clients = state.peers.filter((p) => p.egress_tunnel_id === t.id);
  const pool = tunnelPool(t);
  const off = t.enabled === false;
  const st = t.enabled === false
    ? { cls: 'off', badge: '', label: 'выключен' }
    : t.signals?.https && !t.signals.https.fresh
      ? { cls: 'off', badge: '<span class="badge">Historical HTTPS observation</span>', label: '' }
    : t.status === 'unknown'
      ? { cls: 'warn', badge: '<span class="badge warn">HTTPS inconclusive</span>', label: '' }
    : t.status === 'up'
      ? { cls: 'on', badge: `<span class="badge ok">HTTPS ${esc(t.latency_ms)} ms</span>`, label: '' }
      // Медленный туннель рабочий, но не годится для видео и звонков —
      // показываем цифру и отличаем цветом, а не прячем среди зелёных.
      : t.status === 'slow'
        ? { cls: 'warn', badge: `<span class="badge warn">HTTPS ${esc(t.latency_ms)} ms, slow</span>`, label: '' }
        : t.status === 'down'
          ? { cls: 'bad', badge: '<span class="badge err">нет ответа</span>', label: '' }
          : t.status === 'not_applied'
            ? { cls: 'off', badge: '<span class="badge">не применён</span>', label: '' }
            : { cls: 'off', badge: '<span class="badge">не проверялся</span>', label: '' };
  return `
      <div class="row${pool ? ' pool' : ''}${off ? ' dim' : ''}">
        <span class="dot ${st.cls}"></span>
        <div class="row-main">
          <div class="row-title">${esc(t.name)}
            <span class="badge${pool ? ' accent' : ''}">${esc(tunnelLabel(t))}</span>
            ${pool ? poolServers(pool, off) : ''}
            ${pool ? poolExcludedBadge(pool) : ''}
            ${st.badge}
            <span class="badge" title="HTTPS does not prove all protocols or clients">Transport: unknown</span>
            ${t.signals?.dns ? `<span class="badge">Runtime DNS: ${esc(t.signals.dns.state)}</span>` : ''}
          </div>
          <div class="row-meta mono">${esc(tunnelEndpoint(t))}</div>
          ${pool ? poolCurrent(pool, off) : ''}
          <div class="row-meta">${used
            ? `используют ${used} ${plural(used, 'правило', 'правила', 'правил')}`
            : 'ни одно правило не ссылается'}${clients.length ? ` · Client exits: ${clients.map((p) => esc(p.name)).join(', ')}` : ''}${st.label ? ' · ' + st.label : ''}${
            pool ? poolSchedule(pool, off) : ''}</div>
        </div>
        <div class="row-actions">
          ${pool ? `<button class="btn btn-sm" data-act="refresh-pool" data-id="${esc(t.id)}" aria-label="Refresh pool ${esc(t.name)}">Обновить</button>` : ''}
          <button class="btn btn-sm" data-act="check-tunnel" data-id="${esc(t.id)}" aria-label="Check tunnel ${esc(t.name)}">Проверить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-tunnel" data-id="${esc(t.id)}" aria-label="Actions for tunnel ${esc(t.name)}" aria-haspopup="menu" aria-expanded="false">⋮</button>
          </div>
        </div>
      </div>`;
}

export function view() {
  if (state.missing.has('tunnels')) {
    return head() + `<div class="card">${notImplemented('Туннели')}</div>`;
  }

  // Встроенный общий пул рисуется как один туннель в общем списке (ADR 0018):
  // отдельного раздела «по странам» больше нет. Свой ряд пул отличает выделением
  // строки и набором данных — каталог вместо адреса, живые серверы, текущий выбор.
  const empty = '<div class="empty">Туннелей пока нет.</div>';
  const listCard = `<div class="card">${
    state.tunnels.map(tunnelRow).join('') || empty}</div>`;

  return head() + listCard + `
    <p class="screen-sub" style="margin-top:10px">
      Тип определяется по вставленному конфигу — выбирать его руками не нужно.
      Исходящие WireGuard-туннели поднимаются в userspace внутри sing-box.
    </p>`;
}

/* --- форма туннеля -------------------------------------------------------- */

/* Ссылки https:// на каталог ключей в подсказке нет намеренно: пул заводит
   демон — один общий (ADR 0018), руками его не создают, и такой ввод получает
   отказ (#71). Предлагать то, что не сработает, — обещать несуществующее. */
const IDLE_HINT = 'Вставьте ссылку vless://, ss://, trojan://, hysteria2://, socks5://, '
  + 'конфиг WireGuard или JSON outbound.';

function modalTunnel(id) {
  const t = id ? tunnelById(id) : null;
  openModal(modalShell(t ? t.name : 'Новый туннель', `
    <div class="field">
      <label for="t-name">Имя</label>
      <input type="text" id="t-name" value="${esc(t ? t.name : '')}" placeholder="Нидерланды" autocomplete="off">
    </div>
    <div class="field" id="t-raw-field">
      <label for="t-raw">Вставьте ссылку или конфиг WireGuard</label>
      <textarea id="t-raw" spellcheck="false" placeholder="vless://…"${t ? ' disabled' : ''}></textarea>
      <div class="parse-result idle" id="t-parse">${esc(t ? 'Загружаю конфиг…' : IDLE_HINT)}</div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="save-tunnel" data-id="${esc(t ? t.id : '')}">Сохранить</button>`),
  (m) => {
    const raw = m.querySelector('#t-raw');
    const currentSession = api.session.guard(), revision = currentModalRevision();
    let timer = null;
    onModalCleanup(() => {
      clearTimeout(timer);
      parseRevision++;
      lastParse = { ok: false, raw: '' };
    });
    // Разбор по мере ввода, но не на каждую букву: это сетевой запрос.
    raw.addEventListener('input', () => {
      parseRevision++;
      lastParse = { ok: false, raw: '' };
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (currentSession() && isCurrentModal(revision)) runParse(raw.value);
      }, 350);
    });
    if (t) loadTunnelRaw(t, m);
    else m.querySelector('#t-name').focus();
  });
}

/* Конфиг существующего туннеля приходит отдельным запросом, а не из списка: в
   нём UUID ключа, а у WARP — приватный ключ устройства, и в ответе экрана они
   ездили бы с каждым опросом (#124). Поле пустое и заблокировано, пока конфиг
   не приехал: пустое активное поле выглядит как «конфига нет», и сохранение
   стёрло бы настоящий.

   Не приехал вовсе — поле открывается пустым и говорит, что вставленный конфиг
   заменит прежний: заводить туннель заново из-за отказа одной ручки незачем. */
async function loadTunnelRaw(t, m) {
  const revision = currentModalRevision();
  const currentSession = api.session.guard();
  const raw = m.querySelector('#t-raw');
  const out = m.querySelector('#t-parse');
  try {
    const got = await api.tunnels.raw(t.id);
    if (!currentSession() || !isCurrentModal(revision)) return;
    if (got.editable === false) {
      // Ключ сгенерировал сервер (WARP): править его руками нечего, поле
      // конфига уступает место объяснению, и правится только имя.
      const field = m.querySelector('#t-raw-field');
      field.innerHTML = '<div class="parse-result idle"></div>';
      field.firstElementChild.textContent = got.note
        || 'Конфиг этого туннеля выдал сервер — править его руками нечего.';
      m.querySelector('#t-name').focus();
      return;
    }
    raw.disabled = false;
    raw.value = got.raw || '';
    raw.focus();
    runParse(raw.value);
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    raw.disabled = false;
    out.className = 'parse-result ' + (err.missing ? 'idle' : 'err');
    out.textContent = err.missing
      ? 'Конфиг не загружен: GET /api/tunnels/{id}/raw ещё не реализован. '
        + 'Вставьте конфиг заново — сохранение заменит прежний.'
      : 'Конфиг не загружен: ' + err.message
        + '. Вставьте его заново — сохранение заменит прежний.';
  }
}

/** Результат последнего разбора: по нему решается, можно ли сохранять. */
let lastParse = { ok: false, raw: '' };
let parseRevision = 0;

async function runParse(raw) {
  const revision = currentModalRevision();
  const requestRevision = ++parseRevision;
  const currentSession = api.session.guard();
  const current = () => currentSession() && isCurrentModal(revision) && requestRevision === parseRevision;
  const out = $('#t-parse');
  if (!out) return;
  const value = (raw || '').trim();
  if (!value) {
    lastParse = { ok: false, raw: '' };
    out.className = 'parse-result idle';
    out.textContent = IDLE_HINT;
    return;
  }
  out.className = 'parse-result idle';
  out.textContent = 'Разбираю…';
  try {
    const r = await api.tunnels.parse(value);
    if (!current()) return;
    lastParse = { ok: true, raw: value };
    const parts = [TUNNEL_LABEL[r.type] || r.type];
    if (r.security && r.security !== 'none') parts.push(String(r.security).toUpperCase());
    if (r.transport) parts.push(String(r.transport).toUpperCase());
    if (r.host) parts.push(`${r.host}${r.port ? ':' + r.port : ''}`);
    const warn = (r.warnings || []).length
      ? `<div style="color:var(--warn);margin-top:4px">⚠ ${(r.warnings).map(esc).join('; ')}</div>`
      : '';
    out.className = 'parse-result ok';
    out.innerHTML = '✓ Распознан ' + esc(parts.join(' · ')) + warn;
  } catch (err) {
    if (!current()) return;
    lastParse = { ok: false, raw: value };
    out.className = 'parse-result ' + (err.missing ? 'idle' : 'err');
    out.textContent = err.missing
      ? 'Предпросмотр недоступен: POST /api/tunnels/parse ещё не реализован. Сохранить всё равно можно — разбор сделает сервер.'
      : '✕ ' + err.message;
    // Пока предпросмотра нет, сохранение не блокируем: иначе туннель не завести.
    if (err.missing) lastParse = { ok: true, raw: value };
  }
}

async function saveTunnel(id) {
  const modal = $('#modal');
  const nameField = modal.querySelector('#t-name');
  const button = modal.querySelector('[data-act="save-tunnel"]');
  if (!nameField || !button || button.disabled) return;
  const name = nameField.value.trim();
  // Server-owned WARP configuration must remain omitted from PATCH.
  const field = modal.querySelector('#t-raw');
  const raw = field ? field.value.trim() : '';
  if (!name) { toast('Enter a tunnel name.', 'err'); return; }
  if (field && field.disabled) { toast('Configuration is still loading.', 'err'); return; }
  if (field && !raw) { toast('Paste the tunnel configuration.', 'err'); return; }
  if (field && lastParse.raw === raw && !lastParse.ok) {
    toast('Correct the configuration before saving.', 'err');
    return;
  }
  const ctx = beginOperation(id ? `tunnel:${id}` : 'create', button, true);
  if (!ctx) return;
  button.textContent = 'Saving…';
  try {
    let saved;
    try {
      saved = id ? await api.tunnels.update(id, field ? { name, raw } : { name }) : await api.tunnels.create(name, raw);
    } catch (err) {
      operationError(ctx, err, 'Tunnel was not saved. Your draft is preserved.');
      return;
    }
    if (!ctx.currentSession()) return;
    acceptTunnel(saved, id ? { id, name } : {});
    markDirty();
    if (ctx.current()) {
      closeModal();
      ctx.modal = false;
      ctx.revision = currentModalRevision();
      ctx.screenNode = $('#screen')?.firstElementChild;
    }
    await reloadAfterMutation(ctx, `Tunnel “${name}” saved`);
  } finally { finishOperation(ctx); }
}

export const actions = {
  'add-tunnel': () => modalTunnel(null),
  'edit-tunnel': (id) => modalTunnel(id),
  'save-tunnel': (id) => saveTunnel(id || null),

  'add-warp': async (_id, button) => {
    const ctx = beginOperation('warp', button);
    if (!ctx) return;
    if (button) button.textContent = 'Registering…';
    try {
      let created;
      try { created = await api.tunnels.addWarp(); }
      catch (err) {
        if (ctx.current() && err.missing) toast('WARP registration is unavailable: POST /api/tunnels/warp is not implemented.');
        else operationError(ctx, err, 'WARP was not added.');
        return;
      }
      if (!ctx.currentSession()) return;
      acceptTunnel(created);
      markDirty();
      await reloadAfterMutation(ctx, `Tunnel “${created?.name || 'WARP'}” added`);
    } finally { finishOperation(ctx); }
  },

  // Pools keep their existing Details/Enable menu; backend deletion rules are unchanged.
  'menu-tunnel': (id, button) => {
    const tunnel = tunnelById(id);
    if (!tunnel) return;
    const toggle = { act: 'toggle-tunnel', id, label: tunnel.enabled === false ? 'Enable' : 'Disable' };
    openMenu(button, tunnelPool(tunnel)
      ? [{ act: 'pool-details', id, label: 'Details' }, toggle]
      : [
        { act: 'edit-tunnel', id, label: 'Edit' }, toggle,
        { act: 'delete-tunnel', id, label: 'Delete', danger: true },
      ]);
  },
  'pool-details': (id) => modalPoolDetails(id),

  'refresh-pool-details': async (id, button) => {
    const ctx = beginOperation(`tunnel:${id}`, button, true);
    if (!ctx) return;
    poolDetailsRevision++;
    if (button) button.textContent = 'Refreshing…';
    try {
      let result;
      try { result = await api.tunnels.refresh(id); }
      catch (err) {
        operationError(ctx, err, 'Catalog was not refreshed.');
        return;
      }
      if (!ctx.currentSession()) return;
      const tunnel = acceptPool(id, result);
      // Durable catalog state survives close; metadata belongs only to this modal.
      if (!ctx.current()) { refresh(); return; }
      const loaded = await loadPoolDetails(id);
      if (!ctx.currentSession()) return;
      if (ctx.current()) toast(loaded
        ? `${tunnel?.name || 'Pool'}: catalog refreshed`
        : 'Catalog refreshed, but server details could not be loaded. Reopen Details to retry.', loaded ? '' : 'err');
      refresh();
    } finally { finishOperation(ctx); }
  },

  'toggle-tunnel': async (id) => {
    const tunnel = tunnelById(id);
    if (!tunnel) return;
    const enabled = tunnel.enabled === false;
    const ctx = beginOperation(`tunnel:${id}`);
    if (!ctx) return;
    try {
      let result;
      try { result = await api.tunnels.update(id, { enabled }); }
      catch (err) { operationError(ctx, err, 'Tunnel was not changed.'); return; }
      if (!ctx.currentSession()) return;
      acceptTunnel(result, { id, enabled });
      markDirty();
      refresh();
    } finally { finishOperation(ctx); }
  },

  'delete-tunnel': async (id) => {
    const tunnel = tunnelById(id);
    if (!tunnel || !confirm(`Delete tunnel “${tunnel.name}”?`)) return;
    const ctx = beginOperation(`tunnel:${id}`);
    if (!ctx) return;
    try {
      try { await api.tunnels.remove(id); }
      catch (err) { operationError(ctx, err, 'Tunnel was not deleted.'); return; }
      if (!ctx.currentSession()) return;
      inventoryRevision++;
      state.tunnels = state.tunnels.filter((item) => item.id !== id);
      markDirty();
      if (ctx.current()) toast('Tunnel deleted');
      refresh();
    } finally { finishOperation(ctx); }
  },

  'refresh-pool': async (id, button) => {
    const ctx = beginOperation(`tunnel:${id}`, button);
    if (!ctx) return;
    if (button) button.textContent = 'Refreshing…';
    try {
      let result;
      try { result = await api.tunnels.refresh(id); }
      catch (err) {
        if (ctx.current() && err.missing) toast('Catalog refresh is unavailable: POST /api/tunnels/{id}/refresh is not implemented.');
        else operationError(ctx, err, 'Catalog was not refreshed.');
        return;
      }
      if (!ctx.currentSession()) return;
      const tunnel = acceptPool(id, result);
      const pool = tunnel?.pool || {};
      if (ctx.current()) toast(typeof pool.servers_alive === 'number' && pool.servers_total
        ? `${tunnel.name}: ${pool.servers_alive} alive of ${pool.servers_total}`
        : `${tunnel?.name || 'Pool'}: catalog refreshed`);
      refresh();
    } finally { finishOperation(ctx); }
  },

  'check-tunnel': async (id, button) => {
    const ctx = beginOperation(`tunnel:${id}`, button);
    if (!ctx) return;
    if (button) button.textContent = 'Checking…';
    try {
      let result;
      try { result = await api.tunnels.check(id); }
      catch (err) { operationError(ctx, err, 'Tunnel check failed.'); return; }
      if (!ctx.currentSession()) return;
      inventoryRevision++;
      const tunnel = tunnelById(id);
      if (tunnel) Object.assign(tunnel, {
        status: result?.status ?? tunnel.status,
        signals: result?.signals ?? tunnel.signals,
        latency_ms: result?.latency_ms ?? null,
        last_check: result?.last_check ?? new Date().toISOString(),
      });
      const ok = tunnel?.status === 'up' || tunnel?.status === 'slow';
      if (ctx.current()) toast(ok
        ? `${tunnel.name}: ${tunnel.latency_ms} ms${tunnel.status === 'slow' ? ' · slow' : ''}`
        : `${tunnel?.name || 'Tunnel'}: ${result?.detail || 'no response'}`, ok ? '' : 'err');
      refresh();
    } finally { finishOperation(ctx); }
  },
};
