// Browser-local evidence only: no server window, persistence or target navigation.
import * as api from '../api.js';
import { state, peerById, openModal, modalShell, currentModalRevision, isCurrentModal, onModalCleanup } from '../shell.js';
import { esc, bytes as fmtBytes } from '../util.js';
import { reasonText, actionText, exitText } from './web-policy.js';

const WINDOW_MS = 90000, FRESH_MS = 30000, POLL_MS = 5000;
const safe = value => Number.isSafeInteger(value) && value >= 0;
// Preserve submillisecond source ordering, including Go's RFC3339Nano timestamps.
function stamp(value) {
  if (typeof value !== 'string') return null;
  const match = /^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(?:\.(\d{1,9}))?Z$/.exec(value);
  if (!match) return null;
  const seconds = Date.parse(match[1] + 'Z');
  return Number.isFinite(seconds) ? BigInt(seconds) * 1000000n + BigInt((match[2] || '').padEnd(9, '0')) : null;
}
function observation(value, started) {
  const observed = stamp(value?.observed_at), sampled = stamp(value?.sampled_at);
  if (value?.fresh !== true || !safe(value.age_ms) || observed === null || sampled === null || observed > sampled) return null;
  const age = Math.max(value.age_ms, Number(sampled - observed) / 1000000) + Math.max(0, performance.now() - started);
  return age < FRESH_MS ? { observed, age, received: performance.now() } : null;
}
const fresh = item => item && item.age + performance.now() - item.received < FRESH_MS;
const reason = code => ({ aggregate_peer_counters: 'Encrypted-peer aggregate counters', current_open_flows: 'Current-open-flow observation', partial_counters: 'Some source counters are unavailable', future_handshake: 'Source handshake time is in the future', peer_not_in_source: 'Client is absent from this source observation', pool_history_only: 'History only; not a new check or current-generation proof', source_lifetime_unknown: 'Existing shared probe cannot be bound to this applied publication', unsafe_counter: 'Counter is missing or outside the safe range', stale_source: 'Source observation is stale', source_unavailable: 'Source observation is unavailable', source_failed: 'Source observation is unavailable', client_disabled: 'Client is disabled', no_pool_history: 'No existing pool history', route_not_known: 'Applied exit is unknown; no check ran', policy_reject: 'Applied policy rejects this request; no check ran', probe_unconfirmed: 'HTTPS observation is inconclusive; the cause is unknown', fixed_target_succeeded: 'Fixed-target HTTPS succeeded', observation_unavailable: 'Source observation is unavailable' }[code] || reasonText(code));
const sourceText = value => ({ wireguard_kernel: 'WireGuard kernel peer counters', authenticated_open_flows: 'Authenticated REALITY current open flows', active_probe: 'Independent fixed-target HTTPS probe', pool_history: 'Existing pool group history' }[value] || 'Unknown');
const domainOnly = value => value.length <= 253 && !/^\d+(?:\.\d+){3}$/.test(value) && value.includes('.') && value.split('.').every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label));

export function openConnectionEvidence(id) {
  const peer = peerById(id);
  if (!peer) return;
  const session = api.session.guard(), screen = state.screen;
  let modal, revision, context = 0, pollToken = 0, probeToken = 0, closed = false, navigated = false;
  let pollController, probeController, timer, pollTimer, deadline = null, baseline = null, current = null, positive = null, exit = null, epoch = null;
  let activityMessage = 'Start a window, then open a page or download on the selected client device.', exitMessage = 'No independent exit observation requested.';
  const active = () => !closed && !navigated && session() && state.screen === screen && isCurrentModal(revision);
  openModal(modalShell(`Connection evidence · ${peer.name}`, `
    <p>Use the selected client device to open a page or download during this 90-second window. This panel never opens the target for you.</p>
    <label class="field">Ingress<select id="evidence-transport"><option value="wg">WireGuard</option><option value="reality">REALITY</option></select></label>
    <label class="field">Optional domain · HTTPS / TCP 443<input type="text" id="evidence-domain" placeholder="example.com" maxlength="253" autocomplete="off"></label>
    <p class="muted">Activity covers the whole selected client ingress. The domain selects applied exit context only; it is never contacted by this check.</p>
    <section><h3>Client activity</h3><div id="evidence-activity" role="status"></div></section>
    <section><h3>Independent gateway exit HTTPS</h3><div id="evidence-exit" role="status"></div></section>
    <details><summary>Scope and limits</summary><p>WireGuard counters are cumulative encrypted-peer bytes received/sent by the gateway, not application bandwidth. REALITY shows current-open-flow totals: closures can decrease counts and bytes, and completed short flows can be missed. Zero or unchanged values cannot prove inactivity or an offline device. No site success, client latency, loss, jitter, UDP/media quality or quality score is measured. HTTPS timing covers gateway to a fixed IP target through the full applied exit chain (or explicitly applied Direct), never the selected website. Pool history is an existing observation, not a new measurement. Conditional gateway A-query policy is not a DNS query; runtime DNS is not per-client.</p></details>`,
    '<button class="btn" id="evidence-start">Start 90-second window</button><button class="btn" id="evidence-check">Check applied exit HTTPS</button><button class="btn" data-act="close-modal">Close</button>'), element => { modal = element; });
  revision = currentModalRevision();
  const transport = modal.querySelector('#evidence-transport'), domain = modal.querySelector('#evidence-domain');
  const startButton = modal.querySelector('#evidence-start'), checkButton = modal.querySelector('#evidence-check');
  transport.value = peer.wireguard_enabled === false && peer.reality_enabled === true ? 'reality' : 'wg';
  const body = () => ({ peer_id: id, transport: transport.value });
  const clear = () => {
    context++; pollToken++; probeToken++; pollController?.abort(); probeController?.abort(); clearTimeout(timer); clearTimeout(pollTimer);
    deadline = null; baseline = current = positive = exit = epoch = null;
    startButton.disabled = false; checkButton.disabled = false;
  };
  function render() {
    if (!active()) return;
    const running = deadline !== null;
    const usable = current && fresh(current.timing);
    const fields = current?.value.transport === 'wg' ? ['rx_bytes', 'tx_bytes'] : ['open_flows', 'open_upload_bytes', 'open_download_bytes'];
    const counts = current?.value[current.value.transport];
    const labels = current?.value.transport === 'wg' ? ['Gateway received', 'Gateway sent'] : ['Current open flows', 'Current-open gateway received', 'Current-open gateway sent'];
    modal.querySelector('#evidence-activity').innerHTML = `<p>${esc(activityMessage)}</p>
      ${running ? `<p>Window: ${Math.max(0, Math.ceil((deadline - performance.now()) / 1000))} seconds remaining. Polling at least 5 seconds apart.</p>` : ''}
      ${current ? `<p>Source: ${esc(sourceText(current.value.source))} · observed ${esc(current.value.observed_at || 'Unknown')} · ${usable ? 'Fresh' : 'Expired / unknown'}</p><p>${fields.map((field, index) => `${labels[index]}: ${usable && safe(counts?.[field]) ? field === 'open_flows' ? counts[field] : fmtBytes(counts[field]) : 'Unknown'}`).join(' · ')}</p>` : ''}
      ${positive ? `<p>${fresh(positive.timing) ? 'Recent evidence' : 'Historical evidence (expired)'}: ${esc(positive.text)} · ${esc(positive.at)}. This does not prove site success.</p>` : ''}`;
    const value = exit?.value, https = value?.https;
    modal.querySelector('#evidence-exit').innerHTML = `<p>${esc(exitMessage)}</p>${value ? `
      <p>Conditional applied routing: ${actionText(value.policy?.routing?.action)}${exitText(value.policy?.routing)} · ${esc(reason(value.policy?.routing?.reason))}</p>
      <p>Conditional gateway A-query policy: ${actionText(value.policy?.gateway_a?.action)} · ${esc(reason(value.policy?.gateway_a?.reason))}</p>
      <p>${!value.policy?.marker ? 'Applied authority is unavailable; no applied snapshot is claimed.' : value.policy?.pending ? 'Global pending saved edits exist; this uses applied policy.' : 'Applied policy context; saved edits are separate.'}</p>
      <p>Source: ${esc(sourceText(https?.source))} · observed ${esc(https?.observed_at || 'Unknown')} · ${https?.source === 'pool_history' ? 'History only; not a new check or current-generation proof' : fresh(exit.timing) ? 'Fresh' : 'Unknown / expired'}</p>
      <p>Gateway fixed-target HTTPS: ${fresh(exit.timing) && https?.source === 'active_probe' && https?.state === 'observed' && safe(https.delay_ms) ? `${https.delay_ms} ms` : 'Unknown'} · ${esc(reason(https?.reason))}</p>
      ${https?.source === 'pool_history' ? `<p>Historical pool group HTTPS: ${fresh(exit.timing) && https?.state === 'observed' && safe(https.delay_ms) ? `${https.delay_ms} ms` : 'Unknown / expired'}. Existing pool group history; no new measurement or member fan-out. The current epoch binds exit selection only, not this historical probe.</p>` : ''}` : ''}`;
  }
  function tick() {
    if (!active()) { clear(); return; }
    if (deadline !== null && performance.now() >= deadline) {
      clear(); activityMessage = 'The 90-second window ended. Start a new window for new evidence.';
      exitMessage = 'Window ended; request a new independent observation if needed.';
    }
    if (baseline && !fresh(baseline.timing)) baseline = null;
    render(); timer = setTimeout(tick, 1000);
  }
  const invalidate = () => { clear(); activityMessage = 'Input changed. Start a new window.'; exitMessage = 'Input changed. Check the applied exit again.'; render(); tick(); };
  domain.addEventListener('input', invalidate); transport.addEventListener('change', invalidate);
  const navigation = () => { clear(); navigated = true; modal.querySelector('#evidence-activity').textContent = 'Navigation changed. Reopen Connection evidence.'; modal.querySelector('#evidence-exit').textContent = ''; };
  window.addEventListener('hashchange', navigation);
  onModalCleanup(() => { clear(); closed = true; window.removeEventListener('hashchange', navigation); });
  function acceptEpoch(next) {
    if (typeof next !== 'string' || !next) {
      baseline = current = positive = exit = null; epoch = null;
      probeToken++; probeController?.abort(); checkButton.disabled = false;
      exitMessage = 'Applied authority is unavailable. Check again after recovery.';
      return false;
    }
    if (epoch !== null && epoch !== next) {
      baseline = current = positive = exit = null;
      probeToken++; probeController?.abort(); checkButton.disabled = false;
      exitMessage = 'Applied authority changed. Check the exit again.';
    }
    epoch = next; return true;
  }
  async function poll(localContext) {
    if (!active() || localContext !== context || deadline === null || performance.now() >= deadline) return;
    const token = ++pollToken, started = performance.now(), selected = body();
    pollController = new AbortController();
    const valid = () => active() && context === localContext && token === pollToken && deadline !== null && performance.now() < deadline;
    try {
      const value = await api.clientEvidence.observe(selected, pollController.signal);
      if (!valid()) return;
      if (value?.peer_id !== id || value?.transport !== selected.transport || value?.scope !== 'client_ingress' || value.source !== (selected.transport === 'wg' ? 'wireguard_kernel' : 'authenticated_open_flows') || !acceptEpoch(value.epoch)) {
        acceptEpoch(null); activityMessage = 'Unknown: no coherent comparable source observation.'; render(); return;
      }
      let timing = observation(value, started);
      // A cached source timestamp cannot restart the local freshness clock.
      if (timing && current?.timing?.observed === timing.observed && current.timing.age + performance.now() - current.timing.received > timing.age) timing = current.timing;
      const counts = value[selected.transport];
      const fields = selected.transport === 'wg' ? ['rx_bytes', 'tx_bytes'] : ['open_flows', 'open_upload_bytes', 'open_download_bytes'];
      const validCounters = fields.every(field => safe(counts?.[field]));
      current = { value, timing };
      if (value.state !== 'observed' || !fresh(timing) || !validCounters) { baseline = null; activityMessage = `Unknown: ${reason(value.reason)}.`; }
      else if (!baseline || !fresh(baseline.timing)) { baseline = current; activityMessage = 'Baseline recorded. Generate activity on the selected client device.'; }
      else if (timing.observed <= baseline.timing.observed) { activityMessage = 'No newer source observation; cached data cannot confirm new activity.'; }
      else {
        const before = baseline.value[selected.transport];
        const decreased = fields.some(field => counts[field] < before[field]);
        const increased = fields.some(field => counts[field] > before[field]);
        if (decreased) activityMessage = selected.transport === 'wg' ? 'Counters decreased or reset; samples are incomparable. A new baseline was recorded.' : 'Open-flow totals decreased or flows closed. This cannot establish inactivity.';
        else if (increased) {
          const text = selected.transport === 'wg' ? 'Encrypted-peer aggregate counters increased' : 'Current-open-flow totals increased (not transferred-byte throughput)';
          positive = { text, at: value.observed_at, timing }; activityMessage = text + '.';
        } else activityMessage = 'No increase between these source observations. This cannot establish inactivity or an offline device.';
        baseline = current;
      }
      render();
    } catch { if (valid()) { baseline = null; current = null; activityMessage = 'Source observation unavailable; transport state and cause are unknown.'; render(); } }
    finally {
      if (valid()) pollTimer = setTimeout(() => poll(localContext), POLL_MS);
    }
  }
  startButton.addEventListener('click', () => {
    if (!active() || deadline !== null) return;
    clear(); deadline = performance.now() + WINDOW_MS; startButton.disabled = true;
    activityMessage = 'Reading a baseline. Open a page or download on the selected client device.';
    render(); tick(); poll(context);
  });
  checkButton.addEventListener('click', async () => {
    if (!active() || checkButton.disabled) return;
    const name = domain.value.trim().toLowerCase();
    if (!domainOnly(name)) { exitMessage = 'Enter a domain without URL, IP, port, path or CIDR.'; render(); return; }
    const localContext = context, token = ++probeToken, selected = { ...body(), domain: name }, started = performance.now();
    probeController = new AbortController(); checkButton.disabled = true; exit = null; exitMessage = 'Checking the applied exit against the fixed HTTPS target…'; render();
    const valid = () => active() && localContext === context && token === probeToken;
    try {
      const value = await api.clientEvidence.checkExit(selected, probeController.signal);
      if (!valid()) return;
      if (value?.peer_id !== id || value?.transport !== selected.transport || value?.domain !== name || value?.scope !== 'gateway_exit_https') { exitMessage = 'Unknown: response context does not match.'; return; }
      // A changed authority invalidates both operations; this response is never reused as a baseline.
      if (epoch !== null && value.epoch !== epoch) { clear(); exitMessage = 'Applied authority changed. Start a new window and check again.'; activityMessage = 'Applied authority changed; previous samples are incomparable.'; tick(); return; }
      const coherent = acceptEpoch(value.epoch);
      exit = { value, timing: coherent && ['active_probe', 'pool_history'].includes(value.https?.source) ? observation({ ...value.https, sampled_at: value.sampled_at }, started) : null };
      exitMessage = 'Independent gateway observation; not client latency or selected-website success.';
    } catch { if (valid()) exitMessage = 'HTTPS observation unavailable; the cause is unknown. This does not establish that the transport is down.'; }
    finally { if (valid()) checkButton.disabled = false; render(); }
  });
  render(); tick();
}
