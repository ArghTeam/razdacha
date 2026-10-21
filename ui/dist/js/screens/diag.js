/* Supported diagnostics and allowlisted summary reports; unavailable viewers stay explicit. */
import * as api from '../api.js';
import { state, toast, refresh } from '../shell.js';
import { esc, download } from '../util.js';

export const title = 'Diagnostics';
let loadRevision = 0;
let run = null;
let runError = null;
function activeRun() {
  if (run && !run.currentSession()) run = null;
  return run;
}

export async function load() {
  const currentSession = api.session.guard();
  const revision = ++loadRevision;
  const current = () => currentSession() && revision === loadRevision;
  try {
    const version = await api.version.get();
    if (!current()) return;
    state.version = version;
  } catch {
    if (!current()) return;
    state.version = null;
  }
  if (activeRun()) return;
  try {
    const result = await api.diag.get();
    if (current() && !activeRun()) state.diag = result;
  } catch (err) {
    if (current()) throw err;
  }
}

const CHECK_ICON = { ok: '✓', warn: '⚠', error: '✕', unknown: '?' };
const OVERALL_TEXT = { ok: 'All checks passed', warn: 'Warnings found', error: 'Errors found', unknown: 'Status unknown' };
function worst(checks) {
  let result = 'unknown';
  for (const check of checks || []) {
    if (check.status === 'error') return 'error';
    if (check.status === 'warn') result = 'warn';
    else if (check.status === 'ok' && result !== 'warn') result = 'ok';
  }
  return result;
}

function unavailableViewers() {
  return `<div class="diag-unavailable" aria-label="Unavailable diagnostic viewers"><p><strong>Logs</strong><span>Log viewing is unavailable in this version.</span></p><p><strong>Generated configuration</strong><span>Configuration viewing is unavailable in this version.</span></p></div>`;
}

export function view() {
  activeRun();
  const failure = runError?.currentSession() ? `<div class="diag-feedback" role="alert">${esc(runError.text)} Use Run checks to retry.</div>` : '';
  if (state.missing.has('diag')) {
    return head() + failure + '<div class="card"><div class="empty">Diagnostics are unavailable in this version.</div></div>' + versionsCard() + unavailableViewers();
  }
  if (!state.diag && !run) {
    return head() + failure + '<div class="card"><div class="empty">Diagnostic results are unavailable. Check the connection and use Run checks to retry.</div></div>' + versionsCard() + unavailableViewers();
  }
  return head() + failure + overallBar() + `<div class="card diag-checks">${rows() || '<div class="empty">No diagnostic results yet. Use Run checks to request a new result.</div>'}<div class="toolbar"><button class="btn" data-act="download-report">Download report</button></div></div>` + versionsCard() + unavailableViewers() + '<p class="screen-sub diag-report-note">Summary reports exclude names, endpoints and free-form details. Review before sharing.</p>';
}

function versionValue(value, unknown = 'Unknown') {
  return value === null || value === undefined || value === '' ? `<dd class="unknown">${esc(unknown)}</dd>` : `<dd>${esc(String(value))}</dd>`;
}
function versionsCard() {
  const value = state.version;
  if (!value) return '<div class="card diag-versions"><div class="empty">Version information is unavailable. Retry when the gateway is reachable.</div></div>';
  const singbox = value.singbox || {};
  return `<div class="card diag-versions">${value.version_mismatch ? `<div class="version-warn"><strong>Running version differs from the installed version</strong>Running ${esc(value.version)}; installed ${esc(value.installed_version || 'Unknown')}. The service may not have restarted after an update.</div>` : ''}<dl class="versions"><dt>Daemon</dt>${versionValue(value.version)}<dt>Commit</dt>${versionValue(value.commit)}<dt>Installed version</dt>${versionValue(value.installed_version, 'Not recorded')}<dt>Database schema</dt>${versionValue(value.schema_version)}<dt>sing-box library</dt>${versionValue(singbox.library)}<dt>sing-box runtime</dt>${versionValue(singbox.runtime, singbox.runtime_detail || 'Unknown')}</dl></div>`;
}
function head() {
  if (!run) return '<div class="screen-head"><h1>Diagnostics</h1><div class="spacer"></div><button class="btn" data-act="rerun-diag">Run checks</button></div>';
  const total = run.ids.length, done = run.done.size;
  return `<div class="screen-head"><h1>Diagnostics</h1><div class="spacer"></div><button class="btn" data-act="rerun-diag" disabled aria-busy="true">Checking… ${done} / ${total || '—'}</button></div>${total ? `<div class="diag-progress" role="progressbar" aria-label="Diagnostic progress" aria-valuemin="0" aria-valuemax="${total}" aria-valuenow="${done}"><div class="diag-progress-fill" style="width:${Math.round(done / total * 100)}%"></div></div>` : ''}`;
}
function rows() {
  const list = run ? run.ids.map(id => run.done.get(id) || { id, title: run.titles.get(id), pending: true }) : state.diag.checks || [];
  return list.map(check => {
    const detail = check.pending ? check.id === run.current ? 'Checking…' : 'Queued' : check.detail || '';
    const long = detail.length > 600 || detail.split('\n').length > 8;
    return `<div class="check${check.pending ? ' pending' : ''}">${check.pending ? '<span class="check-icon check-spin" aria-hidden="true"></span>' : `<span class="check-icon ${esc(check.status)}" aria-label="${esc(check.status || 'unknown')}">${CHECK_ICON[check.status] || '?'}</span>`}<div><div class="check-title">${esc(check.title)}</div><div class="check-detail">${long ? `<details class="diag-check-detail"><summary>Show diagnostic details</summary><pre tabindex="0">${esc(detail)}</pre></details>` : esc(detail)}</div></div></div>`;
  }).join('');
}
function overallBar() {
  if (run) return `<div class="overall"><span class="check-icon check-spin" aria-hidden="true"></span><strong>Checks running</strong><span class="overall-time">${esc(run.titles.get(run.current) || '')}</span></div>`;
  const diag = state.diag, status = diag.checks?.length ? diag.overall || worst(diag.checks) : 'unknown';
  const stamp = diag.checked_at && Date.parse(diag.checked_at);
  return `<div class="overall"><span class="check-icon ${esc(status)}">${CHECK_ICON[status] || '?'}</span><strong>${esc(OVERALL_TEXT[status] || 'Status unknown')}</strong><span class="overall-time">${stamp ? `Checked ${esc(new Date(stamp).toLocaleString('en-GB'))}` : 'Check time unavailable'}</span></div>`;
}

const reportCheckNames = { wg: 'WireGuard', singbox: 'sing-box', tunnels: 'Tunnels', nft: 'Network policy', route: 'Marked routing', forward: 'IP forwarding', mtu: 'Path MTU', lists: 'Lists' };
const reportStatus = value => ['ok', 'warn', 'error', 'unknown'].includes(value) ? value : 'unknown';
const reportInteger = value => Number.isSafeInteger(value) && value >= 0 ? value : 'unknown';
const reportVersion = value => /^(?:v)?(\d+\.\d+\.\d+)/.exec(String(value || ''))?.[1] || (value === 'dev' ? 'dev' : 'unknown');

function report() {
  const diag = state.diag || {}, version = state.version || {}, settings = state.settings || {};
  // Export an explicit summary, never arbitrary server/user text. Raw details stay local.
  const output = [
    'razdacha — diagnostic report', `date: ${new Date().toISOString()}`,
    'Summary only: names, endpoints, DNS values, credentials and free-form details are excluded.',
    'Version cores omit release suffixes. Review before sharing.', '',
    '## Versions', `daemon: ${reportVersion(version.version)}`,
    `commit: ${/^[0-9a-f]{7,40}$/i.test(version.commit || '') ? version.commit : 'unknown'}`,
    `installed: ${reportVersion(version.installed_version)}`,
    `version mismatch: ${version.version_mismatch === true ? 'yes' : 'no'}`,
    `database schema: ${reportInteger(version.schema_version)}`,
    `sing-box library: ${reportVersion(version.singbox?.library)}`,
    `sing-box runtime: ${reportVersion(version.singbox?.runtime)}`, '',
    '## Check statuses', `overall: ${reportStatus(diag.checks?.length ? diag.overall || worst(diag.checks) : 'unknown')}`,
    ...(diag.checks || []).map((check, index) => `${Object.hasOwn(reportCheckNames, check.id) ? reportCheckNames[check.id] : `Check ${index + 1}`}: ${reportStatus(check.status)}`), '',
    '## Configuration summary', `WireGuard port: ${reportInteger(settings.wg_listen_port)}`,
    `client MTU: ${reportInteger(settings.client_mtu)}`,
    `clients: ${state.peers.length}; enabled: ${state.peers.filter(peer => peer.enabled !== false).length}`,
    `tunnels: ${state.tunnels.length}; enabled: ${state.tunnels.filter(tunnel => tunnel.enabled !== false).length}`,
    `rules: ${state.rules.length}; enabled: ${state.rules.filter(rule => rule.enabled !== false).length}`, '',
  ].join('\n');
  download('razdacha-diag.txt', output);
  toast('Summary report downloaded. Review before sharing.');
}

/* Reruns keep one operation token; old sessions cannot dispatch another probe. */
async function rerun() {
  if (activeRun()) return;
  const checks = state.diag?.checks || [];
  const execution = {
    currentSession: api.session.guard(),
    ids: checks.map(check => check.id),
    titles: new Map(checks.map(check => [check.id, check.title])),
    done: new Map(), current: checks[0]?.id,
  };
  run = execution;
  runError = null;
  loadRevision++;
  const current = () => execution.currentSession() && run === execution;
  refresh();
  try {
    if (!execution.ids.length) {
      let result = await api.diag.run();
      if (!current()) return;
      if (!result?.checks) result = await api.diag.get();
      if (!current()) return;
      state.diag = result;
    } else {
      await probeTunnels(current);
      if (!current()) return;
      let checkedAt = null, failed = 0;
      for (const id of execution.ids) {
        if (!current()) return;
        execution.current = id;
        refresh();
        try {
          const result = await api.diag.run(id);
          if (!current()) return;
          const list = result?.checks || [];
          const check = list.find(check => check.id === id) || (list.length === 1 ? list[0] : null);
          execution.done.set(id, check || unanswered(execution, id, 'The gateway did not return this check.'));
          if (result?.checked_at) checkedAt = result.checked_at;
        } catch (err) {
          if (!current()) return;
          failed++;
          execution.done.set(id, unanswered(execution, id, err.message));
        }
      }
      const results = execution.ids.map(id => execution.done.get(id));
      state.diag = { checks: results, overall: worst(results), checked_at: checkedAt };
      if (failed) toast(`${failed} checks did not respond. Review the results.`, 'err');
    }
    if (current()) {
      state.missing.delete('diag');
      toast('Diagnostic checks completed');
    }
  } catch (err) {
    if (current()) runError = { text: err.message || 'Checks could not be completed.', currentSession: execution.currentSession };
  } finally {
    if (run === execution) run = null;
    if (execution.currentSession()) refresh();
  }
}
async function probeTunnels(current) {
  try {
    const tunnels = await api.tunnels.list();
    if (!current()) return;
    for (const tunnel of tunnels.filter(tunnel => tunnel.enabled !== false)) {
      if (!current()) return;
      try { await api.tunnels.check(tunnel.id); }
      catch { /* Supported diagnostics report tunnel failures without aborting the run. */ }
    }
  } catch { /* Diagnostic checks remain useful when the tunnel list is unavailable. */ }
}
const unanswered = (execution, id, detail) => ({ id, title: execution.titles.get(id) || id, status: 'unknown', detail: `Check did not respond: ${detail}` });

export const actions = { 'download-report': report, 'rerun-diag': rerun };
