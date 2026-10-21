// Run: NODE_PATH=<installed Playwright modules> node ui/tests/settings-persistence.cjs
// Optional: SETTINGS_SCREENSHOTS=/absolute/temp/path saves clean responsive captures.
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs');
const path = require('node:path');
const { chromium } = require('playwright');
const root = path.resolve(__dirname, '../dist');
const initial = { wg_listen_port: 51820, endpoint_host: 'gateway.example.test', client_mtu: 1280, dns_upstream: '1.1.1.1', list_update_interval: 86400, pool_update_interval: 3600, tunnel_check_interval: 120, wg_pool: '10.8.0.0/24', dns_type: 'udp', pool_country_blocklist: ['RU', 'BY'] };
const harness = `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><link rel="stylesheet" href="/app.css"><title>Settings fixture</title><div id="gate" hidden></div><main id="app"><button id="open">Settings</button><div id="apply-bar" hidden>Changes pending Apply</div></main><div id="modal-backdrop" class="modal-backdrop" hidden><div id="modal" class="modal" role="dialog" aria-modal="true"></div></div><div id="toasts"></div><script type="module">import * as shell from '/js/shell.js'; import * as api from '/js/api.js'; import {modalSettings} from '/js/screens/settings.js'; shell.state.settings = ${JSON.stringify(initial)}; window.fixture={shell,api,open:modalSettings}; document.querySelector('#open').onclick=modalSettings; document.addEventListener('click',e=>{if(e.target.closest('[data-act="close-modal"]'))shell.closeModal()}); api.onUnauthorized(()=>shell.closeModal());</script></html>`;
function fixtureServer() {
  const writes = [];
  let settings = structuredClone(initial);
  let notify = { enabled: false, chat_id: '-1001234567890', token_set: true };
  let backup = { enabled: false, interval_hours: 24, passphrase_set: true, telegram_ready: true, last_sent_at: null, last_error: '' };
  let notifySecret='synthetic-old-token', backupSecret='synthetic-old-passphrase';
  const controls = { fail: '', hold: '', release: null };
  const server = http.createServer(async (req, res) => {
    const url = new URL(req.url, 'http://localhost');
    if (url.pathname.startsWith('/api/')) {
      let data = ''; for await (const chunk of req) data += chunk;
      const body = data ? JSON.parse(data) : null;
      if (req.method !== 'GET') writes.push({ path: url.pathname, method: req.method, body });
      // Match the production settings handler: read the full record before write.
      const snapshot = structuredClone(settings);
      const notifySnapshot = {...notify, secret:notifySecret};
      const backupSnapshot = {...backup, secret:backupSecret};
      if (controls.hold === url.pathname) { controls.hold = ''; await new Promise(resolve => controls.release = resolve); }
      res.setHeader('Content-Type', 'application/json');
      if (controls.fail === url.pathname) { controls.fail = ''; if(url.pathname==='/api/backup/send') backup.last_error='Synthetic request failure'; res.writeHead(500); res.end(JSON.stringify({error:'Synthetic request failure'})); return; }
      let result;
      if (url.pathname === '/api/settings') { if (body) settings = {...snapshot,...body}; result = settings; }
      else if (url.pathname === '/api/notify') { if (body) { notifySecret=body.token ?? notifySnapshot.secret; notify={enabled:body.enabled ?? notifySnapshot.enabled,chat_id:body.chat_id ?? notifySnapshot.chat_id,token_set:!!notifySecret}; } result = notify; }
      else if (url.pathname === '/api/backup') { if (body) { backupSecret=body.passphrase ?? backupSnapshot.secret; backup={...backup,enabled:body.enabled ?? backupSnapshot.enabled,interval_hours:body.interval_hours ?? backupSnapshot.interval_hours,passphrase_set:!!backupSecret}; } result = backup; }
      else { if(url.pathname==='/api/backup/send') backup={...backup,last_sent_at:'2026-10-21T12:00:00Z',last_error:''}; result = {ok:true}; }
      res.end(JSON.stringify(result)); return;
    }
    if (url.pathname === '/') { res.setHeader('Content-Type','text/html'); res.end(harness); return; }
    const file = path.resolve(root, '.' + url.pathname);
    if (!file.startsWith(root + path.sep) || !fs.existsSync(file)) { res.writeHead(404); res.end(); return; }
    res.setHeader('Content-Type', file.endsWith('.js') ? 'text/javascript' : 'text/css'); res.end(fs.readFileSync(file));
  });
  return { server, writes, controls, secrets:()=>({token:notifySecret,passphrase:backupSecret}) };
}
async function run() {
  const fixture = fixtureServer();
  await new Promise(resolve => fixture.server.listen(0, '127.0.0.1', resolve));
  const url = `http://127.0.0.1:${fixture.server.address().port}`;
  const browser = await chromium.launch({channel: process.env.PLAYWRIGHT_CHANNEL || undefined, headless:true});
  try {
    async function fixturePage(viewport) {
      const page = await browser.newPage({viewport});
      await page.route('**/*', route => new URL(route.request().url()).origin === url ? route.continue() : route.abort());
      return page;
    }
    const page = await fixturePage({width:1440,height:900});
    const errors=[]; page.on('pageerror',e=>errors.push(e.message));
    const open = async () => { await page.evaluate(()=>window.fixture.open()); await page.waitForFunction(()=>document.querySelector('#settings-backup fieldset')?.disabled === false); };
    const hold = endpoint => { fixture.controls.hold=endpoint; fixture.controls.release=null; };
    const waitHeld = async () => {
      for (let i=0;i<1000;i++) {
        if (fixture.controls.release) return;
        await new Promise(resolve=>setTimeout(resolve,5));
      }
      throw new Error('Held request was not received');
    };
    const nav = kind => page.locator(`[data-settings-section="${kind}"]`).click();
    const submit = kind => page.locator(`#settings-${kind} [type="submit"]`).click();
    const message = (kind,text) => page.waitForFunction(({kind,text})=>document.querySelector(`#settings-${kind} .settings-status`).textContent.includes(text),{kind,text});
    await page.goto(url); await page.waitForFunction(()=>window.fixture);
    // Prove this alternate origin is reachable without interception, then reject it
    // in the guarded page. The control loads only local CSS, never application code.
    const alternateOriginTarget = `http://localhost:${fixture.server.address().port}/app.css`;
    const control = await browser.newPage();
    try {
      await control.goto(`${url}/app.css`);
      assert.equal(await control.evaluate(async target => {
        try { await fetch(target, {mode:'no-cors'}); return true; } catch { return false; }
      }, alternateOriginTarget), true, 'The loopback alias must be reachable without the guard');
    } finally { await control.close(); }
    assert.equal(await page.evaluate(async target => {
      try { await fetch(target, {mode:'no-cors'}); return false; } catch { return true; }
    }, alternateOriginTarget), true, 'The exact-origin guard must refuse the reachable alias');
    await open();
    await page.locator('#s-mtu').fill('999'); await submit('network');
    await message('network','Client MTU: Enter a value of at least 1280.');
    assert.equal(fixture.writes.length,0); assert.equal(await page.locator('#s-mtu').getAttribute('aria-invalid'),'true');
    await page.locator('#s-mtu').fill('1300'); await nav('telegram'); await page.locator('#s-ntf-chat').fill('-100999');
    await nav('network'); assert.equal(await page.locator('#s-mtu').inputValue(),'1300');
    await submit('network'); await message('network','Network saved');
    assert.deepEqual(fixture.writes[0].body,{client_mtu:1300});
    assert.equal(fixture.writes.length,1);
    await nav('schedules'); await page.locator('#s-pool').selectOption('1800'); await submit('schedules'); await message('schedules','Schedules saved');
    assert.deepEqual(fixture.writes[1].body,{pool_update_interval:1800});
    assert.equal(await page.evaluate(()=>window.fixture.shell.state.settings.client_mtu),1300);
    assert.deepEqual(await page.evaluate(()=>window.fixture.shell.state.settings.pool_country_blocklist),['RU','BY']);
    await nav('telegram'); assert.equal(await page.locator('#s-ntf-chat').inputValue(),'-100999');
    assert(await page.locator('[data-delivery="telegram"]').isDisabled());
    fixture.controls.fail='/api/notify'; await submit('telegram'); await message('telegram','Save could not be confirmed');
    assert.equal(await page.locator('#s-ntf-chat').inputValue(),'-100999');
    await submit('telegram'); await message('telegram','Telegram saved');
    assert(!Object.hasOwn(fixture.writes.at(-1).body,'token'));
    const before=fixture.writes.length; await page.locator('[data-delivery="telegram"]').click(); await message('telegram','Test message sent');
    assert.equal(fixture.writes.length,before+1); assert.equal(fixture.writes.at(-1).path,'/api/notify/test');
    fixture.controls.fail='/api/notify/test'; await page.locator('[data-delivery="telegram"]').click(); await message('telegram','Delivery failed');
    await nav('backup'); await submit('backup'); await message('backup','Backup saved'); assert(!Object.hasOwn(fixture.writes.at(-1).body,'passphrase'));
    fixture.controls.fail='/api/backup'; await submit('backup'); await message('backup','Save could not be confirmed');
    await submit('backup'); await message('backup','Backup saved');
    await page.locator('[data-delivery="backup"]').click(); await message('backup','Encrypted backup sent'); await page.waitForFunction(()=>document.querySelector('#s-bak-last').textContent.startsWith('Last sent:')); assert.equal(fixture.writes.at(-1).path,'/api/backup/send');
    fixture.controls.fail='/api/backup/send'; await page.locator('[data-delivery="backup"]').click(); await message('backup','Delivery failed'); await page.waitForFunction(()=>document.querySelector('#s-bak-last').textContent.includes('Last delivery error: Synthetic request failure'));
    await nav('network'); fixture.controls.fail='/api/settings'; await submit('network'); await message('network','Save could not be confirmed');
    // Reopening reflects saved values and discards unsaved drafts only.
    await nav('telegram'); await page.locator('#s-ntf-chat').fill('unsaved-draft');
    await page.evaluate(()=>window.fixture.shell.closeModal()); await open();
    assert.equal(await page.locator('#s-mtu').inputValue(),'1300');
    await nav('schedules'); assert.equal(await page.locator('#s-pool').inputValue(),'1800');
    await nav('telegram'); assert.equal(await page.locator('#s-ntf-chat').inputValue(),'-100999');
    assert.equal(await page.locator('#s-ntf-token').inputValue(),'');
    await nav('backup'); assert.equal(await page.locator('#s-bak-int').inputValue(),'24');
    assert.equal(await page.locator('#s-bak-phrase').inputValue(),'');
    // Shared settings writes serialize across sections and replacement dialogs.
    await nav('network'); await page.locator('#s-mtu').fill('1320'); hold('/api/settings');
    await submit('network'); await waitHeld();
    const requestCount=fixture.writes.length;
    await nav('schedules'); await page.locator('#s-pool').selectOption('21600'); await submit('schedules');
    await message('schedules','Waiting to save this section');
    assert.equal(fixture.writes.length,requestCount,'A concurrent settings PATCH must remain queued');
    await page.evaluate(()=>window.fixture.shell.closeModal()); await open();
    await nav('network'); await page.locator('#s-port').fill('51821'); await submit('network'); await message('network','Waiting to save this section');
    assert.equal(fixture.writes.length,requestCount,'Replacement dialogs share the same settings queue');
    fixture.controls.release(); await message('network','Network saved');
    const persisted=await page.evaluate(()=>window.fixture.api.settings.get());
    assert.equal(persisted.client_mtu,1320); assert.equal(persisted.pool_update_interval,21600); assert.equal(persisted.wg_listen_port,51821);
    assert.equal(await page.locator('#s-mtu').inputValue(),'1320','Untouched values reflect the confirmed queued result');
    // A failed head request releases the queue for another section.
    await page.locator('#s-mtu').fill('1340'); hold('/api/settings'); fixture.controls.fail='/api/settings';
    await submit('network'); await waitHeld();
    await nav('schedules'); await page.locator('#s-pool').selectOption('43200'); await submit('schedules'); await message('schedules','Waiting to save this section');
    fixture.controls.release(); await message('schedules','Schedules saved'); await message('network','Save could not be confirmed');
    const afterFailure=await page.evaluate(()=>window.fixture.api.settings.get());
    assert.equal(afterFailure.client_mtu,1320); assert.equal(afterFailure.pool_update_interval,43200);
    // Pending secret rotation must survive an ordinary edit in a replacement form.
    await nav('telegram'); await page.locator('#s-ntf-token').fill('synthetic-rotated-token'); hold('/api/notify'); await submit('telegram'); await waitHeld();
    await open(); await nav('telegram'); await page.locator('#s-ntf-chat').fill('-100replacement');
    const notifyCount=fixture.writes.length; await submit('telegram'); await message('telegram','Waiting to save this section'); assert.equal(fixture.writes.length,notifyCount);
    fixture.controls.release(); await message('telegram','Telegram saved');
    assert.equal(fixture.secrets().token,'synthetic-rotated-token'); assert.deepEqual(fixture.writes.at(-1).body,{chat_id:'-100replacement'});
    await nav('backup'); await page.locator('#s-bak-phrase').fill('synthetic-rotated-passphrase'); hold('/api/backup'); await submit('backup'); await waitHeld();
    await open(); await nav('backup'); await page.locator('#s-bak-int').selectOption('6');
    const backupCount=fixture.writes.length; await submit('backup'); await message('backup','Waiting to save this section'); assert.equal(fixture.writes.length,backupCount);
    fixture.controls.release(); await message('backup','Backup saved');
    assert.equal(fixture.secrets().passphrase,'synthetic-rotated-passphrase'); assert.deepEqual(fixture.writes.at(-1).body,{interval_hours:6});
    // A pending response cannot write feedback into a replacement dialog.
    hold('/api/notify'); await nav('telegram'); await submit('telegram'); await page.waitForFunction(()=>document.querySelector('#settings-telegram').dataset.busy==='true');
    await waitHeld(); await open(); fixture.controls.release(); await page.waitForResponse(r=>r.url().endsWith('/api/notify') && r.request().method()==='PUT');
    assert(!await page.locator('#settings-telegram .settings-status').textContent().then(x=>x.includes('Telegram saved')));
    // A successful response after a session change must not mutate shared state.
    await nav('network'); await page.locator('#s-host').fill('old-session.example.test'); hold('/api/settings'); await submit('network');
    await page.waitForFunction(()=>document.querySelector('#settings-network').dataset.busy==='true');
    await waitHeld();
    const sessionRequestCount=fixture.writes.filter(write=>write.path==='/api/settings').length;
    await nav('schedules'); await page.locator('#s-pool').selectOption('1800'); await submit('schedules'); await message('schedules','Waiting to save this section');
    await nav('telegram'); await page.locator('#s-ntf-token').fill('synthetic-canceled-session-token'); await submit('telegram'); await message('telegram','Waiting to save this section');
    await page.evaluate(()=>{window.fixture.api.session.login('synthetic'); window.fixture.shell.state.settings.endpoint_host='new-session.example.test'; window.fixture.shell.closeModal();});
    fixture.controls.release(); await page.waitForResponse(r=>r.url().endsWith('/api/settings'));
    assert.equal(await page.evaluate(()=>window.fixture.shell.state.settings.endpoint_host),'new-session.example.test');
    await open(); await nav('network'); await page.locator('#s-dns').fill('8.8.8.8'); await submit('network'); await message('network','Network saved');
    assert.equal(fixture.writes.filter(write=>write.path==='/api/settings').length,sessionRequestCount+1,'Old-session queued settings intent must not dispatch');
    assert(!fixture.writes.some(write=>write.body?.token==='synthetic-canceled-session-token'),'Old-session queued secret rotation must not dispatch');

    // Loading failure is local and retryable; invalid short secrets never reach PUT.
    fixture.controls.fail='/api/notify'; await page.evaluate(()=>window.fixture.open());
    await nav('telegram'); await message('telegram','Could not load settings');
    assert(await page.locator('#s-ntf-chat').isDisabled());
    await page.getByRole('button',{name:'Retry loading'}).click(); await message('telegram','Saved settings loaded');
    await nav('backup'); await page.locator('#s-bak-phrase').fill('short');
    const prior=fixture.writes.length; await submit('backup'); await message('backup','at least 8 characters'); assert.equal(fixture.writes.length,prior);
    await page.locator('#s-bak-phrase').fill('synthetic-new-passphrase'); await submit('backup'); await message('backup','Backup saved');
    assert.equal(await page.locator('#s-bak-phrase').inputValue(),'');
    assert.equal(fixture.writes.at(-1).body.passphrase,'synthetic-new-passphrase');
    await nav('telegram'); await page.locator('#s-ntf-token').fill('synthetic-token'); await submit('telegram'); await message('telegram','Telegram saved');
    assert.equal(await page.locator('#s-ntf-token').inputValue(),'');
    // Late loading responses cannot refill a replacement modal.
    hold('/api/notify'); await page.evaluate(()=>window.fixture.open());
    await page.waitForFunction(()=>document.querySelector('#settings-telegram fieldset').disabled);
    await waitHeld();
    await page.evaluate(()=>window.fixture.shell.openModal(window.fixture.shell.modalShell('Replacement','<p>Replacement content</p>','')));
    fixture.controls.release(); await page.waitForResponse(r=>r.url().endsWith('/api/notify'));
    assert.equal(await page.locator('#modal h2').textContent(),'Replacement');
    assert.equal(await page.locator('#s-ntf-chat').count(),0);
    // Unsupported presets retain their valid saved current values.
    await page.evaluate(()=>{window.fixture.shell.state.settings.pool_update_interval=2700;window.fixture.shell.state.settings.tunnel_check_interval=30;});
    await open(); await nav('schedules');
    assert.equal(await page.locator('#s-pool').inputValue(),'2700'); assert.equal(await page.locator('#s-check').inputValue(),'30');
    // Native focus trapping keeps keyboard navigation in the dialog.
    await page.locator('#modal .modal-foot button').focus(); await page.keyboard.press('Tab');
    assert(await page.evaluate(()=>document.querySelector('#modal').contains(document.activeElement)));
    // Clean contexts prevent stress errors and drafts from entering visual evidence.
    for (const width of [1440,1024,375,320]) {
      const clean=await fixturePage({width,height:900}); await clean.goto(url); await clean.waitForFunction(()=>window.fixture); await clean.evaluate(()=>window.fixture.open());
      await clean.waitForFunction(()=>document.querySelector('#settings-backup fieldset')?.disabled===false);
      // Keyboard focus must stay geometrically clear of sticky dialog chrome.
      await clean.locator('[data-settings-section="schedules"]').click();
      await clean.locator('[data-settings-section="backup"]').focus();
      for (let i=0;i<4;i++) await clean.keyboard.press('Tab');
      await clean.waitForFunction(()=>{
        const target=document.activeElement;
        if (!target.matches('#settings-schedules [type="submit"]')) return false;
        const rect=target.getBoundingClientRect();
        return rect.top>=document.querySelector('.modal-head').getBoundingClientRect().bottom && rect.bottom<=document.querySelector('.modal-foot').getBoundingClientRect().top;
      });
      assert.equal(await clean.locator('.settings-section input,.settings-section select').evaluateAll(els=>els.filter(e=>!e.labels.length).length),0);
      for (const kind of ['network','schedules','telegram','backup']) {
        await clean.locator(`[data-settings-section="${kind}"]`).click();
        await clean.locator(`#settings-${kind} [type="submit"]`).scrollIntoViewIfNeeded();
        assert(await clean.locator(`#settings-${kind} [type="submit"]`).isVisible());
        assert(await clean.locator('#modal').evaluate(el=>el.scrollWidth<=el.clientWidth+1));
        if (process.env.SETTINGS_SCREENSHOTS) { fs.mkdirSync(process.env.SETTINGS_SCREENSHOTS,{recursive:true}); await clean.screenshot({path:path.join(process.env.SETTINGS_SCREENSHOTS,`clean-${width}-${kind}-bottom.png`)}); }
      }
      await clean.close();
    }
    assert.deepEqual(errors,[]); console.log('Settings persistence, delivery, lifetime and responsive checks passed.');
  } finally { fixture.controls.release?.(); await browser.close(); fixture.server.closeAllConnections(); await new Promise(resolve=>fixture.server.close(resolve)); }
}
if (require.main === module) run().catch(err=>{console.error(err);process.exitCode=1;});
module.exports = {fixtureServer};
