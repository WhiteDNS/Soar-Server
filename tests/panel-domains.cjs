const path=require('node:path');
module.exports=async function testDomains({page,url,out,assert}){
 await page.evaluate(()=>location.hash='security');await page.locator('#page-security').waitFor({state:'visible'});
 await page.waitForFunction(()=>document.getElementById('tunnel-domains').value==='soar.example.org');
 await page.locator('#tunnel-domains').fill('soar.example.org\nt.example.net');
 await page.locator('#tunnel-primary-trigger').click();await page.getByRole('option',{name:'t.example.net',exact:true}).click();
 await page.locator('#tunnel-domains-form button[type=submit]').click();
 await page.waitForFunction(()=>document.querySelectorAll('.domain-record').length===2);
 const cardGap=await page.locator('#page-security').evaluate(el=>{const cards=el.querySelectorAll('.maintenance-card');return cards[1].getBoundingClientRect().top-cards[0].getBoundingClientRect().bottom;});assert.ok(cardGap>=20,'tunnel and panel domain cards need separate spacing');
 assert.equal(await page.locator('[data-tunnel-nameserver="t.example.net"]').inputValue(),'ns1.example.net');
 assert.match(await page.locator('.domain-record').nth(1).textContent(),/NS\s+t\.example\.net\s+→\s+ns1\.example\.net/);
 const data=await (await page.request.get(url+'/api/clients')).json();const client=data.clients.find(c=>c.engines==='both'&&!c.enabled);assert.ok(client);assert.match(client.connect_zone,/\.t\.example\.net$/);
 const links=await (await page.request.get(url+'/api/clients/'+client.id+'/link')).json();
 const profile=JSON.parse(Buffer.from(links.links.soar.split('://')[1],'base64url'));assert.match(profile.profile.server.domain,/\.t\.example\.net$/);
 const sub=await (await page.request.get(url+'/api/clients/'+client.id+'/subscription')).json();const subscription=await page.request.get(sub.url+'?format=plain');assert.equal(subscription.status(),403,'changing domains must not reactivate disabled accounts');
 await page.route('**/api/tunnel-domains/check',route=>route.fulfill({json:{reachable:true}}));
 await page.locator('.domain-record').nth(1).getByRole('button').click();await page.locator('#tunnel-domains-feedback').filter({hasText:'تأیید شد'}).waitFor();await page.unroute('**/api/tunnel-domains/check');
 await page.locator('#toast').waitFor({state:'hidden'});
 for(const width of [1440,375,320]){await page.setViewportSize({width,height:1100});await page.evaluate(()=>panelUI.closeMenu());if(width<=800)await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().left>=innerWidth-1);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'domain layout overflow '+width);await page.screenshot({path:path.join(out,'domains-'+width+'.png'),fullPage:true,animations:'disabled'});}
 await page.setViewportSize({width:1440,height:1100});await page.locator('#tunnel-domains').fill('soar.example.org');await page.locator('#tunnel-domains-form button[type=submit]').click();await page.waitForFunction(()=>document.querySelectorAll('.domain-record').length===1);
 console.log('PASS: domain editing, independent NS instructions, preferred links, disabled-account enforcement and responsive domain forms.');
};
