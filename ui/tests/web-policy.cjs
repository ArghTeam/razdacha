// Actual embedded modules, synthetic intercepted requests only; no daemon/probes.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
const key = 'POST /api/route/explain';
const result = { scope: 'Web policy / TCP 443', state: 'conditional', pending: true, marker: 'synthetic-marker', routing: { state: 'conditional', action: 'direct', reason: 'domain_suffix', name: '<img src=x onerror=alert(1)>' }, gateway_a: { state: 'conditional', action: 'reject', reason: 'generated_dns_action' }, candidates: [], limits: ['No traffic is measured.'] };
(async () => {
  const browser = await chromium.launch({ headless: true });
  let count = 0;
  const check = async fn => { const t = await fixture(browser); try { await fn(t); count++; } finally { await t.page.close(); } };
  const start = async t => { t.holds.add(key); await t.invoke('peer-web-policy'); await t.page.fill('#web-policy-domain', 'example.com'); await t.page.click('#web-policy-submit'); await t.waitHeld(key); };
  const settle = t => t.page.waitForFunction(() => !document.querySelector('#web-policy-submit')?.disabled);
  try {
    await check(async t => {
      await t.invoke('peer-web-policy'); await t.page.click('#web-policy-submit');
      assert.match(await t.page.textContent('#web-policy-result'), /Enter a domain/);
      assert.equal(t.calls.filter(c => c.key === key).length, 0);
      t.holds.add(key); await t.page.fill('#web-policy-domain', 'example.com'); await t.page.click('#web-policy-submit'); await t.waitHeld(key);
      await t.page.evaluate(() => document.querySelector('#web-policy-domain').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })));
      assert.equal(t.calls.filter(c => c.key === key).length, 1);
      assert.deepEqual(t.calls.find(c => c.key === key).body, { peer_id: 'example', transport: 'wg', domain: 'example.com' });
      await t.reply(key, result); await settle(t);
      const text = await t.page.textContent('#web-policy-result'); assert.match(text, /Global pending saved edits/); assert.match(text, /Conditional routing.*Direct/); assert.match(text, /Conditional gateway A-query policy.*Reject/); assert.match(text, /<img/);
      assert.equal(await t.page.locator('#web-policy-result img').count(), 0);
      assert.equal(t.calls.filter(c => /route\/test|domain\/reachability|\/config|\/apply$/.test(c.key)).length, 0);
    });
    for (const change of ['domain', 'transport']) for (const status of [200, 500]) await check(async t => {
      await start(t);
      if (change === 'domain') await t.page.fill('#web-policy-domain', 'changed.example'); else await t.page.selectOption('#web-policy-transport', 'reality');
      await t.reply(key, status === 200 ? result : { error: 'Obsolete failure' }, status); await settle(t);
      assert.match(await t.page.textContent('#web-policy-result'), /Input changed/); assert.doesNotMatch(await t.page.textContent('#web-policy-result'), /Obsolete|Conditional routing/);
      t.overrides.set(key, { status: 200, body: { ...result, state: 'disabled', reason: 'transport_disabled', pending: false } });
      await t.page.click('#web-policy-submit'); await t.page.getByText('This connection method is disabled', { exact: true }).waitFor();
    });
    for (const change of ['close', 'replace', 'navigation_return', 'relogin', 'expiry', 'client_replacement']) for (const status of [200, 500]) await check(async t => {
      await start(t);
      if (change === 'close') await t.page.keyboard.press('Escape');
      if (change === 'replace') await t.replace();
      if (change === 'navigation_return') {
        await t.page.evaluate(() => { location.hash = 'rules'; }); await t.page.waitForFunction(() => location.hash === '#rules');
        await t.page.evaluate(() => { location.hash = 'peers'; }); await t.page.waitForFunction(() => location.hash === '#peers');
        await t.page.getByText('Navigation changed. Reopen Web policy for a new explanation.', { exact: true }).waitFor();
      }
      if (change === 'relogin') await t.relogin();
      if (change === 'expiry') {
        t.overrides.set('GET /api/session', { status: 401, body: {} });
        await t.page.evaluate(async () => { try { await (await import('/js/api.js')).session.get(); } catch {} });
      }
      if (change === 'client_replacement') {
        await t.page.evaluate(async peer => { const s = await import('/js/shell.js'); s.state.peers.push({ ...peer, id: 'second', name: 'Second client' }); }, examplePeer);
        await t.invoke('peer-web-policy', 'second');
      }
      const before = await t.state();
      await t.reply(key, status === 200 ? result : { error: 'Obsolete failure' }, status);
      await t.page.evaluate(async () => { try { await (await import('/js/api.js')).session.get(); } catch {} });
      assert.equal((await t.state()).modal, before.modal);
    });
    await check(async t => {
      await start(t); await t.reply(key, { ...result, state: 'unknown', pending: false, routing: { state: 'unknown', action: 'unknown', reason: 'earlier_unresolved_candidates' }, candidates: [{ name: 'Earlier CIDR', action: 'tunnel', reason: 'unresolved_address_or_native_predicate' }] }); await settle(t);
      assert.match(await t.page.textContent('#web-policy-result'), /Earlier CIDR/);
      t.overrides.set(key, { status: 404, body: { error: 'Client no longer exists.' } }); await t.page.click('#web-policy-submit'); await t.page.getByText('Client no longer exists.', { exact: true }).waitFor();
      t.overrides.set(key, { status: 503, body: { error: 'Authority unavailable.' } }); await t.page.click('#web-policy-submit'); await t.page.getByText('Authority unavailable.', { exact: true }).waitFor();
      assert.equal((await t.state()).dirty, false);
    });
    await check(async t => {
      await start(t); await t.reply(key, { ...result, state: 'unknown', reason: 'authority_unavailable', marker: undefined, pending: false }); await settle(t);
      const text = await t.page.textContent('#web-policy-result'); assert.match(text, /Applied authority is unavailable/); assert.doesNotMatch(text, /Applied policy snapshot/);
    });
    await check(async t => {
      await start(t); await t.reply(key, { ...result, routing: { state: 'conditional', action: 'tunnel', reason: 'whole_client_exit', tunnel_id: 'exit', tunnel_name: 'Committed exit', via_tunnel_id: 'warp', via_tunnel_name: 'Committed WARP' } }); await settle(t);
      assert.match(await t.page.textContent('#web-policy-result'), /Committed exit → Committed WARP.*Client-wide exit/);
    });
    console.log(`Passed ${count} synthetic Chromium Web policy cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
