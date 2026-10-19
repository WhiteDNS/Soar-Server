const path=require('node:path');
const crypto=require('node:crypto');
module.exports=async({page,context,url,password,out,assert})=>{
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('#login-name').fill('admin');await page.locator('#login-password').fill(password);
 await page.locator('#login-form button[type=submit]').click();await page.locator('#app').waitFor({state:'visible'});
 const ids=await page.evaluate(async()=>{
  const session=await fetch('/api/session').then(r=>r.json()),ids=[];
  for(let i=1;i<=3;i++){
   const response=await fetch('/api/clients',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':session.csrf},body:JSON.stringify({name:'bulk-fixture-'+i,enabled:true,stream_limit:16})});
   if(!response.ok)throw Error('bulk fixture failed');ids.push((await response.json()).id);
  }return ids;
 });
 await page.evaluate(()=>refresh());await page.evaluate(()=>location.hash='clients');
 await page.locator('#page-clients').waitFor({state:'visible'});
 await page.locator('#search').fill('bulk-fixture-');assert.equal(await page.locator('#clients-body tr').count(),3);
 await page.locator('#clients-body input[type=checkbox]').first().check();
 assert.equal(await page.locator('#select-all-clients').evaluate(el=>el.indeterminate),true);
 await page.evaluate(()=>refresh());assert.equal(await page.locator('#clients-body input:checked').count(),1,'polling lost selection');
 await page.locator('#select-all-clients').check();assert.equal(await page.locator('#clients-body input:checked').count(),3);
 await page.locator('#bulk-delete').click();await page.locator('#confirm-dialog').waitFor({state:'visible'});
 assert.match(await page.locator('#confirm-title').textContent(),/۳/);await page.locator('#confirm-cancel').click();
 assert.equal(await page.locator('#clients-body tr').count(),3,'cancel deleted accounts');
 await page.locator('#search').fill('bulk-fixture-1');assert.equal(await page.locator('#bulk-toolbar').isHidden(),true,'search did not clear selection');
 await page.locator('#search').fill('bulk-fixture-');await page.locator('#select-all-clients').check();
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await page.screenshot({path:path.join(out,'bulk-selection-'+theme+'-1440.png'),fullPage:true,animations:'disabled'});
 }
 for(const width of [414,320]){
  await page.setViewportSize({width,height:1000});await page.screenshot({path:path.join(out,'bulk-selection-'+width+'.png'),fullPage:true,animations:'disabled'});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'bulk toolbar overflow');
 }
 await page.setViewportSize({width:1440,height:1100});await page.evaluate(()=>document.documentElement.dataset.theme='dark');
 await page.locator('#bulk-delete').click();await page.locator('#confirm-ok').click();
 await page.locator('#clients-body tr').first().waitFor({state:'hidden'});assert.equal(await page.locator('#bulk-toolbar').isHidden(),true);
 await page.locator('#search').fill('');assert.equal(await page.locator('#clients-body tr').count(),4,'unselected accounts were affected');
 for(const id of ids)assert.equal((await context.request.get(url+'/api/clients/'+id)).status(),404);
 await page.locator('nav a[data-page=admin]').click();await page.locator('#admin-save').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.getElementById('admin-current-name').value==='admin');
 const newPassword=crypto.randomBytes(24).toString('base64url');
 await page.locator('#admin-new-password').fill(newPassword);await page.locator('#admin-repeat-password').fill(newPassword+'x');
 await page.locator('#admin-current-password').fill(password);await page.locator('#admin-save').click();
 assert.match(await page.locator('#admin-feedback').textContent(),/یکسان نیستند/);
 await page.locator('#admin-repeat-password').fill(newPassword);await page.locator('#admin-current-password').fill('incorrect-password');
 await page.locator('#admin-save').click();await page.locator('#admin-feedback').filter({hasText:'رمز فعلی'}).waitFor();
 assert.equal(await page.locator('#app').isVisible(),true,'wrong current password signed out');
 await page.locator('[data-admin-password=admin-new-password]').click();assert.equal(await page.locator('#admin-new-password').getAttribute('type'),'text');
 await page.locator('[data-admin-password=admin-new-password]').click();
 await page.locator('#admin-new-name').fill('updated-admin');await page.locator('#admin-current-password').fill(password);
 for(const theme of ['dark','light']){
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await page.screenshot({path:path.join(out,'administrator-'+theme+'-1440.png'),fullPage:true,animations:'disabled'});
 }
 for(const width of [375,320]){
  await page.setViewportSize({width,height:1100});await page.screenshot({path:path.join(out,'administrator-'+width+'.png'),fullPage:true,animations:'disabled'});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'administrator form overflow');
 }
 const other=await context.browser().newContext();
 try{
  assert.equal((await other.request.post(url+'/api/login',{data:{name:'admin',password}})).status(),200);
  await page.locator('#admin-save').click();await page.locator('#login-view').waitFor({state:'visible'});
  assert.equal(await page.locator('#login-name').inputValue(),'updated-admin');
  assert.equal((await other.request.get(url+'/api/session')).status(),401,'other administrator session survived');
  assert.equal((await other.request.post(url+'/api/login',{data:{name:'admin',password}})).status(),401,'old credentials still work');
 }finally{await other.close();}
 await page.locator('#login-password').fill(newPassword);await page.locator('#login-form button[type=submit]').click();
 await page.locator('#app').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('admin-current-name').value==='updated-admin');
 await page.setViewportSize({width:1440,height:1100});await page.locator('#app .language-toggle').click();
 await page.locator('html[lang=en]').waitFor();await page.locator('#app').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.getElementById('admin-current-name').value==='updated-admin');
 assert.equal(await page.locator('#page-admin h1').innerText(),'Administrator account');
 const untranslated=await page.locator('#page-admin').evaluate(el=>{
  const walker=document.createTreeWalker(el,NodeFilter.SHOW_TEXT),bad=[];
  while(walker.nextNode())if(/[\u0600-\u06ff]/.test(walker.currentNode.nodeValue))bad.push(walker.currentNode.nodeValue);
  return bad;
 });assert.deepEqual(untranslated,[]);
 await page.screenshot({path:path.join(out,'administrator-en-1440.png'),fullPage:true,animations:'disabled'});
 // Username-only changes keep the password and still revoke the previous session.
 await page.locator('#admin-current-password').fill(newPassword);await page.locator('#admin-new-name').fill('renamed-admin');
 await page.locator('#admin-save').click();await page.locator('#login-view').waitFor({state:'visible'});
 await page.locator('#login-password').fill(newPassword);await page.locator('#login-form button[type=submit]').click();
 await page.locator('#app').waitFor({state:'visible'});await page.waitForFunction(()=>document.getElementById('admin-current-name').value==='renamed-admin');
 console.log('PASS: filtered bulk selection, cancel/delete, preserved accounts; administrator validation, rename/password, session revocation, English and mobile layouts.');
};
