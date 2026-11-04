// Actual embedded client/settings modules with synthetic authenticated API responses.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer, config } = require('./client.fixture.cjs');
const dual = { ...examplePeer, wireguard_enabled: true, reality_enabled: true, reality_revision: 3, reality_profile_status: 'available' };
(async () => {
  const browser = await chromium.launch({ headless: true });
  let cases = 0;
  async function check(run, options = {}) {
    const t = await fixture(browser, options); const errors = [];
    t.page.on('pageerror', error => errors.push(error.message));
    try {
      await run(t); assert.deepEqual(errors, []);
      assert.deepEqual(t.calls.filter(c => /^(POST|PATCH|DELETE) \/api\/tunnels/.test(c.key)), []);
      cases++;
    } finally { await t.page.close(); }
  }
  try {
    await check(async t => {
      await t.open(); assert(await t.page.isChecked('#client-wireguard')); assert(!await t.page.isChecked('#client-reality'));
      await t.readyConfig(); assert.equal((await t.state()).conf, config);
      await t.page.check('#client-reality'); assert.equal((await t.state()).conf, '');
      await t.invoke('save-client-details'); await t.done();
      assert.deepEqual(t.calls.find(c => c.key.startsWith('PATCH')).body, { reality_enabled: true });
      assert(await t.page.isChecked('#client-wireguard'));
    });
    await check(async t => {
      await t.open(); await t.readyConfig(); await t.invoke('select-client-reality'); await t.done();
      await t.page.locator('#client-config-result').waitFor({ state: 'visible' });
      assert.equal((await t.state()).conf, t.realityURI); assert.equal((await t.state()).filename, 'reality-example.txt');
      assert(await t.page.locator('#modal canvas').count() > 0);
      await t.page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
      await t.invoke('copy-client-config'); await t.done(); assert.equal(await t.page.evaluate(() => navigator.clipboard.readText()), t.realityURI);
      const download = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-conf"]').click(); assert.equal((await download).suggestedFilename(), 'reality-example.txt');
      await t.page.keyboard.press('Escape'); assert.equal((await t.state()).conf, ''); assert.equal(await t.page.locator('#modal canvas').count(), 0);
    }, { peers: [dual] });
    for (const leave of ['method', 'close', 'session']) await check(async t => {
      await t.open(); const key = 'GET /api/peers/example/reality-config'; t.holds.add(key);
      await t.invoke('select-client-reality'); await t.done(); await t.expand(); await t.waitHeld(key);
      if (leave === 'method') { await t.invoke('select-client-wg'); await t.done(); await t.page.locator('#client-config-result').waitFor({ state: 'visible' }); }
      else if (leave === 'close') await t.page.keyboard.press('Escape'); else await t.relogin();
      const before = await t.state(); await t.replyConfig(key, 'vless://old-credential@example.test:8443', 'old.txt');
      await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); });
      assert.deepEqual(await t.state(), before);
    }, { peers: [dual] });
    for (const status of ['pending', 'guarded', 'disabled']) await check(async t => {
      await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
      assert.equal(t.calls.filter(c => c.key.endsWith('/reality-config')).length, 0);
      assert.match(await t.page.locator('#client-config-status').textContent(), status === 'pending' ? /Apply/ : status === 'guarded' ? /guard|Apply/i : /disabled|Enable/i);
      assert.equal(await t.page.locator('[data-act="copy-client-config"]').isDisabled(), true);
      assert(!await t.page.locator('.client-row').textContent().then(text => text.includes(examplePeer.address)));
    }, { peers: [{ ...dual, wireguard_enabled: false, reality_profile_status: status, enabled: status !== 'disabled' }] });
    await check(async t => {
      await t.open(); await t.readyConfig(); await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.done();
      assert.deepEqual(t.calls.find(c => c.key.endsWith('/reality/rotate')).body, { expected_revision: 3 });
      assert.equal((await t.state()).peers[0].reality_revision, 4); assert.equal((await t.state()).conf, '');
      assert.match(await t.page.locator('#client-reality-rotation-status').textContent(), /Apply/);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); await t.readyConfig(); t.overrides.set('POST /api/peers/example/reality/rotate', { status: 503, body: { error: 'Ingress reconciliation failed' } });
      await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.done();
      assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-reality-rotation-status').textContent(), /Ingress reconciliation failed.*may already be revoked/);
      await t.invoke('retry-client-config'); await t.done(); assert.equal((await t.state()).conf, '');
      t.setPeer({ id: 'example', reality_profile_status: 'guarded', reality_revision: 4 });
      await t.invoke('refresh-reality-status'); await t.done(); assert.match(await t.page.locator('#client-reality-status').textContent(), /guard|Apply/i);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.page.locator('#btn-settings').click(); await t.page.check('#s-reality-on'); await t.page.fill('#s-reality-name', 'tls.example'); await t.page.fill('#s-reality-target', 'tls.example');
      await t.page.locator('#settings-network [type="submit"]').click();
      await t.page.waitForFunction(() => document.querySelector('#settings-network .settings-status').textContent.includes('saved'));
      assert.deepEqual(t.calls.find(c => c.key === 'PATCH /api/settings').body, { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' });
      assert.equal(t.calls.filter(c => c.key === 'POST /api/apply').length, 0);
    });
    await check(async t => {
      await t.invoke('add-peer'); await t.done(); await t.page.fill('#peer-name', 'Reality device');
      await t.page.uncheck('#new-peer-wg'); await t.page.check('#new-peer-reality');
      await t.invoke('create-peer'); await t.done();
      assert.deepEqual(t.calls.find(c => c.key === 'POST /api/peers').body, { name: 'Reality device', wireguard_enabled: false, reality_enabled: true });
      assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-config-status').textContent(), /Apply/);
      await t.page.keyboard.press('Escape'); await t.page.locator('#btn-apply').click();
      await t.page.waitForFunction(() => document.querySelector('#apply-bar').hidden);
      await t.open('created'); await t.readyConfig(); assert.equal((await t.state()).conf, t.realityURI);
      assert.equal(t.calls.filter(c => c.key === 'GET /api/peers/created/config').length, 0);
    }, { settings: { reality_enabled: true } });
    await check(async t => {
      await t.open(); await t.readyConfig(); t.overrides.set('POST /api/peers/example/reality/rotate', { status: 409, body: { error: 'Credential revision changed; reload before replacing' } });
      await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.done();
      assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-reality-rotation-status').textContent(), /revision changed/);
      assert.equal(await t.page.locator('[data-act="inspect-reality-rotation"]').isDisabled(), true);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); await t.expand(); await t.page.getByText('The server did not return a valid Reality import profile.', { exact: true }).waitFor();
      assert.equal((await t.state()).conf, ''); assert.equal(await t.page.locator('[data-act="copy-client-config"]').isDisabled(), true);
    }, { peers: [{ ...dual, wireguard_enabled: false }], realityURI: 'invalid response' });
    await check(async t => {
      await t.open(); const key = 'GET /api/peers'; t.holds.add(key); await t.invoke('refresh-reality-status'); await t.waitHeld(key);
      await t.page.fill('#client-name', 'Retained edit'); await t.reply(key, [{ ...dual, name: 'Outdated remote name' }]); await t.done();
      assert.equal(await t.page.inputValue('#client-name'), 'Retained edit'); assert.match(await t.page.locator('#client-reality-rotation-status').textContent(), /edits are retained/);
    }, { peers: [dual] });
    await check(async t => {
      await t.open(); await t.readyConfig(); await t.invoke('inspect-reality-rotation'); await t.done();
      await t.page.evaluate(async () => { (await import('/js/shell.js')).state.peers[0].reality_revision = 4; });
      await t.invoke('confirm-reality-rotation'); await t.done();
      assert.deepEqual(t.calls.find(c => c.key.endsWith('/reality/rotate')).body, { expected_revision: 3 });
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); const key = 'GET /api/peers/example/reality-config'; t.holds.add(key); await t.expand(); await t.waitHeld(key);
      await t.page.evaluate(async () => { (await import('/js/shell.js')).state.peers[0].reality_revision = 4; });
      await t.replyConfig(key, t.realityURI, 'old.txt');
      await t.page.getByText('Reality access changed while loading. Reload its status before requesting another profile.', { exact: true }).waitFor();
      assert.equal((await t.state()).conf, '');
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    for (const failureAfterReopen of [true, false]) await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'POST /api/peers/example/reality/rotate'; t.holds.add(key);
      await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.waitHeld(key);
      await t.page.evaluate(() => { window.rotationPromise = window.actionPromise; });
      if (failureAfterReopen) {
        await t.page.keyboard.press('Escape'); await t.open(); await t.expand();
        assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-config-status').textContent(), /still reconciling/);
        assert.equal(t.calls.filter(c => c.key.endsWith('/reality-config')).length, 1);
      }
      await t.reply(key, { error: 'Ingress reconciliation failed after revocation' }, 503); await t.page.evaluate(() => window.rotationPromise);
      if (!failureAfterReopen) { await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); }
      assert.equal((await t.state()).conf, ''); assert.equal(await t.page.locator('[data-act="copy-client-config"]').isDisabled(), true);
      assert.match(await t.page.locator('#client-reality-status').textContent(), /may already be revoked/);
      await t.invoke('retry-client-config'); await t.done(); assert.equal(t.calls.filter(c => c.key.endsWith('/reality-config')).length, 1);
      t.setPeer({ id: 'example', reality_revision: 4, reality_profile_status: 'guarded' });
      await t.invoke('refresh-reality-status'); await t.done(); assert.match(await t.page.locator('#client-reality-status').textContent(), /guarded/);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); await t.readyConfig(); const read = 'GET /api/peers', rotate = 'POST /api/peers/example/reality/rotate'; t.holds.add(read); t.holds.add(rotate);
      await t.invoke('refresh-reality-status'); await t.waitHeld(read); await t.page.evaluate(() => { window.statusPromise = window.actionPromise; });
      await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.waitHeld(rotate);
      await t.reply(rotate, { error: 'Reconciliation unavailable' }, 503); await t.done();
      await t.reply(read, [{ ...dual, wireguard_enabled: false }]); await t.page.evaluate(() => window.statusPromise);
      assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-reality-status').textContent(), /may already be revoked/);
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'POST /api/peers/example/reality/rotate'; t.holds.add(key);
      await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.waitHeld(key);
      await t.page.evaluate(() => { window.rotationPromise = window.actionPromise; }); await t.relogin(); await t.open(); await t.readyConfig();
      await t.reply(key, { error: 'Previous login failure' }, 503); await t.page.evaluate(() => window.rotationPromise);
      assert.equal((await t.state()).conf, t.realityURI); assert.equal(await t.page.locator('[data-act="copy-client-config"]').isDisabled(), false);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    for (const width of [320, 390, 1440]) await check(async t => {
      await t.open(); await t.readyConfig(); await t.page.locator('[data-act="select-client-reality"]').focus(); await t.page.keyboard.press('Enter');
      await t.page.waitForFunction(() => document.querySelector('#modal').dataset.conf?.startsWith('vless://'));
      assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.equal(await t.page.locator('[data-act="select-client-reality"]').getAttribute('aria-pressed'), 'true');
    }, { peers: [dual], viewport: { width, height: 900 } });
    console.log(`Passed ${cases} synthetic Chromium Reality connection cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
