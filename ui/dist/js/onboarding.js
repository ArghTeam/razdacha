import * as api from './api.js';
import * as qr from './qr.js';
import { state, openModal, closeModal, modalShell, currentModalRevision, isCurrentModal, onModalCleanup, toastError, modalOpen, markDirty } from './shell.js';
import { esc, download } from './util.js';

let wizard = null;
let navigation = 0;
export function navigationChanged() { navigation++; }
const current = ctx => wizard === ctx && ctx.session() && isCurrentModal(ctx.revision);

function render(ctx) {
  if (!current(ctx)) return;
  const locked = ctx.status.prepared && !ctx.status.applied;
  const step = ctx.step;
  const steps = ['Choose', 'Review & apply', 'Connect client'];
  const indicator = `<ol class="setup-steps" aria-label="Setup progress">${steps.map((label, index) => `<li ${index === step ? 'aria-current="step"' : ''}><span>${index + 1}</span>${label}</li>`).join('')}</ol>`;
  let body, foot;
  if (step === 0) {
    body = `${indicator}<h3>Choose how to start</h3><p class="screen-sub">Free exits are operated by third parties. Availability, video and calls are not guaranteed. You can add your own tunnels later.</p>
      ${ctx.status.read_only ? '<p class="hint">This server already has routing policy. Setup keeps it unchanged and helps deliver your existing client profile. Manage shared tunnels and countries in the normal panel.</p>' : ''}
      <fieldset class="setup-choices" ${ctx.busy || locked ? 'disabled' : ''}><legend>Optional free tunnels</legend>
        <label><input type="radio" name="setup-mode" value="free" ${ctx.mode === 'free' ? 'checked' : ''} ${ctx.status.read_only ? 'disabled' : ''}> Use free tunnels <span class="hint">Selective starter resources for this client only</span></label>
        <label><input type="radio" name="setup-mode" value="skip" ${ctx.mode === 'skip' ? 'checked' : ''}> ${ctx.status.read_only ? 'Keep current configuration' : 'Skip free setup'} <span class="hint">Existing tunnels and routes are preserved</span></label>
      </fieldset>
      <div class="field"><label for="setup-country">Exit country</label><input id="setup-country" type="text" value="${esc(ctx.country)}" maxlength="4" autocomplete="off" ${ctx.mode !== 'free' || ctx.status.read_only || locked || ctx.busy ? 'disabled' : ''}><div class="hint">Auto, or one two-letter country code. RU/BY remain excluded.</div></div>`;
    foot = '<button class="btn" data-act="close-modal">Later</button><button class="btn btn-primary" data-act="setup-review">Review setup</button>';
  } else if (step === 1) {
    const peer = state.peers.find(p => p.id === ctx.status.peer_id);
    body = `${indicator}<h3>Review before applying</h3><dl class="setup-review"><dt>Client</dt><dd>${esc(peer?.name || 'Initial client')}</dd><dt>Choice</dt><dd>${ctx.mode === 'free' ? 'Free selective starter rules' : 'Keep existing policy; no free setup changes'}</dd><dt>Country</dt><dd>${ctx.mode === 'free' ? esc(ctx.country) : 'Unchanged'}</dd></dl>
      <p class="hint">Only this explicit action saves and applies setup through the server's guarded publication. Other clients are not assigned. Unrelated pending changes must be reviewed separately.</p>
      ${locked ? '<p class="hint">Setup choices are saved. Retry Apply to finish this configuration; closing does not discard pending intent.</p>' : ''}`;
    foot = `<button class="btn" data-act="setup-back">Back</button><button class="btn btn-primary" data-act="setup-apply">${locked ? 'Retry Apply' : 'Apply setup'}</button>`;
  } else {
    const peer = state.peers.find(p => p.id === ctx.status.peer_id);
    body = `${indicator}<h3>Your client is ready to import</h3><p class="screen-sub">Configuration accepted. Tunnel connectivity, video and calls are not verified. Import this saved profile into WireGuard.</p><p><strong>${esc(peer?.name || 'Initial client')}</strong></p>
      ${ctx.status.pool_enabled && ctx.status.candidate_count === 0 ? '<div class="client-apply-note" role="status"><strong>No eligible free exits are available.</strong><span>Resources assigned to this pool reject until it refreshes; they do not fall back to direct routing. Open Tunnels and explicitly Refresh the built-in pool, then check its runtime health.</span></div>' : ''}
      <button class="btn" data-act="setup-profile">${ctx.config ? 'Reload saved profile' : 'Show QR & download profile'}</button>
      <div id="setup-profile-result" class="setup-profile" ${ctx.config ? '' : 'hidden'}><canvas id="setup-qr" aria-label="Private WireGuard profile QR"></canvas><p class="hint">This QR contains private connection keys. Share only with this client.</p><button class="btn" data-act="setup-download" ${ctx.config ? '' : 'disabled'}>Download .conf</button></div>`;
    foot = '<button class="btn" data-act="close-modal">Later</button><button class="btn btn-primary" data-act="setup-finish">Finish setup</button>';
  }
  ctx.modal.querySelectorAll('canvas').forEach(canvas => { canvas.width = 0; canvas.height = 0; });
  ctx.modal.innerHTML = modalShell('Set up razdacha', body + '<p id="setup-status" class="client-status" role="status" aria-live="polite"></p>', foot);
  ctx.modal.querySelector('h2').id = 'modal-title';
  ctx.modal.setAttribute('aria-labelledby', 'modal-title');
  if (ctx.config && step === 2) {
    try { qr.draw(ctx.modal.querySelector('#setup-qr'), ctx.config.text); }
    catch { setStatus(ctx, 'QR unavailable. Download the saved profile instead.', true); }
  }
  ctx.modal.querySelectorAll('input[name="setup-mode"]').forEach(input => input.addEventListener('change', () => {
    ctx.mode = input.value;
    const country = ctx.modal.querySelector('#setup-country');
    country.disabled = ctx.mode !== 'free';
  }));
  if (ctx.busy) busyControls(ctx, true);
  ctx.modal.querySelector('h2').tabIndex = -1;
  ctx.modal.querySelector('h2').focus();
}

function setStatus(ctx, text, error = false) {
  if (!current(ctx)) return;
  const element = ctx.modal.querySelector('#setup-status');
  element.textContent = text;
  element.classList.toggle('err', error);
  element.setAttribute('role', error ? 'alert' : 'status');
}

function busyControls(ctx, value) {
  ctx.busy = value;
  ctx.modal.querySelectorAll('button').forEach(button => { if (button.dataset.act !== 'close-modal') button.disabled = value; });
}

function open(status) {
  const ctx = { status, session: api.session.guard(), step: status.applied ? 2 : status.prepared ? 1 : 0, mode: status.read_only ? 'skip' : status.mode || 'skip', country: status.country || 'AUTO', busy: false, config: null, revokes: [] };
  openModal(modalShell('Set up razdacha', '<p>Loading setup…</p>', ''));
  ctx.modal = document.querySelector('#modal');
  ctx.revision = currentModalRevision();
  wizard = ctx;
  onModalCleanup(() => { ctx.config = null; ctx.revokes.forEach(revoke => revoke()); ctx.revokes = []; if (wizard === ctx) wizard = null; });
  render(ctx);
}

export async function maybePending() {
  const session = api.session.guard();
  const revision = currentModalRevision(), screen = state.screen, route = navigation;
  try { const status = await api.onboarding.get(); if (session() && revision === currentModalRevision() && screen === state.screen && route === navigation && status.pending && !modalOpen()) open(status); }
  catch (error) { if (session() && !error.missing && error.status !== 401) toastError(error, 'Setup status unavailable'); }
}

async function reopen() {
  const session = api.session.guard();
  const revision = currentModalRevision(), screen = state.screen, route = navigation;
  try { const status = await api.onboarding.reopen(); if (session() && revision === currentModalRevision() && screen === state.screen && route === navigation) open(status); }
  catch (error) { if (session()) toastError(error, 'Setup could not be opened'); }
}

async function applySetup() {
  const ctx = wizard;
  if (!ctx || !current(ctx) || ctx.busy) return;
  busyControls(ctx, true);
  setStatus(ctx, ctx.status.prepared ? 'Checking and applying saved setup…' : 'Saving setup and acquiring candidates…');
  try {
    if (!ctx.status.prepared) {
      ctx.status = await api.onboarding.configure(ctx.mode, ctx.country);
      if (!current(ctx)) return;
      markDirty();
    }
    setStatus(ctx, 'Applying configuration. Sources may need time to load…');
    await api.onboarding.apply();
    if (!current(ctx)) return;
    ctx.status.applied = true;
    try { const latest = await api.onboarding.get(); if (current(ctx)) ctx.status = latest; } catch { /* Accepted Apply remains accepted if inventory status is temporarily unavailable. */ }
    if (!current(ctx)) return;
    ctx.step = 2;
    render(ctx);
  } catch (error) { if (current(ctx)) setStatus(ctx, error.message || 'Setup was not applied. Retry when sources are ready.', true); }
  finally { if (current(ctx)) { busyControls(ctx, false); if (ctx.status.prepared && !ctx.status.applied) ctx.modal.querySelector('[data-act="setup-apply"]').textContent = 'Retry Apply'; } }
}

async function profile() {
  const ctx = wizard;
  if (!ctx || !current(ctx) || ctx.busy || !ctx.status.applied) return;
  busyControls(ctx, true); setStatus(ctx, 'Loading the saved private profile…');
  try { const config = await api.peers.config(ctx.status.peer_id); if (!current(ctx)) return; ctx.config = config; render(ctx); setStatus(ctx, 'Saved profile ready. Import it into WireGuard.'); }
  catch (error) { if (current(ctx)) setStatus(ctx, error.message || 'Profile unavailable. Retry.', true); }
  finally { if (current(ctx)) busyControls(ctx, false); }
}

async function finish() {
  const ctx = wizard;
  if (!ctx || !current(ctx) || ctx.busy) return;
  busyControls(ctx, true);
  try { await api.onboarding.finish(); if (current(ctx)) closeModal(); }
  catch (error) { if (current(ctx)) { busyControls(ctx, false); setStatus(ctx, error.message || 'Setup was not completed. Retry.', true); } }
}

export const actions = {
  'open-setup': reopen,
  'setup-review': () => {
    const ctx = wizard; if (!ctx || !current(ctx) || ctx.busy) return;
    ctx.country = ctx.modal.querySelector('#setup-country').value.trim().toUpperCase() || 'AUTO';
    if (ctx.mode === 'free' && !/^(AUTO|[A-Z]{2})$/.test(ctx.country)) { setStatus(ctx, 'Enter Auto or one two-letter country code.', true); return; }
    if (ctx.mode === 'free' && ['RU', 'BY'].includes(ctx.country)) { setStatus(ctx, 'RU and BY are excluded. Choose Auto or another country.', true); return; }
    ctx.step = 1; render(ctx);
  },
  'setup-back': () => { const ctx = wizard; if (ctx && current(ctx) && !ctx.busy) { ctx.step = 0; render(ctx); } },
  'setup-apply': applySetup,
  'setup-profile': profile,
  'setup-finish': finish,
  'setup-download': () => { const ctx = wizard; if (ctx && current(ctx) && ctx.config && !ctx.busy) ctx.revokes.push(download(ctx.config.filename || 'client.conf', ctx.config.text)); },
};
