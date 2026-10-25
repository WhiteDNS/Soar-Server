const path=require('node:path');

module.exports=async({page,out,assert})=>{
 const entry=await page.evaluate(()=>({language:localStorage.getItem('whitedns-panel-language'),sidebar:localStorage.getItem('whitedns-panel-sidebar'),theme:localStorage.getItem('soar-theme'),hash:location.hash,width:innerWidth,height:innerHeight}));
 const sections=['dashboard','clients','antiflood','settings','events','admin','backup','updates','security'];
 const open=async section=>{await page.evaluate(section=>location.hash=section,section);await page.locator('#page-'+section).waitFor({state:'visible'});};
 const bounds=async label=>{
  const problems=await page.evaluate(()=>{
   const bad=[];
   if(document.documentElement.scrollWidth>innerWidth+1)bad.push('document overflow');
   for(const el of document.querySelectorAll('.page:not([hidden]) h1,.page:not([hidden]) h2,.page:not([hidden]) input,.page:not([hidden]) textarea,.page:not([hidden]) button,.topbar button')){
    if(el.closest('[hidden],.table-scroll')||!el.getClientRects().length)continue;
    const r=el.getBoundingClientRect();
    if(r.width&&(r.left < -1||r.right>innerWidth+1))bad.push((el.id||el.tagName)+': '+r.left+'..'+r.right);
   }
   return bad;
  });
  assert.deepEqual(problems,[],label);
 };
 try{
  for(const language of ['fa','en']){
   await page.evaluate(language=>{localStorage.setItem('whitedns-panel-language',language);localStorage.setItem('whitedns-panel-sidebar','expanded');},language);
   await page.reload();await page.locator('#app').waitFor({state:'visible'});
   await page.setViewportSize({width:1680,height:1000});
   await page.waitForFunction(()=>{const r=document.querySelector('.sidebar').getBoundingClientRect();return r.x>=-1&&r.right<=innerWidth+1;});
   assert.equal(await page.locator('#menu-toggle').getAttribute('aria-expanded'),'true');
   const rail=await page.locator('.sidebar').boundingBox();
   const brandSpacing=await page.locator('.brand').evaluate(el=>{const box=el.getBoundingClientRect(),text=el.lastElementChild.getBoundingClientRect(),title=getComputedStyle(el.querySelector('b')),caption=getComputedStyle(el.querySelector('small'));return {top:text.top-box.top,bottom:box.bottom-text.bottom,title:parseFloat(title.fontSize),caption:parseFloat(caption.fontSize)};});
   assert.ok(brandSpacing.top>=12&&brandSpacing.bottom>=12,'brand text touches its frame: '+JSON.stringify(brandSpacing));
   assert.ok(brandSpacing.title>=20&&brandSpacing.caption>=12,'brand typography is too small');
   const foot=await page.locator('.sidebar-foot').boundingBox();
   assert.ok(foot.y+foot.height<=1000-24,'sidebar footer touches viewport bottom');
   assert.ok(foot.x>rail.x&&foot.x+foot.width<rail.x+rail.width,'sidebar footer lacks side gutters');
   assert.ok(await page.locator('.sidebar-foot').evaluate(el=>parseFloat(getComputedStyle(el).borderBottomWidth)>=1),'sidebar footer needs a complete frame');
   for(const link of await page.locator('nav a').all()){
    const r=await link.boundingBox();assert.ok(r.x>=rail.x&&r.x+r.width<=rail.x+rail.width+1,'navigation item escapes rail: '+JSON.stringify({r,rail}));
    assert.ok(await link.locator('.nav-label').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'navigation label truncates: '+await link.getAttribute('aria-label'));
   }
   await page.locator('#sidebar-fold').click();
   await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().width<100);
   assert.equal(await page.locator('#menu-toggle').getAttribute('aria-expanded'),'false');
   const compact=await page.locator('.sidebar').boundingBox();assert.ok(compact.width<100,'desktop rail did not collapse');
   await page.waitForFunction(()=>{const r=document.querySelector('.sidebar').getBoundingClientRect(),b=document.getElementById('sidebar-fold').getBoundingClientRect(),edge=document.documentElement.dir==='rtl'?r.left:r.right;return Math.abs(b.x+b.width/2-edge)<2;});
   await page.locator('nav a[data-page=antiflood]').focus();await page.locator('#rail-tooltip').waitFor({state:'visible'});
   assert.equal(await page.locator('#rail-tooltip').textContent(),await page.locator('nav a[data-page=antiflood]').getAttribute('aria-label'));
   await page.keyboard.press('Escape');await page.locator('#rail-tooltip').waitFor({state:'hidden'});
   await page.locator('nav a[data-page=antiflood]').click();await page.locator('#page-antiflood').waitFor({state:'visible'});
   await page.screenshot({path:path.join(out,`design-${language}-compact.png`),animations:'disabled'});
   await page.reload();await page.locator('#app').waitFor({state:'visible'});
   assert.ok((await page.locator('.sidebar').boundingBox()).width<100,'compact preference not persisted');
   assert.equal(await page.locator('#sidebar-fold').getAttribute('aria-expanded'),'false');
   await page.locator('#sidebar-fold').focus();await page.keyboard.press('Enter');
   await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().width>200);
   assert.equal(await page.locator('#sidebar-fold').getAttribute('aria-expanded'),'true');
   await page.locator('#sidebar-fold').click();
   await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().width<100);
   await page.locator('#menu-toggle').click();
   await page.waitForFunction(()=>document.querySelector('.sidebar').getBoundingClientRect().width>200);
   for(const theme of ['dark','light']){
    if(await page.locator('html').getAttribute('data-theme')!==theme)await page.locator('#theme-toggle').click();
    for(const section of sections){
     await open(section);await bounds(`${language} ${theme} ${section} desktop`);
     if(section==='backup')assert.doesNotMatch(await page.locator('#restore-feedback').innerText(),/maintenance service unavailable/,'known maintenance message leaked raw English into the UI');
     const toolbar=await page.locator('.toolbar-location,.top-actions').evaluateAll(els=>els.map(el=>{const r=el.getBoundingClientRect(),css=getComputedStyle(el);return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,border:parseFloat(css.borderTopWidth),radius:parseFloat(css.borderTopLeftRadius),background:css.backgroundColor};}));
     assert.equal(toolbar.length,2);for(const box of toolbar){assert.ok(box.border>=1&&box.radius>=8,'toolbar group lacks a complete rounded frame');assert.ok(box.top>=8,'toolbar group touches top edge');}
     assert.ok(toolbar[0].right+8<=toolbar[1].left||toolbar[1].right+8<=toolbar[0].left,'toolbar frames overlap');
     for(const button of ['#menu-toggle','#refresh','#theme-toggle','#logout','#app .language-toggle']){
      const target=page.locator(button),before=await target.evaluate(el=>getComputedStyle(el).backgroundColor);await target.hover();
      await page.waitForFunction(({button,before})=>getComputedStyle(document.querySelector(button)).backgroundColor!==before,{button,before});
     }
     await page.mouse.move(0,0);
     if(section==='antiflood'){
      await page.waitForFunction(()=>document.getElementById('flood-account_rate').value!=='');
      await page.locator('#flood-mode-trigger').click();
      if(language==='en'){
       const text=await page.locator('#panel-select-options').innerText();
       assert.ok(!/[\u0600-\u06ff]/.test(text),'English dropdown contains Persian');
       assert.ok(text.includes('Off'),'Off option missing');
      }
      const menu=await page.locator('#panel-select-options').boundingBox();assert.ok(menu.width<=500,'mode dropdown unnecessarily covers the form');
      await page.screenshot({path:path.join(out,`design-${language}-${theme}-antiflood-menu.png`),animations:'disabled'});
      await page.keyboard.press('Escape');
      const group=page.locator('.flood-group').first();await group.locator('summary').click();assert.equal(await group.getAttribute('open'),null);await group.locator('summary').click();
      const rate=page.locator('#flood-account_rate'),original=await rate.inputValue();await rate.fill(String(Number(original)+1));await page.locator('#flood-dirty').waitFor({state:'visible'});
      await page.evaluate(()=>window.panelAntiFlood.refreshStatus());assert.equal(await rate.inputValue(),String(Number(original)+1));
      await rate.fill(original);await page.locator('#flood-dirty').waitFor({state:'hidden'});
      const cards=await page.locator('.flood-overview>.card').evaluateAll(els=>els.map(e=>{const r=e.getBoundingClientRect();return {x:r.x,right:r.right,bottom:r.bottom,y:r.y};}));
      assert.ok(cards[1].x-cards[0].right>=16||cards[0].x-cards[1].right>=16||cards[1].y-cards[0].bottom>=16,'protection cards touch');
      const contrast=await page.evaluate(()=>{
       const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d',{willReadFrequently:true});
       const pixel=()=>[...ctx.getImageData(0,0,1,1).data].slice(0,3).map(n=>{n/=255;return n<=.04045?n/12.92:((n+.055)/1.055)**2.4;}).reduce((n,v,i)=>n+v*[.2126,.7152,.0722][i],0);
       return ['#flood-runtime-mode','.flood-metric span','#flood-form .primary','.flood-group .setting-range','nav a[aria-current=page]'].map(selector=>{
        const el=document.querySelector(selector),ancestors=[];for(let p=el;p;p=p.parentElement)ancestors.push(p);
        ctx.clearRect(0,0,1,1);ctx.fillStyle='white';ctx.fillRect(0,0,1,1);
        for(const p of ancestors.reverse()){ctx.fillStyle=getComputedStyle(p).backgroundColor;ctx.fillRect(0,0,1,1);}
        const bg=pixel();ctx.fillStyle=getComputedStyle(el).color;ctx.fillRect(0,0,1,1);const fg=pixel();return {selector,ratio:(Math.max(bg,fg)+.05)/(Math.min(bg,fg)+.05)};
       });
      });
      for(const pair of contrast)assert.ok(pair.ratio>=4.5,`insufficient ${theme} contrast: ${JSON.stringify(pair)}`);
     }
     // Inspect all expandable settings and capture the complete page, not just
     // the first viewport of a long form.
     await page.locator('#page-'+section+' details:not([open])>summary').evaluateAll(els=>els.forEach(el=>el.click()));
     if(section==='settings'){
      for(const trigger of await page.locator('#page-settings .select-trigger').all()){
       await trigger.click();await page.locator('#panel-select-options').waitFor({state:'visible'});await page.keyboard.press('Escape');
      }
      for(const help of await page.locator('#page-settings .help-trigger').all()){
       await help.hover();await page.locator('#field-tooltip').waitFor({state:'visible'});await page.keyboard.press('Escape');
      }
      let index=0;for(const group of await page.locator('#settings-form>.settings-section,#telegram-form details').all()){
       await group.screenshot({path:path.join(out,`settings-group-${language}-${theme}-${++index}.png`),animations:'disabled'});
      }
     }
     await page.evaluate(()=>window.scrollTo(0,0));
     await page.screenshot({path:path.join(out,`design-${language}-${theme}-${section}-desktop.png`),animations:'disabled',fullPage:true});
     for(const width of [320,375,414,768,1024]){
      await page.setViewportSize({width,height:1000});await bounds(`${language} ${theme} ${section} ${width}`);
      if(width===375)await page.screenshot({path:path.join(out,`design-${language}-${theme}-${section}-375.png`),animations:'disabled',fullPage:true});
     }
     await page.setViewportSize({width:1680,height:1000});
    }
   }
   for(const width of [320,375,414,768]){
    await page.setViewportSize({width,height:720});await page.locator('#menu-toggle').click();
    assert.equal(await page.locator('.workspace').evaluate(el=>el.inert),true,'drawer background must be inert');
    assert.equal(await page.locator('.sidebar').getAttribute('aria-modal'),'true');
    const foot=await page.locator('.sidebar-foot').boundingBox();assert.ok(foot.y+foot.height<=720-20,'drawer footer touches bottom edge');
    await page.keyboard.press('Escape');assert.equal(await page.locator('.workspace').evaluate(el=>el.inert),false);
    assert.equal(await page.evaluate(()=>document.activeElement.id),'menu-toggle');
    await page.locator('#menu-toggle').click();await page.locator('nav a[data-page=updates]').click();
    assert.equal(await page.locator('.sidebar').evaluate(el=>el.inert),true);
   }
  }
  await page.emulateMedia({reducedMotion:'reduce'});await open('dashboard');
  assert.equal(await page.locator('#page-dashboard').evaluate(el=>getComputedStyle(el).animationName),'none');
  console.log('PASS: every page in both languages/themes at 1680/1024/768/414/375/320; desktop collapse/persistence, bounded navigation/dropdown, translation, dirty edits, drawer focus and reduced motion.');
 }finally{
  await page.emulateMedia({reducedMotion:'no-preference'});await page.setViewportSize({width:entry.width,height:entry.height});
  await page.evaluate(entry=>{for(const [key,value] of [['whitedns-panel-language',entry.language],['whitedns-panel-sidebar',entry.sidebar],['soar-theme',entry.theme]]){if(value===null)localStorage.removeItem(key);else localStorage.setItem(key,value);}location.hash=entry.hash;},entry);
  await page.reload();await page.locator('#app').waitFor({state:'visible'});await open(entry.hash.slice(1)||'dashboard');
 }
};
