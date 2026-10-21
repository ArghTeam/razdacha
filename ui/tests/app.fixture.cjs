// Exact-origin synthetic full-app fixture. No daemon, installed state or real probes.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist');
const origin = 'http://127.0.0.1:43189';
const defaultState = {
  authenticated: true,
  settings: { client_mtu: 1280, wg_pool: '192.0.2.0/24', wg_server_address: '192.0.2.1', endpoint_host: 'gateway.example' },
  peers: [{ id: 'example', name: 'Example client', address: '192.0.2.2', enabled: true }],
  tunnels: [{ id: 'exit', name: 'Example exit', type: 'socks', enabled: true }],
  rules: [], community: [], dirty: false,
  notify: { enabled: false, token_set: false, chat_id: '' },
  backup: { enabled: false, passphrase_set: false, interval_hours: 24 },
  version: { version: '0.10.0-synthetic', installed_version: '0.10.0-synthetic', commit: '0000000000000000000000000000000000000000', schema_version: 1, singbox: { library: '1.12.25', runtime: '1.12.25' } },
  diag: { overall: 'warn', checked_at: '2026-10-21T09:00:00Z', checks: [{ id: 'wg', title: 'WireGuard', status: 'ok', detail: 'Synthetic interface available' }, { id: 'nft', title: 'Policy', status: 'warn', detail: 'Synthetic pending policy' }] },
};
const clientConfig = '[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 192.0.2.2/32\nDNS = 192.0.2.1\nMTU = 1280\n\n[Peer]\nPublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nEndpoint = gateway.example:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n';
const keyFor = (method, requestPath) => requestPath === undefined ? method : `${method.toUpperCase()} ${requestPath}`;
const jsonResponse = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) });
function createAppModel(overrides = {}) {
  const state = { ...structuredClone(defaultState), ...structuredClone(overrides) };
  const json = jsonResponse;
  function reply(entry) {
    const { key, pathname, url } = entry;
    if (key === 'GET /api/session') return json({ authenticated: state.authenticated });
    if (key === 'POST /api/login') { state.authenticated = true; return json({}); }
    if (key === 'POST /api/logout') { state.authenticated = false; return json({}); }
    if (key === 'GET /api/settings') return json(state.settings);
    if (key === 'GET /api/notify') return json(state.notify);
    if (key === 'GET /api/backup') return json(state.backup);
    if (key === 'GET /api/peers') return json(state.peers);
    if (key === 'GET /api/tunnels') return json(state.tunnels);
    if (key === 'GET /api/rules') return json(state.rules);
    if (key === 'GET /api/lists/community') return json(state.community);
    if (key === 'GET /api/version') return json(state.version);
    if (key === 'GET /api/diag') return json(state.diag);
    if (key === 'GET /api/apply/status') return json({ dirty: state.dirty });
    if (key === 'POST /api/apply') { state.dirty = false; return json({ ok: true }); }
    if (entry.method === 'POST' && pathname === '/api/diag/run') {
      const id = new URL(url).searchParams.get('check');
      const result = state.diag || { checks: [] };
      return json({ ...result, checks: id ? (result.checks || []).filter(check => check.id === id) : result.checks || [] });
    }
    if (entry.method === 'POST' && /^\/api\/tunnels\/[^/]+\/check$/.test(pathname)) return json({ status: 'ok' });
    if (entry.method === 'GET' && /^\/api\/peers\/[^/]+\/config$/.test(pathname)) return { status: 200, contentType: 'text/plain', headers: { 'Content-Disposition': 'attachment; filename="example.conf"' }, body: clientConfig };
    return null;
  }
  return { state, reply };
}

async function appFixture(browser, options = {}) {
  const model = createAppModel(options.state);
  const state = model.state;
  const page = await browser.newPage({ acceptDownloads: true, viewport: options.viewport || { width: 1440, height: 1000 }, colorScheme: options.theme || 'light' });
  const journal = [], unexpected = [], errors = [], held = new Map(), holds = new Set(), failures = new Map(Object.entries(options.responses || {})), handlers = new Map(Object.entries(options.handlers || {}));
  const pendingRequests = new Set();
  page.on('request', request => pendingRequests.add(request));
  page.on('requestfinished', request => pendingRequests.delete(request));
  page.on('requestfailed', request => pendingRequests.delete(request));
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(latest => {
    sessionStorage.setItem('razdacha:latest-release', JSON.stringify({ at: Date.now(), tag: latest }));
    // Observe completion of the real async Apply listener, without replacing its work.
    const add = EventTarget.prototype.addEventListener;
    EventTarget.prototype.addEventListener = function(type, listener, settings) {
      if (this.id === 'btn-apply' && type === 'click' && typeof listener === 'function') {
        return add.call(this, type, function(event) {
          const result = listener.call(this, event);
          window.fixtureApply = Promise.resolve(result);
          return result;
        }, settings);
      }
      return add.call(this, type, listener, settings);
    };
  }, options.latestRelease || null);
  const json = (body, status = 200) => ({ status, contentType: 'application/json', body: JSON.stringify(body) });
  function standard(entry) {
    const response = model.reply(entry);
    if (response) return response;
    unexpected.push(entry);
    return json({ error: 'Unexpected synthetic request' }, 501);
  }
  async function dispatch(entry, override) {
    let response = override;
    if (!response && failures.has(entry.key)) response = failures.get(entry.key);
    if (!response && handlers.has(entry.key)) response = await handlers.get(entry.key)(entry, state);
    if (!response) response = standard(entry);
    if (response.abort) return entry.route.abort(response.abort);
    await entry.route.fulfill(response);
  }
  for (const key of options.hold || []) holds.add(key);
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url());
    const entry = { route, request, method: request.method(), pathname: url.pathname, url: url.href, key: `${request.method()} ${url.pathname}${url.search}`, body: request.postData() ? request.postDataJSON() : null };
    journal.push(entry);
    if (url.origin !== origin) { unexpected.push(entry); return route.abort(); }
    if (!url.pathname.startsWith('/api/')) {
      const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
      if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) { unexpected.push(entry); return route.fulfill({ status: 404, body: 'Unexpected synthetic asset' }); }
      return route.fulfill({ body: fs.readFileSync(file), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' });
    }
    if (holds.has(entry.key)) { const queue = held.get(entry.key) || []; queue.push(entry); held.set(entry.key, queue); return; }
    await dispatch(entry);
  });
  const hold = (method, requestPath) => holds.add(keyFor(method, requestPath));
  const fail = (method, requestPath, body = { error: 'Synthetic request failed' }, status = 503) => failures.set(keyFor(method, requestPath), json(body, status));
  const failNetwork = (method, requestPath) => failures.set(keyFor(method, requestPath), { abort: 'connectionrefused' });
  const recover = (method, requestPath) => failures.delete(keyFor(method, requestPath));
  const respond = (method, requestPath, handler) => handlers.set(keyFor(method, requestPath), handler);
  async function waitHeld(method, requestPath) {
    const key = keyFor(method, requestPath);
    for (let attempt = 0; attempt < 300 && !held.get(key)?.length; attempt++) await new Promise(resolve => setTimeout(resolve, 10));
    assert(held.get(key)?.length, `No held request: ${key}`);
  }
  async function release(method, requestPath, override) {
    if (requestPath && typeof requestPath === 'object') { override = requestPath; requestPath = undefined; }
    const key = keyFor(method, requestPath); await waitHeld(key);
    const queue = held.get(key), entry = queue.shift();
    if (!queue.length) held.delete(key);
    holds.delete(key);
    await dispatch(entry, override);
  }
  const applyDone = () => page.evaluate(() => window.fixtureApply);
  async function settle() {
    // Intentionally held earlier-session requests must not block a new login's readiness.
    const busy = () => [...pendingRequests].filter(request => ![...held.values()].flat().some(entry => entry.request === request));
    const deadline = Date.now() + 10000;
    while (Date.now() < deadline) {
      if (!busy().length) {
        await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
        if (!busy().length) return;
      }
      await new Promise(resolve => setTimeout(resolve, 10));
    }
    assert.fail(`Synthetic requests did not settle: ${busy().map(request => request.url()).join(', ')}`);
  }
  async function login() { await page.fill('#password', 'synthetic-password'); await page.locator('#login-submit').click(); await page.locator('#app').waitFor({ state: 'visible' }); await page.locator('#screen .screen-head').waitFor(); await settle(); }
  async function navigate(screen) {
    await page.locator(`[data-screen="${screen}"]`).click();
    await page.waitForFunction(value => document.querySelector('#tabs [aria-selected="true"]')?.dataset.screen === value && location.hash === '#' + value, screen);
    await settle();
  }
  async function logout() { if (await page.locator('#modal-backdrop').isVisible()) await page.keyboard.press('Escape'); await page.locator('#btn-logout').click(); await page.locator('#gate').waitFor({ state: 'visible' }); }
  async function snapshot() { return page.evaluate(async () => { const { state } = await import('/js/shell.js'); return { screen: state.screen, dirty: state.dirty, version: state.version, diag: state.diag, peers: state.peers, rules: state.rules, toasts: document.querySelector('#toasts').textContent, gate: !document.querySelector('#gate').hidden, modal: document.querySelector('#modal').textContent }; }); }
  function assertClean() { assert.deepEqual(unexpected.map(entry => `${entry.method} ${entry.url}`), []); assert.deepEqual(errors, []); }
  await page.goto(`${origin}/${options.screen ? '#' + options.screen : ''}`);
  if (options.readiness !== 'none') {
    await page.locator(options.readiness === 'login' || !state.authenticated ? '#gate' : '#app').waitFor({ state: 'visible' });
    if (options.readiness !== 'login' && state.authenticated) await page.locator('#screen .screen-head').waitFor();
    await settle();
  }
  return { page, state, journal, unexpected, errors, held, holds, hold, fail, failNetwork, recover, respond, waitHeld, release, json, settle, applyDone, login, logout, navigate, snapshot, assertClean };
}
module.exports = { appFixture, createAppModel, defaultState, clientConfig, origin, root };
