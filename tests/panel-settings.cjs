const path=require('node:path');

module.exports=async function testSettings({page,url,out,assert}){
 const settings=page.locator('#settings-form');
 await page.waitForFunction(()=>document.getElementById('soar-maxQName').value==='101');
 assert.equal(await settings.locator('.setting-field').count(),20);
 assert.equal(await settings.locator('.help-trigger').count(),20);
 for(const input of await settings.locator('.setting-field input,.setting-field select,.setting-field textarea').all()){
  const id=await input.getAttribute('aria-describedby');
  assert.ok(id,'field without accessible explanation');
  assert.ok((await page.locator('#'+id).textContent()).length>50);
 }
 const help=page.locator('#field-tooltip');
 const query=page.locator('#soar-maxQName');
 await query.scrollIntoViewIfNeeded();await query.hover();await help.waitFor({state:'visible'});
 assert.match(await help.textContent(),/آپلود/);
 await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
 await query.focus();await help.waitFor({state:'visible'});
 await page.screenshot({path:path.join(out,'settings-tooltip-1440.png'),fullPage:true});
 await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
 const auth=await page.request.get(url+'/api/session').then(r=>r.json());
 const saved=()=>page.request.get(url+'/api/settings').then(r=>r.json());
 const initial=(await saved()).soarSettings;
 await page.locator('[data-soar-preset=balanced]').click();
 assert.equal(await query.inputValue(),'180');assert.equal((await saved()).soarSettings.maxQName,101,'preset must not save automatically');
 await page.locator('#soar-safeQName').fill('128');
 await page.locator('#soar-transport').selectOption('off');await page.locator('#soar-discovery').selectOption('false');
 await page.locator('#soar-holdMs').fill('200');await page.locator('#soar-maxStreams').fill('96');
 await page.locator('#soar-streamWindowKiB').fill('512');await page.locator('#soar-sendBufferKiB').fill('128');
 await page.locator('#soar-sessionIdleSeconds').fill('60');await page.locator('#soar-dialTimeoutSeconds').fill('120');
 const response=page.waitForResponse(r=>r.url()===url+'/api/settings'&&r.request().method()==='PUT');
 await settings.locator('[type=submit]').click();assert.equal((await response).status(),200);
 const expected={transport:'off',discovery:false,discoveryWant:1,discoveryConcurrency:1,discoveryPaceMs:1500,safeQName:128,maxQName:180,maxAnswer:896,holdMs:200,maxInflight:64,maxPathWindow:16,maxStreams:96,streamWindowKiB:512,sendBufferKiB:128,sessionIdleSeconds:60,dialTimeoutSeconds:120};
 assert.deepEqual((await saved()).soarSettings,expected);
 const accounts=await page.request.get(url+'/api/clients').then(r=>r.json());
 const {link}=await page.request.get(url+'/api/clients/'+accounts.clients[0].id+'/link').then(r=>r.json());
 const exported=JSON.parse(Buffer.from(link.slice(7),'base64url').toString()).profile.soarSettings;
 assert.deepEqual(exported,{...expected,country:'IR'});
 await page.reload();await page.locator('#app').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('soar-safeQName').value==='128');
 assert.equal(await query.inputValue(),'180');
 await page.locator('#soar-safeQName').fill('200');await settings.locator('[type=submit]').click();
 assert.equal(await page.locator('#soar-safeQName').evaluate(e=>e.validity.valid),false);
 assert.deepEqual((await saved()).soarSettings,expected,'invalid crossed limits changed saved tuning');
 const bad=await saved();bad.soarSettings.maxAnswer=9999;
 assert.equal((await page.request.put(url+'/api/settings',{headers:{'X-CSRF-Token':auth.csrf},data:bad})).status(),400);
 assert.deepEqual((await saved()).soarSettings,expected);
 await page.locator('[data-soar-preset=stability]').click();
 const reset=page.waitForResponse(r=>r.url()===url+'/api/settings'&&r.request().method()==='PUT');
 await settings.locator('[type=submit]').click();assert.equal((await reset).status(),200);
 assert.deepEqual((await saved()).soarSettings,initial,'stability preset differs from migrated defaults');
 for(const width of [1440,768,375]){
  await page.setViewportSize({width,height:1100});await page.evaluate(()=>window.scrollTo(0,0));
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'settings overflow at '+width);
  await page.screenshot({path:path.join(out,`settings-${width}.png`),fullPage:true});
  const button=page.locator('#soar-maxAnswer').locator('..').locator('.help-trigger');
  await button.scrollIntoViewIfNeeded();await button.click();await help.waitFor({state:'visible'});
  const rect=await help.boundingBox();assert.ok(rect.x>=0&&rect.x+rect.width<=width+1,'tooltip overflow at '+width);
  await page.screenshot({path:path.join(out,`settings-help-${width}.png`)});
  await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
 }
 const mobile=await page.context().browser().newContext({viewport:{width:375,height:900},hasTouch:true,isMobile:true,storageState:await page.context().storageState()});
 try{
  const touch=await mobile.newPage();await touch.goto(url+'/#settings');
  await touch.waitForFunction(()=>document.getElementById('soar-maxQName').value==='101');
  const trigger=touch.locator('#soar-maxQName').locator('..').locator('.help-trigger');
  await trigger.scrollIntoViewIfNeeded();await trigger.tap();await touch.locator('#field-tooltip').waitFor({state:'visible'});
  assert.match(await touch.locator('#field-tooltip').textContent(),/آپلود/);
  await touch.screenshot({path:path.join(out,'settings-touch-375.png')});
  await trigger.tap();await touch.locator('#field-tooltip').waitFor({state:'hidden'});
 }finally{await mobile.close();}
 await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>window.scrollTo(0,0));
 console.log('PASS: all 20 settings have help; hover/focus/touch buttons, Escape, bounds, presets, save/reload and exported tuning.');
};
