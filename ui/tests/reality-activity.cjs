// Actual embedded modules with synthetic controller-derived activity observations.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
const dual = { ...examplePeer, online: false, wireguard_enabled: true, reality_enabled: true, reality_revision: 3, reality_profile_status: 'available', reality_profile_downloadable: true };
const observed = (count, at = new Date().toISOString()) => ({ state: 'observed', open_flows: count, observed_at: at });
(async () => {
  const browser = await chromium.launch({ headless: true }); let cases = 0;
  async function check(peer, run) { const t = await fixture(browser, { peers: [peer], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } }); try { await run(t); cases++; } finally { await t.page.close(); } }
  try {
    await check({ ...dual, reality_activity: observed(2) }, async t => {
      assert.match(await t.page.locator('.client-row').innerText(), /Reality · 2 open flows/);
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 1);
      assert.match(await t.page.locator('.client-row').innerText(), /No WireGuard handshake yet/);
    });
    for (const value of [observed(0), { state: 'unavailable', open_flows: null, observed_at: null }, observed(-1)]) {
      await check({ ...dual, reality_activity: value }, async t => {
        assert.equal(await t.page.locator('.client-name .dot.on').count(), 0);
        assert.match(await t.page.locator('[data-reality-activity]').innerText(), value.open_flows === 0 ? /No open flows observed/ : /Activity unavailable/);
      });
    }
    // Server clock may lead or lag by hours; receipt freshness remains local.
    for (const offset of [-3600000, 3600000]) await check({ ...dual, reality_activity: observed(1, new Date(Date.now() + offset).toISOString()) }, async t => {
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 1);
      await t.page.clock.install(); await t.open(); await t.page.fill('#client-name', 'Unsaved draft');
      const reads = t.calls.filter(c => c.key === 'GET /api/peers').length;
      await t.page.clock.fastForward(31000);
      await t.page.waitForFunction(() => document.querySelector('[data-reality-activity]').textContent.includes('Activity unavailable'));
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 0);
      assert.match(await t.page.locator('#client-reality-status').innerText(), /Activity unavailable/);
      assert.equal(await t.page.inputValue('#client-name'), 'Unsaved draft');
      assert.equal(t.calls.filter(c => c.key === 'GET /api/peers').length, reads);
    });
    for (const operation of ['rotation', 'global-disable']) await check({ ...dual, reality_activity: observed(2) }, async t => {
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 1);
      const key = operation === 'rotation' ? 'POST /api/peers/example/reality/rotate' : 'PATCH /api/settings';
      if (operation === 'rotation') {
        await t.open(); await t.invoke('inspect-reality-rotation'); await t.done(); t.holds.add(key);
        await t.invoke('confirm-reality-rotation'); await t.waitHeld(key);
      } else {
        await t.page.evaluate(async () => { (await import('/js/screens/settings.js')).modalSettings(); });
        await t.page.check('#s-reality-on');
        // Saved fixture enables Reality to make disabling an actual auth mutation.
        await t.page.uncheck('#s-reality-on'); t.holds.add(key);
        await t.page.locator('#settings-network [type="submit"]').click(); await t.waitHeld(key);
      }
      await t.page.waitForFunction(() => !document.querySelector('.client-name .dot').classList.contains('on'));
      await t.reply(key, { error: 'Synthetic reconciliation uncertainty' }, 503);
      await t.page.waitForFunction(() => document.querySelector('[data-reality-activity]').textContent.includes('may already be revoked') || document.querySelector('[data-reality-activity]').textContent.includes('uncertain'));
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 0);
    });
    console.log(`Passed ${cases} REALITY activity browser cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
