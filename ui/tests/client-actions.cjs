// Run with Playwright installed normally or resolved through NODE_PATH.
// This suite preserves #244 lifetime regressions through the themed #246 controls.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, examplePeer } = require('./client.fixture.cjs');
(async () => {
  const browser = await chromium.launch({ headless: true });
  let count = 0;
  async function check(run, options) { const t = await fixture(browser, options); try { await run(t); count++; } finally { await t.page.close(); } }
  try {
    for (const mode of ['enter', 'mouse']) await check(async t => {
      const key = 'POST /api/peers'; t.holds.add(key); await t.invoke('add-peer'); await t.page.fill('#peer-name', 'Created example');
      if (mode === 'mouse') await t.page.locator('[data-act="create-peer"]').click(); else await t.page.locator('#peer-name').press('Enter');
      await t.waitHeld(key); await t.page.locator('#peer-name').press('Enter'); await t.page.locator('#peer-name').press('Enter');
      await t.page.evaluate(() => document.querySelector('[data-act="create-peer"]').click());
      assert.equal(t.calls.filter(x => x.key === key).length, 1);
      await t.page.keyboard.press('Escape'); await t.reply(key, { ...examplePeer, id: 'created' });
      await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.peers.length === 2);
      assert((await t.state()).dirty); assert.equal((await t.state()).modal, '');
    });
    await check(async t => {
      t.holds.add('POST /api/peers'); await t.invoke('add-peer'); await t.page.fill('#peer-name', 'Retry example'); await t.page.locator('#peer-name').press('Enter');
      await t.reply('POST /api/peers', { error: 'Create rejected' }, 500); await t.page.getByText('Create rejected', { exact: true }).waitFor();
      assert.equal(await t.page.inputValue('#peer-name'), 'Retry example'); await t.page.locator('[data-act="create-peer"]').click();
      await t.page.locator('#client-config-result').waitFor({ state: 'visible' }); assert((await t.state()).conf.includes('PrivateKey'));
      await t.page.keyboard.press('Escape'); assert.equal((await t.state()).conf, ''); assert.equal((await t.state()).filename, ''); assert.equal(await t.page.locator('#modal canvas').count(), 0);
    });
    for (const kind of ['config', 'create']) for (const status of [200, 500]) await check(async t => {
      const key = kind === 'config' ? 'GET /api/peers/example/config' : 'POST /api/peers'; t.holds.add(key);
      if (kind === 'config') { await t.open(); await t.expand(); } else { await t.invoke('add-peer'); await t.page.fill('#peer-name', 'Replacement example'); await t.invoke('create-peer'); }
      await t.waitHeld(key); await t.replace();
      if (kind === 'config' && status === 200) await t.replyConfig(); else await t.reply(key, status === 200 ? { ...examplePeer, id: 'created' } : { error: 'Late failure' }, status);
      if (kind === 'create') await t.done(); else await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); });
      assert.equal(await t.page.locator('#modal h2').textContent(), 'Replacement'); assert.equal((await t.state()).conf, '');
      if (kind === 'create' && status === 200) { assert((await t.state()).dirty); assert.equal((await t.state()).peers.length, 2); }
    });
    // Rename/toggle are now one transactional themed Save; preflight/deletion/followup remain separate stages.
    for (const action of ['rename', 'toggle', 'delete', 'preflight', 'followup']) for (const status of [200, 500]) await check(async t => {
      await t.open(); const key = ['preflight', 'followup'].includes(action) ? 'GET /api/rules' : action === 'delete' ? 'DELETE /api/peers/example' : 'PATCH /api/peers/example';
      if (action !== 'followup') t.holds.add(key);
      if (action === 'rename' || action === 'toggle') { if (action === 'rename') await t.page.fill('#client-name', 'Renamed example'); else await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details'); }
      else {
        if (action === 'followup') t.holds.add('DELETE /api/peers/example');
        await t.invoke('inspect-client-delete');
        if (action !== 'preflight') { await t.done(); await t.invoke('confirm-client-delete'); }
        if (action === 'followup') { await t.waitHeld('DELETE /api/peers/example'); t.holds.add(key); await t.reply('DELETE /api/peers/example', { inactivated_rule_ids: [] }); }
      }
      await t.waitHeld(key); t.holds.delete(key); await t.relogin(); const before = await t.state();
      await t.reply(key, status === 500 ? { error: 'Late failure' } : ['preflight', 'followup'].includes(action) ? [] : { ...examplePeer, inactivated_rule_ids: [] }, status);
      await t.done(); assert.deepEqual(await t.state(), before);
    });
    await check(async t => {
      await t.open(); t.holds.add('PATCH /api/peers/example'); await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example');
      t.holds.add('GET /api/session'); await t.page.evaluate(() => { import('/js/api.js').then(api => api.session.get().catch(() => {})); });
      await t.reply('GET /api/session', {}, 401); await t.page.locator('#gate').waitFor({ state: 'visible' }); const before = await t.state();
      await t.reply('PATCH /api/peers/example', examplePeer); await t.done(); assert.deepEqual(await t.state(), before);
    });
    for (const action of ['rename', 'toggle', 'delete']) for (const status of [200, 500]) await check(async t => {
      await t.open(); const key = (action === 'delete' ? 'DELETE' : 'PATCH') + ' /api/peers/example'; t.holds.add(key);
      if (action === 'delete') { await t.invoke('inspect-client-delete'); await t.done(); await t.invoke('confirm-client-delete'); }
      else { if (action === 'rename') await t.page.fill('#client-name', 'Renamed example'); else await t.page.uncheck('#client-enabled'); await t.invoke('save-client-details'); }
      await t.reply(key, status === 500 ? { error: 'Action rejected' } : { inactivated_rule_ids: [] }, status); await t.done(); const result = await t.state();
      if (status === 500) { assert.equal(result.peers.length, 1); assert.equal(result.peers[0].name, examplePeer.name); assert.match(result.modal, /Action rejected/); }
      else if (action === 'rename') { assert.equal(result.peers[0].name, 'Renamed example'); assert.match(result.modal, /Settings saved/); }
      else if (action === 'toggle') { assert.equal(result.peers[0].enabled, false); assert(result.dirty); }
      else { assert.equal(result.peers.length, 0); assert(result.dirty); assert.match(result.toasts, /Client deleted/); }
    });
    await check(async t => {
      await t.open(); t.holds.add('GET /api/rules'); await t.invoke('inspect-client-delete'); await t.waitHeld('GET /api/rules'); await t.replace(); await t.reply('GET /api/rules', []); await t.done();
      assert.equal(await t.page.locator('#modal h2').textContent(), 'Replacement');
    });
    await check(async t => {
      await t.open(); t.holds.add('PATCH /api/peers/example'); await t.page.fill('#client-name', 'Renamed example'); await t.invoke('save-client-details'); await t.waitHeld('PATCH /api/peers/example');
      await t.open(); await t.page.fill('#client-name', 'New unsaved draft'); await t.reply('PATCH /api/peers/example', examplePeer); await t.done();
      assert.equal((await t.state()).peers[0].name, 'Renamed example'); assert.equal(await t.page.inputValue('#client-name'), 'New unsaved draft');
    });
    await check(async t => {
      await t.open(); t.holds.add('GET /api/rules'); await t.invoke('inspect-client-delete');
      await t.reply('GET /api/rules', [{ id: 'rule', name: 'Example selected rule', peer_scope: 'selected', peer_ids: ['example'] }]); await t.done();
      const text = await t.page.locator('#client-delete-confirmation').textContent(); assert.match(text, /Affected rules: Example selected rule/); assert.match(text, /Inactive after deletion: Example selected rule/); assert.match(text, /explicit activation/); assert.match(text, /Apply/);
      await t.page.fill('#client-name', 'Preserved draft'); await t.invoke('cancel-client-delete'); assert.equal(await t.page.inputValue('#client-name'), 'Preserved draft');
    });
    for (const status of [200, 500]) await check(async t => {
      const key = 'POST /api/peers'; t.holds.add(key); await t.invoke('add-peer'); await t.page.fill('#peer-name', 'Old session example'); await t.invoke('create-peer'); await t.waitHeld(key); await t.page.keyboard.press('Escape'); await t.relogin(); const before = await t.state();
      await t.reply(key, status === 200 ? { ...examplePeer, id: 'old' } : { error: 'Old failure' }, status); await t.done(); assert.deepEqual(await t.state(), before);
    });
    console.log(`Passed ${count} synthetic Chromium client-action lifetime cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
