// Synthetic loopback browser fixture for the production client screen. No daemon.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist');
const origin = 'http://127.0.0.1:43187';
const examplePeer = { id: 'example', name: 'Travel laptop', address: '192.0.2.2', enabled: true, rx_bytes: 24576, tx_bytes: 1024 };
const exampleTunnels = [{ id: 'exit', name: 'Example exit', enabled: true, type: 'socks' }, { id: 'disabled', name: 'Backup exit', enabled: false, type: 'socks' }, { id: 'empty', name: 'Empty pool', enabled: true, source: 'pool', pool: { servers_alive: 0 } }];
const config = '[Interface]\nPrivateKey = AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\nAddress = 192.0.2.2/32\nDNS = 192.0.2.1\nMTU = 1280\n\n[Peer]\nPublicKey = AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=\nEndpoint = gateway.example:51820\nAllowedIPs = 0.0.0.0/0, ::/0\nPersistentKeepalive = 25\n';
async function fixture(browser, options = {}) {
  const page = await browser.newPage({ acceptDownloads: true, viewport: options.viewport || { width: 1440, height: 1000 }, colorScheme: options.theme || 'light' });
  const held = new Map(), holds = new Set(), overrides = new Map(), calls = [];
  let peers = structuredClone(options.peers || [examplePeer]), tunnels = structuredClone(options.tunnels || exampleTunnels), rules = options.rules || [];
  for (const p of peers) if (p.reality_profile_downloadable === undefined) p.reality_profile_downloadable = p.reality_profile_status === 'available';
  let authenticated = true;
  let settings = { client_mtu: 1280, wg_pool: '192.0.2.0/24', wg_server_address: '192.0.2.1', wg_listen_port: 51820, endpoint_host: 'gateway.example', dns_upstream: '192.0.2.53', ...(options.settings || {}) };
  const realityURI = options.realityURI || 'vless://11111111-1111-4111-8111-111111111111@gateway.example:8443?security=reality&type=tcp&sni=tls.example&fp=chrome&pbk=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA&sid=0011223344556677#Travel%20laptop';
  const profileHeaders = p => ({ 'X-Razdacha-Reality-State': p?.reality_profile_status || 'available', 'X-Razdacha-Reality-Revision': String(p?.reality_revision || 3) });
  const json = (route, body, status = 200, headers = {}) => route.fulfill({ status, headers, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), key = `${request.method()} ${url.pathname}`;
    if (url.origin !== origin) return route.abort();
    if (!url.pathname.startsWith('/api/')) {
      const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
      if (!file.startsWith(root + path.sep)) return route.abort();
      return route.fulfill({ body: fs.readFileSync(file), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' });
    }
    calls.push({ key, body: request.postData() ? request.postDataJSON() : null });
    if (holds.has(key)) { assert(!held.has(key), `Unexpected concurrent ${key}`); held.set(key, route); return; }
    if (overrides.has(key)) { const value = overrides.get(key); return json(route, value.body, value.status, value.headers); }
    if (url.pathname === '/api/session') return json(route, { authenticated });
    if (url.pathname === '/api/logout') { authenticated = false; return json(route, {}); }
    if (url.pathname === '/api/login') { authenticated = true; return json(route, {}); }
    if (key === 'GET /api/peers') return json(route, peers);
    if (key === 'POST /api/peers') { const p = { ...examplePeer, id: 'created', ...request.postDataJSON() }; if (p.reality_enabled) Object.assign(p, { reality_profile_status: 'pending', reality_profile_downloadable: settings.reality_enabled === true, reality_revision: 1 }); peers.push(p); return json(route, p); }
    if (url.pathname.endsWith('/reality-client-config')) return route.fulfill({ contentType: 'application/json', headers: { ...profileHeaders(peers.find(p => p.id === url.pathname.split('/')[3])), 'Content-Disposition': 'attachment; filename="razdacha-reality.json"', 'Cache-Control': 'no-store' }, body: JSON.stringify({ dns: { servers: [{ address: '10.8.0.1' }] }, inbounds: [{ type: 'tun' }], outbounds: [{ type: 'vless' }] }) });
    if (url.pathname.endsWith('/reality-config')) {
      const p = peers.find(p => p.id === url.pathname.split('/')[3]);
      if (!p?.enabled || !p.reality_enabled || !p.reality_profile_downloadable || !['pending', 'available'].includes(p.reality_profile_status)) return json(route, { error: 'Apply saved Reality settings before requesting a profile', code: 'not_ready' }, 409);
      return route.fulfill({ contentType: 'text/plain', headers: { ...profileHeaders(p), 'Content-Disposition': 'attachment; filename="reality-example.txt"', 'Cache-Control': 'no-store' }, body: realityURI });
    }
    if (url.pathname.endsWith('/reality/rotate')) {
      const p = peers.find(p => p.id === url.pathname.split('/')[3]);
      if (request.postDataJSON().expected_revision !== p.reality_revision) return json(route, { error: 'Reality credential changed; reload before replacing it', code: 'conflict' }, 409);
      p.reality_revision++; p.reality_profile_status = 'pending';
      return json(route, p);
    }
    if (key.startsWith('PATCH /api/peers/')) { const p = peers.find(p => p.id === url.pathname.split('/')[3]); Object.assign(p, request.postDataJSON()); return json(route, p); }
    if (key.startsWith('DELETE /api/peers/')) { peers = peers.filter(p => p.id !== url.pathname.split('/')[3]); return json(route, { inactivated_rule_ids: [] }); }
    if (key === 'GET /api/settings') return json(route, settings);
    if (key === 'PATCH /api/settings') { Object.assign(settings, request.postDataJSON()); return json(route, settings); }
    if (key === 'POST /api/apply') { for (const p of peers) if (p.reality_enabled && p.enabled && settings.reality_enabled) p.reality_profile_status = 'available'; return json(route, {}); }
    if (url.pathname.endsWith('/config')) return route.fulfill({ contentType: 'text/plain', headers: { 'Content-Disposition': `attachment; filename="${peers.find(p => p.id === url.pathname.split('/')[3])?.name === 'Renamed example' ? 'renamed' : 'example'}.conf"` }, body: config });
    if (url.pathname === '/api/tunnels') return json(route, tunnels);
    if (url.pathname === '/api/rules') return json(route, rules);
    if (url.pathname.includes('/lists/')) return json(route, []);
    if (url.pathname === '/api/apply/status') return json(route, { dirty: options.dirty === true });
    if (url.pathname === '/api/version') return json(route, { version: 'synthetic-preview' });
    return json(route, {});
  });
  await page.goto(origin); await page.locator('.client-row').first().waitFor();
  const invoke = (action, id = 'example') => page.evaluate(async ({ action, id }) => {
    const peers = await import('/js/screens/peers.js');
    window.actionDone = false;
    window.actionPromise = Promise.resolve(peers.actions[action](id)).finally(() => { window.actionDone = true; });
  }, { action, id });
  const state = () => page.evaluate(async () => {
    const { state } = await import('/js/shell.js');
    return { peers: state.peers, rules: state.rules, dirty: state.dirty, toasts: document.querySelector('#toasts').textContent, modal: document.querySelector('#modal').textContent, conf: document.querySelector('#modal').dataset.conf || '', filename: document.querySelector('#modal').dataset.confName || '' };
  });
  const waitHeld = async key => { for (let i = 0; i < 300 && !held.has(key); i++) await new Promise(r => setTimeout(r, 10)); assert(held.has(key), `Missing held ${key}`); };
  const reply = async (key, body = {}, status = 200) => { await waitHeld(key); const route = held.get(key); held.delete(key); holds.delete(key); await json(route, body, status); };
  const replyConfig = async (key = 'GET /api/peers/example/config', text = config, filename = 'example.conf', metadata = {}) => { await waitHeld(key); const route = held.get(key); held.delete(key); holds.delete(key); await route.fulfill({ contentType: 'text/plain', headers: { ...profileHeaders(peers.find(p => p.id === key.split('/')[3])), ...metadata, 'Content-Disposition': `attachment; filename="${filename}"` }, body: text }); };
  const done = () => page.waitForFunction(() => window.actionDone);
  const open = async (id = 'example') => {
    // Replacement-dialog lifetime cases intentionally enter the handler directly.
    if (await page.locator('#modal-backdrop').isVisible()) await invoke('peer-details', id);
    else { await page.locator(`[data-act="menu-peer"][data-id="${id}"]`).click(); await page.locator('#row-menu [data-act="peer-details"]').click(); }
    await page.locator('#client-name').waitFor();
  };
  const expand = async () => { await page.locator('#client-configuration > summary').click(); };
  const readyConfig = async () => { if (!await page.locator('#client-configuration').getAttribute('open').then(x => x !== null)) await expand(); await page.locator('#client-config-result').waitFor({ state: 'visible' }); };
  const replace = () => page.evaluate(async () => { const s = await import('/js/shell.js'); s.openModal(s.modalShell('Replacement', '<p>Keep this interaction</p>', '')); });
  const relogin = async () => { if (await page.locator('#modal-backdrop').isVisible()) await page.keyboard.press('Escape'); await page.locator('#btn-logout').click(); await page.locator('#gate').waitFor({ state: 'visible' }); await page.fill('#password', 'synthetic'); await page.locator('#login-submit').click(); await page.locator('.client-row').first().waitFor(); await page.evaluate(() => document.querySelector('#toasts').replaceChildren()); };
  return { page, holds, held, calls, overrides, invoke, state, reply, replyConfig, done, open, expand, readyConfig, replace, relogin, waitHeld, setTunnels: value => { tunnels = value; }, setPeers: value => { peers = value; }, setPeer: value => { const p = peers.find(p => p.id === value.id); if (p) Object.assign(p, value); }, realityURI };
}
module.exports = { fixture, examplePeer, exampleTunnels, config, root, origin };
