const path=require('node:path');

module.exports=async({page,out,assert})=>{
 const form=page.locator('#client-form');let fixture;
 const chooseEngine=async value=>{const label=await form.locator('[name=engines]').evaluate((el,value)=>[...el.options].find(option=>option.value===value).textContent,value);await page.locator('#client-engines-trigger').click();await page.locator('#panel-select-options').getByRole('option',{name:label,exact:true}).click();};
 const read=()=>page.evaluate(async()=>{const result=await api('/clients');return result.clients.find(c=>c.name==='MasterDNS browser regression');});
 try{
  await page.evaluate(()=>location.hash='clients');await page.locator('#page-clients').waitFor({state:'visible'});
  await page.locator('#page-clients .new-client').click();
  await form.locator('[name=name]').fill('MasterDNS browser regression');
  await chooseEngine('all');
  const limit=form.locator('[name=masterdns_session_limit]');
  assert.equal(await limit.inputValue(),'255');
  for(const value of ['0','256','1.5','']){await limit.fill(value);assert.equal(await limit.evaluate(el=>el.checkValidity()),false);}
  await limit.fill('9');await form.locator('[type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});
  fixture=await read();assert.equal(fixture.engines,'all');assert.equal(fixture.masterdns_session_limit,9);
  const row=page.locator('#clients-body tr').filter({hasText:'MasterDNS browser regression'});
  await row.getByRole('button',{name:'لینک / QR',exact:true}).click();await page.locator('#share-dialog').waitFor({state:'visible'});
  await page.locator('#share-masterdns-mode').click();
  const link=await page.locator('#share-link').inputValue();assert.match(link,/^masterdns:\/\//);
  const payload=JSON.parse(Buffer.from(link.split('://')[1],'base64url')).profile;
  assert.match(payload.server.domain,/^mdns-1\./);assert.equal(payload.server.encryption_method,5);assert.match(payload.server.encryption_key,/^[a-f0-9]{64}$/);
  await page.locator('#share-qr').evaluate(img=>img.decode());assert.match(await page.locator('#share-qr').getAttribute('src'),/engine=masterdns$/);
  await page.locator('#share-sub-mode').click();const sub=await page.locator('#share-link').inputValue();
  const response=await page.request.get(sub);assert.equal(response.status(),200);
  const entries=Buffer.from((await response.text()).trim(),'base64').toString().trim().split('\n');
  assert.equal(entries.length,3);assert.deepEqual(entries.map(x=>x.split('://')[0]),['soar','cottendns','masterdns']);
  for(const width of [1440,375,320]){
   await page.setViewportSize({width,height:1100});await page.locator('#share-masterdns-mode').click();
   assert.ok(await page.locator('#share-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'MasterDNS sharing overflow '+width);
   await page.screenshot({path:path.join(out,`masterdns-share-${width}.png`),fullPage:true});
  }
  await page.setViewportSize({width:1440,height:1100});await page.locator('#share-dialog .close-dialog').click();
  await row.getByRole('button',{name:'ویرایش',exact:true}).click();assert.equal(await limit.inputValue(),'9');
  await chooseEngine('masterdns');await form.locator('[type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});
  fixture=await read();assert.equal(fixture.engines,'masterdns');assert.equal(fixture.masterdns_session_limit,9);
  await row.getByRole('button',{name:'لینک / QR',exact:true}).click();await page.locator('#share-dialog').waitFor({state:'visible'});
  assert.match(await page.locator('#share-link').inputValue(),/^masterdns:\/\//);
  assert.equal(await page.locator('#share-profile-mode').isVisible(),false);assert.equal(await page.locator('#share-cotten-mode').isVisible(),false);
  await page.locator('#share-dialog .close-dialog').click();
 }finally{
  await page.setViewportSize({width:1440,height:1100});
  if(fixture)await page.evaluate(async id=>{await api('/clients/'+id,'DELETE');await refresh();},fixture.id);
 }
 console.log('PASS: MasterDNS selection, session bounds, persisted edits, isolated profile, QR, three-engine subscription and mobile sharing.');
};
