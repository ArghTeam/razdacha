// Actual router and native pointer interactions under a controlled browser clock.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { appFixture } = require('./app.fixture.cjs');
const { normalState } = require('./integrated.fixture.cjs');
const readCount = t => t.journal.filter(entry => entry.key === 'GET /api/tunnels').length;
async function fixture(browser, options = {}) {
  return appFixture(browser, {
    screen: 'tunnels', state: normalState, ...options,
    beforeGoto: page => page.clock.install({ time: new Date() }),
  });
}
async function openMenu(t) {
  await t.page.locator('[data-act="menu-tunnel"][data-id="exit"]').click();
  await t.page.locator('#row-menu [data-act="edit-tunnel"]').waitFor();
  return { anchor: await t.page.locator('[data-act="menu-tunnel"][data-id="exit"]').elementHandle(), menu: await t.page.locator('#row-menu').elementHandle() };
}
async function preserved(t, owned) {
  assert(await owned.anchor.evaluate(el => el.isConnected));
  assert(await owned.menu.evaluate(el => el.isConnected));
  assert.equal(await t.page.locator('#row-menu').count(), 1);
  assert.equal(await t.page.evaluate(() => document.activeElement.closest('#row-menu') !== null), true);
}
async function editAndResume(t, expected = 'Demo exit') {
  await t.page.locator('#row-menu [data-act="edit-tunnel"]').click();
  await t.page.locator('#t-raw:not(:disabled)').waitFor(); await t.page.locator('#t-parse.ok').waitFor();
  assert.equal(await t.page.inputValue('#t-name'), expected);
  assert(t.journal.some(entry => entry.key === 'GET /api/tunnels/exit/raw'));
  assert.equal(await t.page.locator('#row-menu').count(), 0);
  await t.page.keyboard.press('Escape');
  const reads = readCount(t); await t.page.clock.fastForward(15000); await t.settle();
  assert.equal(readCount(t), reads + 1, 'Polling did not resume after the editor closed');
}
(async () => {
  const browser = await chromium.launch({ headless: true }); let count = 0;
  async function check(name, run, options) {
    const t = await fixture(browser, options);
    try { await run(t); t.assertClean(); count++; console.log(`PASS ${name}`); }
    finally { await t.page.close(); }
  }
  try {
    for (const width of [1440, 390]) await check(`${width}: open menu blocks poll admission and pointer Edit remains usable`, async t => {
      const owned = await openMenu(t), reads = readCount(t);
      await t.page.clock.fastForward(30000); await t.settle();
      assert.equal(readCount(t), reads); await preserved(t, owned);
      await editAndResume(t);
    }, { viewport: { width, height: width < 500 ? 844 : 1000 } });
    for (const result of ['success', 'error']) await check(`in-flight poll ${result} preserves the later menu and resumes afterward`, async t => {
      t.hold('GET', '/api/tunnels'); await t.page.clock.fastForward(15000); await t.waitHeld('GET', '/api/tunnels');
      const owned = await openMenu(t);
      const fresh = normalState.tunnels.map(tunnel => tunnel.id === 'exit' ? { ...tunnel, name: 'Updated exit' } : tunnel);
      await t.release('GET', '/api/tunnels', result === 'success' ? t.json(fresh) : t.json({ error: 'Synthetic poll failure' }, 503));
      await t.settle(); await preserved(t, owned);
      const expected = result === 'success' ? 'Updated exit' : 'Demo exit';
      assert.equal(await t.page.evaluate(async () => (await import('/js/shell.js')).state.tunnels.find(tunnel => tunnel.id === 'exit').name), expected);
      await editAndResume(t, expected);
    });
    await check('explicit navigation closes a menu retained against an earlier poll', async t => {
      t.hold('GET', '/api/tunnels'); await t.page.clock.fastForward(15000); await t.waitHeld('GET', '/api/tunnels');
      await openMenu(t); await t.navigate('rules');
      await t.release('GET', '/api/tunnels', t.json(normalState.tunnels)); await t.settle();
      assert.equal((await t.snapshot()).screen, 'rules'); assert.equal(await t.page.locator('#row-menu').count(), 0);
      assert.equal(await t.page.locator('[data-act="menu-rule"]').count(), 1);
    });
    await check('expiry from an in-flight poll still dismisses the menu and returns to login', async t => {
      t.hold('GET', '/api/tunnels'); await t.page.clock.fastForward(15000); await t.waitHeld('GET', '/api/tunnels');
      await openMenu(t); await t.release('GET', '/api/tunnels', t.json({ error: 'Synthetic expiry' }, 401));
      await t.settle(); await t.page.locator('#gate').waitFor({ state: 'visible' });
      assert.equal(await t.page.locator('#row-menu').count(), 0);
      await t.login(); await t.navigate('tunnels'); await openMenu(t); await editAndResume(t);
    });
    for (const domain of ['tunnel', 'rule']) for (const initial of [true, false]) await check(`${domain}: advertised ${initial ? 'Disable' : 'Enable'} survives an opposite-state background reply`, async t => {
      const collection = domain === 'tunnel' ? 'tunnels' : 'rules';
      const id = domain === 'tunnel' ? 'exit' : 'work';
      const endpoint = `/api/${collection}`;
      t.hold('GET', endpoint); await t.page.clock.fastForward(15000); await t.waitHeld('GET', endpoint);
      await t.page.locator(`[data-act="menu-${domain}"][data-id="${id}"]`).click();
      const control = t.page.locator(`#row-menu [data-act="toggle-${domain}"]`);
      assert.equal(await control.innerText(), initial ? 'Disable' : 'Enable');
      assert.equal(await control.getAttribute('data-enabled'), String(!initial));
      // Another administrator changed saved state while this menu retained its intent.
      t.state[collection].find(item => item.id === id).enabled = !initial;
      await t.release('GET', endpoint, t.json(t.state[collection])); await t.settle();
      assert.equal(await t.page.evaluate(async ({collection, id}) => (await import('/js/shell.js')).state[collection].find(item => item.id === id).enabled, {collection, id}), !initial);
      assert.equal(await control.innerText(), initial ? 'Disable' : 'Enable');
      await control.click(); await t.settle();
      const patches = t.journal.filter(entry => entry.key === `PATCH ${endpoint}/${id}`);
      assert.equal(patches.length, 1); assert.deepEqual(patches[0].body, { enabled: !initial });
    }, { screen: domain === 'tunnel' ? 'tunnels' : 'rules', state: {
      ...normalState,
      [domain === 'tunnel' ? 'tunnels' : 'rules']: normalState[domain === 'tunnel' ? 'tunnels' : 'rules'].map(item => item.id === (domain === 'tunnel' ? 'exit' : 'work') ? { ...item, enabled: initial } : item),
    } });
    for (const domain of ['tunnel', 'rule']) await check(`${domain}: absent or invalid toggle intent never dispatches`, async t => {
      await t.page.evaluate(async domain => {
        const module = await import(`/js/screens/${domain === 'tunnel' ? 'tunnels' : 'rules'}.js`);
        const id = domain === 'tunnel' ? 'exit' : 'work';
        await module.actions[`toggle-${domain}`](id);
        for (const enabled of ['', 'yes', 'TRUE', true]) await module.actions[`toggle-${domain}`](id, { dataset: { enabled } });
      }, domain);
      await t.settle(); assert.equal(t.journal.filter(entry => entry.method === 'PATCH').length, 0);
    }, { screen: domain === 'tunnel' ? 'tunnels' : 'rules' });
    console.log(`Passed ${count} background-poll/menu interaction cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
