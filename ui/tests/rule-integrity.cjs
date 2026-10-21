// Run with the pinned development-only dependencies: node ui/tests/rule-integrity.cjs
// Runs the committed modules in Chromium against disposable synthetic API responses.
const { chromium } = require('playwright');
const assert = require('node:assert/strict');
const http = require('node:http');
const fs = require('node:fs/promises');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist');
const fixture = `<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><link rel="stylesheet" href="/app.css"><div id="app"><main id="screen"></main><div id="apply-bar" hidden></div></div><div id="gate" hidden></div><div id="modal-backdrop" class="modal-backdrop" hidden><div id="modal" class="modal" role="dialog"></div></div><div id="toasts"></div><script type="module">
import * as shell from '/js/shell.js'; import * as rules from '/js/screens/rules.js'; import * as api from '/js/api.js';
window.h = {shell, rules, api};
document.addEventListener('click', e => { const b=e.target.closest('[data-act]'); if(!b)return; if(b.dataset.act==='close-modal')shell.closeModal(); else window.actionPending=rules.actions[b.dataset.act]?.(b.dataset.id); });
</script>`;
const rule = (extra = {}) => ({id:'r1',name:'Example rule',enabled:true,action:'tunnel',tunnel_id:'t1',via_tunnel_id:'',community_lists:['available','missing'],domains:['example.com'],subnets:[],remote_lists:[],peer_scope:'all',peer_ids:[],...extra});
const defaults = () => ({rules:[rule()],tunnels:[{id:'t0',name:'Disabled',enabled:false},{id:'t1',name:'Available',enabled:true}],peers:[{id:'p1',name:'Client one',enabled:true},{id:'p2',name:'Client two',enabled:true}],communityLists:[{key:'available',title:'Available source'}],dirty:false});
let server, browser, passed = 0;
async function run(name, test) {
  const context=await browser.newContext(); const page=await context.newPage();
  let records=[], held, fail=false, delay=false, listHeld, holdList=false;
  await context.route('**/*', route => new URL(route.request().url()).hostname === '127.0.0.1' ? route.continue() : route.abort());
  const errors=[]; page.on('pageerror', e=>errors.push(e.message));
  await page.route('**/api/**', async route => {
    const request=route.request(), method=request.method();
    if (request.url().endsWith('/api/rules') && method==='GET') {
      if(holdList) {listHeld=()=>route.fulfill({json:[rule({name:'Old list'})]}); return;}
      await route.fulfill({json:[rule({name:'Reloaded'})]}); return;
    }
    if(request.url().includes('/api/rules') && (method==='PATCH'||method==='POST')) {
      records.push({method,body:request.postDataJSON()});
      const finish=()=>route.fulfill(fail ? {status:422,json:{error:'Synthetic validation failure'}} : {json:rule()});
      if(delay) held=finish; else await finish(); return;
    }
    await route.fulfill({json:{ok:true}});
  });
  await page.goto(`http://127.0.0.1:${server.address().port}/`);
  await page.waitForFunction(()=>window.h);
  const setup=async(extra={})=>page.evaluate(data=>Object.assign(h.shell.state,data),{...defaults(),...extra});
  const edit=async()=>page.evaluate(()=>h.rules.actions['edit-rule']('r1'));
  const save=async()=>page.locator('[data-act="save-rule"]').click();
  const waitHeld=async(get)=>{for(let i=0;i<100;i++){if(get())return;await new Promise(resolve=>setTimeout(resolve,10));}throw new Error('Expected a pending synthetic request');};
  const waitRecord=()=>waitHeld(()=>held);
  try {
    await setup();
    await test({page,setup,edit,save,records,waitRecord,delay:()=>{delay=true;},fail:()=>{fail=true;},release:async()=>{assert(held,'request pending');await held();},holdList:()=>{holdList=true;},waitList:()=>waitHeld(()=>listHeld),resumeLists:()=>{holdList=false;},releaseList:async()=>{assert(listHeld);await listHeld();}});
    if (process.env.UI_SCREENSHOTS_DIR && await page.locator('#modal-backdrop').isVisible()) {
      await fs.mkdir(process.env.UI_SCREENSHOTS_DIR,{recursive:true});
      await page.screenshot({path:path.join(process.env.UI_SCREENSHOTS_DIR,name.replace(/[^a-z0-9]+/gi,'-')+'.png'),fullPage:true});
    }
    assert.deepEqual(errors,[]); console.log(`PASS ${name}`); passed++;
  } finally {await context.close();}
}
(async()=>{
 server=http.createServer(async(req,res)=>{try{const url=new URL(req.url,'http://localhost'); if(url.pathname==='/'){res.setHeader('Content-Type','text/html');res.end(fixture);return;} const file=path.resolve(root,'.'+url.pathname);if(!file.startsWith(root+path.sep)){res.writeHead(404).end();return;}res.setHeader('Content-Type',file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/plain');res.end(await fs.readFile(file));}catch{res.writeHead(404).end();}});
 await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve)); browser=await chromium.launch({headless:true});
 for(const catalog of [[],[{key:'available',title:'Available source'}]]) await run(`catalog ${catalog.length ? 'partial':'missing'} rename preserves all IDs`,async({page,setup,edit,save,records})=>{
   await setup({communityLists:catalog});await edit();assert.equal(await page.locator('input[name="list"]:checked').count(),2);
   await page.locator('#r-name').fill('Renamed');await save();await page.waitForFunction(()=>h.shell.state.dirty);
   assert.deepEqual(records[0].body.community_lists,['available','missing']);
 });
 await run('URL-only sources visible, inert and omitted from PATCH',async({page,setup,edit,save,records})=>{
   const url='https://example.invalid/<img-onerror=bad>.json';await setup({rules:[rule({community_lists:[],domains:[],remote_lists:[url]})]});await edit();
   assert((await page.locator('#modal').innerText()).includes(url));assert.equal(await page.locator('#modal img').count(),0);await save();await page.waitForFunction(()=>h.shell.state.dirty);assert(!('remote_lists' in records[0].body));
 });
 await run('new/empty/missing/disabled target never substituted',async({page,setup,edit,save,records})=>{
   await page.evaluate(()=>h.rules.actions['add-rule']());assert.equal(await page.locator('#r-tunnel').inputValue(),'');
   await page.locator('#r-name').fill('New');await page.locator('#r-domains').fill('example.com');await save();assert.equal(records.length,0);assert(await page.locator('#r-error').isVisible());
   for(const target of ['', 'absent', 't0']) {await setup({rules:[rule({tunnel_id:target})]});await edit();assert.equal(await page.locator('#r-tunnel').inputValue(),target);assert.match(await page.locator('#r-action-hint').innerText(),/blocked.*no direct fallback/);}
   await setup({tunnels:[{id:'t1',name:'Empty pool',enabled:true,pool:{servers_total:0,servers_alive:null}}]});await edit();assert.match(await page.locator('#r-action-hint').innerText(),/blocked/);
   await setup({tunnels:[]});await edit();assert.equal(await page.locator('#r-tunnel').inputValue(),'t1');
 });
 await run('disjoint, intersecting and whole-Internet scopes',async({page,setup,edit})=>{
   const upper=rule({id:'upper',peer_scope:'selected',peer_ids:['p1']});const mine=rule({peer_scope:'selected',peer_ids:['p2']});
   await setup({rules:[upper,mine]});await edit();assert(!(await page.locator('#r-overlap').isVisible()));
   await page.locator('input[name="peer"][value="p1"]').check();assert(await page.locator('#r-overlap').isVisible());
   await setup({rules:[upper,mine],peers:[{id:'p1',name:'Exit client',enabled:true,egress_tunnel_id:'t1'},{id:'p2',name:'Shared client',enabled:true}]});await edit();await page.locator('input[name="peer"][value="p1"]').check();assert(!(await page.locator('#r-overlap').isVisible()));
 });
 for(const replacement of [false,true])await run(`delayed save after ${replacement?'replacement':'close'} preserves pending state`,async({page,edit,save,delay,release,waitRecord})=>{
   await edit();delay();await save();await waitRecord();await page.evaluate(()=>h.shell.closeModal());if(replacement){await page.evaluate(()=>h.rules.actions['add-rule']());await page.locator('#r-name').fill('Replacement draft');}
   await release();await page.waitForFunction(()=>h.shell.state.dirty);await page.waitForFunction(()=>h.shell.state.rules[0].name==='Reloaded');
   if(replacement)assert.equal(await page.locator('#r-name').inputValue(),'Replacement draft');else assert(!(await page.locator('#modal-backdrop').isVisible()));
 });
 await run('delayed save from previous session cannot mark dirty or close draft',async({page,edit,save,delay,release,waitRecord})=>{
   await edit();delay();await save();await waitRecord();await page.evaluate(async()=>{h.shell.closeModal();await h.api.session.login('synthetic');h.rules.actions['add-rule']();});await page.locator('#r-name').fill('New session draft');await release();await page.evaluate(()=>window.actionPending);assert.equal(await page.locator('#r-name').inputValue(),'New session draft');assert.equal(await page.evaluate(()=>h.shell.state.dirty),false);
 });
 await run('failed stale save cannot write replacement error',async({page,edit,save,delay,fail,release,waitRecord})=>{
   await edit();delay();fail();await save();await waitRecord();await page.evaluate(()=>h.rules.actions['add-rule']());await page.locator('#r-name').fill('Replacement');await release();await page.evaluate(()=>window.actionPending);assert.equal(await page.locator('#r-name').inputValue(),'Replacement');assert(!(await page.locator('#r-error').isVisible()));
 });
 await run('validation and server failure stay actionable',async({page,edit,save,fail,records})=>{
   await edit();await page.locator('#r-domains').fill('https://bad.invalid/path');await save();assert.equal(records.length,0);assert.match(await page.locator('#r-error').innerText(),/Invalid domain lines/);
   await page.locator('#r-domains').fill('example.com');fail();await save();await page.waitForFunction(()=>!document.querySelector('[data-act="save-rule"]').disabled);assert.match(await page.locator('#r-error').innerText(),/Synthetic validation failure/);assert.equal(await page.locator('#r-domains').inputValue(),'example.com');
 });
 await run('disabled empty selected orphan remains disabled',async({page,setup,edit,save,records})=>{await setup({rules:[rule({enabled:false,peer_scope:'selected',peer_ids:[]})]});await edit();await save();await page.waitForFunction(()=>h.shell.state.dirty);assert.equal(records[0].body.peer_scope,'selected');assert.deepEqual(records[0].body.peer_ids,[]);assert(!('enabled' in records[0].body));});
 await run('old list completion cannot overwrite new session',async({page,holdList,releaseList,waitList})=>{
   holdList();await page.evaluate(()=>{window.oldLoad=h.rules.load();});await waitList();await page.evaluate(()=>{h.shell.state.rules=[{id:'new-session'}];return h.api.session.login('synthetic');});await releaseList();await page.evaluate(()=>window.oldLoad);assert.equal(await page.evaluate(()=>h.shell.state.rules[0].id),'new-session');
 });
 await run('newer inventory load wins in the same session',async({page,holdList,releaseList,waitList,resumeLists})=>{
   holdList();await page.evaluate(()=>{window.oldLoad=h.rules.load();});await waitList();resumeLists();await page.evaluate(()=>h.rules.load());await releaseList();await page.evaluate(()=>window.oldLoad);assert.equal(await page.evaluate(()=>h.shell.state.rules[0].name),'Reloaded');
 });
 await run('unavailable chain reference survives rename and explicit direct removes chain',async({page,setup,edit,save,records})=>{
   await setup({rules:[rule({via_tunnel_id:'missing-warp'})]});await edit();assert.equal(await page.locator('#r-via').inputValue(),'missing-warp');assert.match(await page.locator('#r-action-hint').innerText(),/blocked/);await save();await page.evaluate(()=>window.actionPending);assert.equal(records[0].body.via_tunnel_id,'missing-warp');
   await setup({rules:[rule({via_tunnel_id:'missing-warp'})]});await edit();await page.locator('input[name="action"][value="direct"]').check();await save();await page.evaluate(()=>window.actionPending);assert.equal(records[1].body.via_tunnel_id,'');assert.equal(records[1].body.tunnel_id,'');
 });
 console.log(`${passed} browser cases passed`);
})().catch(e=>{console.error(e);process.exitCode=1;}).finally(async()=>{await browser?.close();server?.close();});
