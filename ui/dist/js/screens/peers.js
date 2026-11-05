/* Client inventory and unified device settings/configuration delivery. */

import * as api from '../api.js';
import * as qr from '../qr.js';
import { beginInventoryRead } from './tunnels.js';
import { beginInventoryRead as beginRuleInventoryRead } from './rules.js';
import {
  state, peerById, tunnelById, closeModal, toast, openModal, modalShell,
  currentModalRevision, isCurrentModal, onModalCleanup,
  notImplemented, refresh, markDirty, openMenu,
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

let activityTimer = null;

// Expire only observation text and dots while a dialog/menu pauses ordinary polls.
// Updating these nodes preserves the editor, focus and unsaved form state.
function scheduleActivityExpiry() {
  if (activityTimer !== null) clearTimeout(activityTimer);
  activityTimer = setTimeout(() => {
    activityTimer = null;
    if (state.screen !== 'peers') return;
    for (const row of document.querySelectorAll('[data-client-activity]')) {
      const peer = peerById(row.dataset.clientActivity);
      if (!peer) continue;
      const on = peer.enabled !== false && (wireGuardEnabled(peer) && isOnline(peer) || realityFlowCount(peer) > 0);
      const dot = row.querySelector('.client-name .dot');
      if (dot) { dot.classList.toggle('on', on); dot.classList.toggle('off', !on); }
      const text = row.querySelector('[data-reality-activity]');
      if (text) text.textContent = realityRecoveryMessage(peer.id) || realityStatusText(peer);
    }
    if (editor && current(editor)) {
      const peer = peerById(editor.id);
      const status = editor.modal.querySelector('#client-reality-status');
      if (peer && status) status.textContent = realityRecoveryMessage(peer.id) || realityStatusText(peer);
    }
    scheduleActivityExpiry();
  }, 1000);
}

export function view() {
  scheduleActivityExpiry();
  if (state.missing.has('peers')) {
    return head() + `<div class="card">${notImplemented('Clients')}</div>`;
  }

  const online = state.peers.filter(peer => wireGuardEnabled(peer) && isOnline(peer)).length;
  const selected = state.peers.filter((p) => p.egress_tunnel_id).length;
  const rows = state.peers.map((p) => {
    const wgOn = wireGuardEnabled(p) && isOnline(p);
    const on = wgOn || realityFlowCount(p) > 0;
    const activity = p.enabled === false ? 'Disabled' : !wireGuardEnabled(p) ? 'WireGuard disabled' : wgOn ? 'WireGuard connected' : p.last_handshake && Number.isFinite(Date.parse(p.last_handshake)) ? `WireGuard handshake ${new Date(p.last_handshake).toLocaleString('en-GB')}` : 'No WireGuard handshake yet';
    const reality = realityEnabled(p) ? `<div class="row-meta" data-reality-activity>${esc(realityRecoveryMessage(p.id) || realityStatusText(p))}</div>` : '';
    return `
      <article class="row client-row${p.enabled === false ? ' dim' : ''}" aria-label="Client ${esc(p.name)}" data-client-activity="${esc(p.id)}">
        <div class="client-identity">
          <div class="client-name"><span class="dot ${on ? 'on' : 'off'}" aria-hidden="true"></span><h2>${esc(p.name)}</h2></div>
          ${wireGuardEnabled(p) ? `<div class="mono row-meta">WG · ${esc(p.address)}</div>` : ''}
          <div class="row-meta">${activity}${wireGuardEnabled(p) ? ` <span class="client-traffic">· ↓ ${observedTraffic(p.rx_bytes)} · ↑ ${observedTraffic(p.tx_bytes)}</span>` : ''}</div>${reality}
        </div>
        <div class="client-route"><div class="client-label">Configured route</div>${routingSummary(p)}</div>
        <div class="row-actions client-actions">
          <button class="icon-btn more-btn" data-act="menu-peer" data-id="${esc(p.id)}" aria-label="Actions for client ${esc(p.name)}" aria-haspopup="menu" aria-expanded="false">⋮</button>
        </div>
      </article>`;
  }).join('');

  const received = aggregateTraffic(state.peers, 'rx_bytes');
  const sent = aggregateTraffic(state.peers, 'tx_bytes');
  return head() + `
    <section class="summary" aria-label="Aggregate WireGuard client traffic">
      <div class="stat"><div class="stat-label">WireGuard received</div><div class="stat-value" data-traffic="received">${received === null ? 'Unknown' : bytes(received)}</div></div>
      <div class="stat"><div class="stat-label">WireGuard sent</div><div class="stat-value" data-traffic="sent">${sent === null ? 'Unknown' : bytes(sent)}</div></div>
    </section>
    <p class="screen-sub">WireGuard byte totals cover current clients and reset with the gateway interface. Missing observations are unknown.</p>
    <div class="client-overview" aria-label="Client inventory summary">
      <span><strong>${state.peers.length}</strong> clients</span>
      <span><strong>${online}</strong> WireGuard connected</span>
      <span><strong>${selected}</strong> selected exit</span>
      <span><strong>${state.peers.length - selected}</strong> shared rules</span>
    </div>
    <div class="client-apply-note"><strong>Configured routing</strong>
      <span>Saved routes need Apply. Device access can change during synchronization before Apply.${state.dirty ? ' Unapplied changes exist.' : ''}</span>
    </div>
    <div class="card client-inventory">${rows || '<div class="empty"><strong>No clients yet</strong><p>Add a device to create its WireGuard configuration, then choose its route.</p><button class="btn" data-act="add-peer">Add client</button></div>'}</div>
    <p class="screen-sub" style="margin-top:10px">WireGuard traffic counters are measured since the gateway interface started. Reality shows open flows from a server snapshot. Its indicator expires after 30 seconds without a new observation; short connections between polls may be missed.</p>`;
}

const observedTraffic = value => typeof value === 'number' && Number.isFinite(value) && value >= 0 ? bytes(value) : 'Unknown';

export function aggregateTraffic(peers, key) {
  const observed = peers.filter(wireGuardEnabled);
  if (!observed.length) return null;
  let total = 0;
  for (const peer of observed) {
    const value = peer[key];
    if (typeof value !== 'number' || !Number.isFinite(value) || value < 0) return null;
    total += value;
    if (!Number.isFinite(total)) return null;
  }
  return total;
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
// Ambiguous credential changes outlive a dialog, but never the initiating login.
const realityRecoveries = new Map();
let realityEpoch = 0, globalRealityRecovery = null;
export const realityStateRevision = () => `${inventoryRevision}:${realityEpoch}`;
export function realitySettingsRecovery() {
  if (globalRealityRecovery && !globalRealityRecovery.currentSession()) globalRealityRecovery = null;
  return globalRealityRecovery;
}
export function beginRealitySettingsMutation() {
  const token = { currentSession: api.session.guard(), pending: true, uncertain: false, error: '' };
  globalRealityRecovery = token; realityEpoch++; inventoryRevision++;
  return token;
}
export function finishRealitySettingsMutation(token, error = null) {
  if (!token?.currentSession() || globalRealityRecovery !== token) return;
  realityEpoch++; inventoryRevision++;
  if (error) { token.pending = false; token.uncertain = true; token.error = `Reality settings could not be confirmed: ${error.message}. Access may already have changed. Reload saved Network settings or Reality status.`; }
  else {
    globalRealityRecovery = null;
    // Configuration changed; only a later authoritative inventory confirms downloadability.
    for (const peer of state.peers) if (realityEnabled(peer)) {
      peer.reality_profile_status = 'pending';
      peer.reality_profile_downloadable = false;
    }
  }
  if (editor && current(editor)) clearClientConfig(editor);
}
export function clearRealitySettingsRecovery(token) {
  if (realitySettingsRecovery() !== token || token?.pending) return false;
  globalRealityRecovery = null; realityEpoch++; inventoryRevision++; return true;
}
function beginPeerRealityMutation(ctx) {
  const token = { currentSession: ctx.currentSession, pending: true, uncertain: false, error: '' };
  realityRecoveries.set(ctx.id, token); realityEpoch++; inventoryRevision++;
  if (editor?.id === ctx.id && current(editor)) clearClientConfig(editor);
  return token;
}
function finishPeerRealityMutation(ctx, token, error = null) {
  if (!token || !ctx.currentSession() || realityRecoveries.get(ctx.id) !== token) return;
  realityEpoch++; inventoryRevision++;
  token.pending = false;
  if (error) { token.uncertain = true; token.error = `${error.message || 'Access change could not be confirmed.'} Access may already be revoked. Reload Reality status before retrying.`; }
  else realityRecoveries.delete(ctx.id);
  if (editor?.id === ctx.id && current(editor)) clearClientConfig(editor);
}
function realityRecovery(id) {
  const recovery = realityRecoveries.get(id);
  if (recovery && !recovery.currentSession()) { realityRecoveries.delete(id); return realitySettingsRecovery(); }
  return recovery || realitySettingsRecovery() || null;
}
function realityRecoveryMessage(id) {
  const recovery = realityRecovery(id);
  return recovery?.pending ? 'Reality access is still reconciling. Wait before requesting another profile.'
    : recovery?.uncertain ? recovery.error : '';
}
const wireGuardEnabled = peer => peer?.wireguard_enabled !== false;
const realityEnabled = peer => peer?.reality_enabled === true;
const peerBaseline = peer => ({
  name: peer.name, enabled: peer.enabled !== false,
  egress_tunnel_id: peer.egress_tunnel_id || null,
  wireguard_enabled: wireGuardEnabled(peer), reality_enabled: realityEnabled(peer),
});
function realityStatus(peer) {
  if (peer?.enabled === false || !realityEnabled(peer)) return 'disabled';
  return ['disabled', 'pending', 'available', 'guarded'].includes(peer.reality_profile_status) ? peer.reality_profile_status : 'pending';
}
function realityFlowCount(peer) {
  const sample = peer?.reality_activity;
  const time = Date.parse(sample?.observed_at);
  const age = api.peerActivityAge(peer);
  if (peer?.enabled === false || realityRecovery(peer?.id) || realityStatus(peer) !== 'available' || sample?.state !== 'observed' || !Number.isInteger(sample.open_flows) || sample.open_flows < 0 || !Number.isFinite(time) || age === null || age < 0 || age > 30000) return null;
  return sample.open_flows;
}
function realityStatusText(peer) {
  switch (realityStatus(peer)) {
    case 'available': {
      const count = realityFlowCount(peer);
      return count === null ? 'Reality · Activity unavailable' : count > 0 ? `Reality · ${count} open ${count === 1 ? 'flow' : 'flows'}` : 'Reality · No open flows observed';
    }
    case 'pending': return peer?.reality_profile_downloadable === true ? 'Works after Apply. Import this profile now, then close this dialog and Apply saved changes to activate access.' : 'Complete and save Reality settings and the public gateway endpoint in Network to obtain a profile. Reload Reality status after saving. Then Apply to activate access.';
    case 'guarded': return 'Reality ingress is guarded. Resolve the server error and Apply before requesting a profile.';
    default: return 'Reality is disabled or not configured. Check Network settings, enable client access and Apply saved changes.';
  }
}

function realityDownloadable(peer) {
  return ['pending', 'available'].includes(realityStatus(peer)) && peer?.reality_profile_downloadable === true;
}
function checkRealityDelivery(result, revision) {
  if (!['pending', 'available'].includes(result.realityState) || !Number.isInteger(result.realityRevision) || result.realityRevision !== revision) throw new Error('Reality access changed while loading. Reload its status before requesting another profile.');
}
function profileAvailable(ctx) {
  const peer = peerById(ctx.id);
  return ctx.profileMethod !== 'reality' || !realityRecovery(ctx.id) && realityDownloadable(peer) && peer?.reality_revision === ctx.configRealityRevision;
}

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
    </div><fieldset class="client-method-choices"><legend>Connection methods</legend><label class="client-routing-choice"><input id="new-peer-wg" type="checkbox" checked><span><strong>WireGuard</strong><span>Standard WireGuard app or router.</span></span></label><label class="client-routing-choice"><input id="new-peer-reality" type="checkbox"><span><strong>Reality (TCP)</strong><span>Requires a compatible app and gateway setup.</span></span></label></fieldset><p class="client-small">Choose either or both methods. Separate profiles share routing rules; there is no automatic fallback.</p><div id="client-create-error" class="client-status err" role="alert"></div>`,
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
    const connections = { wireguard_enabled: $('#new-peer-wg').checked, reality_enabled: $('#new-peer-reality').checked };
    if (!connections.wireguard_enabled && !connections.reality_enabled) {
      button.disabled = false; button.textContent = 'Create client';
      message(error, 'Choose at least one connection method.', 'err'); return;
    }
    const peer = await api.peers.create(name, connections);
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

function modalPeerDetails(id, showConfig = false, preferredMethod = '') {
  const peer = peerById(id);
  if (!peer) return;
  const ctx = {
    id, baseline: peerBaseline(peer), profileMethod: preferredMethod === 'reality' && realityEnabled(peer) ? 'reality' : wireGuardEnabled(peer) ? 'wg' : 'reality',
    currentSession: api.session.guard(), revision: null, saving: false, deleting: false, rotating: false,
    configGeneration: 0, configPending: false, configReady: false, tunnels: [],
  };
  const revision = openModal(modalShell('Client details', `
    <div class="client-details">
      <p class="client-snapshot">${wireGuardEnabled(peer) ? `<span class="mono">WG · ${esc(peer.address)}</span>` : ''}<span>Configured settings · ${wireGuardEnabled(peer) ? isOnline(peer) ? 'Recent WireGuard handshake' : 'No recent WireGuard handshake' : 'WireGuard disabled'}</span></p>
      <section class="client-section" aria-labelledby="client-device-heading">
        <h3 id="client-device-heading">Device</h3>
        <div class="client-device-fields">
          <div class="field"><label for="client-name">Device name</label><input id="client-name" type="text" value="${esc(peer.name)}" autocomplete="off" aria-describedby="client-name-error"><div id="client-name-error" class="client-field-error" role="alert"></div></div>
          <label class="client-enabled"><input id="client-enabled" type="checkbox"${peer.enabled !== false ? ' checked' : ''}><span>Enabled<span class="client-small">Allow this device to connect</span></span></label>
        </div>
      </section>
      <section class="client-section" aria-labelledby="client-method-heading">
        <h3 id="client-method-heading">Connection methods</h3>
        <fieldset class="client-method-choices"><legend class="client-small">Both methods share this client's rules and Internet exit.</legend>
          <label class="client-routing-choice"><input id="client-wireguard" type="checkbox"${wireGuardEnabled(peer) ? ' checked' : ''}><span><strong>WireGuard</strong><span>Use a standard WireGuard app or router.</span></span></label>
          <label class="client-routing-choice"><input id="client-reality" type="checkbox"${realityEnabled(peer) ? ' checked' : ''}><span><strong>Reality (TCP)</strong><span>VLESS + REALITY with a compatible app or router.</span></span></label>
        </fieldset>
        <p id="client-reality-status" class="client-small">${esc(realityStatusText(peer))}</p>
        <p id="client-method-error" class="client-field-error" role="alert"></p>
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
      <div class="client-save-contract"><strong>Save settings, then Apply new access and routing.</strong><span>Disabling access or replacing a key requires server reconciliation and can briefly interrupt other connections. WireGuard enabled changes can affect device access within the next 10-second sync, before Apply. Global pending status does not identify this device's active route.</span></div>
      <div id="client-save-status" class="client-status" role="status" aria-live="polite"></div>
      <details id="client-configuration" class="client-disclosure"${showConfig ? ' open' : ''}>
        <summary><span id="client-config-heading" class="client-config-heading">${wireGuardEnabled(peer) ? 'WireGuard configuration' : 'Reality profile'}</span><span>QR, copy and download</span></summary>
        <div class="client-config-content"><div class="client-profile-methods" role="group" aria-label="Profile connection method"><button class="btn btn-sm" data-act="select-client-wg" aria-pressed="${wireGuardEnabled(peer)}">WireGuard</button><button class="btn btn-sm" data-act="select-client-reality" aria-pressed="${!wireGuardEnabled(peer)}">Reality</button></div><p class="client-small">${showConfig ? 'Client created. Device and routing settings are above. ' : ''}Generated by the gateway from saved settings. Copy and download do not save edits or apply routing.</p>
          <div id="client-config-status" class="client-status" role="status">Open to load configuration.</div>
          <button class="btn" data-act="retry-client-config" hidden>Retry configuration</button>
          <p id="client-reality-dns-note" class="client-small" hidden>Enable VPN/TUN capture and send UDP DNS through this connection to 10.8.0.1:53. This preserves domain routing, including QUIC and encrypted TLS names. URI import does not configure DNS. Some apps import only outbounds from JSON; use their full-configuration mode or set gateway DNS explicitly.</p><p id="client-reality-pending-note" class="client-status" role="status" hidden><strong>Works after Apply.</strong> Import now. Connect after Apply. Apply publishes all saved changes. <button class="btn btn-sm" data-act="go-to-client-apply">Close and go to Apply</button></p><div id="client-config-result" hidden><div class="client-config-grid"><div class="client-qr"><canvas id="client-qr"></canvas></div><div class="client-config-delivery"><p class="client-small"><span id="client-profile-help">${wireGuardEnabled(peer) ? 'Scan with WireGuard or use the saved configuration.' : 'Import this profile into a compatible VLESS + REALITY app.'}</span></p><p id="client-config-draft-note" class="client-small" role="status" hidden>Save the name change before copying or downloading.</p><div class="client-delivery-actions"><button class="btn" data-act="copy-client-config">Copy configuration</button><button class="btn" data-act="download-reality-json" hidden>Download sing-box profile</button><button class="btn" data-act="download-conf">${wireGuardEnabled(peer) ? 'Download .conf' : 'Download profile'}</button></div><span id="client-config-filename" class="mono client-small"></span></div></div><details class="client-config-text"><summary>Show configuration text</summary><pre class="pane" id="client-config-text"></pre></details></div>
          <p class="client-small">Contains private connection credentials. Closing clears the panel's copies; copied clipboard content remains outside the panel.</p>
          <details class="client-reality-management"><summary>Manage Reality access</summary><p class="client-small">Replacing the key revokes the previous credential and requires server reconciliation. Existing connections can be interrupted. The replacement profile can be imported now and works after Apply.</p><button class="btn" data-act="inspect-reality-rotation">Replace Reality key…</button><div id="client-reality-confirmation" hidden><p class="client-small">Replace this client's Reality key? The old profile will stop working. WireGuard keys remain unchanged, but reconciliation may briefly interrupt connections.</p><div class="client-delete-actions"><button class="btn" data-act="cancel-reality-rotation">Keep key</button><button class="btn btn-danger" data-act="confirm-reality-rotation">Replace key</button></div></div><div id="client-reality-rotation-status" class="client-status" role="status"></div><button class="btn" data-act="refresh-reality-status">Reload Reality status</button></details>
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
    modal.querySelectorAll('#client-name, #client-enabled, #client-wireguard, #client-reality, [name="client-route-mode"], #client-target').forEach(control => {
      const update = () => {
        syncDetails(ctx);
        if (current(ctx) && modal.querySelector('#client-configuration').open && !ctx.configReady && !ctx.configPending && !Object.keys(changes(ctx)).length) loadClientConfig(ctx);
      };
      control.addEventListener('input', update);
      control.addEventListener('change', update);
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
    wireguard_enabled: modal.querySelector('#client-wireguard').checked,
    reality_enabled: modal.querySelector('#client-reality').checked,
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
  const noMethod = values.enabled && !values.wireguard_enabled && !values.reality_enabled;
  modal.querySelector('#client-method-error').textContent = noMethod ? 'Choose a connection method or disable the client.' : '';
  const invalid = !values.name || whole && !values.egress_tunnel_id || noMethod;
  modal.querySelector('[data-act="save-client-details"]').disabled = ctx.saving || ctx.deleting || ctx.rotating || invalid || !Object.keys(changes(ctx)).length;
  const transportDraft = values.wireguard_enabled !== ctx.baseline.wireguard_enabled || values.reality_enabled !== ctx.baseline.reality_enabled || values.enabled !== ctx.baseline.enabled;
  const profileDraft = values.name !== ctx.baseline.name || transportDraft;
  const note = modal.querySelector('#client-config-draft-note');
  note.hidden = !ctx.configReady || !profileDraft;
  note.textContent = values.name !== ctx.baseline.name ? 'Save the name change before copying or downloading.' : 'Save connection changes before copying or downloading.';
  const peer = peerById(ctx.id);
  modal.querySelector('#client-reality-status').textContent = realityRecoveryMessage(ctx.id) || realityStatusText(peer);
  modal.querySelector('#client-reality-dns-note').hidden = ctx.profileMethod !== 'reality';
  modal.querySelector('#client-reality-pending-note').hidden = ctx.profileMethod !== 'reality' || !ctx.configReady || ctx.configRealityState !== 'pending' && realityStatus(peer) !== 'pending';
  modal.querySelector('#client-config-heading').textContent = ctx.profileMethod === 'wg' ? 'WireGuard configuration' : 'Reality profile';
  modal.querySelector('#client-profile-help').textContent = ctx.profileMethod === 'wg' ? 'Scan with WireGuard or use the saved configuration.' : 'URI import requires a compatible app configured for TUN and DNS through the connection. The sing-box JSON profile includes TUN and DNS routing.';
  modal.querySelector('[data-act="download-conf"]').textContent = ctx.profileMethod === 'wg' ? 'Download .conf' : 'Download profile';
  for (const method of ['wg', 'reality']) {
    const control = modal.querySelector(`[data-act="select-client-${method}"]`);
    control.setAttribute('aria-pressed', String(ctx.profileMethod === method));
    control.disabled = ctx.saving || ctx.rotating || (method === 'wg' ? !ctx.baseline.wireguard_enabled : !ctx.baseline.reality_enabled);
  }
  modal.querySelector('.client-reality-management').hidden = ctx.profileMethod !== 'reality' || !ctx.baseline.reality_enabled;
  modal.querySelector('[data-act="inspect-reality-rotation"]').disabled = ctx.saving || ctx.rotating || ctx.deleting || realityRecovery(ctx.id) || profileDraft || !Number.isInteger(peer?.reality_revision) || peer.reality_revision < 1;
  if (transportDraft && (ctx.configReady || ctx.configPending)) clearClientConfig(ctx);
  if (transportDraft) message(modal.querySelector('#client-config-status'), 'Save connection changes before requesting a profile.');
  modal.querySelectorAll('#client-name, #client-enabled, #client-wireguard, #client-reality, [name="client-route-mode"], #client-target').forEach(control => { control.disabled = ctx.saving || ctx.rotating; });
  // Deliver only a saved identity; rename changes the gateway-generated filename.
  modal.querySelector('[data-act="go-to-client-apply"]').disabled = ctx.saving || ctx.rotating || ctx.deleting || Object.keys(changes(ctx)).length > 0;
  modal.querySelector('[data-act="download-reality-json"]').hidden = ctx.profileMethod !== 'reality';
  modal.querySelectorAll('[data-act="copy-client-config"], [data-act="download-conf"], [data-act="download-reality-json"]').forEach(button => { button.disabled = !ctx.configReady || ctx.saving || ctx.rotating || profileDraft || !profileAvailable(ctx); });
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
  ctx.configRealityState = null;
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
  const method = ctx.profileMethod, peer = peerById(ctx.id);
  const status = ctx.modal.querySelector('#client-config-status');
  if (method === 'wg' && !wireGuardEnabled(peer)) { message(status, 'WireGuard is disabled for this client.', 'err'); return; }
  if (method === 'reality' && !realityDownloadable(peer)) { message(status, realityStatusText(peer), realityStatus(peer) === 'guarded' ? 'err' : ''); return; }
  if (method === 'reality' && realityRecovery(ctx.id)) { message(status, realityRecoveryMessage(ctx.id), 'err'); return; }
  if (Object.keys(changes(ctx)).some(key => ['enabled', 'wireguard_enabled', 'reality_enabled'].includes(key))) { message(status, 'Save connection changes before requesting a profile.'); return; }
  const generation = ctx.configGeneration, epoch = realityEpoch, realityRevision = peer?.reality_revision;
  const savedName = peerById(ctx.id)?.name || ctx.baseline.name;
  ctx.configPending = true;
  const modal = ctx.modal;
  const retry = modal.querySelector('[data-act="retry-client-config"]');
  retry.hidden = true;
  message(status, 'Loading saved configuration…');
  try {
    const result = await (method === 'wg' ? api.peers.config(ctx.id) : api.peers.realityConfig(ctx.id));
    const text = method === 'wg' ? result.text : result.text.trim();
    if (method === 'reality' && (!text.startsWith('vless://') || /[\r\n]/.test(text))) throw new Error('The server did not return a valid Reality import profile.');
    const filename = result.filename || `reality-${String(ctx.id).replace(/[^a-zA-Z0-9_-]/g, '_')}.txt`;
    if (!current(ctx) || generation !== ctx.configGeneration || method === 'reality' && epoch !== realityEpoch) return;
    if (method === 'reality' && (peerById(ctx.id)?.reality_revision !== realityRevision || !realityDownloadable(peerById(ctx.id)))) throw new Error('Reality access changed while loading. Reload its status before requesting another profile.');
    if (method === 'reality') checkRealityDelivery(result, realityRevision);
    ctx.configRealityRevision = realityRevision;
    ctx.configRealityState = result.realityState;
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
    if (!current(ctx) || generation !== ctx.configGeneration || method === 'reality' && epoch !== realityEpoch) return;
    message(status, err.message || 'Configuration unavailable. Retry without losing your edits.', 'err');
    retry.hidden = false;
  } finally {
    if (generation === ctx.configGeneration) ctx.configPending = false;
    if (current(ctx)) syncDetails(ctx);
  }
}

async function saveClientDetails() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting || ctx.rotating) return;
  const values = draft(ctx), patch = changes(ctx), modal = ctx.modal;
  if (!values.name) {
    modal.querySelector('#client-name').setAttribute('aria-invalid', 'true');
    modal.querySelector('#client-name-error').textContent = 'Enter a device name.';
    modal.querySelector('#client-name').focus(); return;
  }
  if (values.egress_tunnel_id === '' || values.enabled && !values.wireguard_enabled && !values.reality_enabled) return;
  if (!Object.keys(patch).length) return;
  const affectsReality = (realityEnabled(peerById(ctx.id)) || patch.reality_enabled === true) && ['enabled', 'wireguard_enabled', 'reality_enabled'].some(key => key in patch);
  if (affectsReality && realityRecovery(ctx.id)) { message(modal.querySelector('#client-save-status'), 'Reload Reality status before another access change.', 'err'); return; }
  const recovery = affectsReality ? beginPeerRealityMutation(ctx) : null;
  ctx.saving = true;
  const controls = [...modal.querySelectorAll('#client-name, #client-enabled, #client-wireguard, #client-reality, [name="client-route-mode"], #client-target')];
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
    const savedPeer = await pending;
    if (!ctx.currentSession()) return;
    finishPeerRealityMutation(ctx, recovery);
    inventoryRevision++;
    const peer = peerById(ctx.id);
    if (peer) {
      Object.assign(peer, patch);
      if (savedPeer?.id === ctx.id) for (const key of ['reality_revision', 'reality_profile_status', 'reality_profile_downloadable']) if (key in savedPeer) peer[key] = savedPeer[key];
    }
    // A rename finishing in another dialog also invalidates this client's delivered filename.
    if (['name', 'enabled', 'wireguard_enabled', 'reality_enabled'].some(key => key in patch) && editor?.id === ctx.id && current(editor)) {
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
    if (peer) ctx.baseline = peerBaseline(peer);
    modal.querySelector('#client-name').value = ctx.baseline.name;
    modal.querySelector('#client-enabled').checked = ctx.baseline.enabled;
    modal.querySelector('#client-wireguard').checked = ctx.baseline.wireguard_enabled;
    modal.querySelector('#client-reality').checked = ctx.baseline.reality_enabled;
    const savedExit = ctx.baseline.egress_tunnel_id;
    modal.querySelectorAll('[name="client-route-mode"]').forEach(control => { control.checked = control.value === (savedExit ? 'tunnel' : 'shared'); });
    if (savedExit) {
      const target = modal.querySelector('#client-target');
      if (![...target.options].some(option => option.value === savedExit)) target.add(new Option('Saved exit · selection retained', savedExit));
      target.value = savedExit;
    }
    modal.querySelector('#client-name').removeAttribute('aria-invalid');
    modal.querySelector('#client-name-error').textContent = '';
    message(modal.querySelector('#client-save-status'), 'Settings saved. Apply new Reality access and routing changes; revoked access requires successful server reconciliation.', 'ok');
  } catch (err) {
    finishPeerRealityMutation(ctx, recovery, err);
    if (current(ctx)) message(modal.querySelector('#client-save-status'), err.message || 'Settings were not saved. Your edits are retained; try again.', 'err');
  } finally {
    if (writes.get(ctx.id) === entry) writes.delete(ctx.id);
    ctx.saving = false;
    if (current(ctx)) {
      controls.forEach(control => { control.disabled = false; });
      button.textContent = 'Save changes';
      syncDetails(ctx);
      if (modal.querySelector('#client-configuration').open && !ctx.configReady && !ctx.configPending) loadClientConfig(ctx);
    }
  }
}

async function inspectClientDelete() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting || ctx.rotating) return;
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
  if (realityEnabled(peerById(ctx.id)) && realityRecovery(ctx.id)) { message(ctx.modal.querySelector('#client-delete-status'), 'Reload Reality status before another access change.', 'err'); return; }
  const recovery = realityEnabled(peerById(ctx.id)) ? beginPeerRealityMutation(ctx) : null;
  ctx.deletePending = true;
  ctx.modal.querySelectorAll('.client-delete-actions button').forEach(button => { button.disabled = true; });
  message(ctx.modal.querySelector('#client-delete-status'), 'Deleting client…');
  try {
    const effects = await api.peers.remove(ctx.id);
    if (!ctx.currentSession()) return;
    inventoryRevision++;
    finishPeerRealityMutation(ctx, recovery);
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
    finishPeerRealityMutation(ctx, recovery, err);
    if (current(ctx)) message(ctx.modal.querySelector('#client-delete-status'), err.message || 'Client was not deleted. Try again.', 'err');
  } finally {
    ctx.deletePending = false;
    if (current(ctx)) ctx.modal.querySelectorAll('.client-delete-actions button').forEach(button => { button.disabled = false; });
  }
}

async function copyClientConfig() {
  const ctx = editor;
  if (!ctx || !current(ctx) || !ctx.configReady || !profileAvailable(ctx) || ctx.saving || ctx.rotating || Object.keys(changes(ctx)).some(key => ['name', 'enabled', 'wireguard_enabled', 'reality_enabled'].includes(key))) return;
  const text = ctx.modal.dataset.conf, generation = ctx.configGeneration;
  try {
    await navigator.clipboard.writeText(text);
    if (current(ctx) && generation === ctx.configGeneration) message(ctx.modal.querySelector('#client-config-status'), 'Configuration copied. Clipboard content remains outside the panel.', 'ok');
  } catch {
    if (current(ctx) && generation === ctx.configGeneration) message(ctx.modal.querySelector('#client-config-status'), ctx.profileMethod === 'wg' ? 'Cannot copy here. Use Download .conf or select the configuration text.' : 'Cannot copy here. Download the profile or select its text.', 'err');
  }
}


function selectClientProfile(method) {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.rotating || ctx.profileMethod === method) return;
  if (method === 'wg' ? !ctx.baseline.wireguard_enabled : !ctx.baseline.reality_enabled) return;
  clearClientConfig(ctx);
  ctx.profileMethod = method;
  ctx.modal.querySelector('#client-reality-confirmation').hidden = true;
  message(ctx.modal.querySelector('#client-reality-rotation-status'), '');
  syncDetails(ctx);
  if (ctx.modal.querySelector('#client-configuration').open) loadClientConfig(ctx);
}

function inspectRealityRotation() {
  const ctx = editor, peer = ctx && peerById(ctx.id);
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting || ctx.rotating || realityRecovery(ctx.id) || !Number.isInteger(peer?.reality_revision) || peer.reality_revision < 1) return;
  if (Object.keys(changes(ctx)).length) { message(ctx.modal.querySelector('#client-reality-rotation-status'), 'Save or discard edits before replacing a key.', 'err'); return; }
  ctx.rotationRevision = peer.reality_revision;
  ctx.modal.querySelector('#client-reality-confirmation').hidden = false;
  ctx.modal.querySelector('[data-act="cancel-reality-rotation"]').focus();
}

async function rotateReality() {
  const ctx = editor, peer = ctx && peerById(ctx.id);
  if (!ctx || !current(ctx) || ctx.saving || ctx.deleting || ctx.rotating || realityRecovery(ctx.id) || ctx.modal.querySelector('#client-reality-confirmation').hidden || !Number.isInteger(peer?.reality_revision) || peer.reality_revision < 1) return;
  if (Object.keys(changes(ctx)).length) return;
  const revision = ctx.rotationRevision;
  if (!Number.isInteger(revision) || revision < 1) return;
  ctx.rotating = true;
  const recovery = { currentSession: ctx.currentSession, pending: true, uncertain: false, error: '' };
  realityRecoveries.set(ctx.id, recovery);
  inventoryRevision++; realityEpoch++;
  clearClientConfig(ctx);
  const status = ctx.modal.querySelector('#client-reality-rotation-status');
  message(status, 'Reconciling revoked access. Existing connections may be interrupted…');
  ctx.modal.querySelectorAll('#client-reality-confirmation button').forEach(button => { button.disabled = true; });
  const previous = writes.get(ctx.id);
  const pending = (async () => {
    if (previous?.currentSession()) await previous.pending.catch(() => {});
    if (!ctx.currentSession()) return;
    return api.peers.rotateReality(ctx.id, revision);
  })();
  const entry = { pending, currentSession: ctx.currentSession };
  writes.set(ctx.id, entry);
  try {
    const updated = await pending;
    if (!ctx.currentSession()) return;
    if (updated?.id !== ctx.id || !Number.isInteger(updated.reality_revision) || updated.reality_revision <= revision || !['pending', 'guarded'].includes(updated.reality_profile_status)) throw new Error('The server did not confirm the replacement credential state.');
    if (peerById(ctx.id)) Object.assign(peerById(ctx.id), updated);
    if (realityRecoveries.get(ctx.id) === recovery) realityRecoveries.delete(ctx.id);
    inventoryRevision++;
    markDirty(); refresh();
    if (editor?.id === ctx.id && current(editor)) clearClientConfig(editor);
    if (!current(ctx)) return;
    ctx.modal.querySelector('#client-reality-confirmation').hidden = true;
    message(status, realityStatusText(peerById(ctx.id)) + ' The previous profile must not be reused.');
  } catch (err) {
    if (!ctx.currentSession()) return;
    // The backend may have durably revoked access before its reconciliation failed.
    realityEpoch++;
    recovery.pending = false;
    recovery.uncertain = true;
    recovery.error = `${err.message || 'Key replacement did not finish.'} Old access may already be revoked. Reload Reality status before retrying.`;
    inventoryRevision++;
    const active = editor?.id === ctx.id && current(editor) ? editor : null;
    if (active) {
      clearClientConfig(active);
      active.modal.querySelector('#client-reality-confirmation').hidden = true;
      message(active.modal.querySelector('#client-reality-rotation-status'), recovery.error, 'err');
    }
  } finally {
    if (writes.get(ctx.id) === entry) writes.delete(ctx.id);
    recovery.pending = false;
    ctx.rotating = false;
    if (editor?.id === ctx.id && current(editor)) syncDetails(editor);
    if (current(ctx)) {
      ctx.modal.querySelectorAll('#client-reality-confirmation button').forEach(button => { button.disabled = false; });
      syncDetails(ctx);
    }
  }
}

async function refreshRealityStatus() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.saving || ctx.rotating || ctx.refreshing || realityRecovery(ctx.id)?.pending) return;
  const status = ctx.modal.querySelector('#client-reality-rotation-status');
  if (Object.keys(changes(ctx)).length) { message(status, 'Save or discard edits before reloading this client.', 'err'); return; }
  ctx.refreshing = true;
  const revision = inventoryRevision, recovery = realityRecovery(ctx.id);
  clearClientConfig(ctx);
  message(status, 'Loading current Reality status…');
  try {
    const globalRecovery = realitySettingsRecovery();
    const [peers, settings] = await Promise.all([api.peers.list(), globalRecovery ? api.settings.get() : Promise.resolve(null)]);
    if (!current(ctx)) return;
    if (revision !== inventoryRevision || recovery !== realityRecovery(ctx.id) || Object.keys(changes(ctx)).length) { message(status, 'Client changed while loading. Your edits are retained; save or discard them before retrying.', 'err'); return; }
    const peer = peers.find(item => item.id === ctx.id);
    if (settings) { state.settings = settings; clearRealitySettingsRecovery(globalRecovery); }
    if (!peer) { realityRecoveries.delete(ctx.id); state.peers = peers; inventoryRevision++; closeModal(); refresh(); toast('Client no longer exists. Saved inventory reloaded.'); return; }
    const stored = peerById(ctx.id);
    if (stored) Object.assign(stored, peer);
    if (realityRecoveries.get(ctx.id) === recovery) realityRecoveries.delete(ctx.id);
    inventoryRevision++;
    refresh();
    modalPeerDetails(ctx.id, true, 'reality');
  } catch (err) {
    if (current(ctx)) message(status, err.message || 'Could not reload Reality status. Try again.', 'err');
  } finally { ctx.refreshing = false; }
}

async function downloadRealityJSON() {
  const ctx = editor;
  if (!ctx || !current(ctx) || ctx.profileMethod !== 'reality' || !ctx.configReady || !profileAvailable(ctx) || ctx.jsonPending || Object.keys(changes(ctx)).length) return;
  const generation = ctx.configGeneration, epoch = realityEpoch, revision = peerById(ctx.id)?.reality_revision;
  ctx.jsonPending = true;
  ctx.modal.querySelector('[data-act="download-reality-json"]').disabled = true;
  message(ctx.modal.querySelector('#client-config-status'), 'Loading sing-box client profile…');
  try {
    const result = await api.peers.realityClientConfig(ctx.id);
    if (!current(ctx) || generation !== ctx.configGeneration || epoch !== realityEpoch || !profileAvailable(ctx) || revision !== peerById(ctx.id)?.reality_revision || Object.keys(changes(ctx)).length) return;
    checkRealityDelivery(result, revision);
    ctx.configRealityState = result.realityState;
    const profile = JSON.parse(result.text);
    if (!profile || !Array.isArray(profile.inbounds) || !Array.isArray(profile.outbounds)) throw new Error('The server did not return a sing-box client profile.');
    if (ctx.revokeDownload) ctx.revokeDownload();
    ctx.revokeDownload = download(result.filename || 'razdacha-reality.json', result.text, 'application/json');
    onModalCleanup(ctx.revokeDownload);
    message(ctx.modal.querySelector('#client-config-status'), 'Sing-box profile downloaded. Import it into a compatible sing-box app.', 'ok');
  } catch (err) { if (current(ctx) && generation === ctx.configGeneration && epoch === realityEpoch) message(ctx.modal.querySelector('#client-config-status'), err.message || 'Client profile download failed. Retry.', 'err'); }
  finally { ctx.jsonPending = false; if (current(ctx)) syncDetails(ctx); }
}

export const actions = {
  'add-peer': modalAddPeer,
  'go-to-client-apply': () => {
    const ctx = editor;
    if (!ctx || !current(ctx) || ctx.saving || ctx.rotating || ctx.deleting || Object.keys(changes(ctx)).length) return;
    closeModal();
    document.querySelector('#btn-apply')?.focus();
  },
  'create-peer': createPeer,
  'menu-peer': (id, button) => openMenu(button, [
    { act: 'peer-details', id, label: 'Settings' },
    { act: 'peer-configuration', id, label: realityEnabled(peerById(id)) ? 'Connection profiles' : 'WireGuard configuration' },
    { act: 'peer-delete-preview', id, label: 'Delete…', danger: true },
  ]),
  'peer-details': id => modalPeerDetails(id),
  'peer-configuration': id => modalPeerDetails(id, true),
  'peer-delete-preview': id => { modalPeerDetails(id); inspectClientDelete(); },
  'save-client-details': saveClientDetails,
  'retry-client-tunnels': () => loadClientTunnels(),
  'retry-client-config': () => loadClientConfig(),
  'select-client-wg': () => selectClientProfile('wg'),
  'select-client-reality': () => selectClientProfile('reality'),
  'inspect-reality-rotation': inspectRealityRotation,
  'cancel-reality-rotation': () => { if (editor && current(editor) && !editor.rotating) editor.modal.querySelector('#client-reality-confirmation').hidden = true; },
  'confirm-reality-rotation': rotateReality,
  'refresh-reality-status': refreshRealityStatus,
  'inspect-client-delete': inspectClientDelete,
  'cancel-client-delete': cancelClientDelete,
  'confirm-client-delete': confirmClientDelete,
  'copy-client-config': copyClientConfig,
  'download-reality-json': downloadRealityJSON,
  'download-conf': () => {
    const ctx = editor;
    if (!ctx || !current(ctx) || !ctx.configReady || !profileAvailable(ctx) || ctx.saving || ctx.rotating || Object.keys(changes(ctx)).some(key => ['name', 'enabled', 'wireguard_enabled', 'reality_enabled'].includes(key))) return;
    if (ctx.revokeDownload) ctx.revokeDownload();
    ctx.revokeDownload = download(ctx.modal.dataset.confName || 'peer.conf', ctx.modal.dataset.conf);
    onModalCleanup(ctx.revokeDownload);
    message(ctx.modal.querySelector('#client-config-status'), ctx.profileMethod === 'wg' ? 'Configuration downloaded. Import it into WireGuard.' : 'Reality profile downloaded. Import it into a compatible app.', 'ok');
  },
};

function routingSummary(peer) {
  if (!peer.egress_tunnel_id) return '<div class="client-route-name">Shared rules</div><div class="row-meta">Otherwise direct</div>';
  if (state.missing.has('tunnels') || state.missing.has('peer-tunnels')) return '<div class="client-route-name">Selected exit</div><div class="row-meta">Exit details unavailable. Open settings to retry.</div>';
  const tunnel = tunnelById(peer.egress_tunnel_id);
  const unavailable = !tunnel || !tunnel.enabled || emptyPool(tunnel);
  return `<div class="client-route-name">Internet → ${esc(tunnel ? tunnel.name : 'Missing tunnel')}</div><div class="row-meta${unavailable ? ' client-route-blocked' : ''}">${unavailable ? 'Blocked target · no direct fallback' : 'IPv4 TCP/UDP + DNS · no direct fallback'}</div>`;
}
