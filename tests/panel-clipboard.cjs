const path=require('node:path');

// Exercise the browser clipboard itself, including a genuinely insecure origin.
module.exports=async function testClipboard({browser,context,page,url,password,out,assert}){
 await context.grantPermissions(['clipboard-read','clipboard-write'],{origin:url});
 const link=await page.locator('#share-link').inputValue();
 const reader=await context.newPage();await reader.goto(url);
 async function copied(source){
  // Clear the previous result so each click must write the clipboard anew.
  await reader.bringToFront();await reader.evaluate(()=>navigator.clipboard.writeText('clipboard sentinel '+crypto.randomUUID()));
  await source.bringToFront();
  await source.locator('#copy-link').click();
  await source.locator('#toast[data-kind=success]').waitFor({state:'visible'});
  await reader.bringToFront();
  assert.equal(await reader.evaluate(()=>navigator.clipboard.readText()),link,'full profile was not written to the clipboard');
  await source.bringToFront();
  assert.equal(await source.locator('#toast').evaluate(el=>el.closest('dialog')?.id),'share-dialog','feedback must be in the modal top layer');
 }
 async function readable(source){
  assert.ok(await source.locator('#toast').evaluate(el=>{
   // Computed colors may be OKLCH as well as rgb(). Let the browser convert
   // them to sRGB pixels before applying the WCAG luminance formula.
   const css=getComputedStyle(el),canvas=document.createElement('canvas');canvas.width=canvas.height=1;
   const ctx=canvas.getContext('2d',{willReadFrequently:true});
   const lum=color=>{ctx.clearRect(0,0,1,1);ctx.fillStyle=color;ctx.fillRect(0,0,1,1);return [...ctx.getImageData(0,0,1,1).data].slice(0,3).map(v=>{v/=255;return v<=.04045?v/12.92:((v+.055)/1.055)**2.4;}).reduce((s,v,i)=>s+v*[.2126,.7152,.0722][i],0);};
   const a=lum(css.color),b=lum(css.backgroundColor),rect=el.getBoundingClientRect();
   return (Math.max(a,b)+.05)/(Math.min(a,b)+.05)>=4.5&&el.contains(document.elementFromPoint(rect.x+rect.width/2,rect.y+rect.height/2));
  }),'feedback must be readable and unobscured');
 }
 await copied(page);await readable(page);
 // A rejected modern API must still attempt a real legacy copy.
 await page.evaluate(()=>{window.savedClipboardWrite=navigator.clipboard.writeText;navigator.clipboard.writeText=()=>Promise.reject(new DOMException('Denied','NotAllowedError'));});
 await copied(page);
 await page.evaluate(()=>{navigator.clipboard.writeText=window.savedClipboardWrite;delete window.savedClipboardWrite;});

 const httpContext=await browser.newContext({viewport:{width:1440,height:1100}});
 try{
  const http=await httpContext.newPage();
  await http.goto(url.replace('127.0.0.1','panel.test'));
  assert.equal(await http.evaluate(()=>isSecureContext),false,'HTTP fallback test must use an insecure origin');
  assert.equal(await http.evaluate(()=>typeof navigator.clipboard),'undefined');
  await http.locator('#login-name').fill('admin');await http.locator('#login-password').fill(password);
  await http.locator('#login-form button[type=submit]').click();await http.locator('#app').waitFor({state:'visible'});
  await http.evaluate(()=>location.hash='clients');
  await http.locator('#clients-body tr').filter({hasText:'کاربر ایران — ویرایش شد'}).getByRole('button',{name:'لینک / QR',exact:true}).click();
  await http.locator('#share-dialog').waitFor({state:'visible'});
  assert.equal(await http.locator('#share-link').inputValue(),link);
  await http.bringToFront();await copied(http);await readable(http);
  await http.screenshot({path:path.join(out,'clipboard-http-dark.png'),fullPage:true,animations:'disabled'});
  await http.evaluate(()=>document.documentElement.dataset.theme='light');
  await copied(http);await readable(http);
  await http.screenshot({path:path.join(out,'clipboard-http-light.png'),fullPage:true,animations:'disabled'});
  await http.setViewportSize({width:375,height:1100});await copied(http);await readable(http);
  assert.ok(await http.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));
  await http.screenshot({path:path.join(out,'clipboard-http-mobile.png'),fullPage:true,animations:'disabled'});
  // A blocked command must report failure, leaving manual copy/download available.
  await http.evaluate(()=>{window.savedExecCommand=document.execCommand;document.execCommand=()=>false;});
  await http.locator('#copy-link').click();await http.locator('#toast[data-kind=error]').waitFor({state:'visible'});
  assert.match(await http.locator('#toast').textContent(),/اجازهٔ کپی نداد/);await readable(http);
  await http.evaluate(()=>{document.execCommand=window.savedExecCommand;delete window.savedExecCommand;});
  await http.locator('#share-dialog .close-dialog').click();await http.locator('#share-dialog').waitFor({state:'hidden'});
  await http.waitForFunction(()=>{const el=document.getElementById('toast');return el.parentElement===document.body&&el.hidden;});
 }finally{await httpContext.close();await reader.close();await page.bringToFront();}
};
