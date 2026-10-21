// Disposable loopback preview using the same allowlisted synthetic model as browser tests.
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { createAppModel, root } = require('./app.fixture.cjs');
async function startPreview(port = 0, overrides = {}) {
  const model = createAppModel(overrides);
  const server = http.createServer(async (request, response) => {
    const url = new URL(request.url, 'http://127.0.0.1');
    if (url.pathname.startsWith('/api/')) {
      const chunks = []; for await (const chunk of request) chunks.push(chunk);
      let body;
      try { body = chunks.length ? JSON.parse(Buffer.concat(chunks)) : null; }
      catch { response.writeHead(400); response.end(); return; }
      const reply = model.reply({ key: `${request.method} ${url.pathname}${url.search}`, method: request.method, pathname: url.pathname, url: url.href, body });
      if (!reply) { response.writeHead(501, { 'Content-Type': 'application/json' }); response.end(JSON.stringify({ error: 'This synthetic preview does not model this operation.' })); return; }
      response.writeHead(reply.status || 200, { 'Content-Type': reply.contentType, ...reply.headers }); response.end(reply.body); return;
    }
    const file = path.resolve(root, '.' + (url.pathname === '/' ? '/index.html' : url.pathname));
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) { response.writeHead(404); response.end(); return; }
    response.writeHead(200, { 'Content-Type': file.endsWith('.js') ? 'text/javascript' : file.endsWith('.css') ? 'text/css' : 'text/html' });
    let content = fs.readFileSync(file);
    if (file.endsWith('index.html')) content = content.toString().replace('<head>', '<head><script>sessionStorage.setItem("razdacha:latest-release",JSON.stringify({at:Date.now(),tag:null}))</script>');
    response.end(content);
  });
  await new Promise(resolve => server.listen(port, '127.0.0.1', resolve));
  return { server, model };
}
module.exports = { startPreview };
if (require.main === module) startPreview(Number(process.env.PORT) || 0).then(({ server }) => console.log(`Synthetic shell/diagnostics preview: http://127.0.0.1:${server.address().port}`));
