const fs=require('node:fs'),path=require('node:path');
module.exports=async({page,out,assert,password})=>{
 const original=await page.evaluate(()=>api('/egress'));
 const accounts=await page.evaluate(async()=>{const v=await api('/clients');return v.clients.map(c=>({id:c.id,zone:c.zone,key:c.public_key,upload:c.upload_bytes,download:c.download_bytes}));});
 const open=async name=>{await page.evaluate(name=>location.hash=name,name);await page.locator('#page-'+name).waitFor({state:'visible'});};
 const choose=async(id,title)=>{await page.locator('#'+id+'-trigger').click();await page.locator('#panel-select-options').getByRole('option',{name:title,exact:true}).click();};
 let routeHandler;
 try{
  await page.setViewportSize({width:1440,height:1100});await open('settings');await page.locator('#egress-save').waitFor({state:'visible'});await page.waitForFunction(()=>!document.getElementById('egress-save').disabled);
  assert.equal(await page.locator('#egress-masterdns-unrestricted').isDisabled(),true,'direct route exposed unrestricted ports');
  await choose('egress-soar-mode','SOCKS5');await page.locator('#egress-soar-address').fill('127.0.0.1:1080');await page.locator('#egress-soar-username').fill('route-user');await page.locator('#egress-soar-password').fill('route-secret-never-return');await page.locator('#egress-soar-ports').fill('1-2000,8443');
  await choose('egress-cotten-mode','HTTP CONNECT');await page.locator('#egress-cotten-address').fill('127.0.0.1:8080');await page.locator('#egress-cotten-unrestricted').check();
  await page.locator('#outbound-logging').check();await page.locator('#outbound-retention').fill('48');await page.locator('#outbound-storage').fill('8');await page.locator('#egress-save').click();await page.waitForFunction(()=>!document.getElementById('egress-save').disabled&&document.getElementById('egress-soar-password').value==='');
  const cfg=await page.evaluate(()=>api('/egress'));
  assert.equal(cfg.soar.mode,'socks5');assert.equal(cfg.soar.password_configured,true);assert.equal(cfg.soar.password,undefined);assert.equal(cfg.cotten.unrestricted,true);assert.equal(cfg.masterdns.mode,'direct');assert.equal(cfg.logs.retention_hours,48);
  assert.equal(await page.locator('#egress-soar-password').inputValue(),'');assert.match(await page.locator('#egress-soar-password').getAttribute('placeholder'),/ذخیره/);
  assert.deepEqual(await page.evaluate(async()=>{const v=await api('/clients');return v.clients.map(c=>({id:c.id,zone:c.zone,key:c.public_key,upload:c.upload_bytes,download:c.download_bytes}));}),accounts,'saving outbound settings changed accounts, keys or usage');
  const now=Date.now();const records=Array.from({length:301},(_,i)=>({id:'connection-'+i,engine:'soar',account_id:'account-fixture',host:'example.org',destination_ip:'8.8.8.8',port:443,network:'tcp',purpose:'traffic',route:'socks5',proxy:'127.0.0.1:1080',started_at:now-60000-i,ended_at:now-30000,upload_bytes:100+i,download_bytes:200+i,state:'completed',reason:''}));
  let reportReads=0,holdNext=null;const filters=[];
  routeHandler=async route=>{
   const url=new URL(route.request().url());filters.push(url.searchParams);reportReads++;
   if(holdNext){const gate=holdNext;holdNext=null;gate.captured();await gate.promise;}
   if(url.searchParams.get('format')==='csv'){await route.fulfill({status:200,contentType:'text/csv; charset=utf-8',body:'connection_id,engine,destination_ip\r\nconnection-fixture,soar,8.8.8.8\r\n'});return;}
   const all=url.searchParams.get('download')==='1',offset=Number(url.searchParams.get('offset')||0),limit=Number(url.searchParams.get('limit')||200);
   await route.fulfill({status:200,contentType:'application/json',body:JSON.stringify({entries:all?records:records.slice(offset,offset+limit),total:records.length,next_offset:offset+limit<records.length?offset+limit:-1,upload_bytes:12345,download_bytes:54321,logging:{enabled:true,retention_hours:48,max_mib:8},dropped:0,storage_errors:0})});
  };
  await page.route('**/api/outbound?*',routeHandler);await open('logs');await page.waitForFunction(()=>document.querySelectorAll('#outbound-body tr').length===200);
  await page.locator('#outbound-from').fill('2026-10-20T10:15:17');await page.locator('#outbound-until').fill('2026-10-28T19:30:23');await choose('outbound-engine','Soar');await page.locator('#outbound-query button[type=submit]').click();await page.waitForFunction(()=>!document.querySelector('#outbound-query button[type=submit]').disabled);
  const last=filters.at(-1);assert.equal(last.get('engine'),'soar');assert.equal(Number(last.get('from')),await page.locator('#outbound-from').evaluate(el=>new Date(el.value).getTime()));assert.equal(Number(last.get('until')),await page.locator('#outbound-until').evaluate(el=>new Date(el.value).getTime()));
  await page.locator('#outbound-next').click();await page.waitForFunction(()=>document.querySelectorAll('#outbound-body tr').length===101);assert.equal(await page.locator('#outbound-next').isDisabled(),true);
  await page.locator('#outbound-previous').click();await page.waitForFunction(()=>document.querySelectorAll('#outbound-body tr').length===200);
  const readsBefore=reportReads;await page.evaluate(()=>refresh({background:true}));assert.equal(reportReads,readsBefore,'background polling reread the full outbound journal');
  const [csv]=await Promise.all([page.waitForEvent('download'),page.locator('#outbound-csv').click()]);const csvPath=path.join(out,'outbound-export.csv');await csv.saveAs(csvPath);assert.match(fs.readFileSync(csvPath,'utf8'),/connection-fixture,soar,8.8.8.8/);
  const [json]=await Promise.all([page.waitForEvent('download'),page.locator('#outbound-json').click()]);const jsonPath=path.join(out,'outbound-export.json');await json.saveAs(jsonPath);assert.equal(JSON.parse(fs.readFileSync(jsonPath,'utf8')).entries.length,301,'JSON download exported only the visible page');
  if(password){
   let captured,release;const seen=new Promise(resolve=>captured=resolve),promise=new Promise(resolve=>release=resolve);holdNext={captured,promise};
   await page.locator('#outbound-query button[type=submit]').click();await seen;
   const beforeSessionReads=reportReads;
   try{
    await page.locator('#logout').click();await page.locator('#login-view').waitFor({state:'visible'});await page.locator('#login-name').fill('admin');await page.locator('#login-password').fill(password);await page.locator('#login-form button[type=submit]').click();await page.locator('#app').waitFor({state:'visible'});
   }finally{release();}
   await page.waitForFunction(()=>document.querySelectorAll('#outbound-body tr').length===200);
   assert.ok(reportReads>beforeSessionReads,'new login reused the old session report without requesting its own snapshot');
  }
  for(const theme of ['dark','light']){
   if(await page.locator('html').getAttribute('data-theme')!==theme)await page.locator('#theme-toggle').click();
   for(const width of [1440,375,320]){await page.setViewportSize({width,height:1100});await open('settings');await page.waitForFunction(()=>!document.getElementById('egress-save').disabled);assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'egress settings overflow '+width);await page.locator('#egress-form').scrollIntoViewIfNeeded();await page.screenshot({path:path.join(out,`egress-${theme}-${width}.png`),fullPage:true});await open('logs');await page.locator('#outbound-body tr').first().waitFor();assert.ok(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),'outbound report overflow '+width);await page.locator('.outbound-report').scrollIntoViewIfNeeded();await page.screenshot({path:path.join(out,`outbound-${theme}-${width}.png`),fullPage:true});}
  }
 }finally{
  if(routeHandler)await page.unroute('**/api/outbound?*',routeHandler);
  await page.evaluate(async previous=>{const latest=await api('/egress');previous.revision=latest.revision;for(const key of ['soar','cotten','masterdns'])previous[key].clear_password=true;await api('/egress','PUT',previous);},original);
  await page.setViewportSize({width:1440,height:1100});if(await page.locator('html').getAttribute('data-theme')!=='dark')await page.locator('#theme-toggle').click();await open('clients');await page.evaluate(()=>refresh());
 }
 console.log('PASS: independent engine proxy settings, masked credentials, proxy-only port exceptions, preserved identities/usage, exact date filters, complete CSV/JSON download, pagination, no report polling and green dark/light desktop/mobile layouts');
};
