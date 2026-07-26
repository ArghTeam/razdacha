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
} from '../util.js';

export const title = 'Туннели';

export async function load() {
  state.tunnels = await api.tunnels.list();
}

const head = () => `
  <div class="screen-head">
    <h1>Туннели</h1>
    <div class="spacer"></div>
    <button class="btn btn-primary" data-act="add-tunnel">+ Добавить</button>
  </div>`;

/* --- пул ------------------------------------------------------------------
   Пул — тот же айтем списка, не отдельный экран и не отдельная сущность:
   правило навешивается на него штатно (спека tasks/63-pool-tunnel-card.md).
   Отличается он выделением строки и своим набором данных: каталог вместо
   адреса, сколько серверов живо, какой выбран сейчас, когда обновлялся.

   Выключенный пул не показывает живых серверов и текущий выбор: цифры от
   выключенного туннеля — ложь, а не ноль. */

/** Сколько серверов в каталоге и сколько из них живы. */
function poolServers(pool, off) {
  const total = Number(pool.servers_total) || 0;
  if (!total) return '';
  // Живых не знаем (поля нет) или знать нечего (пул выключен) — говорим только
  // про размер каталога, а не подставляем ноль.
  if (off || typeof pool.servers_alive !== 'number') {
    return `<span class="badge">${total} ${plural(total, 'сервер', 'сервера', 'серверов')} в каталоге</span>`;
  }
  const alive = pool.servers_alive;
  return `<span class="badge ${alive ? 'ok' : 'err'}">живых ${alive} из ${total}</span>`;
}

/** Сервер, через который пул идёт прямо сейчас.
    Страна показывается всегда, даже если каталог уже вписал её в имя: по названию
    вроде «VLESS (Germany) 11293» это угадывается, а по «11293» — нет. */
function poolCurrent(pool, off) {
  const cur = pool.current || {};
  if (off || !cur.name) return '';
  const parts = [];
  if (cur.country) parts.push(String(cur.country));
  parts.push(String(cur.name));
  if (cur.latency_ms != null) parts.push(`${cur.latency_ms} мс`);
  return `<div class="row-meta pool-now">Сейчас: ${esc(parts.join(' · '))}</div>`;
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

export function view() {
  if (state.missing.has('tunnels')) {
    return head() + `<div class="card">${notImplemented('Туннели')}</div>`;
  }

  const rows = state.tunnels.map((t) => {
    const used = state.rules.filter((r) => r.tunnel_id === t.id).length;
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
  }).join('');

  return head() + `
    <div class="card">${rows || '<div class="empty">Туннелей пока нет.</div>'}</div>
    <p class="screen-sub" style="margin-top:10px">
      Тип определяется по вставленному конфигу — выбирать его руками не нужно.
      Исходящие WireGuard-туннели поднимаются в userspace внутри sing-box.
    </p>`;
}

/* --- форма туннеля -------------------------------------------------------- */

const IDLE_HINT = 'Вставьте ссылку vless://, ss://, trojan://, hysteria2://, socks5://, '
  + 'конфиг WireGuard, JSON outbound — или ссылку https:// на каталог ключей, '
  + 'тогда получится пул с ротацией.';

function modalTunnel(id) {
  const t = id ? tunnelById(id) : null;
  openModal(modalShell(t ? t.name : 'Новый туннель', `
    <div class="field">
      <label for="t-name">Имя</label>
      <input type="text" id="t-name" value="${esc(t ? t.name : '')}" placeholder="Нидерланды" autocomplete="off">
    </div>
    <div class="field">
      <label for="t-raw">Вставьте ссылку или конфиг WireGuard</label>
      <textarea id="t-raw" spellcheck="false" placeholder="vless://…">${esc(t ? t.raw : '')}</textarea>
      <div class="parse-result idle" id="t-parse">${esc(IDLE_HINT)}</div>
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
    if (t) runParse(raw.value);
    (t ? raw : m.querySelector('#t-name')).focus();
  });
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
  const raw = $('#t-raw').value.trim();
  if (!name) { toast('Введите имя туннеля', 'err'); return; }
  if (!raw) { toast('Вставьте конфиг туннеля', 'err'); return; }
  if (lastParse.raw === raw && !lastParse.ok) {
    toast('Конфиг не разобран — исправьте его перед сохранением', 'err');
    return;
  }
  const btn = $('#modal [data-act="save-tunnel"]');
  btn.disabled = true;
  try {
    if (id) await api.tunnels.update(id, { name, raw });
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
  'edit-tunnel': (id) => modalTunnel(id),
  'save-tunnel': (id) => saveTunnel(id || null),

  'menu-tunnel': (id, btn) => {
    const t = tunnelById(id);
    openMenu(btn, [
      { act: 'edit-tunnel', id, label: 'Изменить' },
      { act: 'toggle-tunnel', id, label: t.enabled === false ? 'Включить' : 'Выключить' },
      { act: 'delete-tunnel', id, label: 'Удалить', danger: true },
    ]);
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
