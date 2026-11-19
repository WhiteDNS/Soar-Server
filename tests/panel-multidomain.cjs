const path=require('node:path');
const fs=require('node:fs');
// Runs while two tunnel domains exist and t.example.net is preferred.
module.exports=async function testMultiDomain({page,out,assert}){
 const decode=link=>JSON.parse(Buffer.from(link.split('://')[1],'base64url')).profile.server.domain;
 await page.evaluate(()=>location.hash='clients');await page.locator('#page-clients').waitFor({state:'visible'});
 await page.locator('#search').fill('');await page.locator('#clients-body tr').first().waitFor();
 const row=page.locator('#clients-body tr').first();
 await row.getByRole('button',{name:'لینک / QR',exact:true}).click();await page.locator('#share-dialog').waitFor({state:'visible'});
 const chips=page.locator('#share-domains button');assert.equal(await chips.count(),2,'share dialog lacks the domain choice');
 assert.equal(await chips.nth(0).getAttribute('data-zone'),'t.example.net','preferred domain is not first');
 assert.equal(await chips.nth(0).getAttribute('aria-pressed'),'true');assert.equal(await chips.nth(0).locator('.domain-tag').count(),1);
 const preferred=await page.locator('#share-link').inputValue();assert.match(decode(preferred),/\.t\.example\.net$/);
 assert.doesNotMatch(await page.locator('#share-qr').getAttribute('src'),/zone=/,'preferred QR changed its URL');
 await chips.nth(1).click();assert.equal(await chips.nth(1).getAttribute('aria-pressed'),'true');
 const other=await page.locator('#share-link').inputValue();assert.match(decode(other),/\.soar\.example\.org$/);assert.notEqual(other,preferred);
 await page.locator('#share-qr').evaluate(img=>img.decode());assert.match(await page.locator('#share-qr').getAttribute('src'),/zone=soar\.example\.org$/);
 for(const width of [1440,375,320]){await page.setViewportSize({width,height:1100});assert.ok(await page.locator('#share-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'share domain overflow '+width);await page.screenshot({path:path.join(out,'share-domains-'+width+'.png'),animations:'disabled'});}
 await page.setViewportSize({width:1440,height:1100});
 await page.locator('#share-sub-mode').click();assert.equal(await page.locator('#share-domains').isHidden(),true,'subscription already covers every domain');
 await page.locator('#share-profile-mode').click();assert.equal(await page.locator('#share-link').inputValue(),other,'domain choice lost when switching tabs');
 await page.locator('#share-dialog .close-dialog').first().click();
 await page.locator('#select-all-clients').check();await page.locator('#bulk-export').click();await page.locator('#export-dialog').waitFor({state:'visible'});
 const picks=page.locator('#export-domain-list input');assert.equal(await picks.count(),2);assert.equal(await page.locator('#export-domains').isVisible(),true);
 for(const width of [1440,375,320]){await page.setViewportSize({width,height:1000});assert.ok(await page.locator('#export-dialog').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'export dialog overflow '+width);await page.screenshot({path:path.join(out,'export-domains-'+width+'.png'),animations:'disabled'});}
 await page.setViewportSize({width:1440,height:1100});
 await picks.nth(0).uncheck();await picks.nth(1).uncheck();assert.equal(await page.locator('#export-download').isDisabled(),true,'export allowed without a domain');
 await picks.nth(1).check();
 let [file]=await Promise.all([page.waitForEvent('download'),page.locator('#export-download').click()]);
 let text=fs.readFileSync(await file.path(),'utf8');const accounts=text.match(/^# /gm).length;
 assert.doesNotMatch(text,/^## /m);assert.ok(text.split('\n').filter(l=>/^soar:\/\//.test(l)).every(l=>/\.soar\.example\.org$/.test(decode(l))),'domain filter ignored');
 await page.locator('#bulk-export').click();await page.locator('#export-dialog').waitFor({state:'visible'});
 [file]=await Promise.all([page.waitForEvent('download'),page.locator('#export-download').click()]);
 text=fs.readFileSync(await file.path(),'utf8');
 assert.equal(text.match(/^## t\.example\.net$/gm).length,accounts);assert.equal(text.match(/^## soar\.example\.org$/gm).length,accounts);
 assert.ok(text.indexOf('## t.example.net')<text.indexOf('## soar.example.org'),'preferred domain must come first');
 await page.locator('#clear-selection').click();
 await page.evaluate(()=>location.hash='security');await page.locator('#page-security').waitFor({state:'visible'});
 console.log('PASS: multi-domain Link / QR choice, preferred-first order, unchanged preferred QR, export domain filter and responsive dialogs.');
};
