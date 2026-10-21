// Disposable exact-origin fixture for the production tunnel module and shell/API.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist');
const origin = 'http://127.0.0.1:43256';
const exampleTunnel = { id: 't1', name: 'Example tunnel', enabled: true, type: 'socks', source: 'url', status: null };
const examplePool = { id: 'pool', name: 'Example pool', enabled: true, source: 'pool', type: 'vless', pool: { servers_total: 3, servers_alive: null } };
const syntheticRaw = 'socks5://proxy.example.invalid:1080';
const applicationHTML = fs.readFileSync(path.join(root, 'index.html'), 'utf8');
const html = applicationHTML.replace('<div id="app" hidden>', '<div id="app">').replace('<script type="module" src="/js/app.js"></script>', `<script type="module">
  import * as tunnels from '/js/screens/tunnels.js'; import * as shell from '/js/shell.js'; import * as api from '/js/api.js';
  window.h = {tunnels, shell, api}; Object.assign(shell.state, window.fixtureState); shell.state.screen = 'tunnels';
  const render = () => { if (shell.state.screen === 'tunnels') document.querySelector('#screen').innerHTML = tunnels.view(); };
  document.addEventListener('razdacha:render', render);
  api.onUnauthorized(() => shell.closeModal({restoreFocus:false}));
  document.addEventListener('click', event => {const button=event.target.closest('[data-act]');if(!button)return; if(button.dataset.act==='close-modal')shell.closeModal();else window.clickPromise=Promise.resolve(tunnels.actions[button.dataset.act]?.(button.dataset.id,button));});
  render(); window.fixtureReady=true;
</script>`);
async function fixture(browser, options = {}) {
  const context = await browser.newContext();
  const page = await context.newPage();
  const held = new Map(), holds = new Set(), overrides = new Map(), calls = [], errors = [];
  let tunnels = structuredClone(options.tunnels || [exampleTunnel, examplePool]);
  page.on('pageerror', error => errors.push(error.message));
  await page.addInitScript(state => { window.fixtureState = state; }, { tunnels, peers: [], rules: [], dirty: false });
  const json = (route, body, status = 200) => route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) });
  await page.route('**/*', async route => {
    const request = route.request(), url = new URL(request.url()), key = `${request.method()} ${url.pathname}`;
    if (url.origin !== origin) return route.abort();
    if (!url.pathname.startsWith('/api/')) {
      const file = path.resolve(root, '.' + url.pathname);
      if (url.pathname === '/') return route.fulfill({ contentType: 'text/html', body: options.application ? applicationHTML : html });
      if (!file.startsWith(root + path.sep)) return route.abort();
      return route.fulfill({ body: fs.readFileSync(file), contentType: file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/plain' });
    }
    calls.push({ key, body: request.postData() ? request.postDataJSON() : null });
    if (holds.has(key)) { if (!held.has(key)) held.set(key, []); held.get(key).push(route); return; }
    if (overrides.has(key)) { const result = overrides.get(key); return json(route, result.body, result.status); }
    if (key === 'GET /api/session') return json(route,{authenticated:true});
    if (key === 'GET /api/peers') return json(route,[{id:'peer1',name:'Example client',address:'192.0.2.2',enabled:true}]);
    if (key === 'GET /api/rules' || url.pathname.includes('/lists/')) return json(route,[]);
    if (key === 'GET /api/settings') return json(route,{client_mtu:1280,wg_pool:'192.0.2.0/24',wg_server_address:'192.0.2.1'});
    if (key === 'GET /api/version') return json(route,{version:'dev'});
    if (key === 'GET /api/apply/status') return json(route,{dirty:false});
    if (key === 'GET /api/tunnels') return json(route, tunnels);
    if (url.pathname.endsWith('/raw')) return json(route, { editable: true, raw: syntheticRaw });
    if (key === 'POST /api/tunnels/parse') return json(route, { type: 'socks', host: 'proxy.example.invalid', port: 1080 });
    if (key === 'POST /api/tunnels/warp') { const created={...exampleTunnel,id:'warp',name:'Synthetic WARP',source:'warp'};tunnels.push(created);return json(route,created); }
    if (key === 'POST /api/tunnels') { const created={...exampleTunnel,id:'created',name:request.postDataJSON().name};tunnels.push(created);return json(route,created); }
    if (url.pathname.endsWith('/refresh')) return json(route, { pool: { servers_total: 5, servers_alive: 2 } });
    if (url.pathname.endsWith('/check')) return json(route, { status: 'up', latency_ms: 17, last_check: '2026-01-01T00:00:00Z' });
    if (url.pathname.endsWith('/pool')) return json(route, { servers: [], excluded: [] });
    if (request.method() === 'PATCH') { const tunnel=tunnels.find(t=>t.id===url.pathname.split('/')[3]);const patch=request.postDataJSON();if(tunnel)Object.assign(tunnel,{name:patch.name ?? tunnel.name,enabled:patch.enabled ?? tunnel.enabled});return json(route,tunnel); }
    if (request.method() === 'DELETE') { tunnels=tunnels.filter(t=>t.id!==url.pathname.split('/')[3]);return json(route,{ok:true}); }
    return json(route, { ok: true });
  });
  await page.goto(origin);
  if (options.application) {
    await page.locator('.client-row').first().waitFor();
    await page.evaluate(async()=>{window.h={shell:await import('/js/shell.js'),tunnels:await import('/js/screens/tunnels.js'),peers:await import('/js/screens/peers.js'),api:await import('/js/api.js')};await h.peers.load();});
  } else await page.waitForFunction(() => window.fixtureReady);
  const invoke = async (action, id) => page.evaluate(({ action, id }) => {
    const button = [...document.querySelectorAll('[data-act]')].find(button => button.dataset.act === action && (!id || button.dataset.id === id));
    const token = (window.actionSequence || 0) + 1;
    window.actionSequence = token;
    window.actionPromises ||= {};
    window.actionPromise = window.actionPromises[token] = Promise.resolve(h.tunnels.actions[action](id, button));
    return token;
  }, { action, id });
  const done = (token) => page.evaluate(token => token ? window.actionPromises[token] : window.actionPromise, token);
  const waitHeld = async (key, count = 1) => { for(let i=0;i<300&&(held.get(key)?.length||0)<count;i++) await new Promise(resolve=>setTimeout(resolve,10));assert((held.get(key)?.length||0)>=count,`Missing held ${key}`); };
  const reply = async (key, body = {}, status = 200, index = 0) => { await waitHeld(key,index+1);const routes=held.get(key),[route]=routes.splice(index,1);if(!routes.length)held.delete(key);await json(route,body,status); };
  const open = async (id = 't1') => { await invoke('edit-tunnel', id); await page.locator('#t-raw:not(:disabled)').waitFor();await page.locator('#t-parse.ok').waitFor(); };
  const replace = () => page.evaluate(() => h.shell.openModal(h.shell.modalShell('Replacement draft','<input id="replacement" value="Keep this draft">','')));
  const relogin = () => page.evaluate(async () => {h.shell.closeModal({restoreFocus:false});await h.api.session.logout();await h.api.session.login('synthetic');h.shell.state.tunnels=[{id:'new-session',name:'New session',enabled:true}];h.shell.state.dirty=false;document.querySelector('#apply-bar').hidden=true;document.querySelector('#toasts').replaceChildren();h.shell.refresh();});
  const state = () => page.evaluate(() => ({ tunnels:h.shell.state.tunnels,dirty:h.shell.state.dirty,modal:document.querySelector('#modal').textContent,toasts:document.querySelector('#toasts').textContent,open:!document.querySelector('#modal-backdrop').hidden }));
  return { page, context, held, holds, calls, overrides, errors, invoke, done, waitHeld, reply, open, replace, relogin, state, setTunnels:value=>{tunnels=structuredClone(value);} };
}
module.exports = { fixture, exampleTunnel, examplePool, syntheticRaw };
