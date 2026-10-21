// Run: NODE_PATH=<directory containing playwright> node ui/tests/client-actions.cjs
// Production browser modules, synthetic intercepted API only; no daemon or deployment.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '../dist');
const origin = 'http://127.0.0.1:43187';
const peer = { id: 'example', name: 'Example device', address: '192.0.2.2', enabled: true };
const config = '[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 192.0.2.2/32\n';
(async () => {
  const browser = await chromium.launch({ headless: true });
  let count = 0;
  try {
    async function fixture(run) {
      const page = await browser.newPage();
      const held = new Map(), holds = new Set(), calls = [];
      let authenticated = true;
      const json = (route, body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
      await page.route('**/*', async route => {
        const request = route.request(), url = new URL(request.url()), key = `${request.method()} ${url.pathname}`;
        if (url.origin !== origin) return route.abort();
        if (!url.pathname.startsWith('/api/')) {
          const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
          if (!file.startsWith(root + path.sep)) return route.abort();
          return route.fulfill({ body: fs.readFileSync(file), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' });
        }
        calls.push(key);
        if (holds.has(key)) { held.set(key, route); return; }
        if (url.pathname === '/api/session') return json(route, { authenticated });
        if (url.pathname === '/api/logout') { authenticated = false; return json(route, {}); }
        if (url.pathname === '/api/login') { authenticated = true; return json(route, {}); }
        if (key === 'GET /api/peers') return json(route, [peer]);
        if (key === 'GET /api/settings') return json(route, { client_mtu: 1280, wg_pool: '192.0.2.0/24', wg_server_address: '192.0.2.1' });
        if (url.pathname.endsWith('/config')) return route.fulfill({ contentType: 'text/plain', headers: { 'Content-Disposition': 'attachment; filename="example.conf"' }, body: config });
        if (url.pathname === '/api/tunnels' || url.pathname === '/api/rules' || url.pathname.includes('/lists/')) return json(route, []);
        if (url.pathname === '/api/apply/status') return json(route, { dirty: false });
        if (url.pathname === '/api/version') return json(route, { version: 'test' });
        return json(route, {});
      });
      await page.goto(origin);
      await page.locator('.client-row').waitFor();
      const invoke = (action) => page.evaluate(async action => {
        const peers = await import('/js/screens/peers.js');
        // Do not await an intentionally held request.
        window.actionDone = false;
        window.actionPromise = Promise.resolve(peers.actions[action]('example')).finally(() => { window.actionDone = true; });
      }, action);
      const state = () => page.evaluate(async () => {
        const { state } = await import('/js/shell.js');
        return { peers: state.peers, rules: state.rules, dirty: state.dirty, toasts: document.querySelector('#toasts').textContent, modal: document.querySelector('#modal').textContent, conf: document.querySelector('#modal').dataset.conf || '' };
      });
      const waitHeld = async key => { await page.waitForFunction(() => true); for (let i = 0; i < 100 && !held.has(key); i++) await new Promise(r => setTimeout(r, 10)); assert(held.has(key), `Missing held ${key}`); };
      const reply = async (key, body = {}, status = 200) => { await waitHeld(key); const route = held.get(key); held.delete(key); holds.delete(key); await json(route, body, status); };
      const done = () => page.waitForFunction(() => window.actionDone);
      const replace = () => page.evaluate(async () => { const s = await import('/js/shell.js'); s.openModal(s.modalShell('Replacement', '<p>Keep this interaction</p>', '')); });
      const relogin = async () => { await page.locator('#btn-logout').click(); await page.locator('#gate').waitFor({ state: 'visible' }); await page.fill('#password', 'synthetic'); await page.locator('#login-submit').click(); await page.locator('.client-row').waitFor(); await page.evaluate(() => document.querySelector('#toasts').replaceChildren()); };
      try { await run({ page, holds, held, calls, invoke, state, reply, done, replace, relogin, waitHeld }); count++; }
      finally { await page.close(); }
    }
    // Mouse, repeated Enter and mixed activation share the same in-flight form gate.
    for (const mode of ['enter', 'mouse']) await fixture(async t => {
      const key = 'POST /api/peers'; t.holds.add(key);
      await t.page.locator('[data-act="add-peer"]').first().click();
      await t.page.fill('#peer-name', 'Created example');
      if (mode === 'mouse') await t.page.locator('[data-act="create-peer"]').click();
      else await t.page.locator('#peer-name').press('Enter');
      await t.waitHeld(key);
      await t.page.locator('#peer-name').press('Enter');
      await t.page.locator('#peer-name').press('Enter');
      await t.page.evaluate(() => document.querySelector('[data-act="create-peer"]').click());
      assert.equal(t.calls.filter(x => x === key).length, 1);
      await t.page.keyboard.press('Escape');
      await t.reply(key, { ...peer, id: 'created', name: 'Created example' });
      await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.peers.length === 2);
      assert.equal((await t.state()).dirty, true);
      assert.equal((await t.state()).modal, '');
    });
    // Error preserves the name and releases the create gate for a real retry.
    await fixture(async t => {
      const key = 'POST /api/peers'; t.holds.add(key);
      await t.page.locator('[data-act="add-peer"]').first().click(); await t.page.fill('#peer-name', 'Retry example');
      await t.page.locator('#peer-name').press('Enter'); await t.reply(key, { error: 'Create rejected' }, 500);
      await t.page.waitForFunction(() => !document.querySelector('[data-act="create-peer"]').disabled);
      assert.equal(await t.page.inputValue('#peer-name'), 'Retry example');
      t.holds.add(key); await t.page.locator('[data-act="create-peer"]').click(); await t.reply(key, { ...peer, id: 'created' });
      await t.page.locator('#qr').waitFor(); assert((await t.state()).conf.includes('PrivateKey'));
      await t.page.keyboard.press('Escape'); assert.equal((await t.state()).conf, '');
      assert.equal(await t.page.locator('#modal canvas').count(), 0);
    });
    // Configuration success/error cannot replace another modal; create success remains visible.
    for (const kind of ['config', 'create']) for (const status of [200, 500]) await fixture(async t => {
      const key = kind === 'config' ? 'GET /api/peers/example/config' : 'POST /api/peers'; t.holds.add(key);
      if (kind === 'config') await t.invoke('peer-config');
      else { await t.page.locator('[data-act="add-peer"]').first().click(); await t.page.fill('#peer-name', 'Replacement example'); await t.page.locator('#peer-name').press('Enter'); }
      await t.waitHeld(key); await t.replace();
      await t.reply(key, status === 200 ? { ...peer, id: 'created' } : { error: 'Late failure' }, status);
      if (kind === 'config') await t.done();
      else await t.page.waitForFunction(() => document.querySelector('#modal h2')?.textContent === 'Replacement');
      assert.equal(await t.page.locator('#modal h2').textContent(), 'Replacement'); assert.equal((await t.state()).conf, '');
      if (kind === 'create' && status === 200) { await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.dirty); assert.equal((await t.state()).peers.length, 2); }
    });
    // Every menu callback, including both rule-list stages, expires with the session.
    for (const action of ['rename-peer', 'toggle-peer', 'delete-peer', 'preflight', 'followup']) for (const status of [200, 500]) await fixture(async t => {
      const key = action === 'preflight' || action === 'followup' ? 'GET /api/rules' : action === 'delete-peer' ? 'DELETE /api/peers/example' : 'PATCH /api/peers/example';
      if (action !== 'followup') t.holds.add(key);
      let dialogs = 0;
      t.page.on('dialog', async d => { dialogs++; await d.accept(d.type() === 'prompt' ? 'Renamed example' : undefined); });
      if (action === 'followup') { t.holds.add('DELETE /api/peers/example'); await t.invoke('delete-peer'); await t.waitHeld('DELETE /api/peers/example'); t.holds.add(key); await t.reply('DELETE /api/peers/example', { inactivated_rule_ids: [] }); }
      else await t.invoke(action === 'preflight' ? 'delete-peer' : action);
      await t.waitHeld(key);
      // Let subsequent screen loads complete while retaining the original held route.
      t.holds.delete(key); await t.relogin(); const before = await t.state(), beforeDialogs = dialogs;
      await t.reply(key, status === 500 ? { error: 'Late failure' } : action === 'preflight' || action === 'followup' ? [] : { ...peer, inactivated_rule_ids: [] }, status);
      await t.done(); assert.deepEqual(await t.state(), before); assert.equal(dialogs, beforeDialogs);
    });
    // Expiration invalidates another pending mutation before its response arrives.
    await fixture(async t => {
      t.holds.add('PATCH /api/peers/example'); await t.invoke('toggle-peer'); await t.waitHeld('PATCH /api/peers/example');
      t.holds.add('GET /api/session'); await t.page.evaluate(() => { import('/js/api.js').then(api => api.session.get().catch(() => {})); });
      await t.reply('GET /api/session', {}, 401); await t.page.locator('#gate').waitFor({ state: 'visible' }); const before = await t.state();
      await t.reply('PATCH /api/peers/example', peer); await t.done(); assert.deepEqual(await t.state(), before);
    });
    // Same-session mutation success/error and orphan explanations remain visible.
    for (const action of ['rename-peer', 'toggle-peer', 'delete-peer']) for (const status of [200, 500]) await fixture(async t => {
      const key = (action === 'delete-peer' ? 'DELETE' : 'PATCH') + ' /api/peers/example'; t.holds.add(key);
      t.page.on('dialog', d => d.accept(d.type() === 'prompt' ? 'Renamed example' : undefined));
      await t.invoke(action); await t.reply(key, status === 500 ? { error: 'Action rejected' } : { inactivated_rule_ids: [] }, status); await t.done();
      const result = await t.state();
      if (status === 500) { assert.equal(result.peers.length, 1); assert.equal(result.peers[0].name, peer.name); assert.match(result.toasts, /Action rejected/); }
      else if (action === 'rename-peer') { assert.equal(result.peers[0].name, 'Renamed example'); assert.match(result.toasts, /Renamed/); }
      else if (action === 'toggle-peer') { assert.equal(result.peers[0].enabled, false); assert(result.dirty); }
      else { assert.equal(result.peers.length, 0); assert(result.dirty); assert.match(result.toasts, /Device deleted/); }
    });
    await fixture(async t => {
      t.holds.add('GET /api/rules'); await t.invoke('delete-peer'); await t.waitHeld('GET /api/rules'); await t.replace();
      let dialogs = 0; t.page.on('dialog', async d => { dialogs++; await d.dismiss(); });
      await t.reply('GET /api/rules', []); await t.done(); assert.equal(dialogs, 0);
    });
    // Another menu action supersedes a pending preflight without losing a completed mutation.
    await fixture(async t => {
      t.holds.add('GET /api/rules'); await t.invoke('delete-peer'); await t.waitHeld('GET /api/rules');
      await t.page.evaluate(() => { window.preflightPromise = window.actionPromise; });
      await t.invoke('toggle-peer'); await t.done();
      let dialogs = 0; t.page.on('dialog', async d => { dialogs++; await d.dismiss(); });
      await t.reply('GET /api/rules', []); await t.page.evaluate(() => window.preflightPromise);
      await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.peers[0].enabled === false);
      assert.equal(dialogs, 0); assert((await t.state()).dirty);
    });
    await fixture(async t => {
      const key = 'PATCH /api/peers/example'; t.holds.add(key);
      t.page.on('dialog', d => d.accept('Renamed example')); await t.invoke('rename-peer'); await t.waitHeld(key);
      await t.replace(); await t.reply(key, peer); await t.done();
      assert.equal((await t.state()).peers[0].name, 'Renamed example'); assert.equal((await t.state()).toasts, '');
      assert.equal(await t.page.locator('#modal h2').textContent(), 'Replacement');
    });
    // Selected-orphan instructions name the affected rules and require explicit activation.
    await fixture(async t => {
      const key = 'GET /api/rules'; t.holds.add(key); await t.invoke('delete-peer');
      let confirmation = ''; t.page.once('dialog', async d => { confirmation = d.message(); await d.dismiss(); });
      await t.reply(key, [{ id: 'rule', name: 'Example selected rule', peer_scope: 'selected', peer_ids: ['example'] }]); await t.done();
      assert.match(confirmation, /Affected rules: Example selected rule/);
      assert.match(confirmation, /Inactive after deletion: Example selected rule/);
      assert.match(confirmation, /Reassign clients and enable these rules explicitly/);
      assert.match(confirmation, /Rule changes require Apply/);
    });
    // A pending create cannot repopulate a later login, regardless of its outcome.
    for (const status of [200, 500]) await fixture(async t => {
      const key = 'POST /api/peers'; t.holds.add(key);
      await t.page.locator('[data-act="add-peer"]').first().click(); await t.page.fill('#peer-name', 'Old session example');
      await t.page.locator('#peer-name').press('Enter'); await t.waitHeld(key);
      await t.page.keyboard.press('Escape'); await t.relogin(); const before = await t.state();
      await t.reply(key, status === 200 ? { ...peer, id: 'old' } : { error: 'Old failure' }, status);
      // A later same-origin fetch drains browser response callbacks before comparison.
      await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); });
      assert.deepEqual(await t.state(), before);
    });
    console.log(`Passed ${count} synthetic Chromium client-action cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
