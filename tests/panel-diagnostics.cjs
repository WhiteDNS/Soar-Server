const path=require('node:path');
module.exports=async({page,out,assert})=>{
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('nav a[data-page=dashboard]').click();
 const report=await page.evaluate(async()=>{const r=await fetch('/api/diagnostics');if(!r.ok)throw Error('diagnostics request failed');return r.json()});
 assert.equal(typeof report.runtime.udp_write_errors,'number');assert.equal(typeof report.accounting.commits,'number');
 assert.ok(report.host.file_descriptors_limit>0);assert.ok(report.host.file_descriptors_used>0);
 const card=page.locator('.runtime-diagnostics');
 for(const lang of ['fa','en']){
  await page.setViewportSize({width:1440,height:1100});
  await page.evaluate(language=>localStorage.setItem('whitedns-panel-language',language),lang);await page.reload();
  await page.locator(`html[lang=${lang}]`).waitFor();await page.locator('#app').waitFor({state:'visible'});
  await card.locator('summary').click();await page.waitForFunction(()=>document.querySelector('#diag-fds').textContent.includes('/'));
  assert.equal(await card.locator('summary').innerText(),lang==='fa'?'عیب‌یابی اتصال':'Connection diagnostics');
  for(const width of [1440,375,320]){
   await page.setViewportSize({width,height:1100});
   assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'diagnostics overflow '+lang+' '+width);
   assert.ok(await card.evaluate(el=>el.scrollWidth<=el.clientWidth+1),'diagnostics card overflow');
   await page.screenshot({path:path.join(out,`diagnostics-${lang}-${width}.png`),fullPage:true});
  }
 }
 await page.setViewportSize({width:1440,height:1100});
 await page.evaluate(()=>localStorage.setItem('whitedns-panel-language','fa'));await page.reload();await page.locator('html[lang=fa]').waitFor();await page.locator('#app').waitFor({state:'visible'});
 console.log('PASS: authenticated aggregate diagnostics, live resource counters, Persian/English responsive views.');
};
