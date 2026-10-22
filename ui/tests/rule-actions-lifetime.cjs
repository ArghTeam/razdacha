const assert=require('node:assert/strict');
const {chromium}=require('playwright');
const {fixture,exampleRule,exampleRules}=require('./rule-actions-lifetime.fixture.cjs');
(async()=>{
 const browser=await chromium.launch({headless:true});let passed=0;
 const count=(t,key)=>t.calls.filter(call=>call.key===key).length;
 const ids=async t=>(await t.state()).rules.map(rule=>rule.id);
 async function run(name,test,options){const t=await fixture(browser,options);try{await test(t);assert.deepEqual(t.errors,[]);assert.deepEqual(t.unknown,[],'Unknown requests');assert.deepEqual(t.external,[],'External requests');passed++;console.log(`PASS ${name}`);}finally{await t.context.close();}}
 const start=async(t,action,id='r1')=>{const token=await t.invoke(action,id);if(action==='delete-rule')await t.page.locator('[data-confirm-accept]').click();return token;};
 const routeKey='POST /api/route/test',reachKey='POST /api/domain/reachability';
 const diagnostic=async(t,action='probe-run',domain='example.invalid')=>{await t.page.locator('#probe-domain').fill(domain);return t.invoke(action);};
 try{
  await run('reorder is single-flight, publishes only accepted order and retries after failure',async t=>{
   const key='PUT /api/rules/order';t.holds.add(key);const token=await t.invoke('rule-down','r1');await t.waitHeld(key);await t.invoke('rule-down','r1');assert.equal(count(t,key),1);assert.deepEqual(await ids(t),['r1','r2','r3']);
   await t.reply(key,{error:'Synthetic reorder failure'},500);await t.done(token);assert.deepEqual(await ids(t),['r1','r2','r3']);const retry=await t.invoke('rule-down','r1');await t.waitHeld(key);await t.reply(key);await t.done(retry);assert.deepEqual(await ids(t),['r2','r1','r3']);assert.equal((await t.state()).dirty,true);
  });
  for(const action of ['delete-rule','toggle-rule','duplicate-rule'])for(const status of [200,500])await run(`reorder ${status} retains concurrently accepted ${action} state`,async t=>{
   const key='PUT /api/rules/order';t.holds.add(key);const token=await t.invoke('rule-down','r1');await t.waitHeld(key);await start(t,action,'r2');await t.done();await t.reply(key,status===200?undefined:{error:'Late reorder failure'},status);await t.done(token);
   const state=await t.state();if(action==='delete-rule')assert(!state.rules.some(rule=>rule.id==='r2'));if(action==='toggle-rule')assert.equal(state.rules.find(rule=>rule.id==='r2').enabled,false);if(action==='duplicate-rule')assert(state.rules.some(rule=>rule.id==='copy-1'));assert.equal(state.dirty,true);
  });
  const actions=[['toggle-rule','PATCH /api/rules/r1'],['duplicate-rule','POST /api/rules'],['delete-rule','DELETE /api/rules/r1'],['rule-down','PUT /api/rules/order']];
  for(const [action,key] of actions)for(const status of [200,500])await run(`${action} old-session ${status} cannot publish state or follow-up reads`,async t=>{
   t.holds.add(key);const token=await start(t,action);await t.waitHeld(key);await t.relogin();await t.replace();const before=t.calls.length;await t.reply(key,status===200?undefined:{error:'Obsolete failure'},status);await t.done(token);const state=await t.state();assert.deepEqual(state.rules.map(rule=>rule.id),['new-session']);assert.equal(state.dirty,false);assert.equal(state.toasts,'');assert.equal(t.calls.length,before);assert(await t.page.locator('#replacement').isVisible());
  });
  for(const [action,key] of actions)await run(`${action} keeps same-session accepted effects after navigation/replacement`,async t=>{
   t.holds.add(key);const token=await start(t,action);await t.waitHeld(key);await t.page.evaluate(()=>{h.shell.state.rules=h.shell.state.rules.map(rule=>({...rule,name:`Current ${rule.id}`}));h.shell.state.screen='peers';document.querySelector('#screen').innerHTML='<h1>Current screen</h1>';});await t.replace();await t.reply(key);await t.done(token);const state=await t.state();assert.equal(state.dirty,true);assert.equal(state.toasts,'');assert(await t.page.locator('#replacement').isVisible());if(action==='delete-rule')assert(!state.rules.some(rule=>rule.id==='r1'));if(action==='toggle-rule'){assert.equal(state.rules.find(rule=>rule.id==='r1').enabled,false);assert.equal(state.rules.find(rule=>rule.id==='r1').name,'Current r1');}if(action==='rule-down')assert.deepEqual(state.rules.map(rule=>rule.id),['r2','r1','r3']);if(action==='duplicate-rule')assert(state.rules.some(rule=>rule.id==='copy-1'));
  });
  await run('URL-only duplicate retains remote sources and DNS flags even after read failure',async t=>{
   const urlOnly={...exampleRule,community_lists:[],domains:[],remote_lists:['https://example.invalid/native.json'],resolve_real_ip:true};t.setRules([urlOnly]);await t.page.evaluate(rule=>{h.shell.state.rules=[rule];h.shell.refresh();},urlOnly);t.overrides.set('GET /api/rules',{status:500,body:{error:'Synthetic inventory read failure'}});await t.invoke('duplicate-rule','r1');await t.done();const post=t.calls.find(call=>call.key==='POST /api/rules').body;assert.deepEqual(post.remote_lists,urlOnly.remote_lists);assert.equal(post.resolve_real_ip,true);assert(!('id'in post));assert(!('priority'in post));const state=await t.state();assert(state.rules.some(rule=>rule.id==='copy-1'));assert.equal(state.dirty,true);assert.match(state.toasts,/copy was saved.*instead of duplicating/s);
  });
  await run('same-target duplicate/toggle/delete gate survives rerender and releases after failure',async t=>{
   t.holds.add('POST /api/rules');const token=await t.invoke('duplicate-rule','r1');await t.waitHeld('POST /api/rules');await t.page.evaluate(()=>h.shell.refresh());await t.invoke('duplicate-rule','r1');await t.invoke('toggle-rule','r1');await start(t,'delete-rule','r1');assert.equal(count(t,'POST /api/rules'),1);assert.equal(count(t,'PATCH /api/rules/r1'),0);assert.equal(count(t,'DELETE /api/rules/r1'),0);await t.reply('POST /api/rules',{error:'Synthetic duplicate failure'},500);await t.done(token);await t.invoke('toggle-rule','r1');await t.done();assert.equal(count(t,'PATCH /api/rules/r1'),1);
  });
  await run('accepted toggle invalidates an older screen inventory response',async t=>{
   const key='GET /api/rules';t.holds.add(key);await t.page.evaluate(()=>{window.oldRead=h.rules.load();});await t.waitHeld(key);await t.invoke('toggle-rule','r1');await t.done();await t.reply(key,exampleRules);await t.page.evaluate(()=>window.oldRead);assert.equal((await t.state()).rules.find(rule=>rule.id==='r1').enabled,false);
  });
  await run('newest read wins and obsolete read failure is ignored',async t=>{
   const key='GET /api/rules';t.holds.add(key);await t.page.evaluate(()=>{window.oldRead=h.rules.load();window.newRead=h.rules.load();});await t.waitHeld(key,2);await t.reply(key,[{...exampleRule,name:'Fresh inventory'}],200,1);await t.page.evaluate(()=>window.newRead);await t.reply(key,{error:'Obsolete read failure'},500);await t.page.evaluate(()=>window.oldRead);assert.equal((await t.state()).rules[0].name,'Fresh inventory');assert.equal((await t.state()).toasts,'');
  });
  await run('shared startup token rejects an already downloaded stale snapshot',async t=>{
   await t.page.evaluate(async()=>{window.startupRead=h.rules.beginInventoryRead();window.startupRules=await h.api.rules.list();});await t.invoke('toggle-rule','r1');await t.done();assert.equal(await t.page.evaluate(()=>window.startupRead.accept(window.startupRules)),false);assert.equal((await t.state()).rules.find(rule=>rule.id==='r1').enabled,false);
  });
  await run('editor follow-up snapshot cannot overwrite a later accepted inventory action',async t=>{
   await t.invoke('edit-rule','r1');t.holds.add('GET /api/rules');const save=await t.invoke('save-rule','r1');await t.waitHeld('GET /api/rules');await t.invoke('toggle-rule','r2');await t.done();await t.reply('GET /api/rules',exampleRules);await t.done(save);assert.equal((await t.state()).rules.find(rule=>rule.id==='r2').enabled,false);
  });
  for(const status of [200,500])await run(`actual Clients deletion follow-up ${status} cannot replace a later Rules mutation`,async t=>{
   await t.page.locator('[data-act="menu-peer"]').first().click();await t.page.locator('#row-menu [data-act="peer-details"]').click();await t.page.locator('#client-name').waitFor();await t.page.locator('[data-act="inspect-client-delete"]').click();await t.page.locator('#client-delete-confirmation').waitFor({state:'visible'});
   t.holds.add('GET /api/rules');await t.page.locator('[data-act="confirm-client-delete"]').click();await t.waitHeld('GET /api/rules');await t.page.evaluate(()=>h.shell.closeModal());await t.page.locator('[data-screen="rules"]').click();await t.waitHeld('GET /api/rules',2);await t.reply('GET /api/rules',exampleRules,200,1);await t.page.locator('[data-act="menu-rule"][data-id="r1"]').click();await t.page.locator('[data-act="toggle-rule"][data-id="r1"]').click();await t.page.waitForFunction(()=>h.shell.state.rules.find(rule=>rule.id==='r1')?.enabled===false);
   await t.reply('GET /api/rules',status===200?exampleRules:{error:'Obsolete deletion read failure'},status);await t.page.waitForTimeout(80);assert.equal((await t.state()).rules.find(rule=>rule.id==='r1').enabled,false);assert.doesNotMatch((await t.state()).toasts,/Reload rules/);
  },{application:true});
  for(const [action,key] of [['probe-run',routeKey],['reach-run',reachKey]])for(const status of [200,500])await run(`${action} old-session ${status} cannot render or clear a new busy request`,async t=>{
   t.holds.add(key);const old=await diagnostic(t,action,'old.example.invalid');await t.waitHeld(key);await t.relogin();const current=await diagnostic(t,action,'new.example.invalid');await t.waitHeld(key,2);await t.reply(key,status===200?{domain:'old.example.invalid',verdict:'Obsolete result',addresses:[]}:{error:'Obsolete probe failure'},status);await t.done(old);assert.equal(await t.page.locator('[data-act="probe-run"]').isDisabled(),true);assert.equal(await t.page.locator('[data-act="reach-run"]').isDisabled(),true);assert.doesNotMatch((await t.state()).screen,/Obsolete/);
   await t.reply(key,{domain:'new.example.invalid',verdict:'Current result',addresses:[],class:'reachable'});await t.done(current);assert.match((await t.state()).screen,/Current result/);assert.equal(await t.page.locator('#probe-domain').inputValue(),'new.example.invalid');assert.equal(await t.page.locator('[data-act="probe-run"]').isEnabled(),true);
  });
  for(const [action,key] of [['probe-run',routeKey],['reach-run',reachKey]])for(const status of [200,500])await run(`${action} completed ${status} output and domain clear on a new-session view`,async t=>{
   t.overrides.set(key,{status,body:status===200?{domain:'old.example.invalid',verdict:'Previous session output',addresses:[],class:'reachable'}:{error:'Previous session output'}});await diagnostic(t,action,'old.example.invalid');await t.done();assert.match((await t.state()).screen,/Previous session output/);await t.relogin();assert.equal(await t.page.locator('#probe-domain').inputValue(),'');assert.doesNotMatch((await t.state()).screen,/Previous session output|old.example.invalid/);assert.equal(await t.page.locator('[data-act="probe-run"]').isEnabled(),true);
  });
  await run('probe action and Enter paths share one in-flight gate',async t=>{
   await t.page.locator('[data-screen="rules"]').click();await t.page.waitForFunction(()=>h.shell.state.screen==='rules');
   t.holds.add(routeKey);const token=await diagnostic(t);await t.waitHeld(routeKey);await t.invoke('probe-run');await t.invoke('reach-run');await t.page.locator('#probe-domain').press('Enter');assert.equal(count(t,routeKey),1);assert.equal(count(t,reachKey),0);await t.reply(routeKey,{domain:'example.invalid',verdict:'Accepted probe',addresses:[]});await t.done(token);
  },{application:true});
  await run('pending probe preserves a newly typed domain draft and labels errors',async t=>{
   t.holds.add(routeKey);const token=await diagnostic(t,'probe-run','first.example.invalid');await t.waitHeld(routeKey);await t.page.locator('#probe-domain').fill('draft.example.invalid');await t.reply(routeKey,{error:'Synthetic routing failure'},503);await t.done(token);assert.equal(await t.page.locator('#probe-domain').inputValue(),'draft.example.invalid');assert.match((await t.state()).screen,/Shared-rule route check.*Synthetic routing failure/s);
   t.holds.delete(routeKey);t.overrides.set(reachKey,{status:503,body:{error:'Synthetic direct failure'}});await t.invoke('reach-run');await t.done();assert.match((await t.state()).screen,/Direct reachability.*Synthetic direct failure/s);
  });
  await run('hash navigation abandons a pending probe and allows a fresh request',async t=>{
   t.holds.add(routeKey);const old=await diagnostic(t,'probe-run','old.example.invalid');await t.waitHeld(routeKey);await t.page.evaluate(()=>{location.hash='peers';h.shell.state.screen='peers';});await t.page.waitForTimeout(20);await t.page.evaluate(()=>{location.hash='rules';h.shell.state.screen='rules';h.shell.refresh();});await t.page.waitForTimeout(20);const current=await diagnostic(t,'probe-run','current.example.invalid');await t.waitHeld(routeKey,2);await t.reply(routeKey,{domain:'old.example.invalid',verdict:'Obsolete navigation result',addresses:[]});await t.done(old);assert.doesNotMatch((await t.state()).screen,/Obsolete navigation/);assert(await t.page.locator('[data-act="probe-run"]').isDisabled());await t.reply(routeKey,{domain:'current.example.invalid',verdict:'Current navigation result',addresses:[]});await t.done(current);assert.match((await t.state()).screen,/Current navigation result/);
  });
  console.log(`${passed} rule inventory/probe lifetime Chromium cases passed`);
 }finally{await browser.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
