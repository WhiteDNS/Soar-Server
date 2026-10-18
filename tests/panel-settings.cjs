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
 await query.scrollIntoViewIfNeeded();await query.hover();await page.waitForTimeout(220);assert.equal(await help.isVisible(),false,'input hover must not show help');
 await query.focus();await page.waitForTimeout(220);assert.equal(await help.isVisible(),false,'input focus must not show help');
 const queryHelp=query.locator('..').locator('.help-trigger');await queryHelp.hover();await help.waitFor({state:'visible'});
 assert.match(await help.textContent(),/آپلود/);
 await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
 await queryHelp.focus();await help.waitFor({state:'visible'});
 await page.screenshot({path:path.join(out,'settings-tooltip-1440.png'),fullPage:true});
 await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
 for(const field of await settings.locator('.setting-field').all()){
  const control=field.locator('input,textarea,.select-trigger');await control.scrollIntoViewIfNeeded();await control.hover();await control.focus();await page.waitForTimeout(190);assert.equal(await help.isVisible(),false,'only the question mark may show help');
 }
 for(const id of ['country','soar-transport','soar-discovery']){
  const trigger=page.locator('#'+id+'-trigger');await trigger.click();await page.locator('#panel-select-options').waitFor({state:'visible'});
  assert.equal(await help.isVisible(),false,'dropdown must not show help');assert.equal(await trigger.getAttribute('aria-expanded'),'true');
  await page.screenshot({path:path.join(out,id+'-options-1440.png'),animations:'disabled'});await page.keyboard.press('Escape');
 }
 const auth=await page.request.get(url+'/api/session').then(r=>r.json());
 const saved=()=>page.request.get(url+'/api/settings').then(r=>r.json());
 const initial=(await saved()).soarSettings;
 await page.locator('[data-soar-preset=balanced]').click();
 assert.equal(await query.inputValue(),'180');assert.equal((await saved()).soarSettings.maxQName,101,'preset must not save automatically');
 await page.locator('#soar-safeQName').fill('128');
 await page.locator('#soar-transport-trigger').click();await page.locator('#panel-select-options [role=option]').filter({hasText:'UDP؛ بدون جایگزین خودکار TCP'}).click();
 await page.locator('#soar-discovery-trigger').click();await page.locator('#panel-select-options [role=option]').filter({hasText:'خاموش؛ فقط Resolverهای دستی'}).click();
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
 assert.match(await page.locator('#soar-transport-trigger').textContent(),/بدون جایگزین/);assert.match(await page.locator('#soar-discovery-trigger').textContent(),/خاموش/);
 await page.locator('#soar-safeQName').fill('200');await settings.locator('[type=submit]').click();
 assert.equal(await page.locator('#soar-safeQName').evaluate(e=>e.validity.valid),false);
 assert.deepEqual((await saved()).soarSettings,expected,'invalid crossed limits changed saved tuning');
 const bad=await saved();bad.soarSettings.maxAnswer=9999;
 assert.equal((await page.request.put(url+'/api/settings',{headers:{'X-CSRF-Token':auth.csrf},data:bad})).status(),400);
 assert.deepEqual((await saved()).soarSettings,expected);
 await page.locator('[data-soar-preset=stability]').click();
 assert.match(await page.locator('#soar-transport-trigger').textContent(),/خودکار/);assert.equal((await page.locator('#soar-discovery-trigger').textContent()).trim(),'فعال');
 const reset=page.waitForResponse(r=>r.url()===url+'/api/settings'&&r.request().method()==='PUT');
 await settings.locator('[type=submit]').click();assert.equal((await reset).status(),200);
 assert.deepEqual((await saved()).soarSettings,initial,'stability preset differs from migrated defaults');
 for(const width of [1440,768,414,375,320]){
  await page.setViewportSize({width,height:1100});await page.evaluate(()=>window.scrollTo(0,0));
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'settings overflow at '+width);
  await page.screenshot({path:path.join(out,`settings-${width}.png`),fullPage:true});
  const button=page.locator('#soar-maxAnswer').locator('..').locator('.help-trigger');
  await button.scrollIntoViewIfNeeded();await button.click();await help.waitFor({state:'visible'});
  const rect=await help.boundingBox();assert.ok(rect.x>=0&&rect.x+rect.width<=width+1,'tooltip overflow at '+width);
  await page.screenshot({path:path.join(out,`settings-help-${width}.png`)});
  await page.keyboard.press('Escape');await help.waitFor({state:'hidden'});
  const dropdown=page.locator('#soar-transport-trigger');await dropdown.scrollIntoViewIfNeeded();await dropdown.click();
  const menu=page.locator('#panel-select-options');await menu.waitFor({state:'visible'});const box=await menu.boundingBox();assert.ok(box.x>=0&&box.x+box.width<=width+1&&box.y>=0&&box.y+box.height<=1101,'dropdown overflow at '+width);
  await page.screenshot({path:path.join(out,`settings-dropdown-${width}.png`),animations:'disabled'});await page.keyboard.press('Escape');
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
  await touch.locator('#soar-transport-trigger').tap();await touch.locator('#panel-select-options [role=option]').filter({hasText:'فقط TCP'}).tap();assert.equal(await touch.locator('#soar-transport').inputValue(),'only');assert.equal(await touch.locator('#field-tooltip').isVisible(),false);
 }finally{await mobile.close();}
 await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>window.scrollTo(0,0));
 console.log('PASS: question-mark-only help for all 20 fields; themed settings dropdowns, touch, Escape, bounds, presets, save/reload and exported tuning.');
};
