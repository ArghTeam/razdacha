// Exact-origin synthetic full-app fixture. No daemon, installed state or real probes.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist');
const origin = 'http://127.0.0.1:43189';
const defaultState = {
  authenticated: true,
  settings: { wg_listen_port: 51820, dns_upstream: '1.1.1.1', list_update_interval: 86400, pool_update_interval: 3600, tunnel_check_interval: 120, client_mtu: 1280, wg_pool: '192.0.2.0/24', wg_server_address: '192.0.2.1', endpoint_host: 'gateway.example' },
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
  let nextId = 1;
  const raws = new Map();
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
    if (key === 'PATCH /api/settings') { Object.assign(state.settings, entry.body); state.dirty = true; return json(state.settings); }
    if (key === 'PUT /api/notify') {
      Object.assign(state.notify, Object.fromEntries(Object.entries(entry.body).filter(([key]) => key !== 'token')));
      if (entry.body.token) state.notify.token_set = true;
      return json(state.notify);
    }
    if (key === 'PUT /api/backup') {
      Object.assign(state.backup, Object.fromEntries(Object.entries(entry.body).filter(([key]) => key !== 'passphrase')));
      if (entry.body.passphrase) state.backup.passphrase_set = true;
      return json(state.backup);
    }
    if (key === 'POST /api/notify/test') return json({ ok: true });
    if (key === 'POST /api/backup/send') { state.backup.last_sent_at = '2026-10-21T12:00:00Z'; return json({ ok: true }); }
    if (key === 'GET /api/backup/download') return { status: 200, contentType: 'application/octet-stream', headers: { 'Content-Disposition': 'attachment; filename="synthetic-backup.db"' }, body: 'Disposable synthetic backup, not a database' };
    if (key === 'POST /api/peers') { const peer = { id: `client-${nextId++}`, name: entry.body.name, address: '192.0.2.3', enabled: true }; state.peers.push(peer); state.dirty = true; return json(peer); }
    if (entry.method === 'PATCH' && /^\/api\/peers\/[^/]+$/.test(pathname)) { const peer = state.peers.find(item => item.id === pathname.split('/')[3]); Object.assign(peer, entry.body); state.dirty = true; return json(peer); }
    if (entry.method === 'DELETE' && /^\/api\/peers\/[^/]+$/.test(pathname)) { state.peers = state.peers.filter(item => item.id !== pathname.split('/')[3]); state.dirty = true; return json({ affected_rule_ids: [], inactivated_rule_ids: [] }); }
    if (key === 'PUT /api/rules/order') { state.rules = entry.body.ids.map(id => state.rules.find(rule => rule.id === id)); state.dirty = true; return json({ ok: true }); }
    if (key === 'POST /api/rules') { const rule = { ...entry.body, id: `rule-${nextId++}`, priority: state.rules.length }; state.rules.push(rule); state.dirty = true; return json(rule); }
    if (entry.method === 'PATCH' && /^\/api\/rules\/[^/]+$/.test(pathname)) { const rule = state.rules.find(item => item.id === pathname.split('/')[3]); Object.assign(rule, entry.body); state.dirty = true; return json(rule); }
    if (entry.method === 'DELETE' && /^\/api\/rules\/[^/]+$/.test(pathname)) { state.rules = state.rules.filter(item => item.id !== pathname.split('/')[3]); state.dirty = true; return json({ ok: true }); }
    if (key === 'POST /api/route/test') return json({ domain: entry.body.domain, verdict: 'Synthetic shared-rule match', rule: { name: 'Example rule', outbound: 'Demo exit' }, addresses: ['198.51.100.2'] });
    if (key === 'POST /api/domain/reachability') return json({ domain: entry.body.domain, class: 'reachable', status: 200, verdict: 'Synthetic direct response' });
    if (key === 'POST /api/tunnels/parse') return json({ type: 'socks', host: 'proxy.example.invalid', port: 1080 });
    if (key === 'POST /api/tunnels/warp' || key === 'POST /api/tunnels') {
      const tunnel = { id: `tunnel-${nextId++}`, name: entry.body?.name || 'Synthetic WARP', enabled: true, type: key.endsWith('/warp') ? 'wireguard' : 'socks', source: key.endsWith('/warp') ? 'warp' : 'url' };
      raws.set(tunnel.id, entry.body?.raw || ''); state.tunnels.push(tunnel); state.dirty = true; return json(tunnel);
    }
    if (entry.method === 'GET' && /^\/api\/tunnels\/[^/]+\/raw$/.test(pathname)) {
      const tunnel = state.tunnels.find(item => item.id === pathname.split('/')[3]);
      return json(tunnel?.source === 'warp' ? { editable: false, raw: '', note: 'Generated configuration is not editable.' } : { editable: true, raw: raws.get(tunnel?.id) || 'socks5://proxy.example.invalid:1080' });
    }
    if (entry.method === 'GET' && /^\/api\/tunnels\/[^/]+\/pool$/.test(pathname)) return json(state.poolDetails || { servers: [], excluded: [] });
    if (entry.method === 'POST' && /^\/api\/tunnels\/[^/]+\/refresh$/.test(pathname)) return json(state.tunnels.find(item => item.id === pathname.split('/')[3]));
    if (entry.method === 'PATCH' && /^\/api\/tunnels\/[^/]+$/.test(pathname)) {
      const tunnel = state.tunnels.find(item => item.id === pathname.split('/')[3]);
      if (entry.body.raw) raws.set(tunnel.id, entry.body.raw);
      Object.assign(tunnel, Object.fromEntries(Object.entries(entry.body).filter(([key]) => key !== 'raw'))); state.dirty = true; return json(tunnel);
    }
    if (entry.method === 'DELETE' && /^\/api\/tunnels\/[^/]+$/.test(pathname)) { state.tunnels = state.tunnels.filter(item => item.id !== pathname.split('/')[3]); state.dirty = true; return json({ ok: true }); }
    if (key === 'GET /api/apply/status') return json({ dirty: state.dirty });
    if (key === 'POST /api/apply') { state.dirty = false; return json({ ok: true }); }
    if (entry.method === 'POST' && pathname === '/api/diag/run') {
      const id = new URL(url).searchParams.get('check');
      const result = state.diag || { checks: [] };
      return json({ ...result, checks: id ? (result.checks || []).filter(check => check.id === id) : result.checks || [] });
    }
    if (entry.method === 'POST' && /^\/api\/tunnels\/[^/]+\/check$/.test(pathname)) return json({ status: 'up', latency_ms: 17, last_check: '2026-10-21T12:00:00Z' });
    if (entry.method === 'GET' && /^\/api\/peers\/[^/]+\/config$/.test(pathname)) return { status: 200, contentType: 'text/plain', headers: { 'Content-Disposition': 'attachment; filename="example.conf"' }, body: clientConfig };
    return null;
  }
  return { state, reply };
}

async function appFixture(browser, options = {}) {
  const fixtureOrigin = options.origin || origin;
  const local = new URL(fixtureOrigin);
  assert(local.protocol === 'http:' && local.hostname === '127.0.0.1' && local.origin === fixtureOrigin, 'Fixture origin must be an exact disposable loopback origin');
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
    if (url.origin !== fixtureOrigin) { unexpected.push(entry); return route.abort(); }
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
  await page.goto(`${fixtureOrigin}/${options.screen ? '#' + options.screen : ''}`);
  if (options.readiness !== 'none') {
    await page.locator(options.readiness === 'login' || !state.authenticated ? '#gate' : '#app').waitFor({ state: 'visible' });
    if (options.readiness !== 'login' && state.authenticated) await page.locator('#screen .screen-head').waitFor();
    await settle();
  }
  return { page, state, journal, unexpected, errors, held, holds, hold, fail, failNetwork, recover, respond, waitHeld, release, json, settle, applyDone, login, logout, navigate, snapshot, assertClean };
}
module.exports = { appFixture, createAppModel, defaultState, clientConfig, origin, root };
