/* Экран «Клиенты». Главный: самая частая задача — добавить устройство. */

import * as api from '../api.js';
import * as qr from '../qr.js';
import {
  state, peerById, tunnelById, closeModal, toast, toastError, openModal, modalShell,
  currentModalRevision, isCurrentModal, onModalCleanup,
  openMenu, notImplemented, notice, refresh, markDirty,
} from '../shell.js';
import { $, esc, bytes, isOnline, download } from '../util.js';

export const title = 'Клиенты';

/** Что нужно экрану. Настройки не обязательны: без них подписи скромнее. */
export async function load() {
  state.peers = await api.peers.list();
  try {
    state.tunnels = await api.tunnels.list();
    state.missing.delete('peer-tunnels');
  } catch (err) {
    if (err.status === 401) throw err;
    state.missing.add('peer-tunnels');
  }
}

export function view() {
  if (state.missing.has('peers')) {
    return head() + `<div class="card">${notImplemented('Клиенты')}</div>`;
  }

  const online = state.peers.filter(isOnline).length;
  const selected = state.peers.filter((p) => p.egress_tunnel_id).length;
  const rows = state.peers.map((p) => {
    const on = isOnline(p);
    const activity = p.enabled === false ? 'Disabled' : on ? 'Connected' : p.last_handshake && Number.isFinite(Date.parse(p.last_handshake)) ? `Last handshake ${new Date(p.last_handshake).toLocaleString('en-GB')}` : 'No connection yet';
    return `
      <article class="row client-row${p.enabled === false ? ' dim' : ''}" aria-label="Client ${esc(p.name)}">
        <div class="client-identity">
          <div class="client-name"><span class="dot ${on ? 'on' : 'off'}" aria-hidden="true"></span><h2>${esc(p.name)}</h2></div>
          <div class="mono row-meta">${esc(p.address)}</div>
          <div class="row-meta">${activity}</div>
          <div class="row-meta client-traffic">↓ ${bytes(p.rx_bytes)} · ↑ ${bytes(p.tx_bytes)}</div>
        </div>
        <div class="client-route"><div class="client-label">Configured route</div>${routingSummary(p)}</div>
        <div class="row-actions client-actions">
          <button class="btn btn-sm client-routing-button" data-act="peer-routing" data-id="${esc(p.id)}" aria-label="Routing for client ${esc(p.name)}">Routing</button>
          <button class="btn btn-sm" data-act="peer-config" data-id="${esc(p.id)}" aria-label="Configuration for client ${esc(p.name)}">Configuration</button>
          <button class="btn btn-sm btn-ghost" data-act="menu-peer" data-id="${esc(p.id)}" aria-label="Actions for client ${esc(p.name)}" aria-haspopup="menu" aria-expanded="false">⋮</button>
        </div>
      </article>`;
  }).join('');

  return head() + `
    <div class="client-overview" aria-label="Client inventory summary">
      <span><strong>${state.peers.length}</strong> clients</span>
      <span><strong>${online}</strong> connected</span>
      <span><strong>${selected}</strong> selected exit</span>
      <span><strong>${state.peers.length - selected}</strong> shared rules</span>
    </div>
    <div class="client-apply-note"><strong>Save → Apply</strong>
      <span>Saved configuration is shown below. Apply activates changes.${state.dirty ? ' Unapplied changes exist.' : ''}</span>
    </div>
    <div class="card client-inventory">${rows || '<div class="empty"><strong>No clients yet</strong><p>Add a device to create its WireGuard configuration, then choose its route.</p><button class="btn" data-act="add-peer">Add client</button></div>'}</div>
    <p class="screen-sub" style="margin-top:10px">Traffic counters are measured since the gateway interface started.</p>`;
}

const head = () => `
  <div class="screen-head">
    <div><h1>Clients</h1><p class="screen-sub client-subtitle">Choose where each device's Internet traffic goes.</p></div>
    <div class="spacer"></div>
    <button class="btn btn-primary" data-act="add-peer">+ Add client</button>
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
  const currentSession = api.session.guard();
  const revision = currentModalRevision();
  const name = $('#peer-name').value.trim();
  if (!name) { toast('Введите имя устройства', 'err'); return; }
  const btn = $('#modal [data-act="create-peer"]');
  btn.disabled = true;
  try {
    const peer = await api.peers.create(name);
    if (!currentSession()) return;
    state.peers.push(peer);
    markDirty();
    refresh();
    if (!isCurrentModal(revision)) return;
    // Сразу QR, без промежуточного шага: ради него кнопку и нажимали.
    await modalPeerConfig(peer.id);
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    btn.disabled = false;
    toastError(err, 'Клиент не создан');
  }
}

/* --- конфиг клиента: QR + скачивание -------------------------------------- */

async function modalPeerConfig(id) {
  const peer = peerById(id) || { id, name: 'Клиент' };
  const revision = openModal(modalShell(peer.name,
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
    if (!isCurrentModal(revision)) return;
    const text = err.missing
      ? 'Демон ещё не отдаёт клиентский конфиг — эндпоинт GET /api/peers/{id}/config не реализован.'
      : err.message;
    openModal(modalShell(peer.name,
      notice('Конфиг недоступен', text, 'err'),
      '<button class="btn" data-act="close-modal">Закрыть</button>'));
    return;
  }

  if (!isCurrentModal(revision)) return;

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
  'peer-routing': modalPeerRouting,
  'save-peer-routing': savePeerRouting,

  'download-conf': () => {
    const m = $('#modal');
    // Имя строит демон и присылает в Content-Disposition: из имени файла
    // WireGuard берёт имя туннеля, и второе имя на клиенте расходилось бы с
    // тем, что отдаёт эндпоинт.
    if (!m.dataset.conf) return;
    onModalCleanup(download(m.dataset.confName || 'peer.conf', m.dataset.conf));
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
    let rules;
    try { rules = await api.rules.list(); }
    catch (err) { toastError(err, 'Cannot inspect affected rules'); return; }
    const affected = rules.filter((r) => r.peer_scope === 'selected' && (r.peer_ids || []).includes(id));
    const orphaned = affected.filter((r) => !(r.peer_ids || []).some((peer) => peer !== id && state.peers.some((p) => p.id === peer)));
    const detail = affected.length ? `\nAffected rules: ${affected.map((r) => r.name).join(', ')}.` : '';
    const inactive = orphaned.length ? `\nInactive after deletion: ${orphaned.map((r) => r.name).join(', ')}. Reassign clients and enable these rules explicitly.` : '';
    if (!confirm(`Delete "${p.name}"? The device will lose access.${detail}${inactive}\nRule changes require Apply.`)) return;
    try {
      const effects = await api.peers.remove(id);
      state.peers = state.peers.filter((x) => x.id !== id);
      markDirty();
      refresh();
      try { state.rules = await api.rules.list(); }
      catch (err) { state.rules = []; toastError(err, 'Device deleted; reload rules before editing'); }
      toast((effects.inactivated_rule_ids || []).length
        ? 'Device deleted. Orphaned rules require reassignment and explicit activation.'
        : 'Device deleted');
    } catch (err) { toastError(err, 'Не удалён'); }
  },
};

function routingSummary(peer) {
  if (!peer.egress_tunnel_id) return '<div class="client-route-name">Shared rules</div><div class="row-meta">Follows shared resource rules; unmatched Internet traffic goes direct.</div>';
  if (state.missing.has('tunnels') || state.missing.has('peer-tunnels')) return '<div class="client-route-name">Selected exit</div><div class="row-meta">Tunnel details unavailable. Open Routing to retry.</div>';
  const tunnel = tunnelById(peer.egress_tunnel_id);
  const unavailable = !tunnel || !tunnel.enabled;
  return `<div class="client-route-name selected-exit">Internet → ${esc(tunnel ? tunnel.name : 'Missing tunnel')}</div>
    <div class="row-meta${unavailable ? ' client-route-blocked' : ''}">${unavailable ? 'Blocked target · no direct fallback' : 'IPv4 TCP/UDP + DNS · no direct fallback'}</div>`;
}

async function modalPeerRouting(id) {
  const peer = peerById(id);
  if (!peer) return;
  const currentSession = api.session.guard();
  const revision = openModal(modalShell(`Routing: ${peer.name}`, '<div class="loading">Loading tunnels…</div>', '<button class="btn" data-act="close-modal">Close</button>'));
  try {
    const tunnels = await api.tunnels.list();
    if (!currentSession() || !isCurrentModal(revision)) return;
    state.tunnels = tunnels;
    state.missing.delete('tunnels');
    state.missing.delete('peer-tunnels');
    const current = peer.egress_tunnel_id;
    const options = tunnels.map((t) => `<option value="${esc(t.id)}"${t.id === current ? ' selected' : ''}>${esc(t.name)}${t.enabled ? '' : ' (disabled — traffic blocked)'}</option>`).join('');
    const missing = current && !tunnelById(current) ? `<option value="${esc(current)}" selected>Missing tunnel — traffic blocked</option>` : '';
    openModal(modalShell(`Routing: ${peer.name}`, `
      <fieldset class="routing-choices"><legend>Internet routing</legend>
        <label class="routing-choice"><input type="radio" name="peer-routing-mode" value="shared"${current ? '' : ' checked'}>
          <span><strong>Shared rules</strong><span>Follow shared resource rules, including their tunnel, direct or block actions. Unmatched traffic goes direct.</span></span></label>
        <label class="routing-choice"><input type="radio" name="peer-routing-mode" value="tunnel"${current ? ' checked' : ''}>
          <span><strong>Whole Internet through a tunnel</strong><span>Send this client's supported Internet traffic and DNS through one exit, overriding shared rules.</span></span></label>
      </fieldset>
      <div class="field" id="peer-routing-target-field"><label for="peer-routing-target">Outgoing tunnel</label>
        <select id="peer-routing-target" aria-describedby="peer-routing-target-status"><option value="">Select a tunnel</option>${missing}${options}</select>
        <div class="hint" id="peer-routing-target-status" role="status"></div></div>
      <div class="routing-scope" id="peer-routing-scope"><strong>Whole Internet mode supports</strong><p>IPv4 TCP/UDP and DNS. IPv6, ping, other IP protocols and private-network forwarding are blocked. Gateway dashboard access remains available.</p></div>
      <p class="routing-save-note">Save changes configuration. <strong>Then Apply to activate it.</strong></p>
      <div id="peer-routing-error" class="gate-error" role="alert" hidden></div>`,
      '<button class="btn" data-act="close-modal">Cancel</button><button class="btn btn-primary" data-act="save-peer-routing">Save routing</button>'), (modal) => {
        modal.dataset.peerId = id;
        const field = modal.querySelector('#peer-routing-target-field');
        const target = modal.querySelector('#peer-routing-target');
        const status = modal.querySelector('#peer-routing-target-status');
        const sync = () => {
          const mode = modal.querySelector('[name="peer-routing-mode"]:checked').value;
          field.hidden = mode !== 'tunnel';
          modal.querySelector('#peer-routing-scope').hidden = mode !== 'tunnel';
          target.disabled = mode !== 'tunnel' || !tunnels.length && !missing;
          const selected = tunnels.find((t) => t.id === target.value);
          status.textContent = !tunnels.length && !missing ? 'No tunnels available. Add a tunnel on the Tunnels screen first.' : !target.value ? 'Choose the exit for this client.' : !selected || !selected.enabled ? 'This target is missing or disabled. Traffic will be blocked after Apply.' : 'No direct fallback if this exit becomes unavailable.';
          status.classList.toggle('client-route-blocked', Boolean(target.value && (!selected || !selected.enabled)));
          modal.querySelector('[data-act="save-peer-routing"]').disabled = mode === 'tunnel' && !target.value;
        };
        modal.querySelectorAll('[name="peer-routing-mode"]').forEach((input) => input.addEventListener('change', sync));
        target.addEventListener('change', sync);
        sync();
      });
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    openModal(modalShell(`Routing: ${peer.name}`, notice('Cannot load tunnels', err.message || 'Try again before changing routing.', 'err'),
      `<button class="btn" data-act="close-modal">Close</button><button class="btn btn-primary" data-act="peer-routing" data-id="${esc(id)}">Retry</button>`));
  }
}

async function savePeerRouting() {
  const modal = $('#modal');
  const id = modal.dataset.peerId;
  const tunnel = modal.querySelector('[name="peer-routing-mode"]:checked').value === 'tunnel' ? $('#peer-routing-target').value : null;
  if (tunnel === '') { toast('Select an outgoing tunnel', 'err'); return; }
  const revision = currentModalRevision();
  const currentSession = api.session.guard();
  const button = modal.querySelector('[data-act="save-peer-routing"]');
  button.disabled = true;
  const controls = [...modal.querySelectorAll('input, select')];
  const disabled = controls.map((control) => control.disabled);
  controls.forEach((control) => { control.disabled = true; });
  modal.querySelector('#peer-routing-error').hidden = true;
  try {
    const peer = await api.peers.update(id, { egress_tunnel_id: tunnel });
    if (!currentSession()) return;
    const index = state.peers.findIndex((p) => p.id === id);
    if (index !== -1) state.peers[index] = { ...state.peers[index], ...peer };
    markDirty();
    refresh();
    if (isCurrentModal(revision)) closeModal();
    toast('Routing saved. Apply to activate it.');
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    button.disabled = false;
    controls.forEach((control, index) => { control.disabled = disabled[index]; });
    const error = modal.querySelector('#peer-routing-error');
    error.textContent = err.message || 'Routing was not saved. Try again.';
    error.hidden = false;
  }
}
