const path=require('node:path');
module.exports=async function testActions({page,out,assert}){
 const row=page.locator('#clients-body tr').filter({hasText:'کاربر ایران — ویرایش شد'}),trigger=row.locator('.account-actions-trigger'),menu=page.locator('#account-actions-menu');
 assert.equal(await row.locator('select').count(),0);
 await trigger.focus();await page.keyboard.press('ArrowDown');await menu.waitFor({state:'visible'});
 assert.equal(await trigger.getAttribute('aria-expanded'),'true');assert.equal(await menu.getByRole('menuitem').count(),5);
 assert.equal(await menu.getByRole('menuitem').first().evaluate(el=>el===document.activeElement),true);
 await page.keyboard.press('End');assert.equal(await menu.getByRole('menuitem').last().evaluate(el=>el===document.activeElement),true);
 await page.keyboard.press('ArrowDown');assert.equal(await menu.getByRole('menuitem').first().evaluate(el=>el===document.activeElement),true);
 await page.keyboard.press('Home');await page.keyboard.press('Escape');await menu.waitFor({state:'hidden'});
 assert.equal(await trigger.evaluate(el=>el===document.activeElement),true);
 await trigger.click();await page.evaluate(()=>refresh());assert.equal(await trigger.getAttribute('aria-expanded'),'true','polling must not destroy the active menu');
 assert.ok(await menu.getByRole('menuitem').first().evaluate(el=>getComputedStyle(el).fontFamily.includes('Vazirmatn')));
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await page.screenshot({path:path.join(out,'account-menu-'+theme+'.png'),fullPage:true,animations:'disabled'});
 }
 await page.keyboard.press('Escape');await page.evaluate(()=>document.documentElement.dataset.theme='dark');
 // Every operation still routes to the correct confirmation without changing data on cancel.
 for(const [action,title] of [['toggle','غیرفعال کردن حساب'],['disconnect','قطع اتصال‌های باز'],['reset','صفر کردن مصرف'],['rotate','تعویض لینک و کلید'],['delete','حذف حساب']]){
  await trigger.click();await menu.locator('[data-action='+action+']').click();
  await page.locator('#confirm-dialog').waitFor({state:'visible'});assert.match(await page.locator('#confirm-title').textContent(),new RegExp(title));
  assert.equal(await trigger.getAttribute('aria-busy'),'true');
  await page.locator('#confirm-cancel').click();await page.locator('#confirm-dialog').waitFor({state:'hidden'});
  await page.waitForFunction(()=>document.activeElement?.classList.contains('account-actions-trigger'));
 }
 await trigger.click();await page.locator('#search').click();await menu.waitFor({state:'hidden'});assert.equal(await trigger.getAttribute('aria-expanded'),'false');
 for(const width of [768,414,375,320]){
  await page.setViewportSize({width,height:850});await trigger.click();await menu.waitFor({state:'visible'});
  assert.ok(await menu.evaluate(el=>{const r=el.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&r.top>=0&&r.bottom<=innerHeight;}),'menu escaped viewport at '+width);
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'menu caused page overflow');
  await page.screenshot({path:path.join(out,'account-menu-'+width+'.png'),animations:'disabled'});
  assert.equal(await menu.isVisible(),true,'menu disappeared during mobile capture at '+width);
  await page.keyboard.press('Escape');
 }
 // A short viewport requires an upward flip or an internally scrollable menu.
 await page.setViewportSize({width:1440,height:400});await trigger.click();await menu.waitFor({state:'visible'});
 assert.ok(await menu.evaluate(el=>el.getBoundingClientRect().bottom<=innerHeight));await page.keyboard.press('Escape');
 await page.setViewportSize({width:1440,height:1100});
 // Keep a non-production eight-state preview beside the component regression checks.
 const states=await trigger.evaluate(el=>el.outerHTML);
 const previewBody=require('node:fs').readFileSync(path.join(__dirname,'account-actions.preview.html'),'utf8').replaceAll(/<button[^>]*>.*?<\/button>/g,()=>states);
 const preview=await page.context().newPage();await preview.route('**/account-actions.preview.html',r=>r.fulfill({contentType:'text/html',body:previewBody}));
 try{await preview.goto(new URL('/account-actions.preview.html',page.url()).href);await preview.evaluate(()=>{for(const row of document.querySelectorAll('tr')){const b=row.querySelector('button'),state=row.querySelector('th').textContent.toLowerCase();b.removeAttribute('aria-controls');if(['hover','focus','active'].includes(state))b.classList.add('is-'+state);if(['disabled','loading'].includes(state)){b.disabled=true;b.setAttribute('aria-disabled','true');}if(state==='loading')b.setAttribute('aria-busy','true');if(['error','success'].includes(state))b.dataset.state=state;}return document.fonts.ready;});await preview.screenshot({path:path.join(out,'account-actions-states.png'),fullPage:true,animations:'disabled'});}finally{await preview.close();}
 console.log('PASS: account menu typography, keyboard, dismissal, confirmation cancel, polling stability, viewport placement and eight-state preview.');
};
