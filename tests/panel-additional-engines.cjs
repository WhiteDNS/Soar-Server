const path=require('node:path');
module.exports=async({page,out,assert})=>{
 const original=await page.locator('#share-link').inputValue();
 const soar=JSON.parse(Buffer.from(original.split('://')[1],'base64url')).profile;
 const keys=new Set();
 for(const [engine,prefix] of [['stormdns','storm.'],['masterdns','master.']]){
  await page.locator('#share-'+engine+'-mode').click();
  const link=await page.locator('#share-link').inputValue();
  assert.ok(link.startsWith(engine+'://'));
  const profile=JSON.parse(Buffer.from(link.split('://')[1],'base64url')).profile;
  assert.equal(profile.server.domain,prefix+soar.server.domain);
  assert.equal(profile.server.encryption_method,5);
  assert.match(profile.server.encryption_key,/^[a-f0-9]{64}$/);
  keys.add(profile.server.encryption_key);
  await page.locator('#share-qr').evaluate(img=>img.decode());
  assert.ok((await page.locator('#share-qr').getAttribute('src')).endsWith('engine='+engine));
 }
 assert.equal(keys.size,2,'engine secrets must be independent');
 for(const width of [1440,375,320]){
  await page.setViewportSize({width,height:1100});
  assert.ok(await page.locator('#share-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1));
  await page.screenshot({path:path.join(out,'four-engines-share-'+width+'.png'),fullPage:true});
 }
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('#share-profile-mode').click();
 assert.equal(await page.locator('#share-link').inputValue(),original);
 console.log('PASS: StormDNS/MasterDNS profiles, separate AES keys, engine QR codes and responsive sharing.');
};
