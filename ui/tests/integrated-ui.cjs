const assert = require('node:assert/strict');
const http = require('node:http');
const { chromium } = require('playwright');
const { suite, act, menu, capture, visibleFocus, scrollEnds } = require('./ui-sweep.helper.cjs');
const { normalState } = require('./integrated.fixture.cjs');
async function close(t, mode = 'escape') {
  if (mode === 'backdrop') await t.page.locator('#modal-backdrop').click({ position: { x: 2, y: 2 } });
  else if (mode === 'footer') await t.page.locator('#modal .modal-foot [data-act="close-modal"]').click();
  else if (mode === 'header') await t.page.locator('#modal .modal-head [data-act="close-modal"]').click();
  else await t.page.keyboard.press('Escape');
  assert(await t.page.locator('#modal-backdrop').isHidden());
  assert.equal(await t.page.locator('#app').evaluate(element => element.inert), false);
}
(async () => {
  const browser = await chromium.launch({ headless: true }); const tests = suite(browser, 'whole-app integration');
  try {
    await tests.check('boot rules response cannot overwrite a mutation while another startup request waits', async t => {
      await t.page.locator('#app').waitFor({ state: 'visible' }); await t.waitHeld('GET /api/settings');
      await t.navigate('rules'); await act(t, 'toggle-rule', 'work');
      assert.equal((await t.snapshot()).rules.find(rule => rule.id === 'work').enabled, false);
      await t.release('GET /api/settings'); await t.waitHeld('GET /api/diag');
      assert.equal((await t.snapshot()).rules.find(rule => rule.id === 'work')?.enabled, false, 'Bootstrap rule snapshot overwrote the accepted toggle');
      assert((await t.snapshot()).dirty);
      await t.release('GET /api/diag'); await t.settle();
    }, { readiness: 'none', hold: ['GET /api/settings', 'GET /api/diag'], state: normalState });
    for (const obsolete of [
      { status: 404, contentType: 'application/json', body: '{"error":"Obsolete bootstrap rule inventory"}' },
      { status: 503, contentType: 'application/json', body: '{"error":"Obsolete bootstrap rule inventory"}' },
      { abort: 'connectionrefused' },
    ]) await tests.check(`obsolete boot rules ${obsolete.status || 'network'} cannot alter inventory or feedback`, async t => {
      await t.page.locator('#app').waitFor({ state: 'visible' }); await t.waitHeld('GET /api/rules'); t.holds.delete('GET /api/rules');
      await t.navigate('rules'); await act(t, 'toggle-rule', 'work');
      await t.release('GET /api/rules', obsolete); await t.waitHeld('GET /api/diag');
      const current = await t.snapshot(); assert.equal(current.rules.find(rule => rule.id === 'work')?.enabled, false); assert(current.dirty);
      assert.equal(await t.page.evaluate(async () => (await import('/js/shell.js')).state.missing.has('rules')), false);
      assert.equal(await t.page.locator('#offline-bar').count(), 0); assert(!current.toasts.includes('Obsolete bootstrap'));
      await t.release('GET /api/diag'); await t.settle();
    }, { readiness: 'none', hold: ['GET /api/rules', 'GET /api/diag'], state: normalState });
    await tests.check('login validation, wrong-password and Retry-After gate recover', async t => {
      await t.page.locator('#login-submit').click(); assert(!t.journal.some(e => e.key === 'POST /api/login'));
      t.fail('POST', '/api/login', { error: 'Synthetic wrong password' }, 401); await t.page.fill('#password', 'synthetic'); await t.page.locator('#login-submit').click(); await t.settle();
      assert(await t.page.locator('#gate').isVisible()); assert.equal(await t.page.inputValue('#password'), ''); assert(await t.page.locator('#login-error').isVisible());
      t.recover('POST', '/api/login'); await t.page.clock.install();
      t.respond('POST', '/api/login', () => ({ ...t.json({ error: 'Synthetic login throttled' }, 429), headers: { 'Retry-After': '2' } }));
      await t.page.fill('#password', 'synthetic'); await t.page.locator('#login-submit').click(); await t.settle(); assert(await t.page.locator('#login-submit').isDisabled());
      await t.page.clock.runFor(2100); assert(await t.page.locator('#login-submit').isEnabled());
      t.respond('POST', '/api/login', (_entry, state) => { state.authenticated = true; return t.json({}); }); await t.login(); assert.equal(await t.page.inputValue('#password'), '');
    }, { readiness: 'login', state: { authenticated: false } });
    await tests.check('Details to Settings to Apply preserves global pending ownership', async t => {
      await act(t, 'peer-details', 'example'); await t.page.fill('#client-name', 'Edited example laptop'); await act(t, 'save-client-details');
      assert((await t.snapshot()).dirty); assert(await t.page.locator('#apply-bar').isVisible()); await close(t);
      await t.navigate('rules'); await act(t, 'edit-rule', 'work'); await t.page.fill('#r-name', 'Edited work resources'); await act(t, 'save-rule');
      assert((await t.snapshot()).dirty); assert.equal(t.state.rules[0].name, 'Edited work resources');
      await t.page.locator('#btn-settings').click(); await t.page.locator('[data-settings-section="schedules"]').click(); await t.page.locator('#s-check').selectOption('300');
      await t.page.locator('#settings-schedules [type="submit"]').click(); await t.page.getByText(/Schedules saved/).waitFor();
      assert.equal(t.state.settings.tunnel_check_interval, 300); assert(await t.page.locator('#apply-bar').isVisible()); await close(t);
      assert(!t.journal.some(e => e.key === 'POST /api/apply')); await t.page.locator('#btn-apply').click(); await t.applyDone();
      assert.equal((await t.snapshot()).dirty, false); assert(await t.page.locator('#apply-bar').isHidden());
      await t.navigate('peers'); assert.match(await t.page.locator('.client-row').first().innerText(), /Edited example laptop/);
    }, { state: normalState });
    // Browser-managed direct downloads bypass Playwright page/context routing.
    // Serve only this disposable file, while app assets/API remain intercepted.
    const directRequests = [], unexpectedDirect = [];
    const downloadServer = http.createServer((request, response) => {
      directRequests.push(`${request.method} ${request.url}`);
      if (request.method !== 'GET' || request.url !== '/api/backup/download') {
        unexpectedDirect.push(request.url); response.writeHead(404).end(); return;
      }
      response.writeHead(200, { 'Content-Type': 'application/octet-stream', 'Content-Disposition': 'attachment; filename="synthetic-backup.db"' });
      response.end('Disposable synthetic backup, not a database');
    });
    await new Promise(resolve => downloadServer.listen(0, '127.0.0.1', resolve));
    try {
    await tests.check('saved-only deliveries and full-backup download do not save drafts', async t => {
      await t.page.locator('#btn-settings').click(); await t.page.locator('#settings-backup fieldset').waitFor({ state: 'attached' });
      await t.page.waitForFunction(() => !document.querySelector('#settings-backup fieldset').disabled);
      await t.page.locator('[data-settings-section="telegram"]').click(); await t.page.locator('[data-delivery="telegram"]').click(); await t.page.getByText(/Test message sent/).waitFor();
      await t.page.fill('#s-ntf-chat', '-100999'); assert(await t.page.locator('[data-delivery="telegram"]').isDisabled());
      await t.page.locator('[data-settings-section="backup"]').click(); await t.page.locator('[data-delivery="backup"]').click(); await t.page.getByText(/Encrypted backup sent/).waitFor(); await t.settle();
      const expectedDownload = `http://127.0.0.1:${downloadServer.address().port}/api/backup/download`;
      assert.equal(await t.page.locator('.settings-download a').evaluate(link => link.href), expectedDownload, 'Direct download must stay at the disposable fixture origin');
      const download = t.page.waitForEvent('download'); await t.page.locator('.settings-download a').click(); const file = await download; assert.equal(file.url(), expectedDownload); assert.equal(await file.failure(), null); assert.equal(file.suggestedFilename(), 'synthetic-backup.db'); assert.equal(require('node:fs').readFileSync(await file.path(), 'utf8'), 'Disposable synthetic backup, not a database');
      assert(!t.journal.some(e => ['PUT /api/notify', 'PUT /api/backup', 'PATCH /api/settings'].includes(e.key)));
      await t.page.locator('[data-settings-section="telegram"]').click(); assert.equal(await t.page.inputValue('#s-ntf-chat'), '-100999');
      assert.deepEqual(directRequests, ['GET /api/backup/download']); assert.deepEqual(unexpectedDirect, []);
    }, { state: normalState, origin: `http://127.0.0.1:${downloadServer.address().port}` });
    } finally { downloadServer.closeAllConnections(); await new Promise(resolve => downloadServer.close(resolve)); }
    await tests.check('offline/reconnect and authenticated expiry restore the login boundary', async t => {
      await t.navigate('tunnels'); t.failNetwork('GET', '/api/peers'); await t.navigate('peers'); assert(await t.page.locator('#offline-bar').isVisible());
      await capture(t.page, 'offline-clients', 'stress'); t.recover('GET', '/api/peers'); await t.navigate('rules'); await t.navigate('peers'); assert.equal(await t.page.locator('#offline-bar').count(), 0);
      await t.page.locator('#btn-settings').click(); await t.page.locator('[data-settings-section="telegram"]').click(); await t.page.waitForFunction(() => !document.querySelector('#s-ntf-token').disabled); await t.page.fill('#s-ntf-token', 'synthetic-unsaved-token');
      t.fail('GET', '/api/session', { error: 'Synthetic expiration' }, 401); await t.page.evaluate(async () => { try { await (await import('/js/api.js')).session.get(); } catch {} });
      assert(await t.page.locator('#gate').isVisible()); assert.equal(await t.page.locator('#s-ntf-token').count(), 0); assert.equal(await t.page.locator('#modal').innerText(), '');
      t.recover('GET', '/api/session'); await t.login(); assert(await t.page.locator('#app').isVisible());
    }, { state: normalState });
    for (const screen of ['tunnels', 'rules']) await tests.check(`${screen} missing endpoint differs from an empty inventory`, async t => {
      const missing = await t.page.evaluate(async screen => (await import('/js/shell.js')).state.missing.has(screen), screen); assert(missing);
      assert(await t.page.locator('#screen .notice').isVisible()); await capture(t.page, `${screen}-missing`, 'stress');
      t.recover('GET', `/api/${screen}`); await t.navigate('peers'); await t.navigate(screen); await t.page.waitForFunction(async screen => document.querySelector('#screen h1')?.textContent.trim() === (await import(`/js/screens/${screen}.js`)).title, screen); assert.equal(await t.page.locator('#screen .notice').count(), 0);
    }, { screen, responses: { [`GET /api/${screen}`]: { status: 404, contentType: 'application/json', body: '{"error":"Synthetic unavailable endpoint"}' } } });
    await tests.check('row menu keyboard, navigation cleanup and version provenance use the real shell', async t => {
      assert.match(await t.page.locator('#brand-version').innerText(), /≠/); assert.match(await t.page.locator('#brand-version').getAttribute('aria-label'), /installed/);
      await t.navigate('rules'); await act(t, 'menu-rule', 'work'); await t.page.keyboard.press('End'); assert.equal(await t.page.evaluate(() => document.activeElement.dataset.act), 'delete-rule');
      await t.page.keyboard.press('Home'); assert.equal(await t.page.evaluate(() => document.activeElement.dataset.act), 'edit-rule'); await t.page.keyboard.press('Escape'); assert.equal(await t.page.locator('#row-menu').count(), 0);
      await act(t, 'menu-rule', 'work'); await t.navigate('diag'); assert.equal(await t.page.locator('#row-menu').count(), 0);
      assert.match(await t.page.locator('#screen').innerText(), /Log viewing is unavailable/); assert.equal(t.journal.some(e => e.pathname === '/api/logs' || e.pathname === '/api/diag/singbox-config'), false);
    }, { state: { ...normalState, version: { version: '0.11.0-synthetic', installed_version: '0.10.0-synthetic', version_mismatch: true } }, latestRelease: 'v0.12.0' });
    for (const [theme, width] of [['light', 1440], ['light', 1024], ['light', 375], ['light', 320], ['dark', 1440], ['dark', 375]]) await tests.check(`${theme} ${width} ordinary full-app screen and dialog sweep`, async t => {
      const prefix = `${theme}-${width}`;
      for (const screen of ['peers', 'tunnels', 'rules', 'diag']) { await t.navigate(screen); await t.page.waitForFunction(async screen => document.querySelector('#screen h1')?.textContent.trim() === (await import(`/js/screens/${screen}.js`)).title, screen); assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width); await capture(t.page, `${prefix}-${screen}`); }
      await t.navigate('peers'); await act(t, 'peer-details', 'example'); await capture(t.page, `${prefix}-client-details`);
      await t.page.locator('#client-configuration > summary').click(); await t.page.locator('#client-config-result').waitFor({ state: 'visible' });
      await t.page.locator('[data-act="download-conf"]').focus(); await visibleFocus(t.page, '[data-act="download-conf"]'); await capture(t.page, `${prefix}-client-configuration`); await close(t, 'footer');
      await t.navigate('rules'); await act(t, 'edit-rule', 'work'); await capture(t.page, `${prefix}-rule-editor`);
      await t.page.locator('[data-act="save-rule"]').focus(); await visibleFocus(t.page, '[data-act="save-rule"]'); await capture(t.page, `${prefix}-rule-editor-bottom`); await close(t, 'backdrop');
      await t.page.locator('#btn-settings').click(); await t.page.waitForFunction(() => !document.querySelector('#settings-backup fieldset').disabled);
      for (const section of ['network', 'schedules', 'telegram', 'backup']) {
        await t.page.locator(`[data-settings-section="${section}"]`).click(); await capture(t.page, `${prefix}-settings-${section}`);
        await t.page.locator(`#settings-${section} [type="submit"]`).focus(); await visibleFocus(t.page, `#settings-${section} [type="submit"]`); await capture(t.page, `${prefix}-settings-${section}-bottom`);
      }
      await close(t); await t.navigate('tunnels'); await act(t, 'add-tunnel'); await capture(t.page, `${prefix}-new-tunnel`);
      await t.page.locator('#t-raw').focus(); await visibleFocus(t.page, '#t-raw'); await close(t, 'footer');
      await menu(t, 'tunnel', 'exit', 'edit-tunnel'); await t.page.locator('#t-parse.ok').waitFor(); await capture(t.page, `${prefix}-edit-tunnel`); await close(t, 'header');
      await menu(t, 'tunnel', 'pool', 'pool-details'); for (const summary of await t.page.locator('.pool-fold > summary').all()) await summary.click();
      await scrollEnds(t.page, '#modal .modal-body'); await t.page.locator('[data-act="refresh-pool-details"]').focus(); await visibleFocus(t.page, '[data-act="refresh-pool-details"]'); await capture(t.page, `${prefix}-pool-details`); await close(t);
      await t.navigate('diag'); await t.page.locator('[data-act="rerun-diag"]').click(); await t.settle(); assert.match(await t.page.locator('#toasts').innerText(), /Diagnostic checks completed/);
      await t.logout(); await t.login(); assert(await t.page.locator('#app').isVisible());
    }, { theme, viewport: { width, height: width < 500 ? 844 : 1000 }, state: normalState });
    tests.done();
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
