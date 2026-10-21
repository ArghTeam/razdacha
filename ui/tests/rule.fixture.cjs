// Local synthetic rule editor. Imported by layout tests and optional manual previews.
const fs = require('node:fs/promises');
const path = require('node:path');
const http = require('node:http');
const root = path.resolve(__dirname, '../dist');
const exampleRule = {
  id: 'r1', name: 'Media and collaboration', enabled: true, action: 'tunnel', tunnel_id: 'exit', via_tunnel_id: '',
  community_lists: [], domains: ['example.com'], subnets: [], remote_lists: [], peer_scope: 'all', peer_ids: [],
};
function dataFor(scenario = 'populated') {
  const communityLists = Array.from({ length: 36 }, (_, i) => ({ key: `source-${i}`, title: `Example community source ${i + 1}`, has_subnets: i % 3 === 0 }));
  const peers = Array.from({ length: 24 }, (_, i) => ({ id: `peer-${i}`, name: i === 0 ? 'LongUnbrokenClientIdentity'.repeat(5) : `Example device ${i + 1}`, enabled: i !== 1, egress_tunnel_id: i === 2 ? 'exit' : null }));
  const rule = { ...exampleRule, community_lists: communityLists.map((list) => list.key), domains: Array.from({ length: 39 }, (_, i) => `service${i}.example.com`), peer_scope: 'selected', peer_ids: peers.map((peer) => peer.id) };
  const data = { rules: [rule], communityLists, peers, tunnels: [{ id: 'exit', name: 'Example exit', enabled: true }, { id: 'off', name: 'Disabled exit', enabled: false }, { id: 'warp', name: 'Example WARP exit', source: 'warp', enabled: true }], dirty: false };
  if (scenario === 'empty') { data.communityLists = []; data.peers = []; data.tunnels = []; Object.assign(rule, { name: 'New empty rule', tunnel_id: '', community_lists: [], domains: [], peer_scope: 'all', peer_ids: [] }); }
  if (scenario === 'unavailable') { data.communityLists = []; Object.assign(rule, { community_lists: ['retained-source'], tunnel_id: 'missing', domains: ['example.com'] }); }
  if (scenario === 'url-only') Object.assign(rule, { community_lists: [], domains: [], peer_scope: 'all', remote_lists: ['https://example.invalid/sources/list.json?format=plain&scope=example', 'https://example.invalid/<escaped-source>.json'] });
  if (scenario === 'orphan') Object.assign(rule, { enabled: false, peer_ids: [], community_lists: [], domains: ['example.com'], inactive_reason: 'no_selected_peers' });
  if (scenario === 'chain') Object.assign(rule, { via_tunnel_id: 'warp', community_lists: [], domains: ['example.com'], peer_scope: 'all' });
  if (scenario === 'missing-chain') Object.assign(rule, { via_tunnel_id: 'missing-warp', community_lists: [], domains: ['example.com'], peer_scope: 'all' });
  return data;
}
async function startServer() {
  const index = (await fs.readFile(path.join(root, 'index.html'), 'utf8')).replace('<div id="app" hidden>', '<div id="app">');
  const server = http.createServer(async (request, response) => {
    try {
      const url = new URL(request.url, 'http://localhost');
      const data = dataFor(url.searchParams.get('scenario') || 'populated');
      if (url.pathname.startsWith('/api/')) {
        let body = ''; for await (const chunk of request) body += chunk;
        response.setHeader('Content-Type', 'application/json');
        if (url.pathname === '/api/rules' && request.method === 'GET') response.end(JSON.stringify(data.rules));
        else response.end(JSON.stringify(body ? { ...exampleRule, ...JSON.parse(body) } : { ok: true }));
        return;
      }
      if (url.pathname === '/') {
        response.setHeader('Content-Type', 'text/html');
        const script = `<script type="module">
          import * as shell from '/js/shell.js'; import * as rules from '/js/screens/rules.js'; import * as api from '/js/api.js';
          Object.assign(shell.state, window.fixtureSeed || ${JSON.stringify(data).replace(/</g, '\\u003c')});
          window.h = {shell, rules, api};
          document.querySelector('#screen').innerHTML = '<h1>Synthetic rule editor preview</h1><p>No installed daemon or networking. Changes are disposable.</p><button class="btn" data-act="edit-rule" data-id="r1">Open example rule</button>';
          document.addEventListener('click', event => { const button = event.target.closest('[data-act]'); if (!button) return; if (button.dataset.act === 'close-modal') shell.closeModal(); else window.actionPending = rules.actions[button.dataset.act]?.(button.dataset.id); });
          rules.actions['edit-rule']('r1');
        </script>`;
        response.end(index.replace('<script type="module" src="/js/app.js"></script>', script));
        return;
      }
      const file = path.resolve(root, '.' + decodeURIComponent(url.pathname));
      if (!file.startsWith(root + path.sep)) { response.writeHead(404).end(); return; }
      response.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/plain');
      response.end(await fs.readFile(file));
    } catch { response.writeHead(404).end(); }
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  return { server, url: `http://127.0.0.1:${server.address().port}` };
}
async function fixture(browser, origin, { width = 1440, height = width < 500 ? 844 : 1000, theme = 'light', scenario = 'populated', data } = {}) {
  const context = await browser.newContext({ viewport: { width, height }, colorScheme: theme });
  await context.route('**/*', route => new URL(route.request().url()).origin === origin ? route.continue() : route.abort());
  const page = await context.newPage();
  const errors = []; page.on('pageerror', error => errors.push(error.message));
  if (data) await page.addInitScript(seed => { window.fixtureSeed = seed; }, data);
  await page.goto(`${origin}/?scenario=${scenario}`);
  await page.locator('#r-name').waitFor({ state: 'visible' });
  return { context, page, errors };
}
module.exports = { startServer, fixture, dataFor, exampleRule };
