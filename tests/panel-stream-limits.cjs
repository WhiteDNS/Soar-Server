const path=require('node:path');

module.exports=async function testStreamLimits({page,url,out,assert}){
 const max=2147483647,form=page.locator('#client-form'),streams=form.locator('[name=streams]');
 const settings=()=>page.request.get(url+'/api/settings').then(r=>r.json());
 const accounts=()=>page.request.get(url+'/api/clients').then(r=>r.json());
 const readClient=async()=> (await accounts()).clients.find(c=>c.name==='Stream limits regression');
 const settingsBody=s=>({base_zone:s.base_zone,country:s.country,resolvers:s.resolvers,allow_ports:s.allow_ports,soarSettings:s.soarSettings,server_stream_limit:s.server_stream_limit});
 const initial=await settings();assert.equal(initial.server_stream_limit,0);assert.equal(initial.soarSettings.maxStreams,128);
 const unchanged=client=>{assert.equal(client.quota_bytes,5*1024**3);assert.equal(client.rate_bytes,96*1024);assert.equal(client.note,'Preserve stream account');assert.equal(client.engines,'both');assert.equal(client.soar_session_limit,32768);assert.equal(client.cotten_session_limit,65535);};
 const saveAccount=async()=>{await form.locator('[type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});};
 const openSettings=async()=>{await page.evaluate(()=>location.hash='settings');await page.locator('#page-settings').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('soar-maxStreams').value==='128');};
 const saveSettings=async()=>{const response=page.waitForResponse(r=>r.url()===url+'/api/settings'&&r.request().method()==='PUT');await page.locator('#settings-form [type=submit]').click();assert.equal((await response).status(),200);};
 const validate=async input=>{
  assert.equal(await input.getAttribute('min'),'0');assert.equal(await input.getAttribute('max'),String(max));
  for(const value of ['-1',String(max+1),'1.5','']){await input.fill(value);assert.equal(await input.evaluate(el=>el.checkValidity()),false,'invalid stream limit accepted: '+value);}
  for(const value of ['0',String(max)]){await input.fill(value);assert.equal(await input.evaluate(el=>el.checkValidity()),true);}
 };
 let fixture;
 try{
  await page.locator('#page-clients .new-client').click();await page.locator('#client-engines').selectOption('both',{force:true});assert.equal(await streams.inputValue(),'0');await validate(streams);
  await form.locator('[name=name]').fill('Stream limits regression');await form.locator('[name=quota]').fill('5');await form.locator('[name=rate]').fill('96');await form.locator('[name=note]').fill('Preserve stream account');await streams.fill('256');await saveAccount();
  fixture=await readClient();assert.equal(fixture.stream_limit,256);unchanged(fixture);
  const row=page.locator('#clients-body tr').filter({hasText:'Stream limits regression'});
  for(const limit of [256,0,max]){
   if(limit!==256){await row.getByRole('button',{name:'ویرایش',exact:true}).click();assert.equal(await streams.inputValue(),String(fixture.stream_limit));await streams.fill(String(limit));await saveAccount();fixture=await readClient();}
   assert.equal(fixture.stream_limit,limit);unchanged(fixture);
   for(const enabled of [false,true]){
    await row.locator('.account-actions-trigger').click();await page.locator('#account-actions-menu [data-action=toggle]').click();await page.locator('#confirm-ok').click();await row.locator(enabled?'.badge.active':'.badge.disabled').waitFor();fixture=await readClient();assert.equal(fixture.enabled,enabled);assert.equal(fixture.stream_limit,limit);unchanged(fixture);
   }
  }
  const auth=await page.request.get(url+'/api/session').then(r=>r.json());
  const clientBody={engines:fixture.engines,name:fixture.name,note:fixture.note,enabled:fixture.enabled,quota_bytes:fixture.quota_bytes,rate_bytes:fixture.rate_bytes,stream_limit:fixture.stream_limit,soar_session_limit:fixture.soar_session_limit,cotten_session_limit:fixture.cotten_session_limit,expires_at:fixture.expires_at,revision:fixture.revision};
  for(const invalid of [-1,max+1,1.5]){
   assert.equal((await page.request.put(url+'/api/clients/'+fixture.id,{headers:{'X-CSRF-Token':auth.csrf},data:{...clientBody,stream_limit:invalid}})).status(),400);assert.deepEqual(await readClient(),fixture,'invalid account limit changed stored data');
  }
  await openSettings();const server=page.locator('#server-stream-limit');assert.equal(await server.inputValue(),'0');await validate(server);await server.fill('4096');await saveSettings();
  let saved=await settings();assert.deepEqual(saved,{...initial,server_stream_limit:4096},'server limit save changed other settings');
  await page.locator('#resolvers').fill('1.1.1.1\n9.9.9.9');await saveSettings();saved=await settings();assert.deepEqual(saved,{...initial,server_stream_limit:4096,resolvers:['1.1.1.1','9.9.9.9']},'unrelated settings save changed stream limit or tuning');
  await page.reload();await page.locator('#app').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('server-stream-limit').value==='4096');
  for(const invalid of [-1,max+1,1.5]){
   assert.equal((await page.request.put(url+'/api/settings',{headers:{'X-CSRF-Token':auth.csrf},data:{...settingsBody(saved),server_stream_limit:invalid}})).status(),400);assert.deepEqual(await settings(),saved,'invalid server limit changed stored settings');
  }
  for(const limit of [max,0]){await server.fill(String(limit));await saveSettings();saved=await settings();assert.equal(saved.server_stream_limit,limit);assert.deepEqual(saved.soarSettings,initial.soarSettings);}
  for(const language of ['fa','en']){
   if(language==='en'){await page.locator('#app .language-toggle').click();await page.locator('html[lang=en][dir=ltr]').waitFor();await page.locator('#app').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('server-stream-limit').value==='0');}
   assert.equal(await page.getByLabel(language==='fa'?'سقف جریان TCP سرور':'Server TCP stream limit',{exact:true}).count(),1);
   assert.match(await page.locator('#server-stream-limit-help').textContent(),language==='fa'?/صفر یعنی نامحدود/:/0 for unlimited/);
   for(const width of [1440,375,320]){await page.setViewportSize({width,height:1100});await server.scrollIntoViewIfNeeded();assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'server settings overflow '+language+' '+width);await page.screenshot({path:path.join(out,`server-stream-limit-${language}-${width}.png`)});}
   await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>location.hash='clients');await page.locator('#page-clients .new-client').click();assert.equal(await streams.inputValue(),'0');
   assert.equal(await page.getByLabel(language==='fa'?'سقف جریان TCP حساب':'Account TCP stream limit',{exact:true}).count(),1);assert.match(await page.locator('#account-stream-limit-help').textContent(),language==='fa'?/صفر = نامحدود/:/0 = unlimited/);
   for(const width of [1440,375,320]){await page.setViewportSize({width,height:1100});assert.ok(await page.locator('#client-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'account form overflow '+language+' '+width);await page.screenshot({path:path.join(out,`account-stream-limit-${language}-${width}.png`)});}
   await page.locator('#client-dialog .close-dialog').first().click();await page.setViewportSize({width:1440,height:1100});await openSettings();
  }
 }finally{
  await page.evaluate(async({initial,id})=>{if(id)await api('/clients/'+id,'DELETE');await api('/settings','PUT',initial);},{initial:settingsBody(initial),id:fixture?.id});
  if(await page.locator('html').getAttribute('lang')==='en'){await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','fa'));await page.reload();await page.locator('html[lang=fa]').waitFor();await page.locator('#app').waitFor({state:'visible'});}
  await page.evaluate(()=>location.hash='clients');await page.locator('#page-clients').waitFor({state:'visible'});await page.evaluate(()=>refresh());
 }
 console.log('PASS: account/server TCP stream limits default to unlimited; 0, custom and maximum persist; toggles, API rejections, unchanged account/settings/profile tuning, Persian/English and 1440/375/320 layout.');
};
