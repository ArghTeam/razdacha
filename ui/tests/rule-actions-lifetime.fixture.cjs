// Exact-origin synthetic fixture for actual rule inventory/probe modules or the app.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '../dist'), origin = 'http://127.0.0.1:43259';
const exampleRule = {id:'r1',name:'Example one',priority:0,enabled:true,action:'tunnel',tunnel_id:'t1',via_tunnel_id:'',community_lists:['source'],domains:['example.com'],subnets:[],remote_lists:['https://example.invalid/source.json'],resolve_real_ip:true,peer_scope:'all',peer_ids:[]};
const exampleRules = [exampleRule,{...exampleRule,id:'r2',name:'Example two',priority:1},{...exampleRule,id:'r3',name:'Example three',priority:2}];
const applicationHTML=fs.readFileSync(path.join(root,'index.html'),'utf8');
const html=applicationHTML.replace('<div id="app" hidden>','<div id="app">').replace('<script type="module" src="/js/app.js"></script>',`<script type="module">
 import * as rules from '/js/screens/rules.js';import * as shell from '/js/shell.js';import * as api from '/js/api.js';
 window.h={rules,shell,api};Object.assign(shell.state,window.fixtureState);shell.state.screen='rules';
 const render=()=>{if(shell.state.screen==='rules')document.querySelector('#screen').innerHTML=rules.view();};
 document.addEventListener('razdacha:render',render);api.onUnauthorized(()=>shell.closeModal({restoreFocus:false}));
 document.addEventListener('click',event=>{const b=event.target.closest('[data-act]');if(!b)return;if(b.dataset.act==='close-modal')shell.closeModal();else window.clickPromise=Promise.resolve(rules.actions[b.dataset.act]?.(b.dataset.id,b));});
 render();window.fixtureReady=true;
</script>`);
async function fixture(browser,options={}) {
 const context=await browser.newContext(),page=await context.newPage();
 const held=new Map(),holds=new Set(options.initialHolds||[]),overrides=new Map(),calls=[],unknown=[],external=[],errors=[];
 let rules=structuredClone(options.rules||exampleRules),peers=[{id:'p1',name:'Example client',address:'192.0.2.2',enabled:true}],nextId=0;
 const tunnels=[{id:'t1',name:'Example exit',enabled:true,type:'socks',source:'url'}];
 page.on('pageerror',error=>errors.push(error.message));
 await page.addInitScript(state=>{window.fixtureState=state;sessionStorage.setItem('razdacha:latest-release',JSON.stringify({at:Date.now(),tag:null}));},{rules,tunnels,peers,communityLists:[{key:'source',title:'Example source',group:'recommended',preset:true}],dirty:false});
 const json=(route,body,status=200)=>route.fulfill({status,contentType:'application/json',body:JSON.stringify(body)});
 const mutate=(key,body)=>{
   if(key==='POST /api/rules'){const rule={...body,id:`copy-${++nextId}`,priority:rules.length};rules.push(rule);return rule;}
   if(key==='PUT /api/rules/order'){const ids=body.ids;rules=ids.map(id=>rules.find(rule=>rule.id===id)).filter(Boolean);rules.forEach((rule,i)=>{rule.priority=i;});return {ok:true};}
   const id=key.split('/').at(-1);
   if(key.startsWith('PATCH /api/rules/')){const rule=rules.find(rule=>rule.id===id);Object.assign(rule,body);return rule;}
   if(key.startsWith('DELETE /api/rules/')){rules=rules.filter(rule=>rule.id!==id);return {ok:true};}
   if(key.startsWith('DELETE /api/peers/')){peers=peers.filter(peer=>peer.id!==id);return {affected_rule_ids:[],inactivated_rule_ids:[]};}
   return {ok:true};
 };
 await page.route('**/*',async route=>{
   const request=route.request(),url=new URL(request.url()),key=`${request.method()} ${url.pathname}`,body=request.postData()?request.postDataJSON():null;
   if(url.origin!==origin){external.push(url.origin+url.pathname);return route.abort();}
   if(!url.pathname.startsWith('/api/')){if(url.pathname==='/')return route.fulfill({contentType:'text/html',body:options.application?applicationHTML:html});const file=path.resolve(root,'.'+url.pathname);if(!file.startsWith(root+path.sep)||!fs.existsSync(file)){unknown.push(key);return route.abort();}return route.fulfill({contentType:file.endsWith('.js')?'text/javascript':file.endsWith('.css')?'text/css':'text/plain',body:fs.readFileSync(file)});}
   calls.push({key,body});
   if(holds.has(key)){if(!held.has(key))held.set(key,[]);held.get(key).push(route);return;}
   if(overrides.has(key)){const value=overrides.get(key);return json(route,value.body,value.status);}
   if(key==='GET /api/rules')return json(route,rules);
   if(key==='GET /api/tunnels')return json(route,tunnels);
   if(key==='GET /api/peers')return json(route,peers);
   if(key==='GET /api/session')return json(route,{authenticated:true});
   if(key==='POST /api/login'||key==='POST /api/logout')return json(route,{});
   if(key==='GET /api/settings')return json(route,{client_mtu:1280,wg_pool:'192.0.2.0/24',wg_server_address:'192.0.2.1'});
   if(key==='GET /api/apply/status')return json(route,{dirty:false});
   if(key==='GET /api/version')return json(route,{version:'dev'});
   if(key==='GET /api/diag')return json(route,{});
   if(key.startsWith('GET /api/lists/'))return json(route,[]);
   if(key==='POST /api/route/test')return json(route,{domain:body.domain,verdict:'Synthetic route result',addresses:['192.0.2.10'],fakeip:false,rule:null});
   if(key==='POST /api/domain/reachability')return json(route,{domain:body.domain,class:'reachable',status:200,verdict:'Synthetic direct result'});
   if(key==='POST /api/rules'||key==='PUT /api/rules/order'||key.startsWith('PATCH /api/rules/')||key.startsWith('DELETE /api/rules/')||key.startsWith('DELETE /api/peers/'))return json(route,mutate(key,body));
   unknown.push(key);return json(route,{error:'Unexpected synthetic request'},501);
 });
 await page.goto(origin);
 if(options.application){await page.locator('#app:not([hidden])').waitFor();await page.evaluate(async()=>{window.h={rules:await import('/js/screens/rules.js'),shell:await import('/js/shell.js'),api:await import('/js/api.js'),peers:await import('/js/screens/peers.js')};});if(!options.bootPending)await page.locator('.client-row').first().waitFor();}
 else await page.waitForFunction(()=>window.fixtureReady);
 const invoke=(action,id)=>page.evaluate(({action,id})=>{const b=[...document.querySelectorAll('[data-act]')].find(b=>b.dataset.act===action&&(!id||b.dataset.id===id));const token=(window.actionSequence||0)+1;window.actionSequence=token;window.actionPromises||={};window.actionPromise=window.actionPromises[token]=Promise.resolve(h.rules.actions[action](id,b));return token;},{action,id});
 const done=token=>page.evaluate(token=>token?window.actionPromises[token]:window.actionPromise,token);
 const waitHeld=async(key,count=1)=>{for(let i=0;i<300&&(held.get(key)?.length||0)<count;i++)await new Promise(resolve=>setTimeout(resolve,10));assert((held.get(key)?.length||0)>=count,`Missing held ${key}`);};
 const reply=async(key,body,status=200,index=0)=>{await waitHeld(key,index+1);const routes=held.get(key),[route]=routes.splice(index,1);if(!routes.length)held.delete(key);const result=status<300?mutate(key,route.request().postData()?route.request().postDataJSON():null):null;await json(route,body??result,status);};
 const relogin=()=>page.evaluate(async()=>{h.shell.closeModal({restoreFocus:false});await h.api.session.logout();await h.api.session.login('synthetic');h.shell.state.rules=[{id:'new-session',name:'New session',enabled:true,action:'direct',domains:['example.com']}];h.shell.state.dirty=false;document.querySelector('#apply-bar').hidden=true;document.querySelector('#toasts').replaceChildren();h.shell.refresh();});
 const replace=()=>page.evaluate(()=>h.shell.openModal(h.shell.modalShell('Replacement','<input id="replacement" value="Keep draft">','')));
 const state=()=>page.evaluate(()=>({rules:h.shell.state.rules,dirty:h.shell.state.dirty,toasts:document.querySelector('#toasts').textContent,screen:document.querySelector('#screen').textContent}));
 return {context,page,holds,held,overrides,calls,unknown,external,errors,invoke,done,waitHeld,reply,relogin,replace,state,setRules:value=>{rules=structuredClone(value);}};
}
module.exports={fixture,exampleRule,exampleRules};
