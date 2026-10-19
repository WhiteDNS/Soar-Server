const path=require('node:path');
module.exports=async({page,out,assert})=>{
 const soar=await page.locator('#share-link').inputValue();
 await page.locator('#share-cotten-mode').click();
 const link=await page.locator('#share-link').inputValue();
 assert.match(link,/^cottendns:\/\//);
 const cotten=JSON.parse(Buffer.from(link.split('://')[1],'base64url')).profile;
 const original=JSON.parse(Buffer.from(soar.split('://')[1],'base64url')).profile;
 assert.equal(cotten.server.domain,'cot.'+original.server.domain);
 assert.equal(cotten.server.encryption_method,5);
 assert.match(cotten.server.encryption_key,/^[a-f0-9]{64}$/);
 assert.notEqual(cotten.id,original.id);
 await page.locator('#share-qr').evaluate(img=>img.decode());
 assert.match(await page.locator('#share-qr').getAttribute('src'),/engine=cotten$/);
 for(const width of [1440,375]){
  await page.setViewportSize({width,height:1100});
  assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  assert.ok(await page.locator('#share-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1));
  await page.screenshot({path:path.join(out,`cotten-share-${width}.png`),fullPage:true});
 }
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('#share-profile-mode').click();
 assert.equal(await page.locator('#share-link').inputValue(),soar);
 console.log('PASS: Cotten AES-256 profile, isolated domain/key, engine QR, sharing and mobile layout.');
};
