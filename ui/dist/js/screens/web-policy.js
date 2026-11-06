/* Conditional committed web policy; no client traffic or probes. */
import * as api from '../api.js';
import { state, peerById, openModal, modalShell, currentModalRevision, isCurrentModal, onModalCleanup } from '../shell.js';
import { esc } from '../util.js';

const reasons = {
  whole_client_exit: 'Client-wide exit', final_policy: 'Direct default', domain_suffix: 'Domain matches this rule',
  effective_rejection: 'Applied policy rejects this request', configured_exit_unavailable: 'Configured exit is unavailable; policy rejects this request',
  earlier_unresolved_candidates: 'An earlier address or native rule may match', unresolved_candidates: 'Address or native rules may match',
  unresolved_address_or_native_predicate: 'Requires address or native matching evidence', generated_dns_action: 'Applied DNS action', final_dns_policy: 'Default upstream DNS',
  native_dns_predicate: 'Native DNS matching is uncertain', public_destination_assumed: 'Assumes a public, nonlocal destination', gateway_domain_guard: 'Gateway domain is protected',
  transport_disabled: 'This connection method is disabled', transport_not_admitted: 'This connection method is not admitted by applied policy', client_not_applied: 'Client access needs Apply',
  authority_unavailable: 'Applied authority is unavailable', authority_changed: 'Policy changed while explaining; try again', ingress_guarded: 'Ingress is guarded',
  authority_read_failed: 'Applied authority could not be read', compiler_unavailable: 'Applied policy cannot be explained', construction_budget: 'Policy exceeds the explanation work limit',
  pending_construction_budget: 'Saved policy exceeds the comparison work limit', pending_read_failed: 'Pending status is unavailable', eligibility_changed: 'Client access changed while explaining',
};
const reasonText = code => reasons[code] || 'Policy information is unavailable';
const actionText = action => ({ direct: 'Direct', tunnel: 'Selected exit', reject: 'Reject', fakeip: 'FakeIP', upstream: 'Upstream DNS', same_exit_upstream: 'DNS through the client exit', unknown: 'Unknown' }[action] || 'Unknown');
const exitText = item => item?.tunnel_id ? ` · ${esc(item.tunnel_name || item.tunnel_id)}${item.via_tunnel_id ? ` → ${esc(item.via_tunnel_name || item.via_tunnel_id)}` : ''}` : '';

export function openWebPolicy(id) {
  const peer = peerById(id);
  if (!peer) return;
  const session = api.session.guard();
  const screen = state.screen;
  let revision, modal, request = 0, closed = false, navigated = false, busy = false;
  const active = () => !closed && !navigated && session() && state.screen === screen && isCurrentModal(revision);
  openModal(modalShell(`Web policy · ${peer.name}`, `
    <p>Shows applied rules for HTTPS (<strong>TCP 443</strong>). This does not open the site or measure connectivity.</p>
    <details><summary>Scope and limits</summary><p>Routing assumes a domain-bearing request and public, nonlocal resolution. Private/gateway guards can reject first. Gateway A-query policy is conditional, not an actual DNS query. Native and address predicates can remain unknown. Dynamic sniffing, DPI resistance, UDP and media quality are not measured.</p></details>
    <label class="field">Ingress<select id="web-policy-transport"><option value="wg">WireGuard</option><option value="reality">REALITY</option></select></label>
    <label class="field">Domain<input id="web-policy-domain" placeholder="example.com" autocomplete="off" maxlength="253"></label>
    <div id="web-policy-result" role="status">Enter a domain to explain applied policy. Saved edits are not applied by this action.</div>`,
    '<button class="btn" id="web-policy-submit">Explain web policy</button><button class="btn" data-act="close-modal">Close</button>'), element => { modal = element; });
  revision = currentModalRevision();
  const result = modal.querySelector('#web-policy-result');
  const button = modal.querySelector('#web-policy-submit');
  const domain = modal.querySelector('#web-policy-domain');
  const transport = modal.querySelector('#web-policy-transport');
  const invalidate = () => { request++; result.textContent = 'Input changed. Explain again to view applied policy.'; };
  domain.addEventListener('input', invalidate);
  transport.addEventListener('change', invalidate);
  const navigation = () => { navigated = true; request++; result.textContent = 'Navigation changed. Reopen Web policy for a new explanation.'; };
  window.addEventListener('hashchange', navigation);
  onModalCleanup(() => { closed = true; request++; window.removeEventListener('hashchange', navigation); });
  const explain = async () => {
    if (!active() || busy) return;
    const body = { peer_id: id, transport: transport.value, domain: domain.value };
    if (!body.domain) { result.textContent = 'Enter a domain without URL, IP, port, path or CIDR.'; return; }
    const token = ++request;
    busy = true; button.disabled = true; result.textContent = 'Reading applied web policy…';
    const current = () => active() && token === request && body.domain === domain.value && body.transport === transport.value;
    try {
      const value = await api.explainWebPolicy(body);
      if (!current()) return;
      const outcome = (title, item) => `<p><strong>${title}</strong>: ${item?.state === 'conditional' ? 'Conditional' : 'Unknown'} · ${actionText(item?.action)}${exitText(item)}${item?.name ? ` · ${esc(item.name)}` : ''} · ${esc(reasonText(item?.reason))}</p>`;
      result.innerHTML = `<strong>${esc(value.scope || 'Web policy / TCP 443')} · ${esc(value.state)}</strong>
        <p>${value.marker ? value.pending ? 'Global pending saved edits exist. This result uses applied policy.' : 'Applied policy snapshot; later changes require a new explanation.' : 'Applied authority is unavailable; pending status is not determined.'}</p>
        ${value.reason ? `<p>${esc(reasonText(value.reason))}</p>` : ''}
        ${outcome('Conditional routing', value.routing)}${outcome('Conditional gateway A-query policy', value.gateway_a)}
        ${(value.candidates || []).length ? `<p>Possible routing candidates, in order:</p><ul>${value.candidates.map(item => `<li>${esc(item.name || reasonText(item.reason))} · ${actionText(item.action)}${exitText(item)} · ${esc(reasonText(item.reason))}</li>`).join('')}</ul>` : ''}
        `;
    } catch (err) { if (current()) result.textContent = err.message || 'Applied web policy is unavailable.'; }
    finally { busy = false; if (active()) button.disabled = false; }
  };
  button.addEventListener('click', explain);
  domain.addEventListener('keydown', event => { if (event.key === 'Enter') { event.preventDefault(); explain(); } });
}
