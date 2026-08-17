/* Экран «Туннели». Добавление — одно поле: тип определяется по вставленному
   конфигу, руками его не выбирают. Разбор идёт на сервере
   (`POST /api/tunnels/parse`) — свой парсер в интерфейсе означал бы второй,
   расходящийся с настоящим. */

import * as api from '../api.js';
import {
  state, tunnelById, toast, toastError, openModal, closeModal, modalShell,
  openMenu, notImplemented, refresh, markDirty,
} from '../shell.js';
import {
  $, esc, plural, since, until, stamp, TUNNEL_LABEL, tunnelLabel, tunnelEndpoint, tunnelPool,
  isWarp,
} from '../util.js';

export const title = 'Туннели';

export async function load() {
  state.tunnels = await api.tunnels.list();
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

/** Страна сервера пула — подпись карточки каталога, не измерение и не сверка:
    проверка нашла расхождение на одном ключе из пяти (issue #161). Метка
    держится в одном месте и переиспользуется везде, где показана страна пула,
    чтобы её нельзя было принять за проверенный факт. */
function poolCountryLabel(country) {
  return `<span class="pool-country" title="Страна — со слов каталога, мы её не проверяли">${
    esc(country)}</span>`;
}

/** Сервер, через который пул идёт прямо сейчас.
    Страна показывается всегда, даже если каталог уже вписал её в имя: по названию
    вроде «VLESS (Germany) 11293» это угадывается, а по «11293» — нет. */
function poolCurrent(pool, off) {
  const cur = pool.current || {};
  if (off || !cur.name) return '';
  const parts = [];
  if (cur.country) parts.push(poolCountryLabel(cur.country));
  parts.push(esc(String(cur.name)));
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

/** Один сервер строкой: страна · имя · задержка. */
function poolServerRow(s, { current } = {}) {
  const parts = [];
  if (s.country) parts.push(poolCountryLabel(s.country));
  parts.push(`<span class="pool-srv-name">${esc(s.title || '—')}</span>`);

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

/** Страны с количеством: флагов нет, в каталоге есть только название страны,
    а угадывать эмодзи по названию — выдумывать данные. */
function poolCountries(list) {
  const byCountry = new Map();
  for (const s of list) {
    const key = s.country || 'без страны';
    byCountry.set(key, (byCountry.get(key) || 0) + 1);
  }
  return [...byCountry.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0], 'ru'))
    .map(([country, n]) => {
      // Тот же смысл, что у страны в строке сервера и в «Сейчас:» — подпись
      // каталога, не факт, — обязан выглядеть так же заметно: пунктирная
      // рамка вместо сплошной, как уже отличает «остывший» чип списка
      // (`.chip.list-cold`), плюс тот же `title`, что и там. Бакету «без
      // страны» подсказку не даём — атрибутировать источнику нечего.
      if (country === 'без страны') {
        return `<span class="chip">${esc(country)} — ${n}</span>`;
      }
      return `<span class="chip pool-country" title="Страна — со слов каталога, мы её не проверяли">${
        esc(country)} — ${n}</span>`;
    })
    .join('');
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

function poolDetailsHTML(t, data) {
  const servers = data.servers || [];
  if (!servers.length) {
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
     <div class="chips">${poolCountries(rest)}</div>
     <div class="pool-list">${rest.map((s) => poolServerRow(s)).join('')}</div>`)}`;
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

async function loadPoolDetails(id) {
  const box = $('#pool-details');
  if (!box) return;
  const t = tunnelById(id);
  try {
    const data = await api.tunnels.poolServers(id);
    box.innerHTML = poolDetailsHTML(t, data);
  } catch (err) {
    box.innerHTML = `<div class="parse-result ${err.missing ? 'idle' : 'err'}"></div>`;
    box.firstElementChild.textContent = err.missing
      ? 'Список серверов появится позже: GET /api/tunnels/{id}/pool ещё не реализован'
      : err.message;
  }
}

/* Страновой пул узнаётся по паре «встроенный + пул»: имя ему завёл демон
   (флаг + страна, `store.Country.PoolName`), и он идёт в раздел дефолтных, а не
   к пользовательским туннелям. Признак — флаг `builtin`, а не разбор имени:
   имя пользователь переименовывает, а `builtin` демон ставит раз при заведении. */
const isCountryPool = (t) => Boolean(t.builtin) && Boolean(tunnelPool(t));

/** Одна строка списка туннелей — общая для дефолтных пулов и своих туннелей. */
function tunnelRow(t) {
  // Правило считается использующим туннель обоими звеньями цепи: вторым
  // звеном туннель держится так же, и удалить его сервер не даст (ADR 0012).
  const used = state.rules.filter((r) => r.tunnel_id === t.id || r.via_tunnel_id === t.id).length;
  const pool = tunnelPool(t);
  const off = t.enabled === false;
    const st = t.enabled === false
      ? { cls: 'off', badge: '', label: 'выключен' }
      : t.status === 'up'
        ? { cls: 'on', badge: `<span class="badge ok">${esc(t.latency_ms)} мс</span>`, label: '' }
        // Медленный туннель рабочий, но не годится для видео и звонков —
        // показываем цифру и отличаем цветом, а не прячем среди зелёных.
        : t.status === 'slow'
          ? { cls: 'warn', badge: `<span class="badge warn">${esc(t.latency_ms)} мс, медленно</span>`, label: '' }
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
            ${st.badge}
          </div>
          <div class="row-meta mono">${esc(tunnelEndpoint(t))}</div>
          ${pool ? poolCurrent(pool, off) : ''}
          <div class="row-meta">${used
            ? `используют ${used} ${plural(used, 'правило', 'правила', 'правил')}`
            : 'ни одно правило не ссылается'}${st.label ? ' · ' + st.label : ''}${
            pool ? poolSchedule(pool, off) : ''}</div>
        </div>
        <div class="row-actions">
          ${pool ? `<button class="btn btn-sm" data-act="refresh-pool" data-id="${esc(t.id)}">Обновить</button>` : ''}
          <button class="btn btn-sm" data-act="check-tunnel" data-id="${esc(t.id)}">Проверить</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-tunnel" data-id="${esc(t.id)}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
}

export function view() {
  if (state.missing.has('tunnels')) {
    return head() + `<div class="card">${notImplemented('Туннели')}</div>`;
  }

  // Дефолтные страновые пулы — сверху отдельным разделом: их завёл сервер, выбор
  // выхода по стране это выбор одного из них. Свои туннели идут ниже своим
  // разделом. Порядок пулов задаёт сервер (набор стран, ADR 0017) — панель его не
  // пересобирает.
  const pools = state.tunnels.filter(isCountryPool);
  const rest = state.tunnels.filter((t) => !isCountryPool(t));

  const poolsCard = pools.length
    ? `<h2 class="screen-section">Дефолтные туннели по странам</h2>
       <p class="screen-sub">Пул на страну: сервер сам держится за живой ключ и
         переключается без перезапуска. Выключены, пока не включите — до этого за
         ключами наружу никто не ходит.</p>
       <div class="card">${pools.map(tunnelRow).join('')}</div>`
    : '';

  const restEmpty = pools.length
    ? '<div class="empty">Своих туннелей пока нет — добавьте кнопкой «+ Добавить».</div>'
    : '<div class="empty">Туннелей пока нет.</div>';
  const restCard = `${pools.length ? '<h2 class="screen-section">Свои туннели</h2>' : ''}
    <div class="card">${rest.map(tunnelRow).join('') || restEmpty}</div>`;

  return head() + poolsCard + restCard + `
    <p class="screen-sub" style="margin-top:10px">
      Тип определяется по вставленному конфигу — выбирать его руками не нужно.
      Исходящие WireGuard-туннели поднимаются в userspace внутри sing-box.
    </p>`;
}

/* --- форма туннеля -------------------------------------------------------- */

/* Ссылки https:// на каталог ключей в подсказке нет намеренно: пулы заводит
   демон — по одному на страну (ADR 0017), руками их не создают, и такой ввод
   получает отказ (#71). Предлагать то, что не сработает, — обещать несуществующее. */
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
    let timer = null;
    // Разбор по мере ввода, но не на каждую букву: это сетевой запрос.
    raw.addEventListener('input', () => {
      clearTimeout(timer);
      timer = setTimeout(() => runParse(raw.value), 350);
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
  const raw = m.querySelector('#t-raw');
  const out = m.querySelector('#t-parse');
  try {
    const got = await api.tunnels.raw(t.id);
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

async function runParse(raw) {
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
  const name = $('#t-name').value.trim();
  // Поля конфига нет вовсе у туннеля, чей ключ выдал сервер (WARP): у него
  // меняется только имя, и слать `raw` в PATCH значило бы стереть ключ.
  const field = $('#t-raw');
  const raw = field ? field.value.trim() : '';
  if (!name) { toast('Введите имя туннеля', 'err'); return; }
  if (field && field.disabled) { toast('Конфиг ещё загружается', 'err'); return; }
  if (field && !raw) { toast('Вставьте конфиг туннеля', 'err'); return; }
  if (field && lastParse.raw === raw && !lastParse.ok) {
    toast('Конфиг не разобран — исправьте его перед сохранением', 'err');
    return;
  }
  const btn = $('#modal [data-act="save-tunnel"]');
  btn.disabled = true;
  try {
    if (id) await api.tunnels.update(id, field ? { name, raw } : { name });
    else await api.tunnels.create(name, raw);
    closeModal();
    markDirty();
    state.tunnels = await api.tunnels.list();
    refresh();
    toast(`Туннель «${name}» сохранён`);
  } catch (err) {
    btn.disabled = false;
    toastError(err, 'Туннель не сохранён');
  }
}

export const actions = {
  'add-tunnel': () => modalTunnel(null),

  /* Добавить WARP. Ключи демон берёт у Cloudflare сам — это единственное
     действие панели, за которым сервер идёт наружу, и происходит оно только по
     этому нажатию.

     Кнопка блокируется на время запроса: регистрация занимает секунды, и второе
     нажатие означало бы второе устройство у Cloudflare. Сервер от этого защищён
     и сам (409), но ждать отказа, чтобы узнать о собственном двойном клике, —
     плохой способ об этом сообщить. */
  'add-warp': async (_id, btn) => {
    btn.disabled = true;
    btn.textContent = 'Регистрирую…';
    try {
      const created = await api.tunnels.addWarp();
      markDirty();
      state.tunnels = await api.tunnels.list();
      refresh();
      toast(`Туннель «${created?.name || 'WARP'}» добавлен`);
    } catch (err) {
      btn.disabled = false;
      btn.textContent = '+ WARP';
      if (err.missing) {
        toast('Добавление WARP появится позже: POST /api/tunnels/warp ещё не реализован');
        return;
      }
      // Текст приходит с сервера: «не удалось связаться с Cloudflare» и
      // «Cloudflare отказал» — разные причины с разными действиями.
      toastError(err, 'WARP не добавлен');
    }
  },
  'edit-tunnel': (id) => modalTunnel(id),
  'save-tunnel': (id) => saveTunnel(id || null),

  /* Меню строки. Наборов ровно два.

     У пула — «Детали» (состав серверов вместо формы конфига) и включение.
     «Изменить» нет: править нечего, кроме каталога. «Удалить» нет: страновые пулы
     заводит демон (ADR 0017), и API удалить встроенный не даёт — прятать кнопку,
     оставляя разрешение в API, значит расходиться с самим собой.

     Набор меню выбирается по тому, что это пул, а не по флагу builtin: у пула
     править нечего, кроме каталога, поэтому «Изменить» и «Удалить» не показываем
     ни у странового встроенного, ни у редкого пула из ручной правки БД. */
  'menu-tunnel': (id, btn) => {
    const t = tunnelById(id);
    const toggle = { act: 'toggle-tunnel', id, label: t.enabled === false ? 'Включить' : 'Выключить' };
    openMenu(btn, tunnelPool(t)
      ? [{ act: 'pool-details', id, label: 'Детали' }, toggle]
      : [
        { act: 'edit-tunnel', id, label: 'Изменить' },
        toggle,
        { act: 'delete-tunnel', id, label: 'Удалить', danger: true },
      ]);
  },

  'pool-details': (id) => modalPoolDetails(id),

  /* Та же ручка, что у кнопки строки, но обновляется открытая модалка: закрывать
     её ради перечитанного каталога незачем. */
  'refresh-pool-details': async (id, btn) => {
    const t = tunnelById(id);
    btn.disabled = true;
    btn.textContent = 'Обновляю…';
    try {
      const res = await api.tunnels.refresh(id);
      if (res && res.pool) Object.assign(t, res);
      refresh();
      await loadPoolDetails(id);
      toast(`${t.name}: каталог обновлён`);
    } catch (err) {
      if (err.missing) toast('Обновление каталога появится позже: '
        + 'POST /api/tunnels/{id}/refresh ещё не реализован');
      else toastError(err, 'Каталог не обновлён');
    } finally {
      btn.disabled = false;
      btn.textContent = 'Обновить каталог';
    }
  },

  'toggle-tunnel': async (id) => {
    const t = tunnelById(id);
    const enabled = t.enabled === false;
    try {
      await api.tunnels.update(id, { enabled });
      t.enabled = enabled;
      markDirty();
      refresh();
    } catch (err) { toastError(err, 'Не переключено'); }
  },

  'delete-tunnel': async (id) => {
    const t = tunnelById(id);
    if (!confirm(`Удалить туннель «${t.name}»?`)) return;
    try {
      await api.tunnels.remove(id);
      state.tunnels = state.tunnels.filter((x) => x.id !== id);
      markDirty();
      refresh();
      toast('Туннель удалён');
    } catch (err) {
      // 409 — на туннель ссылается правило; текст приходит с сервера.
      toastError(err, 'Туннель не удалён');
    }
  },

  /* Перечитать каталог пула. Ручки ещё нет: 404 означает «не написали», и это
     говорится словами — так же, как предпросмотр разбора в форме туннеля. */
  'refresh-pool': async (id, btn) => {
    const t = tunnelById(id);
    btn.disabled = true;
    btn.textContent = 'Обновляю…';
    try {
      const res = await api.tunnels.refresh(id);
      // Ручка может вернуть весь туннель или только блок пула — принимаем оба.
      if (res && res.pool) Object.assign(t, res);
      else if (res) t.pool = { ...(t.pool || {}), ...res };
      refresh();
      const p = t.pool || {};
      toast(typeof p.servers_alive === 'number' && p.servers_total
        ? `${t.name}: живых ${p.servers_alive} из ${p.servers_total}`
        : `${t.name}: каталог обновлён`);
    } catch (err) {
      btn.disabled = false;
      btn.textContent = 'Обновить';
      if (err.missing) {
        toast('Обновление каталога появится позже: POST /api/tunnels/{id}/refresh ещё не реализован');
        return;
      }
      toastError(err, 'Каталог не обновлён');
    }
  },

  'check-tunnel': async (id, btn) => {
    const t = tunnelById(id);
    btn.disabled = true;
    btn.textContent = 'Проверяю…';
    try {
      const res = await api.tunnels.check(id);
      Object.assign(t, {
        status: res?.status ?? t.status,
        latency_ms: res?.latency_ms ?? null,
        last_check: res?.last_check ?? new Date().toISOString(),
      });
      refresh();
      const ok = t.status === 'up' || t.status === 'slow';
      toast(ok ? `${t.name}: ${t.latency_ms} мс${t.status === 'slow' ? ' — медленно' : ''}`
        : `${t.name}: ${res?.detail || 'нет ответа'}`, ok ? '' : 'err');
    } catch (err) {
      btn.disabled = false;
      btn.textContent = 'Проверить';
      toastError(err, 'Проверка не выполнена');
    }
  },
};
