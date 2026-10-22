// Actual tunnel/shell/API modules with exact-origin intercepted synthetic responses.
const assert = require('node:assert/strict');
const { chromium } = require('playwright');
const { fixture, exampleTunnel, examplePool, syntheticRaw } = require('./tunnel-actions.fixture.cjs');
(async () => {
  const browser = await chromium.launch({ headless: true });
  let passed = 0;
  async function run(name, test, options) {
    const t = await fixture(browser, options);
    try { await test(t); assert.deepEqual(t.errors, []); passed++; console.log(`PASS ${name}`); }
    finally { await t.context.close(); }
  }
  const calls = (t, key) => t.calls.filter(call => call.key === key);
  const replaceInventory = (t) => t.page.evaluate(() => {
    h.shell.state.tunnels = h.shell.state.tunnels.map(tunnel => ({...tunnel,name:`Current ${tunnel.id}`}));
  });
  const openNew = async (t, name = 'Created example') => {
    await t.invoke('add-tunnel');await t.page.locator('#t-name').fill(name);await t.page.locator('#t-raw').fill(syntheticRaw);await t.page.locator('#t-parse.ok').waitFor();
  };
  const startRow = async (t, action, id) => {

    if(action==='refresh-pool-details'){await t.invoke('pool-details',id);await t.page.locator('#pool-details .parse-result').waitFor();}
    const token=await t.invoke(action,id);if(action==='delete-tunnel')await t.page.locator('[data-confirm-accept]').click();return token;
  };
  try {
    await run('whole-app delayed Clients inventory cannot undo an accepted Tunnels toggle',async t=>{
      await t.page.locator('[data-screen="rules"]').click();await t.page.waitForFunction(()=>h.shell.state.screen==='rules');
      t.holds.add('GET /api/tunnels');await t.page.locator('[data-screen="peers"]').click();await t.waitHeld('GET /api/tunnels');
      await t.page.locator('[data-screen="tunnels"]').click();await t.waitHeld('GET /api/tunnels',2);
      await t.reply('GET /api/tunnels',[exampleTunnel,examplePool],200,1);await t.page.locator('[data-act="menu-tunnel"][data-id="t1"]').click();
      await t.page.locator('[data-act="toggle-tunnel"][data-id="t1"]').click();await t.page.waitForFunction(()=>h.shell.state.tunnels.find(item=>item.id==='t1')?.enabled===false);
      await t.reply('GET /api/tunnels',[exampleTunnel,examplePool]);await t.page.waitForTimeout(100);
      assert.equal((await t.state()).tunnels.find(item=>item.id==='t1').enabled,false,'Delayed Clients GET replaced accepted tunnel state');assert.equal((await t.state()).dirty,true);
      assert(await t.page.locator('.row.dim').filter({hasText:'Example tunnel'}).isVisible());
    },{application:true});
    await run('pending create closes its original modal after actual hash navigation',async t=>{
      await t.page.locator('[data-screen="tunnels"]').click();await t.page.waitForFunction(()=>h.shell.state.screen==='tunnels');await openNew(t);t.holds.add('POST /api/tunnels');const token=await t.invoke('save-tunnel');await t.waitHeld('POST /api/tunnels');
      await t.page.evaluate(()=>{location.hash='rules';});await t.page.waitForFunction(()=>h.shell.state.screen==='rules');assert(await t.page.locator('#t-name').isVisible());
      t.setTunnels([exampleTunnel,examplePool,{...exampleTunnel,id:'created'}]);await t.reply('POST /api/tunnels',{...exampleTunnel,id:'created'});await t.done(token);assert.equal((await t.state()).open,false,'Confirmed create left a resubmittable original editor');assert.equal((await t.state()).dirty,true);assert.equal(calls(t,'POST /api/tunnels').length,1);
    },{application:true});
    await run('connected row button is restored after unrelated modal open and close',async t=>{
      const key='POST /api/tunnels/t1/check';t.holds.add(key);const token=await t.invoke('check-tunnel','t1');await t.waitHeld(key);await t.invoke('add-tunnel');await t.page.evaluate(()=>h.shell.closeModal());await t.reply(key,{error:'Obsolete check error'},500);await t.done(token);
      await t.page.locator('[data-act="menu-tunnel"][data-id="t1"]').click();assert.equal(await t.page.locator('#row-menu [data-act="check-tunnel"]').isEnabled(),true);await t.page.keyboard.press('Escape');assert.equal((await t.state()).toasts,'');
    });
    await run('shared startup read token rejects a snapshot after accepted mutation',async t=>{
      await t.page.evaluate(async()=>{window.startupRead=h.tunnels.beginInventoryRead();window.startupSnapshot=await h.api.tunnels.list();});await t.invoke('toggle-tunnel','t1');await t.done();const accepted=await t.page.evaluate(()=>window.startupRead.accept(window.startupSnapshot));assert.equal(accepted,false);assert.equal((await t.state()).tunnels.find(item=>item.id==='t1').enabled,false);
    });
    await run('obsolete Clients tunnel read error cannot mark newer metadata missing',async t=>{
      const key='GET /api/tunnels';t.holds.add(key);await t.page.evaluate(async()=>{const peers=await import('/js/screens/peers.js');h.shell.state.screen='peers';window.oldPeerLoad=peers.load();});await t.waitHeld(key);await t.page.evaluate(()=>{window.newTunnelLoad=h.tunnels.load();});await t.waitHeld(key,2);
      await t.reply(key,[{...exampleTunnel,name:'Newest tunnel inventory'}],200,1);await t.page.evaluate(()=>window.newTunnelLoad);await t.reply(key,{error:'Obsolete Clients metadata failure'},500);await t.page.evaluate(()=>window.oldPeerLoad);assert.equal(await t.page.evaluate(()=>h.shell.state.missing.has('peer-tunnels')),false);assert.equal((await t.state()).tunnels[0].name,'Newest tunnel inventory');
    });
    for(const outcome of ['success','failure']) await run(`delayed Save ${outcome} preserves a replacement draft`,async t=>{
      await t.open();t.holds.add('PATCH /api/tunnels/t1');await t.page.locator('#t-name').fill('Saved example');
      const token=await t.invoke('save-tunnel','t1');await t.waitHeld('PATCH /api/tunnels/t1');await t.replace();
      if(outcome==='success')t.setTunnels([{...exampleTunnel,name:'Saved example'},examplePool]);
      await t.reply('PATCH /api/tunnels/t1',outcome==='success'?{...exampleTunnel,name:'Saved example'}:{error:'Old save error'},outcome==='success'?200:422);await t.done(token);
      assert(await t.page.locator('#replacement').isVisible(),'A delayed Save closed the replacement editor');assert.equal(await t.page.locator('#replacement').inputValue(),'Keep this draft');
      const state=await t.state();assert.equal(state.dirty,outcome==='success');assert.equal(state.toasts,'');if(outcome==='success')assert.equal(state.tunnels.find(item=>item.id==='t1').name,'Saved example');
    });
    await run('same-session Save survives ordinary close and updates pending state',async t=>{
      await t.open();t.holds.add('PATCH /api/tunnels/t1');const token=await t.invoke('save-tunnel','t1');await t.waitHeld('PATCH /api/tunnels/t1');
      await t.page.evaluate(()=>h.shell.closeModal());await t.reply('PATCH /api/tunnels/t1',exampleTunnel);await t.done(token);assert.equal((await t.state()).dirty,true);assert.equal((await t.state()).open,false);
    });
    for(const status of [200,422])await run(`old-session Save ${status} cannot change state or start inventory reload`,async t=>{
      await t.open();t.holds.add('PATCH /api/tunnels/t1');const token=await t.invoke('save-tunnel','t1');await t.waitHeld('PATCH /api/tunnels/t1');await t.relogin();await t.replace();
      const before=calls(t,'GET /api/tunnels').length;await t.reply('PATCH /api/tunnels/t1',status===200?exampleTunnel:{error:'Old session failure'},status);await t.done(token);
      const state=await t.state();assert.deepEqual(state.tunnels.map(item=>item.id),['new-session']);assert.equal(state.dirty,false);assert.equal(state.toasts,'');assert(await t.page.locator('#replacement').isVisible());assert.equal(calls(t,'GET /api/tunnels').length,before);
    });
    await run('save failure retains draft and permits retry',async t=>{
      await t.open();await t.page.locator('#t-name').fill('Retained draft');t.overrides.set('PATCH /api/tunnels/t1',{status:422,body:{error:'Synthetic save failure'}});
      await t.invoke('save-tunnel','t1');await t.done();assert.equal(await t.page.locator('#t-name').inputValue(),'Retained draft');assert.equal(await t.page.locator('#t-raw').inputValue(),syntheticRaw);assert.equal(await t.page.locator('[data-act="save-tunnel"]').isEnabled(),true);assert.match((await t.state()).toasts,/Synthetic save failure/);assert.equal((await t.state()).dirty,false);
      t.overrides.delete('PATCH /api/tunnels/t1');await t.invoke('save-tunnel','t1');await t.done();assert.equal((await t.state()).dirty,true);
    });
    await run('create submit is single-flight across replacement and rerender',async t=>{
      await openNew(t);t.holds.add('POST /api/tunnels');const token=await t.invoke('save-tunnel');await t.waitHeld('POST /api/tunnels');await t.invoke('save-tunnel');
      await openNew(t,'Replacement creation draft');await t.invoke('save-tunnel');assert.equal(calls(t,'POST /api/tunnels').length,1);
      await t.reply('POST /api/tunnels',{...exampleTunnel,id:'created'});await t.done(token);assert.equal(await t.page.locator('#t-name').inputValue(),'Replacement creation draft');assert.equal((await t.state()).dirty,true);
    });
    await run('new-session create does not wait for previous-session create',async t=>{
      await openNew(t);t.holds.add('POST /api/tunnels');const old=await t.invoke('save-tunnel');await t.waitHeld('POST /api/tunnels');await t.relogin();await openNew(t,'Current session');const current=await t.invoke('save-tunnel');await t.waitHeld('POST /api/tunnels',2);
      t.setTunnels([{...exampleTunnel,id:'current',name:'Current session'}]);await t.reply('POST /api/tunnels',{...exampleTunnel,id:'current',name:'Current session'},200,1);await t.done(current);const before=calls(t,'GET /api/tunnels').length;
      await t.reply('POST /api/tunnels',{...exampleTunnel,id:'old'});await t.done(old);assert.deepEqual((await t.state()).tunnels.map(item=>item.id),['current']);assert.equal(calls(t,'GET /api/tunnels').length,before);
    });
    for(const kind of ['create','update','warp'])await run(`${kind} success remains confirmed when inventory reload fails`,async t=>{
      t.overrides.set('GET /api/tunnels',{status:500,body:{error:'Synthetic inventory read failure'}});
      if(kind==='create')await openNew(t);if(kind==='update'){await t.open();await t.page.locator('#t-name').fill('Confirmed rename');}
      await t.invoke(kind==='warp'?'add-warp':'save-tunnel',kind==='update'?'t1':undefined);await t.done();const state=await t.state();assert.equal(state.dirty,true);assert.match(state.toasts,/saved|added/);assert.match(state.toasts,/Inventory refresh failed/);assert.match(state.toasts,/instead of repeating/);
      if(kind==='update')assert.equal(state.tunnels.find(item=>item.id==='t1').name,'Confirmed rename');else assert(state.tunnels.some(item=>item.id===(kind==='warp'?'warp':'created')));
      assert(state.tunnels.every(item=>!Object.hasOwn(item,'raw')));if(kind==='warp')assert.equal(await t.page.locator('[data-act="add-warp"]').count(),0);
    });
    await run('WARP registration stays single-flight when its row rerenders',async t=>{
      t.holds.add('POST /api/tunnels/warp');const token=await t.invoke('add-warp');await t.waitHeld('POST /api/tunnels/warp');await t.page.evaluate(()=>h.shell.refresh());await t.invoke('add-warp');assert.equal(calls(t,'POST /api/tunnels/warp').length,1);
      t.setTunnels([exampleTunnel,examplePool,{...exampleTunnel,id:'warp',source:'warp'}]);await t.reply('POST /api/tunnels/warp',{...exampleTunnel,id:'warp',source:'warp'});await t.done(token);assert.equal((await t.state()).dirty,true);
    });
    const rows=[
      ['add-warp',undefined,'POST /api/tunnels/warp',{...exampleTunnel,id:'warp',source:'warp'}],
      ['toggle-tunnel','t1','PATCH /api/tunnels/t1',{id:'t1',enabled:false}],
      ['delete-tunnel','t1','DELETE /api/tunnels/t1',{ok:true}],
      ['refresh-pool','pool','POST /api/tunnels/pool/refresh',{pool:{servers_total:5,servers_alive:2}}],
      ['refresh-pool-details','pool','POST /api/tunnels/pool/refresh',{pool:{servers_total:5,servers_alive:2}}],
      ['check-tunnel','t1','POST /api/tunnels/t1/check',{status:'up',latency_ms:17}],
    ];
    for(const [action,id,key,result] of rows)for(const status of [200,500])await run(`${action} ${status} from an old session is silent and cannot dispatch follow-up`,async t=>{
      t.holds.add(key);const token=await startRow(t,action,id);await t.waitHeld(key);await t.relogin();await t.replace();const before=t.calls.length;
      await t.reply(key,status===200?result:{error:'Obsolete action failure'},status);await t.done(token);const state=await t.state();assert.deepEqual(state.tunnels.map(item=>item.id),['new-session']);assert.equal(state.dirty,false);assert.equal(state.toasts,'');assert.equal(t.calls.length,before);assert(await t.page.locator('#replacement').isVisible());
    });
    for(const [action,id,key,result] of rows.filter(([action])=>!['add-warp','refresh-pool-details'].includes(action)))await run(`${action} accepted after navigation updates current inventory identity without stale feedback`,async t=>{
      t.holds.add(key);const token=await startRow(t,action,id);await t.waitHeld(key);await replaceInventory(t);await t.page.evaluate(()=>{h.shell.state.screen='rules';document.querySelector('#screen').innerHTML='<h1>Current screen</h1>';});
      await t.reply(key,result);await t.done(token);const state=await t.state();assert.equal(state.toasts,'');
      if(action==='delete-tunnel')assert(!state.tunnels.some(item=>item.id===id));
      else {const tunnel=state.tunnels.find(item=>item.id===id);assert.equal(tunnel.name,`Current ${id}`);if(action==='toggle-tunnel')assert.equal(tunnel.enabled,false);if(action==='refresh-pool')assert.equal(tunnel.pool.servers_total,5);if(action==='check-tunnel')assert.equal(tunnel.status,'up');}
      assert.equal(state.dirty,['toggle-tunnel','delete-tunnel'].includes(action));
    });
    await run('row check is single-flight and releases its gate after error',async t=>{
      const key='POST /api/tunnels/t1/check';t.holds.add(key);const token=await t.invoke('check-tunnel','t1');await t.waitHeld(key);await t.invoke('check-tunnel','t1');assert.equal(calls(t,key).length,1);await t.reply(key,{error:'Synthetic check failure'},500);await t.done(token);await t.page.locator('[data-act="menu-tunnel"][data-id="t1"]').click();assert.equal(await t.page.locator('#row-menu [data-act="check-tunnel"]').isEnabled(),true);await t.page.keyboard.press('Escape');
      t.holds.delete(key);await t.invoke('check-tunnel','t1');await t.done();assert.equal(calls(t,key).length,2);assert.equal((await t.state()).tunnels.find(item=>item.id==='t1').status,'up');
    });
    await run('inventory coalesces unchanged loads and ignores a snapshot superseded by mutation',async t=>{
      const key='GET /api/tunnels';t.holds.add(key);await t.page.evaluate(()=>{window.oldLoad=h.tunnels.load();window.coalescedLoad=h.tunnels.load();});await t.waitHeld(key);assert.equal(calls(t,key).length,1);
      await t.invoke('toggle-tunnel','t1');await t.done();await t.page.evaluate(()=>{window.freshLoad=h.tunnels.load();});await t.waitHeld(key,2);await t.reply(key,[{...exampleTunnel,name:'Fresh inventory',enabled:false}],200,1);await t.page.evaluate(()=>window.freshLoad);
      await t.reply(key,[{...exampleTunnel,name:'Obsolete inventory',enabled:true}]);await t.page.evaluate(()=>Promise.all([window.oldLoad,window.coalescedLoad]));assert.equal((await t.state()).tunnels[0].name,'Fresh inventory');assert.equal((await t.state()).tunnels[0].enabled,false);
    });
    await run('old-session inventory read failure is ignored',async t=>{
      const key='GET /api/tunnels';t.holds.add(key);await t.page.evaluate(()=>{window.oldLoad=h.tunnels.load();});await t.waitHeld(key);await t.relogin();await t.reply(key,{error:'Old read failure'},500);await t.page.evaluate(()=>window.oldLoad);assert.deepEqual((await t.state()).tunnels.map(item=>item.id),['new-session']);assert.equal((await t.state()).toasts,'');
    });
    await run('pool refresh after replacement keeps durable data without requesting replacement metadata',async t=>{
      await t.invoke('pool-details','pool');await t.page.locator('#pool-details .parse-result').waitFor();const key='POST /api/tunnels/pool/refresh';t.holds.add(key);const token=await t.invoke('refresh-pool-details','pool');await t.waitHeld(key);await t.replace();const before=calls(t,'GET /api/tunnels/pool/pool').length;
      await t.reply(key,{pool:{servers_total:8,servers_alive:null}});await t.done(token);assert.equal(calls(t,'GET /api/tunnels/pool/pool').length,before);assert.equal((await t.state()).tunnels.find(item=>item.id==='pool').pool.servers_total,8);assert(await t.page.locator('#replacement').isVisible());assert.equal((await t.state()).toasts,'');
    });
    await run('confirmed pool refresh distinguishes metadata reload failure',async t=>{
      await t.invoke('pool-details','pool');await t.page.locator('#pool-details .parse-result').waitFor();t.overrides.set('GET /api/tunnels/pool/pool',{status:500,body:{error:'Synthetic metadata failure'}});
      await t.invoke('refresh-pool-details','pool');await t.done();assert.match((await t.state()).toasts,/Catalog refreshed, but server details could not be loaded/);assert.equal((await t.state()).tunnels.find(item=>item.id==='pool').pool.servers_total,5);assert.equal(await t.page.locator('[data-act="refresh-pool-details"]').isEnabled(),true);
    });
    await run('newer pool metadata wins over the initial read',async t=>{
      const key='GET /api/tunnels/pool/pool';t.holds.add(key);await t.invoke('pool-details','pool');await t.waitHeld(key);const token=await t.invoke('refresh-pool-details','pool');await t.waitHeld(key,2);
      await t.reply(key,{servers:[{title:'Fresh server',in_rotation:true,alive:true}]},200,1);await t.done(token);await t.reply(key,{servers:[{title:'Obsolete server',in_rotation:true,alive:true}]});await t.page.waitForTimeout(30);assert.match(await t.page.locator('#pool-details').innerText(),/Fresh server/);assert.doesNotMatch(await t.page.locator('#pool-details').innerText(),/Obsolete server/);
    });
    for(const status of [200,500])await run(`raw read ${status} after replacement cannot refill a modal or start parse`,async t=>{
      const key='GET /api/tunnels/t1/raw';t.holds.add(key);await t.invoke('edit-tunnel','t1');await t.waitHeld(key);await t.replace();await t.reply(key,status===200?{raw:syntheticRaw,editable:true}:{error:'Old raw failure'},status);await t.page.waitForTimeout(30);assert(await t.page.locator('#replacement').isVisible());assert.equal(calls(t,'POST /api/tunnels/parse').length,0);assert.equal((await t.state()).toasts,'');
    });
    await run('raw read from a previous session never starts parse',async t=>{
      const key='GET /api/tunnels/t1/raw';t.holds.add(key);await t.invoke('edit-tunnel','t1');await t.waitHeld(key);await t.relogin();await t.reply(key,{raw:syntheticRaw,editable:true});await t.page.waitForTimeout(30);assert.equal(calls(t,'POST /api/tunnels/parse').length,0);assert.equal((await t.state()).open,false);
    });
    for(const newestValid of [true,false])await run(`parser order preserves the newest ${newestValid?'valid':'invalid'} input`,async t=>{
      await t.invoke('add-tunnel');await t.page.locator('#t-name').fill('Parser example');const key='POST /api/tunnels/parse';t.holds.add(key);await t.page.locator('#t-raw').fill('socks5://old.example.invalid:1080');await t.waitHeld(key);
      const current='socks5://new.example.invalid:1080';await t.page.locator('#t-raw').fill(current);await t.waitHeld(key,2);await t.reply(key,newestValid?{type:'socks',host:'new.example.invalid'}:{error:'Newest invalid input'},newestValid?200:422,1);
      await t.reply(key,newestValid?{error:'Obsolete parser failure'}:{type:'socks',host:'old.example.invalid'},newestValid?422:200);await t.page.waitForTimeout(30);
      assert.equal(await t.page.locator('#t-raw').inputValue(),current);assert.match(await t.page.locator('#t-parse').innerText(),newestValid?/new.example.invalid/:/Newest invalid input/);
      await t.invoke('save-tunnel');await t.done();assert.equal(calls(t,'POST /api/tunnels').length,newestValid?1:0);
    });
    await run('closing clears raw copies and cancels pending debounce',async t=>{
      await t.invoke('add-tunnel');await t.page.locator('#t-raw').fill(syntheticRaw);await t.replace();await t.page.waitForTimeout(400);assert.equal(calls(t,'POST /api/tunnels/parse').length,0);assert.equal(await t.page.locator('#modal textarea').count(),0);assert.doesNotMatch((await t.state()).modal,/proxy.example.invalid/);
    });
    await run('session invalidation cancels debounce even before modal cleanup',async t=>{
      await t.invoke('add-tunnel');await t.page.locator('#t-raw').fill(syntheticRaw);await t.page.evaluate(()=>h.api.session.login('synthetic'));await t.page.waitForTimeout(400);assert.equal(calls(t,'POST /api/tunnels/parse').length,0);
    });
    await run('server-owned WARP configuration stays absent from rename PATCH',async t=>{
      t.overrides.set('GET /api/tunnels/t1/raw',{status:200,body:{editable:false,raw:'',note:'Server-managed configuration'}});await t.invoke('edit-tunnel','t1');await t.page.locator('#t-raw').waitFor({state:'detached'});await t.page.locator('#t-name').fill('Renamed managed tunnel');await t.invoke('save-tunnel','t1');await t.done();assert.deepEqual(calls(t,'PATCH /api/tunnels/t1')[0].body,{name:'Renamed managed tunnel'});
    });
    console.log(`${passed} tunnel action/lifetime Chromium cases passed`);
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});
