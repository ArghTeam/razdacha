// Synthetic functional/session checks. Responsive shell assertions follow phase-2 CSS integration.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { appFixture, defaultState, clientConfig } = require('./app.fixture.cjs');
(async () => {
  const browser = await chromium.launch({ headless: true });
  let cases = 0;
  async function check(run, options = {}) { const fixture = await appFixture(browser, options); try { await run(fixture); fixture.assertClean(); cases++; } finally { await fixture.page.close(); } }
  const invokeDiag = (fixture, action = 'rerun-diag') => fixture.page.evaluate(async action => { const diag = await import('/js/screens/diag.js'); window.diagPromise = Promise.resolve(diag.actions[action]()); }, action);
  const awaitDiag = fixture => fixture.page.evaluate(() => window.diagPromise);
  try {
    await check(async t => {
      assert.equal(await t.page.locator('[data-act="logs"], [data-act="singbox-config"]').count(), 0);
      assert.match(await t.page.locator('#screen').textContent(), /Log viewing is unavailable in this version/);
      assert.match(await t.page.locator('#screen').textContent(), /Configuration viewing is unavailable in this version/);
      assert.equal(t.journal.some(entry => entry.pathname === '/api/logs' || entry.pathname === '/api/diag/singbox-config'), false);
      assert.equal(await t.page.locator('[data-act="download-report"]').isEnabled(), true);
      const download = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-report"]').click();
      const file = await download; assert.equal(file.suggestedFilename(), 'razdacha-diag.txt');
      const text = require('node:fs').readFileSync(await file.path(), 'utf8'); assert.match(text, /diagnostic report/); assert.match(text, /free-form details are excluded/);
      assert.equal(text.includes(clientConfig), false);
    }, { screen: 'diag' });
    await check(async t => {
      assert.match(await t.page.locator('#screen').textContent(), /No diagnostic results yet/); assert.match(await t.page.locator('.overall').textContent(), /Status unknown/);
      const reportDownload = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-report"]').click();
      const emptyReport = require('node:fs').readFileSync(await (await reportDownload).path(), 'utf8');
      assert.match(emptyReport, /overall: unknown/); assert.equal(emptyReport.includes('overall: ok'), false);
      const key = 'POST /api/diag/run'; t.hold(key); await invokeDiag(t); await t.waitHeld(key); await invokeDiag(t);
      assert.equal(t.journal.filter(entry => entry.key === key).length, 1); t.state.diag = structuredClone(defaultState.diag); await t.release(key); await awaitDiag(t);
      // Re-rendered results come from the supported endpoint, never invented checks.
      await t.page.waitForFunction(() => !document.querySelector('[data-act="rerun-diag"]').disabled);
    }, { screen: 'diag', state: { diag: { overall: 'ok', checks: [] } } });
    for (const status of [404, 503]) await check(async t => {
      const text = await t.page.locator('#screen').textContent();
      if (status === 404) assert.match(text, /Diagnostics are unavailable in this version/);
      else { assert.match(text, /Diagnostic results are unavailable/); assert.equal(text.includes('Diagnostics are unavailable in this version'), false); }
      t.recover('GET', '/api/diag'); await t.page.locator('[data-act="rerun-diag"]').click(); await t.page.locator('.check').nth(1).waitFor(); assert.equal(await t.page.locator('.check').count(), 2);
    }, { screen: 'diag', responses: { 'GET /api/diag': { status, contentType: 'application/json', body: JSON.stringify({ error: 'Synthetic diagnostics unavailable' }) } } });
    await check(async t => {
      await invokeDiag(t); await awaitDiag(t); await t.settle();
      assert.deepEqual(t.journal.filter(entry => entry.method === 'POST' && entry.pathname === '/api/diag/run').map(entry => new URL(entry.url).searchParams.get('check')), ['wg', 'nft']);
      assert.equal(t.journal.filter(entry => entry.key === 'POST /api/tunnels/exit/check').length, 1);
      assert.match(await t.page.locator('#toasts').textContent(), /Diagnostic checks completed/);
    }, { screen: 'diag' });
    for (const status of [200, 500]) await check(async t => {
      const key = 'POST /api/diag/run?check=wg'; t.hold(key); await invokeDiag(t); await t.waitHeld(key);
      await t.logout(); await t.login(); const before = await t.snapshot();
      await t.release(key, t.json(status === 200 ? { checks: [{ id: 'wg', title: 'OLD RESULT', status: 'ok' }] } : { error: 'Old failure' }, status)); await awaitDiag(t);
      assert.deepEqual(await t.snapshot(), before); assert.equal(t.journal.filter(entry => entry.key === 'POST /api/diag/run?check=nft').length, 0);
      await t.page.locator('[data-screen="diag"]').click(); await t.settle(); assert.equal(await t.page.locator('[data-act="rerun-diag"]').isEnabled(), true);
    }, { screen: 'diag' });
    await check(async t => {
      const key = 'POST /api/tunnels/exit/check'; t.hold(key); await invokeDiag(t); await t.waitHeld(key);
      await t.logout(); await t.login(); const before = await t.snapshot(); await t.release(key); await awaitDiag(t); assert.deepEqual(await t.snapshot(), before);
      assert.equal(t.journal.filter(entry => entry.method === 'POST' && entry.pathname === '/api/diag/run').length, 0);
    }, { screen: 'diag' });
    for (const endpoint of ['/api/version', '/api/diag']) await check(async t => {
      const key = `GET ${endpoint}`; t.hold(key); await t.page.evaluate(() => { window.oldLoad = import('/js/screens/diag.js').then(diag => diag.load()); }); await t.waitHeld(key); t.holds.delete(key);
      await t.logout(); await t.login(); const before = await t.snapshot(); await t.release(key, t.json(endpoint.endsWith('version') ? { version: 'OLD VERSION' } : { checks: [{ title: 'OLD CHECK' }] })); await t.page.evaluate(() => window.oldLoad); assert.deepEqual(await t.snapshot(), before);
    });
    await check(async t => {
      await t.page.evaluate(async () => { const shell = await import('/js/shell.js'); for (let i = 0; i < 20; i++) shell.toast('Synthetic notification ' + i); });
      assert.equal(await t.page.locator('.toast').count(), 3); assert.deepEqual(await t.page.locator('.toast-message').allTextContents(), ['Synthetic notification 17', 'Synthetic notification 18', 'Synthetic notification 19']);
      await t.page.evaluate(async () => { (await import('/js/shell.js')).toast('Synthetic notification 19'); }); assert.equal(await t.page.locator('.toast').count(), 3);
      await t.page.locator('.toast-dismiss').last().focus(); await t.page.keyboard.press('Enter'); assert.equal(await t.page.locator('.toast').count(), 2);
      assert.equal(await t.page.locator('.toast-dismiss').last().evaluate(element => element === document.activeElement), true);
      await t.logout(); assert.equal(await t.page.locator('.toast').count(), 0); await t.login(); assert.equal(await t.page.locator('.toast').count(), 0);
    });
    for (const status of [200, 422]) await check(async t => {
      const key = 'POST /api/apply'; t.hold(key); await t.page.locator('#btn-apply').click(); await t.waitHeld(key); await t.logout(); await t.login(); const before = await t.snapshot();
      await t.release(key, t.json(status === 200 ? { ok: true } : { error: 'Old Apply rejection' }, status)); await t.applyDone(); assert.deepEqual(await t.snapshot(), before); assert.equal(await t.page.locator('#btn-apply').isEnabled(), true);
    }, { state: { dirty: true } });
    await check(async t => {
      t.fail('POST', '/api/apply', { error: 'Synthetic Apply validation failure' }, 422); await t.page.locator('#btn-apply').click(); await t.applyDone(); assert((await t.snapshot()).dirty); assert.match((await t.snapshot()).toasts, /Synthetic Apply validation failure/);
      t.recover('POST', '/api/apply'); await t.page.locator('#btn-apply').click(); await t.applyDone(); assert.equal((await t.snapshot()).dirty, false); assert.match((await t.snapshot()).toasts, /Configuration applied/);
    }, { state: { dirty: true } });
    await check(async t => {
      // Empty inventories and unauthenticated readiness are intentional fixture modes.
      assert.equal(await t.page.locator('#gate').isVisible(), true); await t.login(); assert.match(await t.page.locator('#screen').textContent(), /No clients yet/);
      await t.page.locator('[data-act="add-peer"]').first().click(); await t.page.keyboard.press('Escape'); assert.equal(await t.page.locator('#modal-backdrop').isHidden(), true);
    }, { readiness: 'login', state: { authenticated: false, peers: [] } });
    await check(async t => {
      const download = t.page.waitForEvent('download'); await t.page.locator('[data-act="download-report"]').click();
      const text = require('node:fs').readFileSync(await (await download).path(), 'utf8');
      for (const sensitive of ['203.0.113.42', '2001:db8::42', 'sensitive.example', 'user:example-password', '123e4567-e89b-12d3-a456-426614174000', 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=', 'example-token', 'Sensitive client', 'Sensitive tunnel', 'Sensitive rule', 'Synthetic credential detail']) assert.equal(text.includes(sensitive), false, sensitive);
      assert.match(text, /WireGuard: warn/); assert.match(text, /daemon: 0.10.0/); assert.match(text, /clients: 1/);
      // Detailed health information remains available locally rather than entering the export.
      assert.match(await t.page.locator('#screen').textContent(), /Synthetic credential detail/);
    }, { screen: 'diag', state: {
      settings: { client_mtu: 1280, endpoint_host: 'sensitive.example', dns_upstream: '203.0.113.42' },
      version: { version: '0.10.0-example-token', commit: 'example-token', singbox: { runtime_detail: 'https://user:example-password@sensitive.example' } },
      peers: [{ id: 'example', name: 'Sensitive client', address: '2001:db8::42', endpoint: '[2001:db8::42]:51820', public_key: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=' }],
      tunnels: [{ id: 'exit', name: 'Sensitive tunnel', host: 'sensitive.example', port: 443, enabled: true }],
      rules: [{ id: 'rule', name: 'Sensitive rule', enabled: true, action: 'direct' }],
      diag: { overall: 'warn', checks: [{ id: 'wg', title: 'Sensitive 203.0.113.42 title', status: 'warn', detail: 'Synthetic credential detail 2001:db8::42 https://user:example-password@sensitive.example UUID 123e4567-e89b-12d3-a456-426614174000 private_key=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA= token=example-token' }] },
    } });
    await check(async t => {
      t.fail('POST', '/api/diag/run', { error: 'Synthetic retry needed' }, 503); await invokeDiag(t); await awaitDiag(t);
      assert.match(await t.page.locator('.diag-feedback').textContent(), /Synthetic retry needed/); assert.equal(await t.page.locator('[data-act="rerun-diag"]').isEnabled(), true);
      t.recover('POST', '/api/diag/run'); t.state.diag = structuredClone(defaultState.diag); await invokeDiag(t); await awaitDiag(t); assert.equal(await t.page.locator('.diag-feedback').count(), 0);
    }, { screen: 'diag', state: { diag: { checks: [] } } });
    for (const requestPath of ['/api/diag', '/api/apply/status']) await check(async t => {
      const key = `GET ${requestPath}`; await t.waitHeld(key); t.holds.delete(key); await t.logout(); t.state.dirty = true; await t.login(); const before = await t.snapshot();
      await t.release(key, t.json(requestPath === '/api/diag' ? { checks: [{ id: 'wg', title: 'OBSOLETE BOOT CHECK', status: 'error' }] } : { dirty: false }));
      await t.page.evaluate(async () => { await (await import('/js/api.js')).session.get(); }); assert.deepEqual(await t.snapshot(), before);
    }, { readiness: 'none', hold: [`GET ${requestPath}`] });
    await check(async t => {
      await t.page.evaluate(async () => { (await import('/js/shell.js')).toast('Notification before expiration'); });
      t.fail('GET', '/api/session', { error: 'Synthetic session expiry' }, 401);
      await t.page.evaluate(async () => { try { await (await import('/js/api.js')).session.get(); } catch {} });
      assert.equal(await t.page.locator('#gate').isVisible(), true); assert.equal(await t.page.locator('.toast').count(), 0);
    });
    await check(async t => {
      await t.page.clock.install(); await t.page.evaluate(async () => { (await import('/js/shell.js')).toast('Expiring synthetic notification'); });
      assert.equal(await t.page.locator('.toast').count(), 1); await t.page.clock.fastForward(4100); assert.equal(await t.page.locator('.toast').count(), 0);
    });
    await check(async t => {
      t.respond('POST', '/api/apply', () => t.json({ ok: true })); await t.page.locator('#btn-apply').click(); await t.applyDone();
      assert.equal((await t.snapshot()).dirty, true); assert.equal(await t.page.locator('#apply-bar').isVisible(), true); assert.match((await t.snapshot()).toasts, /Configuration applied/);
    }, { state: { dirty: true } });
    await check(async t => {
      await t.page.clock.install();
      const notify = text => t.page.evaluate(async text => (await import('/js/shell.js')).toast(text), text);
      await notify('First'); await notify('Second'); await notify('Third');
      await t.page.locator('.toast-dismiss').first().focus(); await notify('Fourth');
      assert.equal(await t.page.locator('.toast-dismiss').last().evaluate(element => element === document.activeElement), true);
      await notify('Fourth');
      assert.equal(await t.page.locator('.toast-dismiss').last().evaluate(element => element === document.activeElement), true);
      await t.page.clock.fastForward(4100);
      assert.equal(await t.page.locator('#tabs [aria-selected="true"]').evaluate(element => element === document.activeElement), true);
      await t.page.locator('#btn-settings').focus(); await notify('Passive');
      assert.equal(await t.page.locator('#btn-settings').evaluate(element => element === document.activeElement), true);
    });
    await check(async t => {
      await t.page.locator('#app').waitFor({ state: 'visible' });
      await t.waitHeld('GET /api/settings');
      await t.navigate('tunnels');
      await t.page.locator('[data-act="menu-tunnel"][data-id="exit"]').click();
      await t.page.locator('[data-act="toggle-tunnel"][data-id="exit"]').click();
      await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.tunnels[0]?.enabled === false);
      assert.equal(t.state.tunnels[0].enabled, false);
      await t.release('GET /api/settings'); await t.settle();
      const accepted = await t.page.evaluate(async () => { const { state } = await import('/js/shell.js'); return { enabled: state.tunnels[0]?.enabled, dirty: state.dirty }; });
      assert.equal(accepted.enabled, false); assert.equal(accepted.dirty, true);
    }, { readiness: 'none', hold: ['GET /api/settings'], handlers: {
      'PATCH /api/tunnels/exit': (entry, state) => { Object.assign(state.tunnels[0], entry.body); state.dirty = true; return { status: 200, contentType: 'application/json', body: JSON.stringify(state.tunnels[0]) }; },
    } });
    for (const staleResponse of [
      { status: 404, contentType: 'application/json', body: '{"error":"Obsolete missing tunnel inventory"}' },
      { status: 503, contentType: 'application/json', body: '{"error":"Obsolete failed tunnel inventory"}' },
      { abort: 'connectionrefused' },
    ]) await check(async t => {
      await t.page.locator('#app').waitFor({ state: 'visible' });
      await t.waitHeld('GET /api/tunnels'); t.holds.delete('GET /api/tunnels');
      await t.navigate('tunnels');
      await t.page.locator('[data-act="menu-tunnel"][data-id="exit"]').click();
      await t.page.locator('[data-act="toggle-tunnel"][data-id="exit"]').click();
      await t.page.waitForFunction(async () => (await import('/js/shell.js')).state.tunnels[0]?.enabled === false);
      await t.release('GET /api/tunnels', staleResponse); await t.settle();
      const current = await t.page.evaluate(async () => { const { state } = await import('/js/shell.js'); return { enabled: state.tunnels[0]?.enabled, missing: state.missing.has('tunnels'), offline: Boolean(document.querySelector('#offline-bar')), text: document.querySelector('#toasts').textContent }; });
      assert.equal(current.enabled, false); assert.equal(current.missing, false); assert.equal(current.offline, false); assert.equal(current.text.includes('Obsolete'), false);
    }, { readiness: 'none', hold: ['GET /api/tunnels'], handlers: {
      'PATCH /api/tunnels/exit': (entry, state) => { Object.assign(state.tunnels[0], entry.body); state.dirty = true; return { status: 200, contentType: 'application/json', body: JSON.stringify(state.tunnels[0]) }; },
    } });
    await check(async t => {
      t.hold('GET /api/version'); await t.navigate('diag'); await t.waitHeld('GET /api/version');
      await t.navigate('peers');
      t.holds.delete('GET /api/version'); t.fail('GET', '/api/diag', { error: 'Synthetic missing diagnostics' }, 404);
      await t.navigate('diag');
      const missing = () => t.page.evaluate(async () => (await import('/js/shell.js')).state.missing.has('diag'));
      assert.equal(await missing(), true);
      await t.release('GET /api/version'); await t.settle();
      assert.equal(await missing(), true); assert.match(await t.page.locator('#screen').textContent(), /Diagnostics are unavailable in this version/);
    });
    console.log(`Passed ${cases} synthetic shell/diagnostic functional and session cases`);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
