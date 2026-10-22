// Each section owns one save boundary and modal-local drafts.
import * as api from '../api.js';
import { state, openModal, modalShell, notice, markDirty, applyDocTitle,
  currentModalRevision, isCurrentModal, onModalCleanup } from '../shell.js';
import { esc, intervalSeconds } from '../util.js';

// Configuration endpoints read/modify/write complete records. Serialize saves
// across dialogs, and never dispatch queued intent in a new session.
let configWrite = Promise.resolve();
function enqueueSave(request, sameSession, onStart) {
  const result = configWrite.then(() => {
    if (!sameSession()) throw new Error('The session changed before this save started');
    onStart();
    return request();
  });
  configWrite = result.then(() => undefined, () => undefined);
  return result;
}
const names = ['Network', 'Schedules', 'Telegram', 'Backup'];
const listIntervals = [[3600, 'Every hour'], [21600, 'Every 6 hours'], [86400, 'Daily'], [604800, 'Weekly']];
const poolIntervals = [[1800, 'Every 30 minutes'], [3600, 'Every hour'], [21600, 'Every 6 hours'], [43200, 'Every 12 hours'], [86400, 'Daily']];
const checkIntervals = [[60, 'Every minute'], [120, 'Every 2 minutes'], [300, 'Every 5 minutes'], [900, 'Every 15 minutes']];
const backupIntervals = [[6, 'Every 6 hours'], [24, 'Daily'], [168, 'Weekly']];
function options(items, value, unit = 'seconds') {
  const current = Number(value);
  const all = items.some(([v]) => v === current) ? items : [[current, `Current: ${current} ${unit}`], ...items];
  return all.map(([v, label]) => `<option value="${v}" ${v === current ? 'selected' : ''}>${esc(label)}</option>`).join('');
}
function field(id, label, attrs, hint = '') {
  return `<div class="field"><label for="${id}">${label}</label><input id="${id}" ${attrs} aria-describedby="${id}-hint ${id}-error"><div class="hint" id="${id}-hint">${hint}</div><span class="settings-field-error" id="${id}-error"></span></div>`;
}
function select(id, label, opts, hint = '') {
  return `<div class="field"><label for="${id}">${label}</label><select id="${id}">${opts}</select><div class="hint">${hint}</div></div>`;
}
function section(name, intro, content, extra = '') {
  const key = name.toLowerCase();
  return `<form class="settings-section" id="settings-${key}" data-section="${key}" novalidate ${key !== 'network' ? 'hidden' : ''}><header><h3>${name}</h3><p>${intro}</p></header><fieldset>${content}</fieldset><div class="settings-status" role="status" aria-live="polite"></div><div class="settings-actions"><button class="btn btn-primary" type="submit">Save ${key}</button>${extra}</div></form>`;
}
export function modalSettings() {
  const s = state.settings;
  if (!s) {
    openModal(modalShell('Settings', notice('Settings unavailable', 'Close this dialog and reload the panel to try again.'), '<button class="btn" data-act="close-modal">Close</button>'));
    return;
  }
  const body = `<div class="settings-hub"><p class="settings-intro">Save each section separately. Changes stay in this dialog until you save or close it.</p><nav class="settings-nav" aria-label="Settings sections">${names.map((name, i) => `<button type="button" class="btn" data-settings-section="${name.toLowerCase()}" aria-controls="settings-${name.toLowerCase()}" aria-current="${i === 0}">${name}<span class="settings-draft" aria-hidden="true"></span></button>`).join('')}</nav>
  ${section('Network', 'Save network settings, then use Apply in the panel to activate them.', `<div class="two-col">
  ${field('s-port', 'WireGuard port', `type="number" required min="1" max="65535" step="1" value="${esc(s.wg_listen_port)}"`)}
  ${field('s-host', 'External address', `type="text" value="${esc(s.endpoint_host)}"`, 'Hostname or IP address used by clients. Client configurations are unavailable while this is empty.')}
  ${field('s-mtu', 'Client MTU', `type="number" required min="1280" max="1420" step="1" value="${esc(s.client_mtu)}"`, 'Default: 1280. Change only when your network requires it.')}
  ${field('s-dns', 'DNS upstream', `type="text" required value="${esc(s.dns_upstream)}"`)}</div>`)}
  ${section('Schedules', 'Timers use saved intervals automatically. Use Apply to also update the active configuration record.', `${select('s-int', 'List refresh', options(listIntervals, intervalSeconds(s.list_update_interval)))}${select('s-pool', 'Pool catalog refresh', options(poolIntervals, s.pool_update_interval || 3600), 'Minimum: 30 minutes. Used when the built-in pool is enabled.')}${select('s-check', 'Tunnel health checks', options(checkIntervals, s.tunnel_check_interval || 120), 'Notifications confirm changes across three checks.')}`)}
  ${section('Telegram', 'The shared delivery channel for notifications and encrypted backups. Saving takes effect immediately.', `<p class="hint">Create a bot with @BotFather, add it to your chat and enter the chat ID.</p>${field('s-ntf-chat', 'Chat ID', 'type="text" autocomplete="off"', 'For example: -1001234567890.')}${field('s-ntf-token', 'Bot token', 'type="password" autocomplete="new-password"', '<span id="s-ntf-secret">Loading saved token status…</span> Leave empty to keep the saved token.')}<label class="settings-check" for="s-ntf-on"><input type="checkbox" id="s-ntf-on"> Send tunnel notifications</label><p class="hint">Test sends a message with saved Telegram settings, even when notifications are off. Save edits first.</p>`, '<button type="button" class="btn" data-delivery="telegram">Test saved channel</button>')}
  ${section('Backup', 'A full backup contains settings, tunnels, rules and every client private key. Protect it like a password.', `<div class="settings-download"><a class="btn" href="${esc(api.backup.DOWNLOAD_PATH)}" download>Download full backup</a><p class="hint">The downloaded file is unencrypted and contains access to the entire VPN. Download uses saved state without saving this form.</p></div>${field('s-bak-phrase', 'Encryption passphrase', 'type="password" autocomplete="new-password"', '<span id="s-bak-secret">Loading saved passphrase status…</span> At least 8 characters. Leave empty to keep the saved passphrase.')}<p class="settings-warning">If you lose the passphrase, encrypted backups cannot be restored. Store it separately from the Telegram chat.</p>${select('s-bak-int', 'Backup delivery interval', options(backupIntervals, 24, 'hours'))}<label class="settings-check" for="s-bak-on"><input type="checkbox" id="s-bak-on"> Send encrypted backups to Telegram</label><p class="hint">Uses the saved Telegram channel. Save Telegram edits in its section first. Send now uses saved settings, even when the schedule is off.</p><p class="hint" id="s-bak-last"></p>`, '<button type="button" class="btn" data-delivery="backup">Send saved backup now</button>')}</div>`;
  openModal(modalShell('Settings', body, '<span class="settings-close-hint">Closing discards unsaved edits only.</span><button class="btn" data-act="close-modal">Close</button>'), mount);
}
function mount(modal) {
  const revision = currentModalRevision();
  const sameSession = api.session.guard();
  const current = () => sameSession() && isCurrentModal(revision);
  const $ = selector => modal.querySelector(selector);
  let notifyCfg = null, backupCfg = null;
  const saved = new Map();
  const forms = [...modal.querySelectorAll('.settings-section')];
  const sectionFields = {
    network: { wg_listen_port: 's-port', endpoint_host: 's-host', client_mtu: 's-mtu', dns_upstream: 's-dns' },
    schedules: { list_update_interval: 's-int', pool_update_interval: 's-pool', tunnel_check_interval: 's-check' },
    telegram: { enabled: 's-ntf-on', chat_id: 's-ntf-chat' },
    backup: { enabled: 's-bak-on', interval_hours: 's-bak-int' },
  };
  const readSection = kind => Object.fromEntries(Object.entries(sectionFields[kind]).map(([key, id]) => {
    const input = $(`#${id}`);
    return [key, input.type === 'checkbox' ? input.checked : input.type === 'number' || input.tagName === 'SELECT' ? Number(input.value) : input.value.trim()];
  }));
  const baseline = Object.fromEntries(Object.keys(sectionFields).map(kind => [kind, readSection(kind)]));
  const changed = (kind, body) => Object.fromEntries(Object.entries(body).filter(([key, value]) => value !== baseline[kind][key]));

  const values = form => JSON.stringify([...form.querySelectorAll('input, select')].map(el => el.type === 'checkbox' ? el.checked : el.value));
  const dirty = form => saved.get(form) !== values(form);
  function status(form, text, kind = '') {
    const box = form.querySelector('.settings-status');
    box.textContent = text; box.className = `settings-status ${kind}`;
  }
  function update(form) {
    const delivery = form.querySelector('[data-delivery]');
    if (delivery) delivery.disabled = !saved.has(form) || dirty(form) || form.dataset.busy === 'true';
    $(`[data-settings-section="${form.dataset.section}"] .settings-draft`).textContent = saved.has(form) && dirty(form) ? ' •' : '';
  }
  function remember(form) { saved.set(form, values(form)); update(form); }
  function busy(form, value) {
    form.dataset.busy = String(value);
    form.querySelector('fieldset').disabled = value;
    form.querySelector('[type="submit"]').disabled = value;
    update(form);
  }
  // Native focus scrolling does not account for the sticky dialog chrome.
  const revealFocus = () => requestAnimationFrame(() => {
    if (!current()) return;
    const target = document.activeElement;
    if (!target || !$('.modal-body').contains(target)) return;
    const bounds = target.getBoundingClientRect();
    const top = $('.modal-head').getBoundingClientRect().bottom + 12;
    const bottom = $('.modal-foot').getBoundingClientRect().top - 12;
    if (bounds.bottom > bottom) $('.modal-body').scrollTop += bounds.bottom - bottom;
    else if (bounds.top < top) $('.modal-body').scrollTop -= top - bounds.top;
  });
  modal.addEventListener('focusin', revealFocus);
  onModalCleanup(() => {
    modal.removeEventListener('focusin', revealFocus);
    saved.clear(); notifyCfg = null; backupCfg = null;
  });
  for (const button of modal.querySelectorAll('[data-settings-section]')) button.addEventListener('click', () => {
    for (const form of forms) form.hidden = form.dataset.section !== button.dataset.settingsSection;
    for (const nav of modal.querySelectorAll('[data-settings-section]')) nav.setAttribute('aria-current', String(nav === button));
    $('.modal-body').scrollTop = 0;
  });
  for (const form of forms) {
    if (['telegram', 'backup'].includes(form.dataset.section)) busy(form, true);
    else remember(form);
    form.addEventListener('input', () => { status(form, dirty(form) ? 'Unsaved changes in this section.' : 'No unsaved changes.'); update(form); });
    form.addEventListener('submit', event => { event.preventDefault(); save(form); });
    form.querySelector('[data-delivery]')?.addEventListener('click', () => deliver(form));
  }
  async function load(kind) {
    const form = $(`#settings-${kind}`);
    busy(form, true); status(form, 'Loading saved settings…');
    try {
      const cfg = await (kind === 'telegram' ? api.notify.get() : api.backup.get());
      if (!current()) return;
      if (kind === 'telegram') {
        notifyCfg = cfg; $('#s-ntf-chat').value = cfg.chat_id || ''; $('#s-ntf-on').checked = !!cfg.enabled;
        $('#s-ntf-secret').textContent = cfg.token_set ? 'A token is saved.' : 'No token saved.';
      } else {
        backupCfg = cfg; $('#s-bak-on').checked = !!cfg.enabled;
        $('#s-bak-int').innerHTML = options(backupIntervals, cfg.interval_hours || 24, 'hours');
        $('#s-bak-secret').textContent = cfg.passphrase_set ? 'A passphrase is saved.' : 'No passphrase saved.';
        $('#s-bak-last').textContent = backupHistory(cfg);
      }
      baseline[kind] = readSection(kind);
      remember(form); status(form, 'Saved settings loaded.');
    } catch (err) {
      if (!current()) return;
      status(form, `Could not load settings: ${err.message}`, 'err');
      const retry = document.createElement('button');
      retry.type = 'button'; retry.className = 'btn'; retry.textContent = 'Retry loading';
      retry.addEventListener('click', () => load(kind));
      form.querySelector('.settings-status').append(' ', retry);
    } finally { if (current() && saved.has(form)) busy(form, false); }
  }
  function invalid(form, input, message) {
    input.setAttribute('aria-invalid', 'true'); $(`#${input.id}-error`).textContent = message;
    status(form, message, 'err'); input.focus();
  }
  function payload(form) {
    for (const el of form.querySelectorAll('[aria-invalid]')) el.removeAttribute('aria-invalid');
    for (const el of form.querySelectorAll('.settings-field-error')) el.textContent = '';
    for (const el of form.querySelectorAll('input')) if (!el.checkValidity() || (el.required && !el.value.trim())) {
      const validity = el.validity;
      const reason = validity.rangeUnderflow ? `Enter a value of at least ${el.min}.`
        : validity.rangeOverflow ? `Enter a value no greater than ${el.max}.`
        : validity.badInput || validity.stepMismatch ? 'Enter a whole number.'
        : 'Enter a value.';
      invalid(form, el, `${el.labels[0].textContent}: ${reason}`); return null;
    }
    const kind = form.dataset.section;
    if (kind === 'network' || kind === 'schedules') return changed(kind, readSection(kind));
    if (kind === 'telegram') {
      const body = { enabled: $('#s-ntf-on').checked, chat_id: $('#s-ntf-chat').value.trim() };
      const token = $('#s-ntf-token').value.trim();
      if (body.enabled && (!body.chat_id || (!token && !notifyCfg?.token_set))) {
        invalid(form, !body.chat_id ? $('#s-ntf-chat') : $('#s-ntf-token'), 'Enter a chat ID and bot token before enabling notifications.'); return null;
      }
      if (token) body.token = token;
      return changed(kind, body);
    }
    const body = { enabled: $('#s-bak-on').checked, interval_hours: Number($('#s-bak-int').value) };
    const phrase = $('#s-bak-phrase').value.trim();
    if (phrase && [...phrase].length < 8) { invalid(form, $('#s-bak-phrase'), 'Use at least 8 characters for the encryption passphrase.'); return null; }
    if (body.enabled && !phrase && !backupCfg?.passphrase_set) { invalid(form, $('#s-bak-phrase'), 'Set an encryption passphrase before enabling backup delivery.'); return null; }
    if (body.enabled && (!notifyCfg?.token_set || !notifyCfg?.chat_id)) { status(form, 'Save a bot token and chat ID in Telegram before enabling backup delivery.', 'err'); return null; }
    if (phrase) body.passphrase = phrase;
    return changed(kind, body);
  }
  async function save(form) {
    if (form.dataset.busy === 'true') return;
    const body = payload(form); if (!body) return;
    const kind = form.dataset.section;
    busy(form, true); status(form, 'Waiting to save this section…');
    try {
      const res = await enqueueSave(() => kind === 'telegram' ? api.notify.save(body) : kind === 'backup' ? api.backup.save(body) : api.settings.update(body), sameSession, () => { if (current()) status(form, 'Saving this section…'); });
      if (!sameSession()) return;
      if (kind === 'network' || kind === 'schedules') {
        const confirmed = Object.fromEntries(Object.keys(sectionFields[kind]).map(key => [key, res?.[key] ?? body[key] ?? baseline[kind][key]]));
        state.settings = { ...state.settings, ...confirmed };
        if (current()) {
          for (const [key, id] of Object.entries(sectionFields[kind])) $(`#${id}`).value = confirmed[key];
          baseline[kind] = confirmed;
        }
        applyDocTitle(); markDirty();
      }
      if (!current()) return;
      if (kind === 'telegram') { notifyCfg = res; $('#s-ntf-chat').value = res.chat_id; $('#s-ntf-on').checked = !!res.enabled; $('#s-ntf-token').value = ''; $('#s-ntf-secret').textContent = res.token_set ? 'A token is saved.' : 'No token saved.'; }
      if (kind === 'backup') { backupCfg = res; $('#s-bak-on').checked = !!res.enabled; $('#s-bak-int').innerHTML = options(backupIntervals, res.interval_hours, 'hours'); $('#s-bak-phrase').value = ''; $('#s-bak-secret').textContent = res.passphrase_set ? 'A passphrase is saved.' : 'No passphrase saved.'; }
      baseline[kind] = readSection(kind);
      remember(form);
      status(form, kind === 'network' ? `Network saved. Use Apply in the panel to activate changes.${res?.requires_client_reconfig ? ' Download and redistribute updated client configurations.' : ''}` : `${names.find(name => name.toLowerCase() === kind)} saved.${kind === 'schedules' ? ' Timers update automatically. Use Apply to update the active configuration record.' : ' No Apply is needed.'}`, 'ok');
    } catch (err) { if (current()) status(form, `Save could not be confirmed: ${err.message}. Your draft is retained. This request did not save other sections.`, 'err'); }
    finally { if (current()) busy(form, false); }
  }
  function backupHistory(cfg) {
    return `${cfg.last_sent_at ? `Last sent: ${new Date(cfg.last_sent_at).toLocaleString()}.` : 'No backup has been sent yet.'}${cfg.last_error ? ` Last delivery error: ${cfg.last_error}` : ''}`;
  }
  async function refreshDeliveryHistory() {
    if (!current()) return;
    try {
      const cfg = await api.backup.get();
      if (current()) $('#s-bak-last').textContent = backupHistory(cfg);
    } catch {
      if (current()) $('#s-bak-last').textContent = 'Delivery history could not be refreshed. Reopen Settings to retry.';
    }
  }
  async function deliver(form) {
    if (dirty(form) || form.dataset.busy === 'true') return;
    const telegram = form.dataset.section === 'telegram';
    if (!notifyCfg?.token_set || !notifyCfg?.chat_id || (!telegram && !backupCfg?.passphrase_set)) {
      status(form, 'Save the Telegram channel and, for backups, an encryption passphrase before sending.', 'err'); return;
    }
    busy(form, true); status(form, 'Sending with saved settings…');
    try {
      await (telegram ? api.notify.test() : api.backup.send());
      if (current()) status(form, telegram ? 'Test message sent. Settings were not changed.' : 'Encrypted backup sent. Settings were not changed.', 'ok');
    } catch (err) { if (current()) status(form, `Delivery failed: ${err.message}. Saved settings are unchanged.`, 'err'); }
    finally {
      if (!telegram) await refreshDeliveryHistory();
      if (current()) busy(form, false);
    }
  }
  load('telegram'); load('backup');
}
export const actions = {};
