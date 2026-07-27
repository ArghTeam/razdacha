/* Экран «Клиенты». Главный: самая частая задача — добавить устройство. */

import * as api from '../api.js';
import * as qr from '../qr.js';
import {
  state, peerById, toast, toastError, openModal, modalShell,
  openMenu, notImplemented, notice, refresh, markDirty,
} from '../shell.js';
import { $, esc, bytes, since, isOnline, download } from '../util.js';

export const title = 'Клиенты';

/** Что нужно экрану. Настройки не обязательны: без них подписи скромнее. */
export async function load() {
  state.peers = await api.peers.list();
}

export function view() {
  if (state.missing.has('peers')) {
    return head() + `<div class="card">${notImplemented('Клиенты')}</div>`;
  }

  const online = state.peers.filter(isOnline).length;
  const rx = state.peers.reduce((a, p) => a + (p.rx_bytes || 0), 0);
  const tx = state.peers.reduce((a, p) => a + (p.tx_bytes || 0), 0);

  const rows = state.peers.map((p) => {
    const on = isOnline(p);
    const meta = p.enabled === false
      ? 'отключён'
      : on
        ? `↓ ${bytes(p.rx_bytes)} · ↑ ${bytes(p.tx_bytes)}${p.endpoint ? ' · ' + esc(p.endpoint) : ''}`
        : `был ${since(p.last_handshake)}`;
    return `
      <div class="row${p.enabled === false ? ' dim' : ''}">
        <span class="dot ${on ? 'on' : 'off'}" title="${on ? 'онлайн' : 'офлайн'}"></span>
        <div class="row-main">
          <div class="row-title">${esc(p.name)}
            <span class="mono" style="color:var(--fg-dim);font-weight:400">${esc(p.address)}</span>
          </div>
          <div class="row-meta">${meta}</div>
        </div>
        <div class="row-actions">
          <button class="btn btn-sm" data-act="peer-config" data-id="${esc(p.id)}">Конфиг</button>
          <div class="menu-wrap">
            <button class="btn btn-sm btn-ghost" data-act="menu-peer" data-id="${esc(p.id)}" aria-label="Ещё">⋮</button>
          </div>
        </div>
      </div>`;
  }).join('');

  return head() + `
    <div class="summary">
      <div class="stat"><div class="stat-label">Устройств</div><div class="stat-value">${state.peers.length}</div></div>
      <div class="stat"><div class="stat-label">Онлайн сейчас</div><div class="stat-value">${online}</div></div>
      <div class="stat"><div class="stat-label">Принято, всего</div><div class="stat-value">${bytes(rx)}</div></div>
      <div class="stat"><div class="stat-label">Отдано, всего</div><div class="stat-value">${bytes(tx)}</div></div>
    </div>
    <div class="card">${rows || '<div class="empty">Клиентов пока нет.</div>'}</div>
    <p class="screen-sub" style="margin-top:10px">
      Трафик считается накопительно с момента создания пира. Адрес, ключи, MTU и DNS
      выставляются сами — их нельзя задать вручную.
    </p>`;
}

const head = () => `
  <div class="screen-head">
    <h1>Клиенты</h1>
    <div class="spacer"></div>
    <button class="btn btn-primary" data-act="add-peer">+ Добавить</button>
  </div>`;

/* --- новый клиент: спрашиваем только имя ---------------------------------- */

function modalAddPeer() {
  const s = state.settings;
  const hint = s
    ? `Адрес из пула ${esc(s.wg_pool)}, ключи, MTU ${esc(s.client_mtu)} и DNS ${esc(s.wg_server_address)} выставятся сами.`
    : 'Адрес, ключи, MTU и DNS выставятся сами.';
  openModal(modalShell('Новый клиент', `
    <div class="field">
      <label for="peer-name">Имя устройства</label>
      <input type="text" id="peer-name" placeholder="iPad Кати" autocomplete="off">
      <div class="hint">${hint}</div>
    </div>`,
  `<button class="btn" data-act="close-modal">Отмена</button>
     <button class="btn btn-primary" data-act="create-peer">Создать</button>`),
  (m) => {
    const input = m.querySelector('#peer-name');
    input.focus();
    input.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') createPeer(); });
  });
}

async function createPeer() {
  const name = $('#peer-name').value.trim();
  if (!name) { toast('Введите имя устройства', 'err'); return; }
  const btn = $('#modal [data-act="create-peer"]');
  btn.disabled = true;
  try {
    const peer = await api.peers.create(name);
    state.peers.push(peer);
    // Сразу QR, без промежуточного шага: ради него кнопку и нажимали.
    await modalPeerConfig(peer.id);
    markDirty();
    refresh();
  } catch (err) {
    btn.disabled = false;
    toastError(err, 'Клиент не создан');
  }
}

/* --- конфиг клиента: QR + скачивание -------------------------------------- */

async function modalPeerConfig(id) {
  const peer = peerById(id) || { id, name: 'Клиент' };
  openModal(modalShell(peer.name,
    '<div class="loading">Готовлю конфиг…</div>',
    '<button class="btn" data-act="close-modal">Закрыть</button>'));

  let conf, filename;
  try {
    // Конфиг собирает демон: публичного ключа сервера нет ни в одной сущности
    // API, и построить .conf на клиенте нечем. Имя файла тоже его: из него
    // WireGuard берёт имя туннеля, и правила длины и алфавита знает только
    // демон.
    ({ text: conf, filename } = await api.peers.config(id));
  } catch (err) {
    const text = err.missing
      ? 'Демон ещё не отдаёт клиентский конфиг — эндпоинт GET /api/peers/{id}/config не реализован.'
      : err.message;
    openModal(modalShell(peer.name,
      notice('Конфиг недоступен', text, 'err'),
      '<button class="btn" data-act="close-modal">Закрыть</button>'));
    return;
  }

  const sub = state.settings
    ? `Адрес ${esc(peer.address || '')} · MTU ${esc(state.settings.client_mtu)} · DNS ${esc(state.settings.wg_server_address)}`
    : esc(peer.address || '');

  openModal(modalShell(peer.name, `
    <div class="qr-wrap">
      <div class="qr-box"><canvas id="qr"></canvas></div>
      <div class="hint" style="text-align:center;color:var(--fg-dim);font-size:13px">
        Отсканируйте приложением WireGuard или скачайте файл.<br>${sub}
      </div>
    </div>
    <details>
      <summary style="cursor:pointer;color:var(--fg-dim);font-size:13px">Показать текст конфига</summary>
      <pre class="pane" style="margin-top:8px">${esc(conf)}</pre>
    </details>`,
  `<button class="btn" data-act="close-modal">Закрыть</button>
     <button class="btn btn-primary" data-act="download-conf" data-id="${esc(id)}">Скачать .conf</button>`),
  (m) => {
    m.dataset.conf = conf;
    m.dataset.confName = filename;
    try {
      qr.draw(m.querySelector('#qr'), conf);
    } catch (e) {
      m.querySelector('.qr-box').innerHTML =
        `<div style="color:#c33;padding:20px">QR не построен: ${esc(e.message)}</div>`;
    }
  });
}

export const actions = {
  'add-peer': modalAddPeer,
  'create-peer': createPeer,
  'peer-config': (id) => modalPeerConfig(id),

  'download-conf': () => {
    const m = $('#modal');
    // Имя строит демон и присылает в Content-Disposition: из имени файла
    // WireGuard берёт имя туннеля, и второе имя на клиенте расходилось бы с
    // тем, что отдаёт эндпоинт.
    download(m.dataset.confName || 'peer.conf', m.dataset.conf || '');
    toast('Файл конфига скачан');
  },

  'menu-peer': (id, btn) => {
    const p = peerById(id);
    openMenu(btn, [
      { act: 'rename-peer', id, label: 'Переименовать' },
      { act: 'toggle-peer', id, label: p.enabled === false ? 'Включить' : 'Отключить' },
      { act: 'delete-peer', id, label: 'Удалить', danger: true },
    ]);
  },

  'rename-peer': async (id) => {
    const p = peerById(id);
    const name = prompt('Новое имя', p.name);
    if (!name || !name.trim() || name.trim() === p.name) return;
    try {
      await api.peers.update(id, { name: name.trim() });
      p.name = name.trim();
      refresh();
      toast('Переименовано');
    } catch (err) { toastError(err, 'Не переименовано'); }
  },

  'toggle-peer': async (id) => {
    const p = peerById(id);
    const enabled = p.enabled === false;
    try {
      await api.peers.update(id, { enabled });
      p.enabled = enabled;
      markDirty();
      refresh();
    } catch (err) { toastError(err, 'Не переключено'); }
  },

  'delete-peer': async (id) => {
    const p = peerById(id);
    if (!confirm(`Удалить «${p.name}»? Устройство потеряет доступ.`)) return;
    try {
      await api.peers.remove(id);
      state.peers = state.peers.filter((x) => x.id !== id);
      markDirty();
      refresh();
      toast('Клиент удалён');
    } catch (err) { toastError(err, 'Не удалён'); }
  },
};
