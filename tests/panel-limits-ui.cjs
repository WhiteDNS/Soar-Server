/* Focused browser coverage with a fresh temporary database, independent of OS clipboard support.
 * PANEL_PLAYWRIGHT=<tools>/node_modules/playwright PANEL_BINARY=<binary> node tests/panel-limits-ui.cjs
 */
const {spawn,spawnSync}=require('node:child_process');
const fs=require('node:fs'),path=require('node:path'),os=require('node:os'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PANEL_PLAYWRIGHT||'playwright');
const binary=process.env.PANEL_BINARY;if(!binary)throw Error('PANEL_BINARY is required');
const out=process.env.PANEL_SCREENSHOTS||path.join(os.tmpdir(),'soar-panel-limits-screenshots');fs.mkdirSync(out,{recursive:true});
const dir=fs.mkdtempSync(path.join(os.tmpdir(),'soar-panel-limits-')),db=path.join(dir,'panel.db'),pw=path.join(dir,'password');
const password=require('node:crypto').randomBytes(24).toString('base64url');fs.writeFileSync(pw,password,{mode:0o600});
const init=spawnSync(binary,['init','--db',db,'--zone','soar.example.org','--password-file',pw],{encoding:'utf8'});fs.unlinkSync(pw);assert.equal(init.status,0,init.stderr);
const service=spawn(binary,['run','--db',db,'--listen','127.0.0.1:0','--dns-listen','127.0.0.1:0'],{stdio:['ignore','pipe','pipe']});
let browser;
(async()=>{try{
 const url=await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('panel did not start')),10000);service.stderr.on('data',b=>{const m=String(b).match(/management=127\.0\.0\.1:(\d+)/);if(m){clearTimeout(timer);resolve('http://127.0.0.1:'+m[1]);}});service.on('exit',code=>{clearTimeout(timer);reject(Error('panel exited '+code));});});
 browser=await chromium.launch({headless:true,...(process.env.PANEL_CHROME?{executablePath:process.env.PANEL_CHROME}:{})});const page=await browser.newPage({viewport:{width:1440,height:1100},acceptDownloads:true});const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await page.goto(url);await page.locator('#login-name').fill('admin');await page.locator('#login-password').fill(password);await page.locator('#login-form button[type=submit]').click();await page.locator('#app').waitFor({state:'visible'});
 await require('./panel-branding.cjs')({page,out,assert});
 await require('./panel-modal-selects.cjs')({page,out,assert});
 await page.locator('#page-dashboard .new-client').click();await page.locator('#client-form [name=name]').fill('کاربر آزمایشی ایران');assert.equal(await page.locator('#client-form [name=streams]').inputValue(),'0');await page.locator('#client-form [type=submit]').click();await page.locator('#client-dialog').waitFor({state:'hidden'});await page.locator('nav a[data-page=clients]').click();await page.locator('#clients-body tr').first().waitFor();
 await require('./panel-session-limits.cjs')({page,out,assert});await require('./panel-masterdns.cjs')({page,out,assert});await require('./panel-stream-limits.cjs')({page,url,out,assert});await require('./panel-diagnostics.cjs')({page,out,assert});assert.deepEqual(errors,[]);console.log('PASS: isolated session/stream-limit and diagnostics browser acceptance checks; screenshots: '+out);
}finally{if(browser)await browser.close();service.kill();}})().catch(e=>{console.error(e);process.exitCode=1;});
