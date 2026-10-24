const path=require('node:path');

module.exports=async({page,out,assert})=>{
 const browser=page.context().browser(),storageState=await page.context().storageState();
 for(const [lang,width,touch,fallback] of [['fa',1440,false,false],['en',1440,false,false],['fa',375,true,false],['en',320,true,false],['en',375,false,true]]){
  const context=await browser.newContext({storageState,viewport:{width,height:1100},hasTouch:touch,isMobile:touch});
  let fixture;const testPage=await context.newPage(),errors=[];testPage.on('pageerror',e=>errors.push(e.message));
  try{
   if(fallback)await testPage.addInitScript(()=>{HTMLElement.prototype.showPopover=undefined;});
   await testPage.goto(page.url());await testPage.locator('#app').waitFor({state:'visible'});
   if(await testPage.locator('html').getAttribute('lang')!==lang){await testPage.locator('#app .language-toggle').click();await testPage.locator(`html[lang=${lang}]`).waitFor();}
   await testPage.evaluate(()=>location.hash='clients');await testPage.locator('#page-clients').waitFor({state:'visible'});
   const activate=locator=>touch?locator.tap():locator.click();
   const form=testPage.locator('#client-form'),dialog=testPage.locator('#client-dialog'),trigger=testPage.locator('#client-engines-trigger'),menu=testPage.locator('#panel-select-options');
   const choose=async value=>{
    await activate(trigger);await menu.waitFor({state:'visible'});
    const index=await form.locator('[name=engines]').evaluate((el,value)=>[...el.options].findIndex(option=>option.value===value),value);
    const option=menu.locator(`[data-index="${index}"]`);await option.scrollIntoViewIfNeeded();
    assert.equal(await option.evaluate(el=>{const r=el.getBoundingClientRect();return el.contains(document.elementFromPoint(r.x+r.width/2,r.y+r.height/2));}),true,`engine option must receive pointer input inside the modal (${lang}/${width}/${fallback?'fallback':'popover'})`);
    await activate(option);assert.equal(await form.locator('[name=engines]').inputValue(),value);await menu.waitFor({state:'hidden'});
   };
   await activate(testPage.locator('#page-clients .new-client'));
   for(const value of ['soar','cotten','masterdns','soar_masterdns','cotten_masterdns','both','all'])await choose(value);
   await trigger.focus();await testPage.keyboard.press('ArrowDown');await testPage.keyboard.press('Home');await testPage.keyboard.press('Enter');assert.equal(await form.locator('[name=engines]').inputValue(),'both');await menu.waitFor({state:'hidden'});
   await activate(trigger);await testPage.keyboard.press('Escape');await menu.waitFor({state:'hidden'});assert.equal(await dialog.isVisible(),true,'Escape closes the menu before the modal');
   await activate(trigger);await activate(form.locator('[name=name]'));await menu.waitFor({state:'hidden'});
   const name=`Engine pointer regression ${lang} ${width} ${fallback}`;await form.locator('[name=name]').fill(name);
   await choose('masterdns');await testPage.screenshot({path:path.join(out,`engine-selected-${lang}-${width}-${fallback}.png`)});
   await activate(form.locator('[type=submit]'));await dialog.waitFor({state:'hidden'});
   fixture=await testPage.evaluate(async name=>(await api('/clients')).clients.find(c=>c.name===name),name);assert.equal(fixture.engines,'masterdns');
   const row=testPage.locator('#clients-body tr').filter({hasText:name});await row.getByRole('button',{name:lang==='fa'?'ویرایش':'Edit',exact:true}).click();
   await choose('all');await activate(form.locator('[type=submit]'));await dialog.waitFor({state:'hidden'});
   assert.equal(await testPage.evaluate(async id=>(await api('/clients')).clients.find(c=>c.id===id).engines,fixture.id),'all');
   await activate(testPage.locator('#status-filter-trigger'));await activate(menu.locator('[data-index="0"]'));await menu.waitFor({state:'hidden'});
   assert.deepEqual(errors,[]);
  }finally{
   if(fixture)await testPage.evaluate(async id=>api('/clients/'+id,'DELETE'),fixture.id);
   await context.close();
  }
 }
 console.log('PASS: real mouse/touch engine selection, all engine choices, modal Escape/outside click, persisted create/edit and shared-menu reuse; Persian/English, desktop/mobile and no-Popover fallback.');
};
