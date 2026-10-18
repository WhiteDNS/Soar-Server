const path=require('node:path');
module.exports=async function testSelects({page,out,assert}){
 const trigger=page.locator('#status-filter-trigger'),menu=page.locator('#panel-select-options');
 assert.equal(await page.locator('select:not([hidden])').count(),0,'every native dropdown must use the shared component');
 await trigger.focus();await page.keyboard.press('ArrowDown');await menu.waitFor({state:'visible'});
 assert.equal(await trigger.getAttribute('role'),'combobox');assert.equal(await menu.locator('[aria-selected=true]').textContent(),'همهٔ وضعیت‌ها');
 await page.keyboard.press('ArrowDown');await page.keyboard.press('Enter');assert.equal(await page.locator('#status-filter').inputValue(),'active');assert.equal(await page.locator('#clients-body tr').count(),2);
 await trigger.click();await menu.getByRole('option',{name:'غیرفعال',exact:true}).click();assert.equal(await page.locator('#clients-body tr').count(),1);
 await trigger.click();await page.keyboard.press('End');await page.keyboard.press('Enter');assert.equal(await page.locator('#clients-body tr').count(),0);await page.locator('#clients-empty').waitFor({state:'visible'});
 await trigger.click();await page.keyboard.press('Home');await page.keyboard.press('Enter');assert.equal(await page.locator('#clients-body tr').count(),4);
 await trigger.click();await page.keyboard.press('ArrowDown');await page.keyboard.press('Escape');assert.equal(await page.locator('#status-filter').inputValue(),'');assert.equal(await trigger.getAttribute('aria-expanded'),'false');
 await trigger.click();await page.keyboard.press('Tab');await menu.waitFor({state:'hidden'});assert.equal(await page.locator('#export-csv').evaluate(el=>el===document.activeElement),true,'Tab must continue outside the dropdown');
 for(const width of [1440,768,414,375,320]){
  await page.setViewportSize({width,height:1100});await trigger.scrollIntoViewIfNeeded();await trigger.click();await menu.waitFor({state:'visible'});
  const rect=await menu.boundingBox();assert.ok(rect.x>=0&&rect.x+rect.width<=width+1&&rect.y>=0&&rect.y+rect.height<=1101,'status dropdown overflow '+width);
  assert.match(await menu.evaluate(el=>getComputedStyle(el).fontFamily),/Vazirmatn/);await page.screenshot({path:path.join(out,`status-dropdown-${width}.png`),animations:'disabled'});
  await page.locator('#search').click();await menu.waitFor({state:'hidden'});
 }
 await page.setViewportSize({width:1440,height:1100});await page.locator('#theme-toggle').click();await trigger.click();await menu.waitFor({state:'visible'});await page.screenshot({path:path.join(out,'status-dropdown-light.png'),animations:'disabled'});await page.keyboard.press('Escape');await page.locator('#theme-toggle').click();
 console.log('PASS: all dropdowns upgraded; status selection, filtering, empty state, keyboard, cancellation, outside click, themes, font and responsive bounds.');
};
