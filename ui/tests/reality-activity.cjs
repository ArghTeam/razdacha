// Actual embedded modules with synthetic controller-derived activity observations.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
const dual = { ...examplePeer, online: false, wireguard_enabled: true, reality_enabled: true, reality_revision: 3, reality_profile_status: 'available', reality_profile_downloadable: true };
const observed = (count, at = new Date().toISOString()) => ({ state: 'observed', open_flows: count, observed_at: at });
const traffic = (count, upload, download) => ({ ...observed(count), open_upload_bytes: upload, open_download_bytes: download });
(async () => {
  const browser = await chromium.launch({ headless: true }); let cases = 0;
  async function check(peer, run) { const t = await fixture(browser, { peers: Array.isArray(peer) ? peer : [peer], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } }); try { await run(t); cases++; } finally { await t.page.close(); } }
  try {
    await check({ ...dual, reality_activity: traffic(2, 1024, 2048) }, async t => {
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), '1.0 KiB');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), '2.0 KiB');
      assert.equal(await t.page.locator('[data-reality-summary="clients"]').innerText(), '1');
      assert.match(await t.page.locator('.client-row').innerText(), /Open-flow received 1.0 KiB · sent 2.0 KiB/);
      assert.match(await t.page.locator('.summary').innerText(), /WireGuard received\n24.0 KiB/);
      assert.match(await t.page.locator('.summary').innerText(), /WireGuard sent\n1.0 KiB/);
      assert.match(await t.page.locator('.screen-sub').allTextContents().then(x => x.join(' ')), /different scopes/);
      // Re-observing the same active flows cannot accumulate their bytes.
      await t.page.evaluate(async () => { await (await import('/js/screens/peers.js')).load(); (await import('/js/shell.js')).refresh(); });
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), '1.0 KiB');
      t.setPeer({ id: dual.id, reality_activity: traffic(1, 20, 40) });
      await t.page.evaluate(async () => { await (await import('/js/screens/peers.js')).load(); (await import('/js/shell.js')).refresh(); });
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), '20 B');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), '40 B');
    });
    await check({ ...dual, wireguard_enabled: false, reality_activity: traffic(0, 0, 0) }, async t => {
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), '0 B');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), '0 B');
      assert.equal(await t.page.locator('[data-reality-summary="clients"]').innerText(), '0');
      assert.match(await t.page.locator('.summary').innerText(), /WireGuard received\n0 B/);
    });
    for (const bad of [null, -1, 0.5, '100', Number.MAX_SAFE_INTEGER + 1]) {
      await check({ ...dual, reality_activity: traffic(1, bad, 25) }, async t => {
        assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), 'Unknown');
        assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), '25 B');
        assert.equal(await t.page.locator('[data-reality-summary="clients"]').innerText(), '1');
      });
    }
    for (const other of [
      { ...traffic(1, 5, 10), open_upload_bytes: null },
      { state: 'unavailable', open_flows: null, observed_at: null },
    ]) await check([{ ...dual, reality_activity: traffic(1, 10, 20) }, { ...dual, id: 'second', name: 'Second', reality_activity: other }], async t => {
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), 'Unknown');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), other.state === 'observed' ? '30 B' : 'Unknown');
      assert.equal(await t.page.locator('[data-reality-summary="clients"]').innerText(), other.state === 'observed' ? '2' : 'Unknown');
    });
    await check([{ ...dual, reality_activity: traffic(1, Number.MAX_SAFE_INTEGER, 2) }, { ...dual, id: 'second', name: 'Second', reality_activity: traffic(1, 1, 3) }], async t => {
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), 'Unknown');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), '5 B');
    });
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
    for (const offset of [-3600000, 3600000]) await check({ ...dual, reality_activity: { ...traffic(1, 100, 200), observed_at: new Date(Date.now() + offset).toISOString() } }, async t => {
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 1);
      await t.page.clock.install(); await t.open(); await t.page.fill('#client-name', 'Unsaved draft');
      const reads = t.calls.filter(c => c.key === 'GET /api/peers').length;
      await t.page.clock.fastForward(31000);
      await t.page.waitForFunction(() => document.querySelector('[data-reality-activity]').textContent.includes('Activity unavailable'));
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 0);
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), 'Unknown');
      assert.equal(await t.page.locator('[data-reality-summary="sent"]').innerText(), 'Unknown');
      assert.equal(await t.page.locator('[data-reality-summary="clients"]').innerText(), 'Unknown');
      assert.match(await t.page.locator('#client-reality-status').innerText(), /Activity unavailable/);
      assert.equal(await t.page.inputValue('#client-name'), 'Unsaved draft');
      assert.equal(await t.page.locator('#client-name').evaluate(el => el === document.activeElement), true);
      assert.equal(t.calls.filter(c => c.key === 'GET /api/peers').length, reads);
    });
    for (const operation of ['rotation', 'global-disable']) await check({ ...dual, reality_activity: traffic(2, 100, 200) }, async t => {
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
      assert.equal(await t.page.locator('[data-reality-summary="received"]').innerText(), 'Unknown');
      await t.reply(key, { error: 'Synthetic reconciliation uncertainty' }, 503);
      await t.page.waitForFunction(() => document.querySelector('[data-reality-activity]').textContent.includes('may already be revoked') || document.querySelector('[data-reality-activity]').textContent.includes('uncertain'));
      assert.equal(await t.page.locator('.client-name .dot.on').count(), 0);
    });
    console.log(`Passed ${cases} REALITY activity browser cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
