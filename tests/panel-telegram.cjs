const path=require('node:path');
module.exports=async({page,out,assert})=>{
 await page.locator('nav a[data-page=settings]').click();
 await page.locator('#telegram-form').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.getElementById('telegram-status').textContent.length>0);
 assert.equal(await page.locator('#telegram-chat-id').count(),0,'chat ID must not be entered manually');
 assert.equal(await page.locator('#telegram-token').getAttribute('type'),'password');
 assert.equal(await page.locator('#telegram-token').inputValue(),'');
 assert.equal(await page.locator('#telegram-report-time').inputValue(),'09:00');
 assert.equal(await page.locator('#telegram-timezone').inputValue(),'Asia/Tehran');
 assert.equal(await page.locator('#telegram-test').isDisabled(),true);
 // Exercise real validation; subsequent Telegram responses are an isolated UI seam.
 await page.locator('#telegram-token').fill('invalid');
 const bad=page.waitForResponse(r=>r.url().includes('/api/telegram')&&r.request().method()==='PUT');
 await page.locator('#telegram-save').click();assert.equal((await bad).status(),400);
 await page.waitForFunction(()=>document.getElementById('telegram-feedback').textContent.length>0);
 let saved={configured:false,enabled:false,paired:false,username:'',language:'auto',alerts:true,daily_report:true,report_time:'09:00',timezone:'Asia/Tehran',status:'disconnected',last_contact:0,error:''};
 const requests=[];let nonce=0;
 await page.route('**/api/telegram{,/test,/pair}',async route=>{
  const req=route.request();requests.push({method:req.method(),body:req.postDataJSON()});
  if(req.method()==='PUT'){
   const body=req.postDataJSON();saved={...saved,...body,configured:true,username:'test_bot',status:body.enabled===false?'disabled':saved.paired?'connected':'awaiting_pairing',last_contact:Date.now()};delete saved.token;
   if(!saved.paired&&body.enabled)saved.pair_url='https://t.me/test_bot?start=test-secret-'+(++nonce);
  }else if(req.method()==='DELETE'){saved={...saved,configured:false,enabled:false,paired:false,username:'',pair_url:'',status:'disconnected',last_contact:0};}
  else if(req.url().endsWith('/pair'))saved.pair_url='https://t.me/test_bot?start=test-secret-'+(++nonce);
  await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify(saved)});
 });
 await page.locator('#telegram-token').fill('123456789:abcdefghijklmnopqrstuvwxyz_123456789');
 await page.locator('#telegram-save').click();await page.waitForFunction(()=>document.getElementById('telegram-token').value===''&&!document.getElementById('telegram-pairing').hidden);
 assert.equal(await page.locator('#telegram-test').isDisabled(),true,'test message requires paired administrator');
 assert.ok((await page.locator('#telegram-pair-link').getAttribute('href')).startsWith('https://t.me/test_bot?start='));
 await page.locator('#telegram-pair-renew').click();await page.waitForFunction(()=>!document.getElementById('telegram-pair-renew').disabled);
 assert.ok(nonce>=2,'connection link renewal failed');
 await page.locator('#telegram-form').screenshot({path:path.join(out,'telegram-pairing-card.png')});
 // Simulate a successful private /start pairing; backend authorization is covered by Go tests.
 saved={...saved,paired:true,pair_url:'',status:'connected'};await page.evaluate(()=>window.panelTelegram.refresh());
 await page.waitForFunction(()=>!document.getElementById('telegram-test').disabled&&document.getElementById('telegram-pairing').hidden);
 await page.locator('#telegram-test').click();await page.waitForFunction(()=>document.getElementById('telegram-feedback').textContent.includes('آزمایش'));
 await page.locator('#telegram-save').click();await page.waitForFunction(()=>!document.getElementById('telegram-save').disabled);
 assert.equal(requests.filter(r=>r.method==='PUT').at(-1).body.token,'');
 assert.ok(requests.filter(r=>r.method==='PUT').every(r=>!Object.hasOwn(r.body,'chat_id')));
 await page.locator('#telegram-form details').evaluate(el=>el.open=true);
 for(const width of [1440,768,375,320]){
  await page.setViewportSize({width,height:1100});await page.locator('#telegram-form').scrollIntoViewIfNeeded();
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'Telegram settings overflow');
  await page.screenshot({path:path.join(out,`telegram-fa-dark-${width}.png`),fullPage:true});
 }
 await page.locator('#theme-toggle').click();await page.screenshot({path:path.join(out,'telegram-fa-light-320.png'),fullPage:true});await page.locator('#theme-toggle').click();
 const disabling=page.waitForResponse(r=>r.url().endsWith('/api/telegram')&&r.request().method()==='PUT'&&r.request().postDataJSON().enabled===false);
 await page.locator('#telegram-disable').click();await page.locator('#confirm-dialog').waitFor({state:'visible'});await page.locator('#confirm-ok').click();await disabling;await page.waitForFunction(()=>document.getElementById('telegram-disable').disabled&&!document.getElementById('telegram-save').disabled);
 assert.equal(saved.enabled,false);
 await page.locator('#telegram-disconnect').click();await page.locator('#confirm-ok').click();await page.waitForFunction(()=>document.getElementById('telegram-disconnect').disabled&&!document.getElementById('telegram-save').disabled);
 assert.equal(saved.configured,false);
 await page.unroute('**/api/telegram{,/test,/pair}');
 await page.setViewportSize({width:1440,height:1100});
};
