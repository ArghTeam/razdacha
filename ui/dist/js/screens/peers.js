/* Client inventory and unified device settings/configuration delivery. */

import * as api from '../api.js';
import * as qr from '../qr.js';
import { beginInventoryRead } from './tunnels.js';
import { beginInventoryRead as beginRuleInventoryRead } from './rules.js';
import {
  state, peerById, tunnelById, closeModal, toast, openModal, modalShell,
  currentModalRevision, isCurrentModal, onModalCleanup,
  notImplemented, refresh, markDirty,
} from '../shell.js';
import { $, esc, bytes, isOnline, download } from '../util.js';

export const title = 'Clients';

let inventoryRevision = 0;
/** Session and load order protect inventory from obsolete poll responses. */
export async function load() {
  const currentSession = api.session.guard();
  const screen = state.screen;
  const revision = ++inventoryRevision;
  const active = () => currentSession() && revision === inventoryRevision && screen === state.screen;
  let peers;
  try { peers = await api.peers.list(); }
  catch (err) { if (active()) throw err; return; }
  if (!active()) return;
  state.peers = peers;
  const tunnelRead = beginInventoryRead();
  try {
    const tunnels = await api.tunnels.list();
    if (!active() || !tunnelRead.accept(tunnels)) return;
    state.missing.delete('peer-tunnels');
  } catch (err) {
    if (!active() || !tunnelRead.current()) return;
    if (err.status === 401) throw err;
    state.missing.add('peer-tunnels');
  }
}

export function view() {
  if (state.missing.has('peers')) {
    return head() + `<div class="card">${notImplemented('Clients')}</div>`;
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
          <button class="btn btn-sm" data-act="peer-details" data-id="${esc(p.id)}" aria-label="Details for client ${esc(p.name)}">Details</button>
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
    <div class="client-apply-note"><strong>Configured routing</strong>
      <span>Saved routes need Apply. Device access can change during synchronization before Apply.${state.dirty ? ' Unapplied changes exist.' : ''}</span>
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

/* Client details keep editable drafts and delivered secrets inside one modal. */
let editor = null;
const emptyPool = tunnel => tunnel?.source === 'pool' && (tunnel.pool?.servers_total === 0 || tunnel.pool?.servers_alive === 0);
const writes = new Map();

function current(ctx) {
  return editor === ctx && ctx.currentSession() && isCurrentModal(ctx.revision);
}

function message(element, text, kind = '') {
  element.textContent = text;
  element.className = `client-status ${kind}`;
}

function modalAddPeer() {
  openModal(modalShell('Add client', `
    <div class="field"><label for="peer-name">Device name</label>
      <input id="peer-name" type="text" placeholder="Travel laptop" autocomplete="off" autofocus>
      <div class="hint">Address, keys, MTU and DNS are assigned automatically.</div>
    </div><div id="client-create-error" class="client-status err" role="alert"></div>`,
  '<button class="btn" data-act="close-modal">Cancel</button><button class="btn btn-primary" data-act="create-peer">Create client</button>'), (modal) => {
    modal.querySelector('#peer-name').addEventListener('keydown', (event) => {
      if (event.key === 'Enter') { event.preventDefault(); createPeer(); }
    });
  });
}

async function createPeer() {
  const input = $('#peer-name');
  const button = $('#modal [data-act="create-peer"]');
  if (!input || !button || button.disabled) return;
  const error = $('#client-create-error');
  const name = input.value.trim();
  if (!name) { message(error, 'Enter a device name.', 'err'); input.focus(); return; }
  const currentSession = api.session.guard();
  const revision = currentModalRevision();
  button.disabled = true;
  button.textContent = 'Creating…';
  try {
    const peer = await api.peers.create(name);
    if (!currentSession()) return;
    inventoryRevision++;
    if (!peerById(peer.id)) state.peers.push(peer);
    markDirty();
    refresh();
    if (isCurrentModal(revision)) modalPeerDetails(peer.id, true);
  } catch (err) {
    if (!currentSession() || !isCurrentModal(revision)) return;
    button.disabled = false;
    button.textContent = 'Create client';
    message(error, err.message || 'Client was not created. Try again.', 'err');
  }
}

function modalPeerDetails(id, showConfig = false) {
  const peer = peerById(id);
  if (!peer) return;
  const ctx = {
    id, baseline: { name: peer.name, enabled: peer.enabled !== false, egress_tunnel_id: peer.egress_tunnel_id || null },
    currentSession: api.session.guard(), revision: null, saving: false, deleting: false,
    configGeneration: 0, configPending: false, configReady: false, tunnels: [],
  };
  const revision = openModal(modalShell('Client details', `
    <div class="client-details">
      <p class="client-snapshot"><span class="mono">${esc(peer.address)}</span><span>Configured settings · ${isOnline(peer) ? 'Recent handshake' : 'No recent handshake'}</span></p>
      <section class="client-section" aria-labelledby="client-device-heading">
        <h3 id="client-device-heading">Device</h3>
        <div class="client-device-fields">
          <div class="field"><label for="client-name">Device name</label><input id="client-name" type="text" value="${esc(peer.name)}" autocomplete="off" aria-describedby="client-name-error"><div id="client-name-error" class="client-field-error" role="alert"></div></div>
          <label class="client-enabled"><input id="client-enabled" type="checkbox"${peer.enabled !== false ? ' checked' : ''}><span>Enabled<span class="client-small">Allow this device to connect</span></span></label>
        </div>
      </section>
      <section class="client-section" aria-labelledby="client-routing-heading">
        <h3 id="client-routing-heading">Internet routing</h3>
        <fieldset class="client-routing-choices"><legend class="client-routing-legend">Routing mode</legend>
          <label class="client-routing-choice"><input type="radio" name="client-route-mode" value="shared"${peer.egress_tunnel_id ? '' : ' checked'}><span><strong>Shared rules</strong><span>Use shared tunnel, direct and block rules. Unmatched traffic goes direct.</span></span></label>
          <label class="client-routing-choice"><input type="radio" name="client-route-mode" value="tunnel"${peer.egress_tunnel_id ? ' checked' : ''}><span><strong>Whole Internet through a tunnel</strong><span>Use one exit for this device, overriding shared rules.</span></span></label>
        </fieldset>
        <div id="client-target-field" class="field"><label for="client-target">Outgoing tunnel</label><select id="client-target" aria-describedby="client-target-status"><option value="">Select a tunnel</option>${peer.egress_tunnel_id ? `<option value="${esc(peer.egress_tunnel_id)}" selected>Saved exit · loading details…</option>` : ''}</select><div id="client-target-status" class="client-small" role="status"></div></div>
        <div class="client-tunnel-feedback"><span id="client-tunnels-status" class="client-small" role="status">Loading tunnels…</span><button class="btn btn-sm" data-act="retry-client-tunnels" hidden>Retry tunnels</button></div>
        <p id="client-route-scope" class="client-scope">IPv4 TCP/UDP and DNS only. IPv6, ping, other protocols and private-network forwarding are blocked. Gateway access remains available. No direct fallback if the exit fails.</p>
      </section>
      <div class="client-save-contract"><strong>Save settings, then Apply routing.</strong><span>Enabled changes can affect device access within the next 10-second sync, before Apply. Saved routing needs Apply; global pending status does not identify this device's active route.</span></div>
      <div id="client-save-status" class="client-status" role="status" aria-live="polite"></div>
      <details id="client-configuration" class="client-disclosure"${showConfig ? ' open' : ''}>
        <summary>WireGuard configuration<span>QR, copy and download</span></summary>
        <div class="client-config-content"><p class="client-small">${showConfig ? 'Client created. Device and routing settings are above. ' : ''}Generated by the gateway from saved settings. Copy and download do not save edits or apply routing.</p>
          <div id="client-config-status" class="client-status" role="status">Open to load configuration.</div>
          <button class="btn" data-act="retry-client-config" hidden>Retry configuration</button>
          <div id="client-config-result" hidden><div class="client-config-grid"><div class="client-qr"><canvas id="client-qr"></canvas></div><div class="client-config-delivery"><p class="client-small">Scan with WireGuard or use the saved configuration.</p><p id="client-config-draft-note" class="client-small" role="status" hidden>Save the name change before copying or downloading.</p><div class="client-delivery-actions"><button class="btn" data-act="copy-client-config">Copy configuration</button><button class="btn" data-act="download-conf">Download .conf</button></div><span id="client-config-filename" class="mono client-small"></span></div></div><details class="client-config-text"><summary>Show configuration text</summary><pre class="pane" id="client-config-text"></pre></details></div>
          <p class="client-small">Contains a private key. Closing clears the panel's copies; copied clipboard content remains outside the panel.</p>
        </div>
      </details>
      <section class="client-danger" aria-labelledby="client-danger-heading"><h3 id="client-danger-heading">Delete device</h3><p class="client-small">Deletion revokes access during synchronization and cannot be undone.</p><button class="btn btn-danger" data-act="inspect-client-delete">Delete client…</button>
        <div id="client-delete-confirmation" hidden><div id="client-delete-effects" class="client-small"></div><p class="client-small">Rule changes need Apply. Orphaned rules require client reassignment and explicit activation.</p><div class="client-delete-actions"><button class="btn" data-act="cancel-client-delete">Keep client</button><button class="btn btn-danger" data-act="confirm-client-delete">Delete permanently</button></div></div>
        <div id="client-delete-status" class="client-status" role="status"></div>
      </section>
    </div>`,
  '<span class="client-close-note">Close discards unsaved edits.</span><button class="btn" data-act="close-modal">Close</button><button class="btn btn-primary" data-act="save-client-details">Save changes</button>'), (modal) => {
    ctx.modal = modal;
    editor = ctx;
    onModalCleanup(() => {
      ctx.configGeneration++;
      ctx.configReady = false;
      if (editor === ctx) editor = null;
    });
    modal.querySelectorAll('#client-name, #client-enabled, [name="client-route-mode"], #client-target').forEach(control => {
      control.addEventListener('input', () => syncDetails(ctx));
      control.addEventListener('change', () => syncDetails(ctx));
    });
    modal.querySelector('#client-configuration').addEventListener('toggle', () => {
      if (current(ctx) && modal.querySelector('#client-configuration').open && !ctx.configReady) loadClientConfig(ctx);
    });
  });
  ctx.revision = revision;
  syncDetails(ctx);
  loadClientTunnels(ctx);
  if (showConfig) {
    const summary = ctx.modal.querySelector('#client-configuration > summary');
    summary.focus();
    summary.scrollIntoView({ block: 'start' });
    loadClientConfig(ctx);
  }
}

function draft(ctx) {
  const modal = ctx.modal;
  return {
    name: modal.querySelector('#client-name').value.trim(),
    enabled: modal.querySelector('#client-enabled').checked,
    egress_tunnel_id: modal.querySelector('[name="client-route-mode"]:checked').value === 'tunnel' ? modal.querySelector('#client-target').value : null,
  };
}

function changes(ctx) {
  const values = draft(ctx);
  return Object.fromEntries(Object.entries(values).filter(([key, value]) => value !== ctx.baseline[key]));
}

function syncDetails(ctx) {
  if (!current(ctx)) return;
  const values = draft(ctx), modal = ctx.modal;
  const whole = values.egress_tunnel_id !== null;
  modal.querySelector('#client-target-field').hidden = !whole;
  modal.querySelector('#client-route-scope').hidden = !whole;
  const tunnel = ctx.tunnels.find(t => t.id === values.egress_tunnel_id);
  const blocked = whole && values.egress_tunnel_id && tunnel && (!tunnel.enabled || emptyPool(tunnel));
  const status = modal.querySelector('#client-target-status');
  status.textContent = !values.egress_tunnel_id ? 'Choose an outgoing tunnel.' : !tunnel ? ctx.tunnelsKnown ? 'Missing exit · traffic blocked, no direct fallback. Selection retained.' : 'Exit details unavailable. Selection retained; no direct fallback.' : blocked ? 'Disabled or empty exit · traffic blocked, no direct fallback.' : 'Selected exit · no direct fallback. Health does not prove client connectivity.';
  status.classList.toggle('client-route-blocked', Boolean(whole && values.egress_tunnel_id && (!tunnel || blocked)));
  const nameInput = modal.querySelector('#client-name');
  nameInput.setAttribute('aria-invalid', String(!values.name));
  modal.querySelector('#client-name-error').textContent = values.name ? '' : 'Enter a device name.';
  const invalid = !values.name || whole && !values.egress_tunnel_id;
  modal.querySelector('[data-act="save-client-details"]').disabled = ctx.saving || ctx.deleting || invalid || !Object.keys(changes(ctx)).length;
  modal.querySelector('#client-config-draft-note').hidden = !ctx.configReady || values.name === ctx.configSavedName;
  // Deliver only a saved identity; rename changes the gateway-generated filename.
  modal.querySelectorAll('[data-act="copy-client-config"], [data-act="download-conf"]').forEach(button => { button.disabled = !ctx.configReady || ctx.saving || values.name !== ctx.configSavedName; });
}

async function loadClientTunnels(ctx = editor) {
  if (!ctx || !current(ctx) || ctx.tunnelsPending) return;
  ctx.tunnelsPending = true;
  ctx.tunnelsKnown = false;
  syncDetails(ctx);
  const modal = ctx.modal, status = modal.querySelector('#client-tunnels-status');
  const retry = modal.querySelector('[data-act="retry-client-tunnels"]');
  retry.hidden = true;
  status.textContent = 'Loading tunnels…';
  try {
    const tunnels = await api.tunnels.list();
    if (!current(ctx)) return;
    const selection = modal.querySelector('#client-target').value;
    ctx.tunnels = tunnels;
    ctx.tunnelsKnown = true;
    const missing = selection && !tunnels.some(t => t.id === selection);
    modal.querySelector('#client-target').innerHTML = '<option value="">Select a tunnel</option>' + (missing ? `<option value="${esc(selection)}">Missing exit · selection retained</option>` : '') + tunnels.map(t => `<option value="${esc(t.id)}">${esc(t.name)}${!t.enabled ? ' (disabled)' : emptyPool(t) ? ' (empty pool)' : ''}</option>`).join('');
    modal.querySelector('#client-target').value = selection;
    status.textContent = tunnels.length ? '' : 'No tunnels available. Add one on the Tunnels screen.';
    syncDetails(ctx);
  } catch (err) {
    if (!current(ctx)) return;
    status.textContent = err.message || 'Cannot load tunnels. Retry before selecting a different exit.';
    retry.hidden = false;
    syncDetails(ctx);
  } finally {
    ctx.tunnelsPending = false;
  }
}

function clearClientConfig(ctx) {
  ctx.configGeneration++;
  ctx.configPending = false;
  ctx.configReady = false;
  const modal = ctx.modal;
  delete modal.dataset.conf;
  delete modal.dataset.confName;
  const canvas = modal.querySelector('#client-qr');
  canvas.width = 0; canvas.height = 0;
  modal.querySelector('#client-config-text').textContent = '';
  modal.querySelector('#client-config-filename').textContent = '';
  modal.querySelector('#client-config-result').hidden = true;
  if (ctx.revokeDownload) { ctx.revokeDownload(); ctx.revokeDownload = null; }
  syncDetails(ctx);
}

async function loadClientConfig(ctx = editor) {
  if (!ctx || !current(ctx) || ctx.configPending) return;
  clearClientConfig(ctx);
  const generation = ctx.configGeneration;
  const savedName = peerById(ctx.id)?.name || ctx.baseline.name;
  ctx.configPending = true;
  const modal = ctx.modal, status = modal.querySelector('#client-config-status');
  const retry = modal.querySelector('[data-act="retry-client-config"]');
  retry.hidden = true;
  message(status, 'Loading saved configuration…');
  try {
    const { text, filename } = await api.peers.config(ctx.id);
    if (!current(ctx) || generation !== ctx.configGeneration) return;
    modal.dataset.conf = text;
    modal.dataset.confName = filename;
    modal.querySelector('#client-config-text').textContent = text;
    modal.querySelector('#client-config-filename').textContent = filename;
    modal.querySelector('#client-config-result').hidden = false;
    ctx.configReady = true;
    ctx.configSavedName = savedName;
    try {
      qr.draw(modal.querySelector('#client-qr'), text);
      message(status, draft(ctx).name === savedName ? 'Saved configuration ready.' : `Saved configuration is for ${savedName}. Save your name edit before delivery.`);
    } catch {
      message(status, 'QR unavailable. Copy or download the configuration instead.', 'err');
    }
  } catch (err) {
    if (!current(ctx) || generation !== ctx.configGeneration) return;
    message(status, err.message || 'Configuration unavailable. Retry without losing your edits.', 'err');
    retry.hidden = false;
  } finally {
    if (generation === ctx.configGeneration) ctx.configPending = false;
    if (current(ctx)) syncDetails(ctx);
  }
}

async function saveClientDetails() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting) return;
  const values = draft(ctx), patch = changes(ctx), modal = ctx.modal;
  if (!values.name) {
    modal.querySelector('#client-name').setAttribute('aria-invalid', 'true');
    modal.querySelector('#client-name-error').textContent = 'Enter a device name.';
    modal.querySelector('#client-name').focus(); return;
  }
  if (values.egress_tunnel_id === '') return;
  if (!Object.keys(patch).length) return;
  ctx.saving = true;
  const controls = [...modal.querySelectorAll('#client-name, #client-enabled, [name="client-route-mode"], #client-target')];
  controls.forEach(control => { control.disabled = true; });
  const button = modal.querySelector('[data-act="save-client-details"]');
  button.textContent = 'Saving…';
  message(modal.querySelector('#client-save-status'), 'Saving settings…');
  syncDetails(ctx);
  // Serialize this client's writes across replaced dialogs. A previous login never blocks a new one.
  const previous = writes.get(ctx.id);
  const pending = (async () => {
    if (previous?.currentSession()) await previous.pending.catch(() => {});
    if (!ctx.currentSession()) return;
    return api.peers.update(ctx.id, patch);
  })();
  const entry = { pending, currentSession: ctx.currentSession };
  writes.set(ctx.id, entry);
  try {
    await pending;
    if (!ctx.currentSession()) return;
    inventoryRevision++;
    const peer = peerById(ctx.id);
    if (peer) Object.assign(peer, patch);
    // A rename finishing in another dialog also invalidates this client's delivered filename.
    if ('name' in patch && editor?.id === ctx.id && current(editor)) {
      const active = editor;
      clearClientConfig(active);
      if (active.modal.querySelector('#client-configuration').open) loadClientConfig(active);
    }
    markDirty();
    refresh();
    if (!current(ctx)) return;
    Object.assign(ctx.baseline, patch);
    // This save may have waited behind a previous dialog's accepted write. Reconcile
    // untouched fields only now, without changing an independently opened draft.
    if (peer) ctx.baseline = { name: peer.name, enabled: peer.enabled !== false, egress_tunnel_id: peer.egress_tunnel_id || null };
    modal.querySelector('#client-name').value = ctx.baseline.name;
    modal.querySelector('#client-enabled').checked = ctx.baseline.enabled;
    const savedExit = ctx.baseline.egress_tunnel_id;
    modal.querySelectorAll('[name="client-route-mode"]').forEach(control => { control.checked = control.value === (savedExit ? 'tunnel' : 'shared'); });
    if (savedExit) {
      const target = modal.querySelector('#client-target');
      if (![...target.options].some(option => option.value === savedExit)) target.add(new Option('Saved exit · selection retained', savedExit));
      target.value = savedExit;
    }
    modal.querySelector('#client-name').removeAttribute('aria-invalid');
    modal.querySelector('#client-name-error').textContent = '';
    message(modal.querySelector('#client-save-status'), 'Settings saved. Apply routing changes; enabled state synchronizes separately.', 'ok');
  } catch (err) {
    if (current(ctx)) message(modal.querySelector('#client-save-status'), err.message || 'Settings were not saved. Your edits are retained; try again.', 'err');
  } finally {
    if (writes.get(ctx.id) === entry) writes.delete(ctx.id);
    ctx.saving = false;
    if (current(ctx)) {
      controls.forEach(control => { control.disabled = false; });
      button.textContent = 'Save changes';
      syncDetails(ctx);
    }
  }
}

async function inspectClientDelete() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting) return;
  ctx.deleting = true;
  const modal = ctx.modal;
  modal.querySelector('[data-act="inspect-client-delete"]').disabled = true;
  message(modal.querySelector('#client-delete-status'), 'Inspecting affected rules…');
  syncDetails(ctx);
  try {
    const rules = await api.rules.list();
    if (!current(ctx)) return;
    const affected = rules.filter(rule => rule.peer_scope === 'selected' && (rule.peer_ids || []).includes(ctx.id));
    const orphaned = affected.filter(rule => !(rule.peer_ids || []).some(id => id !== ctx.id && peerById(id)));
    modal.querySelector('#client-delete-effects').innerHTML = `<p>Delete <strong>${esc(peerById(ctx.id)?.name || ctx.baseline.name)}</strong> permanently?</p><p>${affected.length ? `Affected rules: ${affected.map(rule => esc(rule.name)).join(', ')}.` : 'No selected-client rules are affected.'}</p>${orphaned.length ? `<p>Inactive after deletion: ${orphaned.map(rule => esc(rule.name)).join(', ')}.</p>` : ''}`;
    modal.querySelector('#client-delete-confirmation').hidden = false;
    modal.querySelector('[data-act="inspect-client-delete"]').hidden = true;
    message(modal.querySelector('#client-delete-status'), '');
    modal.querySelector('[data-act="cancel-client-delete"]').focus();
  } catch (err) {
    if (current(ctx)) message(modal.querySelector('#client-delete-status'), err.message || 'Cannot inspect affected rules. Try again.', 'err');
    ctx.deleting = false;
  } finally {
    if (current(ctx)) { modal.querySelector('[data-act="inspect-client-delete"]').disabled = false; syncDetails(ctx); }
  }
}

function cancelClientDelete() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.deletePending) return;
  ctx.deleting = false;
  ctx.modal.querySelector('#client-delete-confirmation').hidden = true;
  ctx.modal.querySelector('[data-act="inspect-client-delete"]').hidden = false;
  message(ctx.modal.querySelector('#client-delete-status'), '');
  syncDetails(ctx);
  ctx.modal.querySelector('[data-act="inspect-client-delete"]').focus();
}

async function confirmClientDelete() {
  const ctx = editor;
  if (!ctx || !current(ctx) || !ctx.deleting || ctx.deletePending || ctx.modal.querySelector('#client-delete-confirmation').hidden) return;
  ctx.deletePending = true;
  ctx.modal.querySelectorAll('.client-delete-actions button').forEach(button => { button.disabled = true; });
  message(ctx.modal.querySelector('#client-delete-status'), 'Deleting client…');
  try {
    const effects = await api.peers.remove(ctx.id);
    if (!ctx.currentSession()) return;
    inventoryRevision++;
    state.peers = state.peers.filter(peer => peer.id !== ctx.id);
    markDirty(); refresh();
    let rulesError = false;
    const ruleRead = beginRuleInventoryRead();
    try {
      const rules = await api.rules.list();
      if (!ctx.currentSession()) return;
      ruleRead.accept(rules);
    } catch {
      if (!ctx.currentSession()) return;
      if (ruleRead.current()) { state.rules = []; rulesError = true; }
    }
    if (!current(ctx)) return;
    closeModal();
    toast(rulesError ? 'Client deleted. Reload rules before editing.' : (effects.inactivated_rule_ids || []).length ? 'Client deleted. Orphaned rules require reassignment, explicit activation and Apply.' : 'Client deleted. Rule changes need Apply.');
  } catch (err) {
    if (current(ctx)) message(ctx.modal.querySelector('#client-delete-status'), err.message || 'Client was not deleted. Try again.', 'err');
  } finally {
    ctx.deletePending = false;
    if (current(ctx)) ctx.modal.querySelectorAll('.client-delete-actions button').forEach(button => { button.disabled = false; });
  }
}

async function copyClientConfig() {
  const ctx = editor;
  if (!ctx || !current(ctx) || !ctx.configReady || ctx.saving || draft(ctx).name !== ctx.configSavedName) return;
  const text = ctx.modal.dataset.conf, generation = ctx.configGeneration;
  try {
    await navigator.clipboard.writeText(text);
    if (current(ctx) && generation === ctx.configGeneration) message(ctx.modal.querySelector('#client-config-status'), 'Configuration copied. Clipboard content remains outside the panel.', 'ok');
  } catch {
    if (current(ctx) && generation === ctx.configGeneration) message(ctx.modal.querySelector('#client-config-status'), 'Cannot copy here. Use Download .conf or select the configuration text.', 'err');
  }
}

export const actions = {
  'add-peer': modalAddPeer,
  'create-peer': createPeer,
  'peer-details': id => modalPeerDetails(id),
  'save-client-details': saveClientDetails,
  'retry-client-tunnels': () => loadClientTunnels(),
  'retry-client-config': () => loadClientConfig(),
  'inspect-client-delete': inspectClientDelete,
  'cancel-client-delete': cancelClientDelete,
  'confirm-client-delete': confirmClientDelete,
  'copy-client-config': copyClientConfig,
  'download-conf': () => {
    const ctx = editor;
    if (!ctx || !current(ctx) || !ctx.configReady || ctx.saving || draft(ctx).name !== ctx.configSavedName) return;
    if (ctx.revokeDownload) ctx.revokeDownload();
    ctx.revokeDownload = download(ctx.modal.dataset.confName || 'peer.conf', ctx.modal.dataset.conf);
    onModalCleanup(ctx.revokeDownload);
    message(ctx.modal.querySelector('#client-config-status'), 'Configuration downloaded. Import it into WireGuard.', 'ok');
  },
};

function routingSummary(peer) {
  if (!peer.egress_tunnel_id) return '<div class="client-route-name">Shared rules</div><div class="row-meta">Shared resource rules; unmatched traffic goes direct.</div>';
  if (state.missing.has('tunnels') || state.missing.has('peer-tunnels')) return '<div class="client-route-name">Selected exit</div><div class="row-meta">Exit details unavailable. Open Details to retry.</div>';
  const tunnel = tunnelById(peer.egress_tunnel_id);
  const unavailable = !tunnel || !tunnel.enabled || emptyPool(tunnel);
  return `<div class="client-route-name">Internet → ${esc(tunnel ? tunnel.name : 'Missing tunnel')}</div><div class="row-meta${unavailable ? ' client-route-blocked' : ''}">${unavailable ? 'Blocked target · no direct fallback' : 'IPv4 TCP/UDP + DNS · no direct fallback'}</div>`;
}
