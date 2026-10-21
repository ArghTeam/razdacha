const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer, exampleTunnels, config } = require('./client.fixture.cjs');
(async () => {
  const browser = await chromium.launch({ headless: true });
  let cases = 0;
  async function check(run, options) { const t = await fixture(browser, options); const errors = []; t.page.on('pageerror', error => errors.push(error.message)); try { await run(t); assert.deepEqual(errors, []); cases++; } finally { await t.page.close(); } }
  try {
    await check(async t => {
      assert.equal(await t.page.locator('.client-row [data-act]').count(), 1); assert.equal(await t.page.locator('.client-row [data-act="peer-details"]').count(), 1);
      await t.open(); await t.page.fill('#client-name', 'Renamed example'); await t.page.uncheck('#client-enabled'); await t.page.check('[name="client-route-mode"][value="tunnel"]'); await t.page.selectOption('#client-target', 'exit');
      await t.invoke('save-client-details'); await t.done();
      assert.deepEqual(t.calls.filter(c => c.key === 'PATCH /api/peers/example').map(c => c.body), [{ name: 'Renamed example', enabled: false, egress_tunnel_id: 'exit' }]);
      assert((await t.state()).dirty); assert.equal(t.calls.filter(c => c.key === 'POST /api/apply').length, 0);
      assert.match((await t.state()).modal, /10-second sync, before Apply/);
    });
    await check(async t => {
      await t.open(); await t.page.fill('#client-name', ''); assert.equal(await t.page.locator('[data-act="save-client-details"]').isDisabled(), true); assert.equal(await t.page.getAttribute('#client-name', 'aria-invalid'), 'true');
      await t.page.fill('#client-name', 'Retained draft'); await t.page.uncheck('#client-enabled');
      t.holds.add('PATCH /api/peers/example'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example'); await t.invoke('save-client-details');
      assert.equal(t.calls.filter(c => c.key.startsWith('PATCH')).length, 1); await t.reply('PATCH /api/peers/example', { error: 'Try this save again' }, 422);
      await t.page.getByText('Try this save again', { exact: true }).waitFor(); assert.equal(await t.page.inputValue('#client-name'), 'Retained draft'); assert.equal(await t.page.isChecked('#client-enabled'), false);
      assert.equal(await t.page.locator('[data-act="save-client-details"]').isDisabled(), false); await t.invoke('save-client-details'); await t.done(); assert.match((await t.state()).modal, /Settings saved/);
    });
    // A newer dialog's unrelated edit sends no stale untouched values, even when queued.
    await check(async t => {
      await t.open(); t.holds.add('PATCH /api/peers/example'); await t.page.fill('#client-name', 'Renamed example'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example');
      await t.open(); await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details');
      assert.equal(t.calls.filter(c => c.key.startsWith('PATCH')).length, 1);
      await t.reply('PATCH /api/peers/example', { ...examplePeer, name: 'Renamed example' }); await t.done();
      assert.deepEqual(t.calls.filter(c => c.key.startsWith('PATCH')).map(c => c.body), [{ name: 'Renamed example' }, { enabled: false }]);
      assert.equal((await t.state()).peers[0].name, 'Renamed example'); assert.equal((await t.state()).peers[0].enabled, false);
      assert.equal(await t.page.inputValue('#client-name'), 'Renamed example');
      await t.page.check('[name="client-route-mode"][value="tunnel"]'); await t.page.selectOption('#client-target', 'exit'); await t.invoke('save-client-details'); await t.done();
      assert.deepEqual(t.calls.filter(c => c.key.startsWith('PATCH')).map(c => c.body), [{ name: 'Renamed example' }, { enabled: false }, { egress_tunnel_id: 'exit' }]);
    });
    // A queued preceding route write must not become an accidental shared-mode edit later.
    await check(async t => {
      await t.open(); t.holds.add('PATCH /api/peers/example');
      await t.page.check('[name="client-route-mode"][value="tunnel"]'); await t.page.selectOption('#client-target', 'exit'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example');
      await t.open(); await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details');
      await t.reply('PATCH /api/peers/example', { ...examplePeer, egress_tunnel_id: 'exit' }); await t.done();
      assert.equal(await t.page.isChecked('[name="client-route-mode"][value="tunnel"]'), true); assert.equal(await t.page.inputValue('#client-target'), 'exit');
      await t.page.fill('#client-name', 'Unrelated later name'); await t.invoke('save-client-details'); await t.done();
      assert.deepEqual(t.calls.filter(c => c.key.startsWith('PATCH')).map(c => c.body), [{ egress_tunnel_id: 'exit' }, { enabled: false }, { name: 'Unrelated later name' }]);
      assert.equal((await t.state()).peers[0].egress_tunnel_id, 'exit');
    });
    // Queued requests from a former session are never dispatched.
    await check(async t => {
      await t.open(); t.holds.add('PATCH /api/peers/example'); await t.page.fill('#client-name', 'Old save'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example');
      await t.open(); await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details'); await t.relogin(); const before = await t.state();
      await t.reply('PATCH /api/peers/example', examplePeer); await t.done(); assert.equal(t.calls.filter(c => c.key.startsWith('PATCH')).length, 1); assert.deepEqual(await t.state(), before);
    });
    for (const exit of ['missing', 'disabled', 'empty', 'unknown-empty']) await check(async t => {
      await t.open(); await t.page.waitForFunction(() => !document.querySelector('#client-tunnels-status').textContent.includes('Loading'));
      assert.equal(await t.page.inputValue('#client-target'), exit); assert.match(await t.page.locator('#client-target-status').textContent(), /unavailable|blocked/);
      await t.page.fill('#client-name', 'Only name changes'); await t.invoke('save-client-details'); await t.done();
      assert.deepEqual(t.calls.find(c => c.key.startsWith('PATCH')).body, { name: 'Only name changes' });
    }, { peers: [{ ...examplePeer, egress_tunnel_id: exit }], tunnels: [...exampleTunnels, { id: 'unknown-empty', name: 'Empty pool without Clash', enabled: true, source: 'pool', pool: { servers_total: 0, servers_alive: null } }] });
    await check(async t => {
      await t.open(); await t.page.check('[name="client-route-mode"][value="tunnel"]'); assert.equal(await t.page.locator('[data-act="save-client-details"]').isDisabled(), true);
      assert.match(await t.page.locator('#client-tunnels-status').textContent(), /No tunnels/); assert.match(await t.page.locator('#client-route-scope').textContent(), /IPv6, ping/);
      await t.page.check('[name="client-route-mode"][value="shared"]'); await t.page.fill('#client-name', 'Editable without tunnels'); await t.invoke('save-client-details'); await t.done(); assert.match((await t.state()).modal, /Settings saved/);
    }, { tunnels: [] });
    await check(async t => {
      await t.open(); await t.page.check('[name="client-route-mode"][value="tunnel"]'); await t.page.selectOption('#client-target', 'exit'); await t.page.fill('#client-name', 'Keep my edits');
      t.overrides.set('GET /api/tunnels', { status: 503, body: { error: 'Tunnel list unavailable' } }); await t.invoke('retry-client-tunnels'); await t.done();
      assert.equal(await t.page.inputValue('#client-target'), 'exit'); assert.equal(await t.page.inputValue('#client-name'), 'Keep my edits');
      t.overrides.delete('GET /api/tunnels'); t.setTunnels([exampleTunnels[1]]); await t.page.locator('[data-act="retry-client-tunnels"]').click();
      await t.page.getByText('Missing exit · selection retained', { exact: true }).waitFor({ state: 'attached' }); assert.equal(await t.page.inputValue('#client-target'), 'exit');
    });
    await check(async t => {
      await t.open(); await t.page.fill('#client-name', 'Keep configuration draft');
      t.overrides.set('GET /api/peers/example/config', { status: 503, body: { error: 'Configuration unavailable' } }); await t.expand(); await t.page.getByText('Configuration unavailable', { exact: true }).waitFor();
      assert.equal(await t.page.inputValue('#client-name'), 'Keep configuration draft'); t.overrides.delete('GET /api/peers/example/config'); await t.page.locator('[data-act="retry-client-config"]').click(); await t.page.locator('#client-config-result').waitFor({ state: 'visible' });
      assert.equal(await t.page.inputValue('#client-name'), 'Keep configuration draft'); assert.equal(await t.page.locator('[data-act="download-conf"]').isDisabled(), true);
      await t.page.locator('#client-configuration > summary').click(); await t.expand(); assert.equal(await t.page.inputValue('#client-name'), 'Keep configuration draft');
    });
    await check(async t => {
      await t.open(); await t.readyConfig(); assert.equal((await t.state()).filename, 'example.conf'); await t.page.fill('#client-name', 'Renamed example');
      assert.equal(await t.page.locator('#client-config-draft-note').isVisible(), true); assert.match(await t.page.locator('#client-config-draft-note').textContent(), /Save the name change/);
      await t.page.fill('#client-name', examplePeer.name); assert.equal(await t.page.locator('#client-config-draft-note').isVisible(), false);
      await t.page.fill('#client-name', 'Renamed example');
      await t.invoke('save-client-details'); await t.done();
      await t.page.waitForFunction(() => document.querySelector('#modal').dataset.confName === 'renamed.conf'); assert.equal(t.calls.filter(c => c.key.endsWith('/config')).length, 2);
      assert.equal(await t.page.locator('[data-act="download-conf"]').isDisabled(), false); assert.equal(await t.page.locator('#client-config-draft-note').isVisible(), false);
    });
    // An older config response cannot revive the old filename after a saved rename.
    await check(async t => {
      await t.open(); const key = 'GET /api/peers/example/config'; t.holds.add(key); await t.expand(); await t.waitHeld(key); t.holds.delete(key);
      await t.page.fill('#client-name', 'Renamed example'); await t.invoke('save-client-details'); await t.done(); await t.page.waitForFunction(() => document.querySelector('#modal').dataset.confName === 'renamed.conf');
      await t.replyConfig(key, 'OLD PRIVATE CONFIGURATION', 'old.conf'); await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); });
      assert.equal((await t.state()).filename, 'renamed.conf'); assert.equal((await t.state()).conf, config);
    });
    for (const result of ['success', 'failure']) await check(async t => {
      await t.open(); await t.readyConfig();
      await t.page.evaluate(() => { window.copied = null; Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: text => { window.copied = text; return new Promise((resolve, reject) => { window.finishCopy = resolve; window.rejectCopy = reject; }); } } }); });
      await t.invoke('copy-client-config'); assert.equal(await t.page.evaluate(() => window.copied), config); await t.replace(); const before = await t.state();
      await t.page.evaluate(result => result === 'success' ? window.finishCopy() : window.rejectCopy(new Error('Synthetic clipboard denial')), result); await t.done(); assert.deepEqual(await t.state(), before);
    });
    await check(async t => {
      await t.open(); await t.readyConfig(); await t.page.evaluate(() => { Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async () => { throw Error('Denied'); } } }); });
      await t.invoke('copy-client-config'); await t.done(); assert.match((await t.state()).modal, /Use Download .conf/);
      await t.page.evaluate(() => { window.revoked = []; const revoke = URL.revokeObjectURL.bind(URL); URL.revokeObjectURL = url => { window.revoked.push(url); revoke(url); }; });
      const download = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-conf"]').click(); assert.equal((await download).suggestedFilename(), 'example.conf');
      await t.page.keyboard.press('Escape'); assert((await t.page.evaluate(() => window.revoked)).length); assert.equal((await t.state()).conf, ''); assert.equal((await t.state()).filename, ''); assert.equal(await t.page.locator('#modal canvas').count(), 0);
    });
    // An old authenticated inventory load cannot overwrite a newer login.
    await check(async t => {
      const key = 'GET /api/peers'; t.holds.add(key); await t.page.evaluate(() => { window.oldLoad = import('/js/screens/peers.js').then(screen => screen.load()); }); await t.waitHeld(key); t.holds.delete(key);
      await t.relogin(); const before = await t.state(); await t.reply(key, [{ ...examplePeer, name: 'Old inventory' }]); await t.page.evaluate(() => window.oldLoad); assert.deepEqual(await t.state(), before);
    });
    // Newer inventory loads and completed mutations supersede older same-session snapshots.
    for (const operation of ['reload', 'mutation']) await check(async t => {
      const key = 'GET /api/peers'; t.holds.add(key); await t.page.evaluate(() => { window.oldLoad = import('/js/screens/peers.js').then(screen => screen.load()); }); await t.waitHeld(key); t.holds.delete(key);
      if (operation === 'reload') await t.page.evaluate(async () => { await (await import('/js/screens/peers.js')).load(); });
      else { await t.open(); await t.page.fill('#client-name', 'Accepted name'); await t.invoke('save-client-details'); await t.done(); }
      const before = await t.state(); await t.reply(key, [{ ...examplePeer, name: 'Obsolete name' }]); await t.page.evaluate(() => window.oldLoad); assert.deepEqual(await t.state(), before);
    });
    for (const status of [200, 500]) await check(async t => {
      const key = 'GET /api/peers/example/config'; await t.open(); t.holds.add(key); await t.expand(); await t.waitHeld(key); await t.relogin(); const before = await t.state();
      if (status === 200) await t.replyConfig(); else await t.reply(key, { error: 'Old configuration failure' }, status);
      await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); }); assert.deepEqual(await t.state(), before);
    });
    await check(async t => {
      await t.page.context().grantPermissions(['clipboard-read', 'clipboard-write']); await t.open(); await t.readyConfig(); await t.invoke('copy-client-config'); await t.done();
      assert.match((await t.state()).modal, /Configuration copied/); assert.equal(await t.page.evaluate(() => navigator.clipboard.readText()), config);
    });
    // Durable same-session mutations still become visible after ordinary dismissal.
    for (const action of ['save', 'delete']) await check(async t => {
      await t.open(); const key = `${action === 'save' ? 'PATCH' : 'DELETE'} /api/peers/example`; t.holds.add(key);
      if (action === 'save') { await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details'); }
      else { await t.invoke('inspect-client-delete'); await t.done(); await t.invoke('confirm-client-delete'); }
      await t.waitHeld(key); await t.page.keyboard.press('Escape'); await t.reply(key, { inactivated_rule_ids: [] }); await t.done();
      const result = await t.state(); assert(result.dirty); assert.equal(result.modal, ''); assert.equal(result.toasts, '');
      if (action === 'save') assert.equal(result.peers[0].enabled, false); else assert.equal(result.peers.length, 0);
    });
    console.log(`Passed ${cases} synthetic Chromium client-details cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
