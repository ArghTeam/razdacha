// Actual embedded client/settings modules with synthetic authenticated API responses.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer, config } = require('./client.fixture.cjs');
const dual = { ...examplePeer, wireguard_enabled: true, reality_enabled: true, reality_revision: 3, reality_profile_status: 'available', reality_profile_downloadable: true };
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
    await check(async t => {
      await t.open(); await t.readyConfig();
      const warning = t.page.locator('#client-reality-pending-note');
      assert(await warning.isVisible());
      await t.page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
      await t.invoke('copy-client-config'); await t.done(); assert(await warning.isVisible());
      for (const action of ['download-conf', 'download-reality-json']) {
        const event = t.page.waitForEvent('download'); await t.page.locator(`[data-act="${action}"]`).click(); await event;
        assert(await warning.isVisible()); assert.match(await warning.textContent(), /Works after Apply/);
      }
      await t.page.fill('#client-name', 'Unsaved edit');
      assert(await t.page.locator('[data-act="go-to-client-apply"]').isDisabled());
      await t.page.fill('#client-name', dual.name);
      await t.page.locator('[data-act="go-to-client-apply"]').click();
      assert.equal((await t.state()).conf, ''); assert.equal(await t.page.locator('#modal-backdrop').isVisible(), false);
      assert.equal(t.calls.filter(c => c.key === 'POST /api/apply').length, 0);
      assert.equal(await t.page.locator('#btn-apply').evaluate(el => el === document.activeElement), true);
    }, { peers: [{ ...dual, wireguard_enabled: false, reality_profile_status: 'pending' }], dirty: true });
    for (const metadata of [
      { 'X-Razdacha-Reality-Revision': '2' },
      { 'X-Razdacha-Reality-State': 'guarded' },
      { 'X-Razdacha-Reality-Revision': '' },
    ]) await check(async t => {
      await t.open(); const key = 'GET /api/peers/example/reality-config'; t.holds.add(key); await t.expand(); await t.waitHeld(key);
      await t.replyConfig(key, t.realityURI, 'stale.txt', metadata);
      await t.page.getByText('Reality access changed while loading. Reload its status before requesting another profile.', { exact: true }).waitFor();
      assert.equal((await t.state()).conf, '');
    }, { peers: [{ ...dual, wireguard_enabled: false, reality_profile_status: 'pending' }] });
    await check(async t => {
      await t.open(); const key = 'GET /api/peers/example/reality-config'; t.holds.add(key); await t.expand(); await t.waitHeld(key);
      await t.replyConfig(key, t.realityURI, 'pending.txt', { 'X-Razdacha-Reality-State': 'pending' });
      await t.readyConfig(); assert(await t.page.locator('#client-reality-pending-note').isVisible());
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    for (const metadata of [
      { 'X-Razdacha-Reality-State': 'pending', 'X-Razdacha-Reality-Revision': '2' },
      { 'X-Razdacha-Reality-State': 'guarded', 'X-Razdacha-Reality-Revision': '3' },
    ]) await check(async t => {
      await t.open(); await t.readyConfig(); const downloads = [];
      t.page.on('download', item => downloads.push(item));
      t.overrides.set('GET /api/peers/example/reality-client-config', { status: 200, headers: metadata, body: { inbounds: [], outbounds: [] } });
      await t.invoke('download-reality-json'); await t.done();
      assert.equal(downloads.length, 0);
      assert.match(await t.page.locator('#client-config-status').textContent(), /Reality access changed while loading/);
      assert(await t.page.locator('#client-reality-pending-note').isVisible());
    }, { peers: [{ ...dual, wireguard_enabled: false, reality_profile_status: 'pending' }] });
    await check(async t => {
      await t.open(); await t.readyConfig();
      const requests = t.calls.filter(c => c.key.endsWith('/reality-config')).length;
      await t.page.evaluate(async () => { (await import('/js/screens/settings.js')).modalSettings(); });
      await t.page.uncheck('#s-reality-on'); await t.page.locator('#settings-network [type="submit"]').click();
      await t.page.getByText(/Network saved. Use Apply/).waitFor();
      assert.equal((await t.state()).peers[0].reality_profile_downloadable, false);
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand();
      assert.equal((await t.state()).conf, '');
      assert.equal(t.calls.filter(c => c.key.endsWith('/reality-config')).length, requests);
      assert.match(await t.page.locator('#client-config-status').textContent(), /Reload Reality status/);
    }, { peers: [{ ...dual, wireguard_enabled: false }], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } });
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
    }, { peers: [{ ...dual, wireguard_enabled: false, reality_profile_status: status, reality_profile_downloadable: false, enabled: status !== 'disabled' }] });
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
      await t.readyConfig(); assert.equal((await t.state()).conf, t.realityURI);
      assert.equal(t.calls.filter(c => c.key === 'POST /api/apply').length, 0);
      assert.match(await t.page.locator('#client-reality-pending-note').textContent(), /Works after Apply/);
      assert(await t.page.locator('#client-reality-pending-note').isVisible());
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
    for (const field of ['#client-enabled', '#client-reality']) await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'PATCH /api/peers/example'; t.holds.add(key);
      await t.page.uncheck(field); await t.invoke('save-client-details'); await t.waitHeld(key); await t.page.evaluate(() => { window.savePromise = window.actionPromise; });
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
      await t.reply(key, { error: 'Revocation persisted; reconciliation failed' }, 503); await t.page.evaluate(() => window.savePromise);
      assert.match(await t.page.locator('#client-reality-status').textContent(), /may already be revoked/);
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
    }, { peers: [dual] });
    await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'DELETE /api/peers/example'; t.holds.add(key);
      await t.invoke('inspect-client-delete'); await t.done(); await t.invoke('confirm-client-delete'); await t.waitHeld(key); await t.page.evaluate(() => { window.deletePromise = window.actionPromise; });
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand();
      t.setPeers([]); await t.reply(key, { error: 'Deletion persisted; reconciliation failed' }, 503); await t.page.evaluate(() => window.deletePromise);
      assert.equal((await t.state()).conf, ''); await t.invoke('refresh-reality-status'); await t.done();
      assert.equal((await t.state()).peers.length, 0); assert.equal((await t.state()).modal, '');
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.page.locator('#btn-settings').click(); await t.page.uncheck('#s-reality-on'); const key = 'PATCH /api/settings'; t.holds.add(key);
      await t.page.locator('#settings-network [type="submit"]').click(); await t.waitHeld(key);
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
      await t.reply(key, { error: 'Global revocation persisted; reconciliation failed' }, 503);
      await t.page.waitForFunction(() => document.querySelector('#client-reality-status').textContent.includes('could not be confirmed'));
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, '');
      t.setPeer({ id: 'example', reality_profile_status: 'guarded' }); await t.invoke('refresh-reality-status'); await t.done();
      assert.match(await t.page.locator('#client-reality-status').textContent(), /guarded/);
    }, { peers: [{ ...dual, wireguard_enabled: false }], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } });
    await check(async t => {
      await t.open(); await t.readyConfig(); assert.equal(t.calls.filter(c => c.key.endsWith('/reality-client-config')).length, 0);
      const event = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-reality-json"]').click();
      const downloaded = await event; assert.equal(downloaded.suggestedFilename(), 'razdacha-reality.json');
      const data = JSON.parse(require('node:fs').readFileSync(await downloaded.path(), 'utf8'));
      assert.equal(data.inbounds[0].type, 'tun'); assert.equal(data.dns.servers[0].address, '10.8.0.1');
      assert.equal((await t.state()).conf, t.realityURI);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    for (const cancel of ['close', 'dirty', 'rotate', 'session']) await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'GET /api/peers/example/reality-client-config'; t.holds.add(key); const downloads = [];
      t.page.on('download', item => downloads.push(item)); await t.invoke('download-reality-json'); await t.waitHeld(key); await t.page.evaluate(() => { window.jsonPromise = window.actionPromise; });
      if (cancel === 'close') await t.page.keyboard.press('Escape');
      else if (cancel === 'dirty') await t.page.fill('#client-name', 'Unsaved name');
      else if (cancel === 'session') await t.relogin();
      else { await t.invoke('inspect-reality-rotation'); await t.done(); await t.invoke('confirm-reality-rotation'); await t.done(); }
      await t.reply(key, { inbounds: [{ type: 'tun' }], outbounds: [{ type: 'vless' }] }); await t.page.evaluate(() => window.jsonPromise);
      assert.equal(downloads.length, 0);
    }, { peers: [{ ...dual, wireguard_enabled: false }] });
    await check(async t => {
      await t.open(); await t.readyConfig(); const key = 'GET /api/peers'; t.holds.add(key);
      await t.invoke('refresh-reality-status'); await t.waitHeld(key); await t.page.evaluate(() => { window.oldStatusPromise = window.actionPromise; });
      await t.page.evaluate(async () => { (await import('/js/screens/settings.js')).modalSettings(); });
      t.overrides.set('PATCH /api/settings', { status: 503, body: { error: 'Global revocation incomplete' } });
      await t.page.uncheck('#s-reality-on'); await t.page.locator('#settings-network [type="submit"]').click();
      await t.page.getByText(/Save could not be confirmed/).waitFor(); await t.page.keyboard.press('Escape'); await t.open(); await t.expand();
      await t.reply(key, [{ ...dual, wireguard_enabled: false }]); await t.page.evaluate(() => window.oldStatusPromise);
      assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-reality-status').textContent(), /could not be confirmed/);
    }, { peers: [{ ...dual, wireguard_enabled: false }], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } });
    await check(async t => {
      t.overrides.set('PATCH /api/settings', { status: 503, body: { error: 'Global reconciliation failed' } });
      await t.page.locator('#btn-settings').click(); await t.page.uncheck('#s-reality-on'); await t.page.locator('#settings-network [type="submit"]').click();
      await t.page.getByText(/Save could not be confirmed/).waitFor(); t.setPeer({ id: 'example', reality_profile_status: 'guarded' });
      await t.page.locator('#s-reality-reload').click(); await t.page.getByText(/Saved Network settings reloaded/).waitFor();
      await t.page.keyboard.press('Escape'); await t.open(); await t.expand(); assert.equal((await t.state()).conf, ''); assert.match(await t.page.locator('#client-reality-status').textContent(), /guarded/);
    }, { peers: [{ ...dual, wireguard_enabled: false }], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } });
    await check(async t => {
      await t.open(); const write = 'PATCH /api/peers/example'; t.holds.add(write); await t.page.fill('#client-name', 'Newer saved name');
      await t.invoke('save-client-details'); await t.waitHeld(write); await t.page.evaluate(() => { window.namePromise = window.actionPromise; });
      await t.page.evaluate(async () => { (await import('/js/screens/settings.js')).modalSettings(); });
      t.overrides.set('PATCH /api/settings', { status: 503, body: { error: 'Global reconciliation failed' } });
      await t.page.uncheck('#s-reality-on'); await t.page.locator('#settings-network [type="submit"]').click(); await t.page.getByText(/Save could not be confirmed/).waitFor();
      const read = 'GET /api/peers'; t.holds.add(read); await t.page.locator('#s-reality-reload').click(); await t.waitHeld(read);
      await t.reply(write, { ...dual, name: 'Newer saved name' }); await t.page.evaluate(() => window.namePromise);
      await t.reply(read, [dual]); await t.page.getByText('Settings or clients changed while loading. Retry the explicit reload.', { exact: true }).waitFor();
      assert.equal((await t.state()).peers[0].name, 'Newer saved name');
    }, { peers: [dual], settings: { reality_enabled: true, reality_server_name: 'tls.example', reality_handshake_server: 'tls.example' } });
    for (const width of [320, 390, 1440]) await check(async t => {
      await t.open(); await t.readyConfig(); await t.page.locator('[data-act="select-client-reality"]').focus(); await t.page.keyboard.press('Enter');
      await t.page.waitForFunction(() => document.querySelector('#modal').dataset.conf?.startsWith('vless://'));
      assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      assert.equal(await t.page.locator('[data-act="select-client-reality"]').getAttribute('aria-pressed'), 'true');
    }, { peers: [dual], viewport: { width, height: 900 } });
    console.log(`Passed ${cases} synthetic Chromium Reality connection cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
