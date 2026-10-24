const path=require('node:path');

module.exports=async function testAntiFlood({page,url,out,assert}){
 const read=()=>page.request.get(url+'/api/antiflood').then(r=>{assert.equal(r.status(),200);return r.json();});
 const original=(await read()).policy;
 const open=async()=>{await page.evaluate(()=>location.hash='antiflood');await page.locator('#page-antiflood').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('flood-account_rate').value!=='');};
 const save=async mode=>{
  await page.locator('#flood-mode-trigger').click();
  await page.locator('#panel-select-options').getByRole('option',{name:mode==='monitor'?'فقط مشاهده':mode==='off'?'خاموش':'فعال؛ محدودسازی و قرنطینه',exact:true}).click();
  const response=page.waitForResponse(r=>r.url()===url+'/api/antiflood'&&r.request().method()==='PUT');
  await page.locator('#flood-form [type=submit]').click();
  if(mode!=='enforce'){await page.locator('#confirm-dialog').waitFor({state:'visible'});await page.locator('#confirm-ok').click();}
  assert.equal((await response).status(),200);
 };
 try{
  await open();assert.equal(await page.locator('#flood-mode').inputValue(),'enforce');
  const rate=page.locator('#flood-account_rate');
  for(const invalid of ['0','1.5','10001','']){await rate.fill(invalid);assert.equal(await rate.evaluate(el=>el.checkValidity()),false);}
  await rate.fill('13');await save('monitor');assert.equal((await read()).policy.account_rate,13);
  await page.reload();await page.locator('#app').waitFor({state:'visible'});await open();assert.equal(await rate.inputValue(),'13');assert.equal(await page.locator('#flood-mode').inputValue(),'monitor');
  // Periodic reporting must not discard an administrator's unsaved edits.
  await rate.fill('14');await page.evaluate(()=>window.panelAntiFlood.refreshStatus());assert.equal(await rate.inputValue(),'14');
  await save('enforce');assert.equal((await read()).policy.mode,'enforce');
  const before=await page.request.get(url+'/api/settings').then(r=>r.json());
  await page.evaluate(async s=>{delete s.antiflood;await api('/settings','PUT',s);},before);
  assert.deepEqual((await read()).policy,{...original,account_rate:14},'legacy settings request reset protection');
  for(const language of ['fa','en']){
   if(language==='en'){await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','en'));await page.reload();await page.locator('#app').waitFor({state:'visible'});await open();}
   assert.equal(await page.locator('html').getAttribute('dir'),language==='en'?'ltr':'rtl');
   for(const width of [1440,375,320]){
    await page.setViewportSize({width,height:1100});
    assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'anti-flood overflow '+language+' '+width);
    await page.screenshot({path:path.join(out,`antiflood-${language}-${width}.png`),fullPage:true});
   }
   if(language==='en'){
    const text=await page.locator('#page-antiflood').innerText();
    assert.ok(!/[\u0600-\u06ff]/.test(text),'untranslated Persian in English protection page');
   }
  }
 }finally{
  await page.evaluate(async p=>api('/antiflood','PUT',p),original);
  await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','fa'));await page.reload();await page.locator('#app').waitFor({state:'visible'});await page.setViewportSize({width:1440,height:1100});
 }
 console.log('PASS: anti-flood configuration, validation, persistence, legacy API preservation, unsaved edits, Persian/English, and desktop/mobile layouts.');
};
