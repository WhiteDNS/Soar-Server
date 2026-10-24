const fs=require('node:fs');
const path=require('node:path');

module.exports=async function testSessionLimits({page,out,assert}){
 const form=page.locator('#client-form'),soar=form.locator('[name=soar_session_limit]'),cotten=form.locator('[name=cotten_session_limit]');
 const readClient=()=>page.evaluate(async()=>{
  const response=await fetch('/api/clients');if(!response.ok)throw Error('session-limit fixture read failed');
  return (await response.json()).clients.find(c=>c.name==='Session limits regression');
 });
 const limits=async(a,b)=>{assert.equal(await soar.inputValue(),String(a));assert.equal(await cotten.inputValue(),String(b));};
 const expected={engines:'both',quota_bytes:3*1024**3,rate_bytes:64*1024,stream_limit:16,note:'Keep account settings',enabled:true};
 const checkAccount=(client,values)=>{assert.ok(client,'session-limit account was not persisted');for(const [key,value] of Object.entries(values))assert.equal(client[key],value,key+' changed unexpectedly');};
 const chooseEngine=engine=>page.locator('#client-engines').selectOption({label:engine},{force:true});
 let fixture;
 try{
  const defaultAccount=await page.evaluate(async()=>{const response=await fetch('/api/clients');return (await response.json()).clients.find(c=>c.name==='کاربر آزمایشی ایران');});
  checkAccount(defaultAccount,{soar_session_limit:32768,cotten_session_limit:65535,stream_limit:0});
  await page.locator('#page-clients .new-client').click();await limits(32768,65535);
  for(const [input,max] of [[soar,32768],[cotten,65535]]){
   assert.equal(await input.getAttribute('min'),'1');assert.equal(await input.getAttribute('max'),String(max));
   for(const invalid of ['0',String(max+1),'1.5','']){
    await input.fill(invalid);assert.equal(await input.evaluate(el=>el.checkValidity()),false,'invalid session limit accepted: '+invalid);
   }
   await input.fill('1');assert.equal(await input.evaluate(el=>el.checkValidity()),true);
   await input.fill(String(max));assert.equal(await input.evaluate(el=>el.checkValidity()),true);
  }
  await form.locator('[name=name]').fill('Session limits regression');await form.locator('[name=quota]').fill('3');await form.locator('[name=rate]').fill('64');await form.locator('[name=streams]').fill('16');await form.locator('[name=note]').fill(expected.note);await form.locator('[name=expires]').fill('2035-06-15T12:30');
  expected.expires_at=await form.locator('[name=expires]').evaluate(el=>new Date(el.value).getTime());
  await soar.fill('9');await cotten.fill('11');await form.locator('button[type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});
  fixture=await readClient();checkAccount(fixture,{...expected,soar_session_limit:9,cotten_session_limit:11});
  const row=page.locator('#clients-body tr').filter({hasText:'Session limits regression'});
  await row.getByRole('button',{name:'ویرایش',exact:true}).click();await limits(9,11);
  await soar.fill('17');await cotten.fill('23');
  // Selecting an engine must retain the other engine's configured limit.
  await chooseEngine('Cotten');await limits(17,23);await chooseEngine('Soar');await limits(17,23);await chooseEngine('Cotten');await limits(17,23);
  await page.screenshot({path:path.join(out,'session-limits-form-1440.png'),fullPage:true});
  await page.setViewportSize({width:375,height:1100});assert.ok(await page.locator('#client-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'session-limit controls overflow on mobile');await page.setViewportSize({width:1440,height:1100});
  await form.locator('button[type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});
  expected.engines='cotten';fixture=await readClient();checkAccount(fixture,{...expected,soar_session_limit:17,cotten_session_limit:23});
  for(const enabled of [false,true]){
   await row.locator('.account-actions-trigger').click();await page.locator('#account-actions-menu [data-action=toggle]').click();await page.locator('#confirm-ok').click();await row.locator(enabled?'.badge.active':'.badge.disabled').waitFor();
   fixture=await readClient();checkAccount(fixture,{...expected,enabled,soar_session_limit:17,cotten_session_limit:23});
  }
  const [download]=await Promise.all([page.waitForEvent('download'),page.locator('#export-csv').click()]);const csv=fs.readFileSync(await download.path(),'utf8');
  assert.match(csv.split('\r\n')[0],/"Soar session limit","Cotten session limit","MasterDNS session limit"$/);assert.match(csv.split('\r\n').find(line=>line.startsWith('"Session limits regression"')),/,"17","23","255"$/);
  // Older responses and restored account objects may not contain the new fields.
  for(const zero of [false,true]){
   await page.evaluate(({id,zero})=>{const legacy={...clients.find(c=>c.id===id)};if(zero){legacy.soar_session_limit=0;legacy.cotten_session_limit=0;}else{delete legacy.soar_session_limit;delete legacy.cotten_session_limit;}openClient(legacy);},{id:fixture.id,zero});
   await limits(32768,65535);await page.locator('#client-dialog .close-dialog').first().click();
  }
 }finally{
  if(fixture){await page.evaluate(async id=>{await api('/clients/'+id,'DELETE');await refresh();},fixture.id);}
 }
 console.log('PASS: separate session-limit defaults, integer/range validation, create/edit persistence, engine changes, enable/disable preservation, CSV and legacy form defaults.');
};
