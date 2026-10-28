const path=require('node:path');

module.exports=async({page,out,assert})=>{
 const open=async name=>{await page.evaluate(name=>location.hash=name,name);await page.locator('#page-'+name).waitFor({state:'visible'});};
 await open('clients');await page.evaluate(()=>refresh());
 await page.evaluate(()=>{window.savedRows=[...document.querySelectorAll('#clients-body tr')];window.removedRows=0;window.rowObserver=new MutationObserver(records=>{for(const record of records)window.removedRows+=record.removedNodes.length;});window.rowObserver.observe(document.getElementById('clients-body'),{childList:true});});
 await page.evaluate(()=>refresh());
 assert.equal(await page.evaluate(()=>savedRows.every((row,i)=>row===document.querySelectorAll('#clients-body tr')[i])&&removedRows===0),true,'polling rebuilt account rows');
 const reads=[];const track=request=>{if(/\/api\/(clients|dashboard)$/.test(request.url()))reads.push(request.url());};
 page.on('request',track);
 await page.evaluate(()=>Promise.all([api('/clients'),api('/clients'),api('/clients')]));
 assert.equal(reads.filter(url=>url.endsWith('/api/clients')).length,1,'simultaneous identical GETs were not coalesced');
 await open('settings');await page.waitForFunction(()=>!document.getElementById('page-settings').hasAttribute('aria-busy'));reads.length=0;
 await page.evaluate(()=>refresh({background:true}));
 assert.equal(reads.length,0,'inactive dashboard/table fetched during background settings refresh');
 await page.route('**/api/session',route=>route.abort('failed'));
 await page.evaluate(()=>refresh({background:true}));
 assert.equal(await page.locator('#sync-state').evaluate(el=>el.classList.contains('stale')),true,'idle forms incorrectly report a fresh sync without checking the server');
 await page.unroute('**/api/session');await page.evaluate(()=>refresh({background:true}));
 assert.equal(await page.locator('#sync-state').evaluate(el=>el.classList.contains('stale')),false,'connection did not recover after a successful lightweight heartbeat');
 assert.equal(await page.locator('#service-chip').getAttribute('data-live'),'true','connection chip remained offline after recovery');
 assert.equal(await page.evaluate(()=>removedRows),0,'hidden table was rebuilt');page.off('request',track);
 await open('dashboard');await page.evaluate(()=>refresh());const before=await page.locator('#stat-accounts').textContent();
 await page.route('**/api/dashboard',async route=>{await new Promise(resolve=>setTimeout(resolve,300));await route.continue();});
 await open('clients');await open('dashboard');
 assert.equal(await page.locator('#stat-accounts').textContent(),before,'tab switch replaced cached account metrics with placeholders');
 await page.screenshot({path:path.join(out,'smooth-navigation-desktop.png'),fullPage:true,animations:'disabled'});
 await page.unrouteAll({behavior:'wait'});await page.evaluate(()=>refresh());
 await page.emulateMedia({reducedMotion:'reduce'});await open('clients');
 assert.equal(await page.locator('#page-clients').evaluate(section=>section.getAnimations().length),0,'reduced-motion preference ignored');
 await page.emulateMedia({reducedMotion:'no-preference'});
 await page.setViewportSize({width:375,height:1000});await open('dashboard');await page.evaluate(()=>refresh());
 await page.waitForFunction(()=>{const r=document.querySelector('.sidebar').getBoundingClientRect();return r.left>=innerWidth-1||r.right<=1;});
 await page.screenshot({path:path.join(out,'smooth-navigation-mobile.png'),fullPage:true,animations:'disabled'});
 assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'navigation introduced mobile overflow');
 await page.setViewportSize({width:1440,height:1100});await open('clients');
 await page.evaluate(()=>{rowObserver.disconnect();delete window.savedRows;delete window.rowObserver;delete window.removedRows;});
 // A mutation arriving during a poll must be followed by an awaited fresh read.
 const countBefore=await page.evaluate(()=>clients.length);
 const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
 const waitForCapture=async(index)=>{let timer;try{await Promise.race([captured[index].promise,new Promise((_,reject)=>{timer=setTimeout(()=>reject(Error('refresh request '+index+' did not arrive')),10000);})]);}finally{clearTimeout(timer);}};
 const captured=[deferred(),deferred()],release=[deferred(),deferred()];let intercepted=0,created,requested;
 await page.route('**/api/clients',async route=>{
  if(route.request().method()!=='GET'||intercepted>=2){await route.continue();return;}
  const index=intercepted++,response=await route.fetch();captured[index].resolve();await release[index].promise;await route.fulfill({response});
 });
 const oldPoll=page.evaluate(()=>refresh({background:true}));
 try{
  await waitForCapture(0);
  created=await page.evaluate(()=>api('/clients','POST',{name:'Refresh sequencing fixture',enabled:false}));
  let completed=false;requested=page.evaluate(()=>refresh()).then(()=>{completed=true;});
  release[0].resolve();await waitForCapture(1);
  await Promise.race([requested,new Promise(resolve=>setTimeout(resolve,30))]);
  assert.equal(completed,false,'manual refresh finished before the post-mutation response arrived');
  release[1].resolve();await requested;
  assert.equal(await page.evaluate(()=>clients.length),countBefore+1,'stale poll replaced the new account snapshot');
  assert.equal(await page.locator('#clients-body tr').count(),countBefore+1);
 }finally{
  release.forEach(gate=>gate.resolve());await oldPoll;if(requested)await requested;
  await page.unrouteAll({behavior:'wait'});
  if(created){await page.evaluate(id=>api('/clients/'+id,'DELETE'),created.id);await page.evaluate(()=>refresh());}
 }
 // Session responses can beat the deferred feature scripts on a warm cache.
 // Force that ordering and require the initial deep-linked form to populate.
 await page.evaluate(()=>location.hash='antiflood');
 await page.route('**/antiflood.js',async route=>{await new Promise(resolve=>setTimeout(resolve,250));await route.continue();});
 await page.reload();await page.locator('#app').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.getElementById('flood-account_rate').value!=='');
 await page.unrouteAll({behavior:'wait'});await open('clients');await page.evaluate(()=>refresh());
 console.log('PASS: stable account DOM, coalesced reads, active-page polling, cached metrics, awaited post-mutation refresh, deferred module startup, reduced motion and mobile navigation');
};
