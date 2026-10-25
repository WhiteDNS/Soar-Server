const path=require('node:path');

module.exports=async({page,out,assert})=>{
 const version=await page.evaluate(async()=>(await api('/dashboard')).version),name='WhiteDNS Server Management';
 const versions=async target=>assert.deepEqual(await target.locator('[data-panel-version]').allTextContents(),Array(4).fill(version));
 const branding=async target=>{
  assert.equal(await target.title(),name);
  for(const selector of ['.login-foot','.sidebar-foot']){const text=await target.locator(selector).textContent();assert.ok(text.includes(name));assert.doesNotMatch(text,/Soar|Lantern/);}
  assert.doesNotMatch(await target.locator('.login-card .muted').innerText(),/Soar/);
  assert.equal(await target.locator('a[href="https://github.com/WhiteDNS/Soar-Server"]').count(),1);
  assert.equal(await target.locator('.sidebar-foot a[href="https://github.com/WhiteDNS/Soar-Server"]').count(),1);
  assert.equal(await target.locator('.main-footer').count(),0);
 };
 const context=await page.context().browser().newContext(),login=await context.newPage();
 try{
  for(const lang of ['fa','en']){
   await login.goto(page.url());await login.evaluate(lang=>localStorage.setItem('whitedns-panel-language',lang),lang);await login.reload();await login.locator(`html[lang=${lang}]`).waitFor();
   await versions(login);await branding(login);
   for(const width of [1440,375,320]){
    await login.setViewportSize({width,height:1100});assert.ok(await login.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'login branding overflow');
    await login.screenshot({path:path.join(out,`branding-login-${lang}-${width}.png`),animations:'disabled'});
   }
  }
 }finally{await context.close();}
 for(const lang of ['fa','en']){
  await page.evaluate(lang=>localStorage.setItem('whitedns-panel-language',lang),lang);await page.reload();await page.locator('#app').waitFor({state:'visible'});await versions(page);await branding(page);
  for(const width of [1440,375,320]){
   await page.setViewportSize({width,height:1100});assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'panel branding overflow');
   if(width<=800)await page.locator('#menu-toggle').click();
   await page.waitForFunction(()=>{const r=document.querySelector('.sidebar').getBoundingClientRect();return r.x>=-1&&r.right<=innerWidth+1;});
   assert.ok(await page.locator('.panel-identity').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'sidebar product name overflow');
   await page.screenshot({path:path.join(out,`branding-panel-${lang}-${width}.png`),fullPage:true,animations:'disabled'});
   if(width<=800)await page.locator('.rail-close').click();
  }
 }
 await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','fa'));await page.reload();await page.locator('#app').waitFor({state:'visible'});
 console.log('PASS: runtime version before/after login across all four displays, neutral product branding, Persian/English and desktop/mobile layout.');
};
