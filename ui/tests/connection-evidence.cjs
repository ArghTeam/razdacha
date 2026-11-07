// Actual embedded modules and intercepted synthetic API only. No daemon or tunnels.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
const observe = 'POST /api/clients/observe', probe = 'POST /api/clients/check-exit';
const at = n => `2026-11-07T12:00:${String(n).padStart(2, '0')}Z`;
const sample = (n = 0, bytes = 0, extra = {}) => ({ scope: 'client_ingress', peer_id: 'example', transport: 'wg', state: 'observed', reason: 'source_observed', epoch: 'boot:publication', sampled_at: at(n), observed_at: at(n), age_ms: 0, fresh: true, source: 'wireguard_kernel', wg: { rx_bytes: bytes, tx_bytes: 0, last_handshake: null }, reality: null, ...extra });
const checked = extra => ({ scope: 'gateway_exit_https', peer_id: 'example', transport: 'wg', domain: 'example.com', epoch: 'boot:publication', sampled_at: at(5), policy: { marker: 'applied-policy', pending: true, routing: { action: 'tunnel', reason: 'whole_client_exit', tunnel_id: 'exit', tunnel_name: '<img src=x onerror=alert(1)>', via_tunnel_id: 'chain', via_tunnel_name: 'Full chain' }, gateway_a: { action: 'reject', reason: 'generated_dns_action' } }, https: { scope: 'gateway_fixed_target_https', state: 'observed', reason: 'fixed_target_succeeded', source: 'active_probe', observed_at: at(5), age_ms: 0, fresh: true, delay_ms: 47 }, ...extra });
(async () => {
  const browser = await chromium.launch({ headless: true });
  let count = 0;
  async function check(fn, options) {
    const t = await fixture(browser, options);
    try {
      console.log(`Connection evidence case ${count + 1}`);
      await t.page.waitForLoadState('networkidle');
      await t.page.clock.install(); await t.page.clock.pauseAt(new Date('2026-11-07T12:00:00Z'));
      t.overrides.set(observe, { status: 200, body: sample() });
      await t.page.locator('[data-act="menu-peer"]').first().click();
      await t.page.locator('#row-menu [data-act="peer-connection-evidence"]').click();
      await t.page.locator('#evidence-start').waitFor();
      await fn(t); count++;
    } finally { await t.page.close(); }
  }
  const text = t => t.page.textContent('#evidence-activity');
  const start = async t => { await t.page.click('#evidence-start'); await t.page.getByText('Baseline recorded. Generate activity on the selected client device.', { exact: true }).waitFor(); };
  const next = async (t, value) => { t.overrides.set(observe, { status: 200, body: value }); const before = t.calls.filter(c => c.key === observe).length; await t.page.clock.runFor(5000); await t.page.waitForFunction(() => !document.querySelector('#evidence-activity').textContent.includes('Reading a baseline')); for (let i=0;i<100 && t.calls.filter(c=>c.key===observe).length===before;i++) await new Promise(r=>setTimeout(r,5)); await new Promise(resolve => setTimeout(resolve, 30)); };
  try {
    await check(async t => {
      await start(t);
      assert.deepEqual(t.calls.find(c => c.key === observe).body, { peer_id: 'example', transport: 'wg' });
      assert.match(await text(t), /Gateway received: 0 B/);
      await t.page.clock.runFor(4999); assert.equal(t.calls.filter(c=>c.key===observe).length, 1);
      t.overrides.set(observe, { status: 200, body: sample(5, 1024) }); await t.page.clock.runFor(1);
      await t.page.getByText('Encrypted-peer aggregate counters increased.', { exact: true }).waitFor();
      assert.match(await text(t), /does not prove site success/);
      await next(t, sample(5, 2048)); assert.match(await text(t), /cached data cannot confirm/);
      await next(t, sample(10, 0)); assert.match(await text(t), /decreased or reset/);
      const before = t.calls.filter(c=>c.key===observe).length;
      await t.page.clock.runFor(90000); assert.match(await text(t), /window ended/);
      const after = t.calls.filter(c=>c.key===observe).length; await t.page.clock.runFor(20000);
      assert.equal(t.calls.filter(c=>c.key===observe).length, after); assert(after >= before);
      assert.doesNotMatch(await text(t), /Recent evidence|Gateway received/);
      assert.equal((await t.state()).dirty, false);
    });
    for (const bad of [ { source: null }, { source: 'invalid_source' }, { observed_at: null }, { observed_at: at(10), sampled_at: at(5) }, { age_ms: 30000 }, { fresh: false }, { wg: { rx_bytes: null, tx_bytes: 0 } }, { wg: { rx_bytes: Number.MAX_SAFE_INTEGER + 1, tx_bytes: 0 } }, { wg: { rx_bytes: -1, tx_bytes: 0 } }, { wg: { rx_bytes: '10', tx_bytes: 0 } } ]) await check(async t => {
      await start(t); await next(t, sample(5, 100, bad));
      assert.match(await text(t), /Unknown/); assert.doesNotMatch(await text(t), /Recent evidence/);
    });
    await check(async t => {
      await start(t); await next(t, sample(5, 100, { epoch: 'same-byte-apply-new-publication' }));
      assert.match(await text(t), /Baseline recorded/); assert.doesNotMatch(await text(t), /Recent evidence/);
      await next(t, sample(10, 101, { epoch: 'new-boot' })); assert.match(await text(t), /Baseline recorded/);
      await next(t, sample(15, 101, { epoch: null })); assert.match(await text(t), /Unknown/);
    });
    await check(async t => {
      await t.page.selectOption('#evidence-transport', 'reality');
      const reality = (n, flows, bytes) => sample(n, 0, { transport: 'reality', source: 'authenticated_open_flows', wg: null, reality: { open_flows: flows, open_upload_bytes: bytes, open_download_bytes: bytes } });
      t.overrides.set(observe, { status: 200, body: reality(0, 1, 100) }); await start(t);
      await next(t, reality(5, 1, 200)); assert.match(await text(t), /totals increased \(not transferred-byte throughput\)/);
      await next(t, reality(10, 0, 0)); assert.match(await text(t), /flows closed/); assert.doesNotMatch(await text(t), /offline device/);
      await t.page.clock.runFor(30000); assert.match(await text(t), /Historical evidence \(expired\)/);
    });
    await check(async t => {
      await t.page.click('#evidence-check'); assert.match(await t.page.textContent('#evidence-exit'), /Enter a domain/);
      for (const value of ['https://example.com', '192.0.2.1', 'example.com:443', 'example.com/path']) { await t.page.fill('#evidence-domain', value); await t.page.click('#evidence-check'); }
      assert.equal(t.calls.filter(c=>c.key===probe).length, 0);
      await t.page.fill('#evidence-domain', 'Example.COM'); t.overrides.set(probe, { status: 200, body: checked() });
      await t.page.click('#evidence-check'); await t.page.getByText(/Gateway fixed-target HTTPS: 47 ms/).waitFor();
      assert.deepEqual(t.calls.find(c=>c.key===probe).body, { peer_id: 'example', transport: 'wg', domain: 'example.com' });
      const output = await t.page.textContent('#evidence-exit'); assert.match(output, /<img.*Full chain/); assert.match(output, /Conditional gateway A-query policy/); assert.match(output, /Global pending saved edits/);
      assert.equal(await t.page.locator('#evidence-exit img').count(), 0);
      await t.page.clock.runFor(30000); assert.match(await t.page.textContent('#evidence-exit'), /HTTPS: Unknown/);
      assert.equal(t.calls.filter(c => /route\/test|domain\/reachability|\/config|\/apply$/.test(c.key)).length, 0);
    });
    for (const change of ['domain', 'transport', 'close', 'replace', 'navigation', 'relogin', 'expiry']) for (const operation of [observe, probe]) for (const status of [200, 500]) await check(async t => {
      t.holds.add(operation);
      if (operation === observe) await t.page.click('#evidence-start');
      else { await t.page.fill('#evidence-domain', 'example.com'); await t.page.click('#evidence-check'); }
      await t.waitHeld(operation);
      if (change === 'domain') await t.page.fill('#evidence-domain', 'changed.example');
      if (change === 'transport') await t.page.selectOption('#evidence-transport', 'reality');
      if (change === 'close') await t.page.keyboard.press('Escape');
      if (change === 'replace') await t.replace();
      if (change === 'navigation') { await t.page.evaluate(() => { location.hash = 'rules'; }); await t.page.getByText('Navigation changed. Reopen Connection evidence.', { exact: true }).waitFor(); }
      if (change === 'relogin') await t.relogin();
      if (change === 'expiry') { t.overrides.set('GET /api/session', { status: 401, body: {} }); await t.page.evaluate(async () => { try { await (await import('/js/api.js')).session.get(); } catch {} }); }
      const before = await t.state(); const requests = t.calls.filter(c => c.key === observe || c.key === probe).length;
      await t.reply(operation, status === 200 ? operation === observe ? sample(5, 1000) : checked() : { error: 'Obsolete failure' }, status);
      await t.page.clock.runFor(10000);
      assert.equal((await t.state()).modal, before.modal);
      assert.equal(t.calls.filter(c=>c.key===observe || c.key===probe).length, requests);
    });
    await check(async t => {
      t.holds.add(observe); await t.page.click('#evidence-start'); await t.waitHeld(observe);
      await t.page.clock.runFor(20000); assert.equal(t.calls.filter(c=>c.key===observe).length, 1);
      await t.reply(observe, sample()); await t.page.getByText('Baseline recorded. Generate activity on the selected client device.', { exact: true }).waitFor();
      await t.page.clock.runFor(11000); assert.match(await text(t), /Expired \/ unknown/);
    });
    for (const changed of [null, 'replacement-publication']) await check(async t => {
      await t.page.fill('#evidence-domain', 'example.com'); await start(t);
      t.holds.add(probe); await t.page.click('#evidence-check'); await t.waitHeld(probe);
      await next(t, sample(5, 100, { epoch: changed }));
      const before = await t.page.textContent('#evidence-exit');
      await t.reply(probe, checked()); await new Promise(resolve => setTimeout(resolve, 30));
      assert.equal(await t.page.textContent('#evidence-exit'), before);
      assert.doesNotMatch(before, /47 ms/);
    });
    await check(async t => {
      await start(t); await next(t, sample(0, 10, { observed_at: '2026-11-07T12:00:00.000000001Z', sampled_at: at(5) }));
      assert.match(await text(t), /Encrypted-peer aggregate counters increased/);
      await next(t, sample(10, 10)); assert.match(await text(t), /No increase/);
    });
    await check(async t => {
      t.holds.add(observe); await t.page.click('#evidence-start'); await t.waitHeld(observe);
      await t.page.clock.runFor(30000); await t.reply(observe, sample());
      await t.page.getByText(/Unknown:/).first().waitFor();
      assert.doesNotMatch(await text(t), /Baseline recorded|Recent evidence/);
    });
    await check(async t => {
      await t.page.fill('#evidence-domain', 'example.com'); await start(t);
      await t.page.evaluate(async peer => { const shell = await import('/js/shell.js'); shell.state.peers.push({ ...peer, id: 'second', name: 'Second client' }); }, examplePeer);
      t.holds.add(observe); await t.page.clock.runFor(5000); await t.waitHeld(observe);
      await t.invoke('peer-connection-evidence', 'second');
      const before = await t.state(); await t.reply(observe, sample(5, 100));
      await t.page.clock.runFor(10000); assert.equal((await t.state()).modal, before.modal);
    });
    await check(async t => {
      assert.equal(await t.page.inputValue('#evidence-transport'), 'reality');
    }, { peers: [{ ...examplePeer, wireguard_enabled: false, reality_enabled: true }] });
    await check(async t => {
      await t.page.fill('#evidence-domain', 'example.com');
      const value = checked({ epoch: 'after-apply-publication', sampled_at: at(10) });
      Object.assign(value.https, { source: 'pool_history', reason: 'pool_history_only', observed_at: at(0), age_ms: 10000 });
      t.overrides.set(probe, { status: 200, body: value });
      await t.page.click('#evidence-check'); await t.page.getByText(/Historical pool group HTTPS: 47 ms/).waitFor();
      const output = await t.page.textContent('#evidence-exit');
      assert.match(output, /not a new check or current-generation proof/);
      assert.match(output, /2026-11-07T12:00:00Z/);
      assert.match(output, /Gateway fixed-target HTTPS: Unknown/);
      assert.match(output, /current epoch binds exit selection only/);
      await t.page.clock.runFor(20000);
      assert.doesNotMatch(await t.page.textContent('#evidence-exit'), /47 ms/);
    });
    for (const mode of ['disabled','pool','not_run','error']) await check(async t => {
      await t.page.fill('#evidence-domain', 'example.com');
      const value = checked();
      if (mode === 'pool') Object.assign(value.https, { source: 'pool_history', reason: 'stale_source', fresh: false, delay_ms: null });
      if (mode === 'not_run' || mode === 'disabled') Object.assign(value.https, { state: 'not_run', source: null, reason: mode === 'disabled' ? 'transport_disabled' : 'policy_reject', delay_ms: null, observed_at: null, fresh: false });
      t.overrides.set(probe, { status: mode === 'error' ? 503 : 200, body: value });
      await t.page.click('#evidence-check'); await t.page.waitForFunction(() => !document.querySelector('#evidence-check').disabled);
      assert.doesNotMatch(await t.page.textContent('#evidence-exit'), /47 ms/);
      if (mode === 'pool') assert.match(await t.page.textContent('#evidence-exit'), /Existing pool group history/);
      if (mode === 'error') assert.match(await t.page.textContent('#evidence-exit'), /cause is unknown/);
    });
    console.log(`Passed ${count} synthetic Chromium Connection evidence cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
