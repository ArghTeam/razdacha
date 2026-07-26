/* Экран «Туннели». Добавление — одно поле: тип определяется по вставленному
   конфигу, руками его не выбирают. Разбор идёт на сервере
   (`POST /api/tunnels/parse`) — свой парсер в интерфейсе означал бы второй,
   расходящийся с настоящим. */

import * as api from '../api.js';
import {
  state, tunnelById, toast, toastError, openModal, closeModal, modalShell,
  openMenu, notImplemented, refresh, markDirty,
} from '../shell.js';
import { $, esc, plural, TUNNEL_LABEL, tunnelEndpoint } from '../util.js';

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

export function view() {
  if (state.missing.has('tunnels')) {
    return head() + `<div class="card">${notImplemented('Туннели')}</div>`;
  }

  const rows = state.tunnels.map((t) => {
    const used = state.rules.filter((r) => r.tunnel_id === t.id).length;
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
      <div class="row${t.enabled === false ? ' dim' : ''}">
        <span class="dot ${st.cls}"></span>
        <div class="row-main">
          <div class="row-title">${esc(t.name)}
            <span class="badge">${esc(TUNNEL_LABEL[t.type] || t.type)}</span>
            ${st.badge}
          </div>
          <div class="row-meta mono">${esc(tunnelEndpoint(t))}</div>
          <div class="row-meta">${used
            ? `используют ${used} ${plural(used, 'правило', 'правила', 'правил')}`
            : 'ни одно правило не ссылается'}${st.label ? ' · ' + st.label : ''}</div>
        </div>
        <div class="row-actions">
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

const IDLE_HINT = 'Вставьте ссылку vless://, ss://, trojan://, hysteria2://, socks5://, конфиг WireGuard или JSON outbound.';

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
