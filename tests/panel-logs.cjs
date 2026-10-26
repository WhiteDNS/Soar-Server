const http=require('node:http');
const path=require('node:path');
module.exports=async({page,out,assert})=>{
 const payloads=[];let tuning=64;
 const provider=http.createServer((req,res)=>{
  let raw='';req.on('data',part=>raw+=part);req.on('end',()=>{
   const body=JSON.parse(raw);payloads.push(body);
   const content=JSON.stringify({answer:'Observed timeouts need client-side measurements. <img src=x onerror=alert(1)>',reason:'Try a smaller window and compare failures before and after.',changes:{'soarSettings.maxInflight':tuning}});
   res.writeHead(200,{'Content-Type':'application/json'});res.end(JSON.stringify({choices:[{finish_reason:'stop',message:{content}}]}));
  });
 });
 await new Promise(resolve=>provider.listen(0,'127.0.0.1',resolve));
 try{
  await page.setViewportSize({width:1440,height:1100});
  await page.evaluate(()=>location.hash='logs');await page.locator('#page-logs').waitFor({state:'visible'});
  await page.waitForFunction(()=>document.querySelector('#assistant-provider').options.length>=14);
  await page.locator('#diagnostic-entries li').first().waitFor();
  const config=await page.evaluate(()=>fetch('/api/assistant/config').then(r=>r.json()));assert.ok(config.providers.find(p=>p.id==='9router'));assert.ok(config.providers.find(p=>p.id==='custom'));
  await page.locator('#assistant-provider-trigger').click();await page.getByRole('option',{name:'Custom',exact:true}).click();
  await page.locator('#assistant-base-url').fill('http://127.0.0.1:'+provider.address().port);
  await page.locator('#assistant-model').fill('fixture-model');await page.locator('#assistant-api-key').fill('fixture-private-key');await page.locator('#assistant-enabled').check();
  await page.locator('#assistant-config-form button[type=submit]').click();await page.waitForFunction(()=>document.querySelector('#assistant-send').disabled===false);
  assert.equal(payloads.length,0,'saving config sent diagnostics');
  await page.locator('#assistant-question').fill('Why are the connections failing?');await page.locator('#assistant-send').click();
  await page.locator('#assistant-error').waitFor({state:'visible'});assert.equal(payloads.length,0,'sent without consent');
  await page.locator('#assistant-context-details summary').click();await page.waitForFunction(()=>document.querySelector('#assistant-context-preview').textContent.includes('captured_at'));
  const context=await page.locator('#assistant-context-preview').innerText();assert.ok(!context.includes('fixture-private-key'));assert.ok(!context.includes('public_key'));
  const before=await page.evaluate(()=>fetch('/api/settings').then(r=>r.json()));tuning=before.soarSettings.maxInflight===64?96:64;
  await page.locator('#assistant-consent').check();await page.locator('#assistant-send').click();await page.locator('#assistant-proposal').waitFor({state:'visible'});
  assert.equal(payloads.length,1);assert.ok(!JSON.stringify(payloads[0]).includes('fixture-private-key'));assert.ok(payloads[0].messages.some(m=>m.content.includes('Diagnostic context')));
  assert.equal(await page.locator('#assistant-chat img').count(),0,'model output rendered HTML');
  const getSettings=()=>page.evaluate(()=>fetch('/api/settings').then(r=>r.json()));
  assert.equal((await getSettings()).soarSettings.maxInflight,before.soarSettings.maxInflight,'chat changed settings');
  await page.locator('#assistant-apply').click();await page.locator('#confirm-dialog').waitFor({state:'visible'});await page.locator('#confirm-cancel').click();
  assert.equal((await getSettings()).soarSettings.maxInflight,before.soarSettings.maxInflight,'cancel applied proposal');
  await page.evaluate(()=>window.scrollTo({top:0,behavior:'instant'}));await page.screenshot({path:path.join(out,'logs-assistant-fa-1440.png'),fullPage:true});
  await page.locator('#assistant-apply').click();await page.locator('#confirm-ok').click();await page.locator('#assistant-undo-panel').waitFor({state:'visible'});
  assert.equal((await getSettings()).soarSettings.maxInflight,tuning);
  await page.reload();await page.locator('#app').waitFor({state:'visible'});
  const restored=await page.evaluate(()=>fetch('/api/assistant/conversation').then(r=>r.json()));assert.ok(restored.undo,'missing server-side undo after reload: '+JSON.stringify(restored));
  try{await page.locator('#assistant-undo-panel').waitFor({state:'visible'});}catch(error){throw Error(error.message+' UI state: '+JSON.stringify(await page.evaluate(()=>({hash:location.hash,pageError:document.querySelector('#page-error').textContent,toast:document.querySelector('#toast').textContent,turns:document.querySelectorAll('.assistant-turn').length}))));}
  assert.equal(await page.locator('.assistant-turn').count(),2,'conversation not restored');
  await page.locator('#assistant-undo').click();await page.locator('#confirm-ok').click();await page.locator('#assistant-undo-panel').waitFor({state:'hidden'});
  assert.equal((await getSettings()).soarSettings.maxInflight,before.soarSettings.maxInflight);
  await page.locator('#logs-live').click();assert.equal(await page.locator('#logs-live').getAttribute('aria-pressed'),'false');
  const download=page.waitForEvent('download');await page.locator('#logs-download').click();assert.match((await download).suggestedFilename(),/^whitedns-diagnostics-.*\.json$/);
  for(const language of ['fa','en'])for(const theme of ['dark','light']){
   await page.evaluate(({language,theme})=>{localStorage.setItem('whitedns-panel-language',language);document.documentElement.dataset.theme=theme;localStorage.setItem('soar-theme',theme);},{language,theme});
   await page.reload();await page.locator(`#app`).waitFor({state:'visible'});await page.waitForFunction(()=>document.querySelector('#assistant-provider').options.length>=14);
   assert.equal(await page.locator('#page-logs h1').innerText(),language==='fa'?'لاگ‌ها و دستیار':'Logs & assistant');
   for(const width of [1440,768,414,375,320]){
    await page.setViewportSize({width,height:1100});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),`diagnostics overflow ${language}/${theme}/${width}`);
    assert.ok(await page.locator('.diagnostic-console').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'log card overflow');
    await page.evaluate(()=>window.scrollTo({top:0,behavior:'instant'}));await page.screenshot({path:path.join(out,`logs-${language}-${theme}-${width}.png`),fullPage:true});
    if(width===320){await page.locator('#assistant-setup').evaluate(el=>el.open=true);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'AI setup overflow');await page.screenshot({path:path.join(out,`logs-setup-${language}-${theme}-${width}.png`),fullPage:true});await page.locator('#assistant-setup').evaluate(el=>el.open=false);}
   }
  }
  await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','fa'));await page.reload();await page.locator('#app').waitFor({state:'visible'});
  await page.waitForFunction(()=>document.querySelector('#assistant-provider').options.length>=14);await page.locator('#assistant-clear').click();await page.waitForFunction(()=>document.querySelectorAll('.assistant-turn').length===0);
  console.log('PASS: logs, data preview, 14 provider presets, explicit destination consent, real local mock chat, safe output, approval/cancel/undo, session restore and Persian/English responsive views.');
 }finally{await new Promise(resolve=>provider.close(resolve));}
};
