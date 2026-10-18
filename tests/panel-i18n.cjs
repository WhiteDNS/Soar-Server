const path = require('node:path');
module.exports = async ({page, out, assert}) => {
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('#app .language-toggle').click();
 await page.locator('html[lang=en][dir=ltr]').waitFor();
 await page.locator('#app').waitFor({state:'visible'});
 await page.locator('#nav-count').filter({hasText:'4'}).waitFor();
 assert.match(await page.locator('nav a[data-page=clients]').innerText(), /Accounts\s*4/);
 await page.locator('nav a[data-page=clients]').click();
 await page.locator('#clients-body tr').first().waitFor();
 assert.ok((await page.locator('#clients-body').innerText()).includes('کاربر ایران — ویرایش شد'), 'user-authored names must be preserved');
 await page.locator('#status-filter-trigger').click();
 await page.getByRole('option',{name:'Active',exact:true}).click();
 assert.equal(await page.locator('#clients-body tr').count(),1);
 await page.locator('#status-filter-trigger').click();await page.getByRole('option',{name:'All statuses',exact:true}).click();
 await page.screenshot({path:path.join(out,'clients-en-1440.png'),fullPage:true});
 for(const section of ['dashboard','settings','backup','updates','security','events']){
  await page.locator(`nav a[data-page=${section}]`).click();
  const pageEl=page.locator('#page-'+section);await pageEl.waitFor({state:'visible'});
  if(section==='settings'){
   await page.locator('#soar-transport-trigger').click();assert.equal(await page.locator('#panel-select-options').evaluate(el=>getComputedStyle(el).direction),'ltr');await page.keyboard.press('Escape');
   const help=page.locator('.setting-field').filter({has:page.locator('#base-zone')}).locator('.help-trigger');
   await help.hover();await page.locator('#field-tooltip').waitFor({state:'visible'});
   assert.equal(await page.locator('#field-tooltip').getAttribute('dir'),'ltr');
   assert.match(await page.locator('#field-tooltip').innerText(),/parent DNS zone/);
   await page.keyboard.press('Escape');
  }
  const authored=await pageEl.evaluate(el=>{
   const walk=document.createTreeWalker(el,NodeFilter.SHOW_TEXT),bad=[];
   while(walk.nextNode()){
    const n=walk.currentNode;
    if(n.parentElement.closest('#events-list,input,textarea'))continue;
    if(/[\u0600-\u06ff]/.test(n.nodeValue))bad.push(n.nodeValue.trim());
   }return bad;
  });
  assert.deepEqual(authored,[],`untranslated text in ${section}`);
  if(section==='dashboard'||section==='settings')await page.screenshot({path:path.join(out,`${section}-en-1440.png`),fullPage:true});
 }
 for(const width of [768,375,320]){
  await page.setViewportSize({width,height:1000});await page.locator('#menu-toggle').click();
  await page.waitForFunction(()=>Math.abs(document.querySelector('.sidebar').getBoundingClientRect().x)<1);
  const rail=await page.locator('.sidebar').boundingBox();assert.ok(Math.abs(rail.x)<1,'English drawer must open from the left');
  await page.locator('nav a[data-page=clients]').click();
  await page.waitForFunction(()=>document.querySelector('.sidebar').inert && document.querySelector('.sidebar').getBoundingClientRect().right<=1);
  await page.screenshot({path:path.join(out,`clients-en-${width}.png`),fullPage:true});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'English page overflows at '+width);
 }
 await page.setViewportSize({width:1440,height:1100});await page.locator('#logout').click();
 await page.locator('#login-view').waitFor({state:'visible'});assert.equal(await page.locator('#login-form h1').innerText(),'Welcome');
 await page.reload();assert.equal(await page.locator('html').getAttribute('lang'),'en');
 await page.screenshot({path:path.join(out,'login-en-1440.png'),fullPage:true});
 await page.locator('#login-view .language-toggle').click();await page.locator('html[lang=fa][dir=rtl]').waitFor();
 assert.equal(await page.locator('#login-form h1').innerText(),'خوش آمدید');
 console.log('PASS: Persian/English persistence, authored text coverage, preserved account names, LTR menus/help and responsive layout.');
};
