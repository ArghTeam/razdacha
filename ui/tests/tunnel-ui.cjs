const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { suite, act, menu, capture, scrollEnds, visibleFocus } = require('./ui-sweep.helper.cjs');
const exit = { id: 'exit', name: 'Demo exit', type: 'socks', source: 'url', enabled: true, host: 'proxy.example.invalid', port: 1080, status: 'up', latency_ms: 17 };
const pool = { id: 'pool', name: 'Demo pool', type: 'vless', source: 'pool', enabled: true, pool: { servers_total: 4, servers_alive: null, servers_excluded: 1 } };
const warp = { id: 'warp', name: 'Demo WARP', type: 'wireguard', source: 'warp', enabled: true };
const server = (id, extra = {}) => ({ id, title: `Example server ${id}`, in_rotation: true, alive: true, latency_ms: 17, ...extra });
async function parsed(page) { await page.locator('#t-parse.ok').waitFor(); }
(async () => {
  const browser = await chromium.launch({ headless: true }); const tests = suite(browser, 'tunnel UI');
  try {
    await tests.check('scoped observations distinguish DNS, HTTPS and unknown transport', async t => {
      const text = await t.page.locator('main').innerText();
      assert.match(text, /HTTPS inconclusive/);
      assert.match(text, /Transport: unknown/);
      assert.match(text, /Runtime DNS: rejected/);
      assert.equal(await t.page.locator('.badge.err').count(), 0);
    }, { screen: 'tunnels', state: { tunnels: [{ ...exit, status: 'unknown', signals: { https: { fresh: true }, dns: { state: 'rejected' }, transport: { state: 'unknown' } } }] } });
    await tests.check('historical HTTPS evidence cannot render green', async t => {
      assert.match(await t.page.locator('main').innerText(), /Historical HTTPS observation/);
      assert.equal(await t.page.locator('.badge.ok').count(), 0);
    }, { screen: 'tunnels', state: { tunnels: [{ ...exit, signals: { https: { fresh: false }, transport: { state: 'unknown' } } }] } });
    await tests.check('new form validates before mutation and preserves parse warnings safely', async t => {
      await act(t, 'add-tunnel'); await act(t, 'save-tunnel'); assert(!t.journal.some(e => e.key === 'POST /api/tunnels'));
      await t.page.fill('#t-name', 'Created demo exit'); await act(t, 'save-tunnel'); assert(!t.journal.some(e => e.key === 'POST /api/tunnels'));
      t.respond('POST', '/api/tunnels/parse', () => t.json({ type: 'socks', host: 'proxy.example.invalid', port: 1080, warnings: ['Synthetic warning <img src=x>'] }));
      await t.page.fill('#t-raw', 'socks5://proxy.example.invalid:1080'); await parsed(t.page);
      assert.match(await t.page.locator('#t-parse').innerText(), /Synthetic warning <img src=x>/); assert.equal(await t.page.locator('#t-parse img').count(), 0);
      await act(t, 'save-tunnel'); assert.equal(t.state.tunnels.at(-1).name, 'Created demo exit'); assert((await t.snapshot()).dirty);
      assert.deepEqual(t.journal.find(e => e.key === 'POST /api/tunnels').body, { name: 'Created demo exit', raw: 'socks5://proxy.example.invalid:1080' });
    }, { screen: 'tunnels', state: { tunnels: [] } });
    for (const [type, raw] of [['wireguard', '[Interface]\nPrivateKey = SYNTHETIC\n'], ['vless', 'vless://synthetic@example.invalid:443'], ['vmess', 'vmess://synthetic'], ['trojan', 'trojan://synthetic@example.invalid:443'], ['shadowsocks', 'ss://synthetic@example.invalid:443'], ['hysteria2', 'hysteria2://synthetic@example.invalid:443'], ['socks', '{"type":"socks","server":"example.invalid"}']]) await tests.check(`server parse ${type} response renders for its synthetic input shape`, async t => {
      t.respond('POST', '/api/tunnels/parse', () => t.json({ type, security: 'tls', transport: 'ws', host: 'example.invalid', port: 443 }));
      await act(t, 'add-tunnel'); await t.page.fill('#t-raw', raw); await parsed(t.page); assert.match(await t.page.locator('#t-parse').innerText(), /TLS.*WS.*example.invalid:443/);
      assert.equal(t.journal.filter(e => e.key === 'POST /api/tunnels/parse').at(-1).body.raw, raw.trim());
      assert(!t.journal.some(e => e.key === 'POST /api/tunnels'));
    }, { screen: 'tunnels' });
    await tests.check('invalid parse prevents save; unavailable preview permits server-side parsing', async t => {
      await act(t, 'add-tunnel'); await t.page.fill('#t-name', 'Example exit');
      t.fail('POST', '/api/tunnels/parse', { error: 'Synthetic invalid input' }, 422); await t.page.fill('#t-raw', 'invalid synthetic input'); await t.page.locator('#t-parse.err').waitFor();
      await act(t, 'save-tunnel'); assert(!t.journal.some(e => e.key === 'POST /api/tunnels'));
      t.fail('POST', '/api/tunnels/parse', { error: 'Synthetic missing preview' }, 404); await t.page.fill('#t-raw', 'socks5://example.invalid:1080');
      await t.page.waitForFunction(() => document.querySelector('#t-parse').classList.contains('idle') && document.querySelector('#t-parse').textContent.includes('POST /api/tunnels/parse'));
      await act(t, 'save-tunnel'); assert(t.journal.some(e => e.key === 'POST /api/tunnels'));
    }, { screen: 'tunnels' });
    for (const status of [404, 503]) await tests.check(`raw configuration ${status} explains replacement without losing name`, async t => {
      await menu(t, 'tunnel', 'exit', 'edit-tunnel'); assert.equal(await t.page.inputValue('#t-name'), 'Demo exit'); assert(await t.page.locator('#t-raw').isEnabled());
      const failure = await t.page.locator('#t-parse').innerText(); assert(failure.includes(status === 404 ? 'GET /api/tunnels/{id}/raw' : 'Synthetic raw read failure')); await t.page.fill('#t-raw', 'socks5://replacement.example.invalid:1080'); await parsed(t.page); await act(t, 'save-tunnel');
      assert.equal(t.journal.find(e => e.key === 'PATCH /api/tunnels/exit').body.raw, 'socks5://replacement.example.invalid:1080');
    }, { screen: 'tunnels', state: { tunnels: [exit] }, responses: { 'GET /api/tunnels/exit/raw': { status, contentType: 'application/json', body: '{"error":"Synthetic raw read failure"}' } } });
    await tests.check('WARP name-only editor never exposes or writes raw configuration', async t => {
      assert.equal(await t.page.locator('[data-act="add-warp"]').count(), 0);
      await menu(t, 'tunnel', 'warp', 'edit-tunnel'); assert.equal(await t.page.locator('#t-raw').count(), 0); assert.match(await t.page.locator('#t-raw-field').innerText(), /not editable/);
      await t.page.fill('#t-name', 'Renamed demo WARP'); await act(t, 'save-tunnel'); assert.deepEqual(t.journal.find(e => e.key === 'PATCH /api/tunnels/warp').body, { name: 'Renamed demo WARP' });
    }, { screen: 'tunnels', state: { tunnels: [warp] } });
    await tests.check('actual inventory buttons bind check, pool refresh, toggle, delete and synthetic WARP', async t => {
      await act(t, 'check-tunnel', 'exit'); assert(t.journal.some(e => e.key === 'POST /api/tunnels/exit/check'));
      await act(t, 'refresh-pool', 'pool'); assert(t.journal.some(e => e.key === 'POST /api/tunnels/pool/refresh'));
      await menu(t, 'tunnel', 'exit', 'toggle-tunnel'); assert.equal(t.state.tunnels.find(item => item.id === 'exit').enabled, false); assert.equal(await t.page.evaluate(async () => (await import('/js/shell.js')).state.tunnels.find(item => item.id === 'exit').enabled), false);
      t.page.once('dialog', dialog => dialog.dismiss()); await menu(t, 'tunnel', 'exit', 'delete-tunnel'); assert(!t.journal.some(e => e.key === 'DELETE /api/tunnels/exit'));
      t.page.once('dialog', dialog => dialog.accept()); await menu(t, 'tunnel', 'exit', 'delete-tunnel'); assert(!t.state.tunnels.some(item => item.id === 'exit'));
      await act(t, 'add-warp'); assert(t.state.tunnels.some(item => item.source === 'warp')); assert.equal(await t.page.locator('[data-act="add-warp"]').count(), 0);
      assert.equal(t.journal.filter(e => e.key === 'POST /api/tunnels/warp').length, 1);
    }, { screen: 'tunnels', state: { tunnels: [exit, pool] } });
    for (const variant of ['empty', 'excluded', 'unknown', 'disabled']) await tests.check(`pool ${variant} state and menu restrictions`, async t => {
      const row = t.page.locator('.row').filter({ has: t.page.locator('[data-act="menu-tunnel"][data-id="pool"]') });
      if (variant === 'unknown' || variant === 'disabled') { assert.match(await row.locator('.badge').allTextContents().then(values => values.join(' ')), /4/); assert.equal(await row.locator('.badge.ok, .badge.err').count(), 0); }
      await act(t, 'menu-tunnel', 'pool'); assert.equal(await t.page.locator('#row-menu [data-act="edit-tunnel"], #row-menu [data-act="delete-tunnel"]').count(), 0);
      await t.page.locator('#row-menu [data-act="pool-details"]').click(); await t.settle();
      const text = await t.page.locator('#pool-details').innerText();
      if (variant === 'empty') { assert.equal(await t.page.locator('#pool-details .pool-list').count(), 0); assert(await t.page.locator('#pool-details > .parse-result.idle').isVisible()); }
      if (variant === 'excluded') { assert.match(text, /1/); await t.page.locator('.pool-fold > summary').click(); assert.match(await t.page.locator('#pool-details').innerText(), /Synthetic country filter/); }
      if (variant === 'unknown') { assert.equal(await t.page.locator('#pool-details .dot.off').count(), 1); assert.equal(await t.page.locator('#pool-details .badge.ok, #pool-details .badge.err').count(), 0); }
      if (variant === 'disabled') assert(await t.page.locator('#pool-details > .parse-result.idle').isVisible());
    }, { screen: 'tunnels', state: { tunnels: [{ ...pool, enabled: variant !== 'disabled' }], poolDetails: variant === 'empty' ? { servers: [], excluded: [] } : variant === 'excluded' ? { servers: [], excluded: [{ title: 'Excluded candidate', reason: 'Synthetic country filter' }] } : { servers: [server('unknown', { alive: null })], excluded: [] } } });
    for (const status of [404, 503]) await tests.check(`pool details ${status} stays local and refresh retries`, async t => {
      t.fail('GET', '/api/tunnels/pool/pool', { error: 'Synthetic pool detail failure' }, status); await menu(t, 'tunnel', 'pool', 'pool-details');
      assert.match(await t.page.locator('#pool-details').innerText(), status === 404 ? /unavailable/ : /Synthetic pool detail failure/);
      t.recover('GET', '/api/tunnels/pool/pool'); await act(t, 'refresh-pool-details', 'pool'); assert(await t.page.locator('#pool-details > .parse-result.idle').isVisible()); assert.equal(await t.page.locator('#pool-details .err').count(), 0);
    }, { screen: 'tunnels', state: { tunnels: [pool] } });
    for (const theme of ['light', 'dark']) for (const width of [1440, 1024, 375, 320]) await tests.check(`${theme} ${width} pool nested scroll ends and focused footer`, async t => {
      await menu(t, 'tunnel', 'pool', 'pool-details');
      for (const summary of await t.page.locator('.pool-fold > summary').all()) await summary.click();
      assert.equal(await t.page.locator('.pool-list').count(), 4);
      await scrollEnds(t.page, '.pool-list', { requireOverflow: true }); await scrollEnds(t.page, '#modal');
      await t.page.locator('[data-act="refresh-pool-details"]').focus(); await visibleFocus(t.page, '[data-act="refresh-pool-details"]');
      assert.equal(await t.page.evaluate(() => document.documentElement.scrollWidth), width);
      await capture(t.page, `pool-${theme}-${width}-all-scroll-ends`, 'stress');
    }, { screen: 'tunnels', theme, viewport: { width, height: width < 500 ? 667 : 900 }, state: { tunnels: [pool], poolDetails: { servers: [...Array.from({ length: 25 }, (_, i) => server(`live-${i}`)), ...Array.from({ length: 25 }, (_, i) => server(`dead-${i}`, { alive: false })), ...Array.from({ length: 25 }, (_, i) => server(`rest-${i}`, { in_rotation: false, alive: null, latency_ms: null }))], excluded: Array.from({ length: 25 }, (_, i) => ({ title: `Excluded example ${i}`, reason: 'Synthetic country filter' })) } } });
    tests.done();
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
