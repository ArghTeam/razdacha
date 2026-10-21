// Local synthetic client preview. Run directly; never connects to a daemon.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { root, examplePeer, exampleTunnels, config } = require('./client.fixture.cjs');
function startPreview(port = 0) {
  let peers = [structuredClone(examplePeer), { ...examplePeer, id: 'tablet', name: 'Office tablet', address: '192.0.2.3', egress_tunnel_id: 'exit' }, { ...examplePeer, id: 'backup', name: 'Backup device', address: '192.0.2.4', enabled: false, egress_tunnel_id: 'disabled' }];
  const server = http.createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    const json = (value, status = 200) => { response.writeHead(status, { 'Content-Type': 'application/json' }); response.end(JSON.stringify(value)); };
    if (url.pathname.startsWith('/api/')) {
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      const data = chunks.length ? JSON.parse(Buffer.concat(chunks)) : {};
      const id = url.pathname.split('/')[3];
      if (url.pathname === '/api/session') return json({ authenticated: true });
      if (url.pathname === '/api/login' || url.pathname === '/api/logout') return json({});
      if (url.pathname === '/api/peers' && request.method === 'POST') { const p = { ...examplePeer, id: `new-${peers.length}`, name: data.name, address: '192.0.2.5' }; peers.push(p); return json(p); }
      if (url.pathname === '/api/peers') return json(peers);
      if (url.pathname.endsWith('/config')) { response.writeHead(200, { 'Content-Type': 'text/plain', 'Content-Disposition': 'attachment; filename="example.conf"' }); return response.end(config); }
      if (url.pathname.startsWith('/api/peers/') && request.method === 'PATCH') { const p = peers.find(p => p.id === id); Object.assign(p, data); return json(p); }
      if (url.pathname.startsWith('/api/peers/') && request.method === 'DELETE') { peers = peers.filter(p => p.id !== id); return json({ inactivated_rule_ids: [] }); }
      if (url.pathname === '/api/tunnels') return json(exampleTunnels);
      if (url.pathname === '/api/rules' || url.pathname.includes('/lists/')) return json([]);
      if (url.pathname === '/api/settings') return json({ wg_pool: '192.0.2.0/24', client_mtu: 1280, wg_server_address: '192.0.2.1' });
      if (url.pathname === '/api/apply/status') return json({ dirty: false });
      if (url.pathname === '/api/version') return json({ version: 'synthetic-preview' });
      return json({});
    }
    const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) { response.writeHead(404); return response.end(); }
    response.writeHead(200, { 'Content-Type': file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' });
    let content = fs.readFileSync(file);
    if (file.endsWith('index.html')) content = content.toString().replace('<head>', '<head><script>sessionStorage.setItem("razdacha:latest-release",JSON.stringify({at:Date.now(),tag:null}))</script>');
    response.end(content);
  });
  return new Promise(resolve => server.listen(port, '127.0.0.1', () => resolve(server)));
}
module.exports = { startPreview };
if (require.main === module) startPreview(Number(process.env.PORT) || 0).then(server => console.log(`Synthetic client preview: http://127.0.0.1:${server.address().port}`));
