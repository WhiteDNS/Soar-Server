'use strict';
window.panelMaintenance=(()=>{
 let restoreToken='',updateVersion='',accessPlan='',accessConfig={},available=false,watching=false;
 const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
 const info=(id,text,error=false)=>{const el=$(id);el.textContent=message({message:text});el.dataset.kind=error?'error':'info';};
 const withFeedback=(button,id,fn)=>withButton(button,async()=>{try{return await fn();}catch(e){info(id,message(e),true);throw e;}});
 function pendingJob(value){try{if(value)sessionStorage.setItem('soar-maintenance-job',value);else sessionStorage.removeItem('soar-maintenance-job');}catch{}}
 function jobText(job){if(!job?.id)return panelText("هنوز عملیاتی انجام نشده است.");const kind=job.kind==='restore'?panelText("بازیابی"):panelText("به‌روزرسانی");const states={running:panelText("در حال انجام است؛ ممکن است ارتباط موقتاً قطع شود."),succeeded:panelText("با موفقیت انجام شد."),rolled_back:panelText("کامل نشد؛ نسخه و اطلاعات قبلی برگردانده شد."),failed:panelText("انجام نشد؛ جزئیات را بررسی کنید.")};return kind+' '+(states[job.state]||job.state)+(job.state==='failed'||job.state==='rolled_back'?' '+job.message:'');}
 function showJob(job){for(const el of document.querySelectorAll('[data-maintenance-state]')){el.textContent=jobText(job);el.dataset.kind=['failed','rolled_back'].includes(job?.state)?'error':'info';}}
 async function load(page){
  const state=await api('/maintenance');available=state.available;$('update-current').textContent=state.version;showJob(state.job);
  $('restore-apply').disabled=!available;$('update-install').disabled=!available||!updateVersion;
  if(!available&&page==='backup')info('restore-feedback',panelText("برای فعال‌شدن بازیابی، نصب‌کنندهٔ همین نسخه باید سرویس نگهداری را نصب کرده باشد. ")+(state.error||''),true);
  if(!available&&page==='updates')info('update-feedback',panelText("سرویس نگهداری در دسترس نیست. نصب‌کنندهٔ همین نسخه را اجرا کنید؛ بررسی نسخه همچنان ممکن است."),true);
  if(page==='security'){
   await loadTunnelDomains();
   const value=await api('/access');accessConfig=value.config;$('access-current-url').value=new URL('.',document.baseURI).href;
   $('access-state').textContent=accessConfig.https?panelText("ارتباط با HTTPS برقرار است؛ گواهی خودکار تمدید می‌شود."):panelText("ارتباط فعلی HTTP است. برای محافظت از رمز و اطلاعات، HTTPS را فعال کنید.");
   $('access-domain').value=accessConfig.domain||'';$('access-email').value=accessConfig.email||'';$('access-email').readOnly=!!accessConfig.https;
   $('access-aliases').value=(accessConfig.aliases||[]).join('\n');$('access-keep-old').checked=true;$('access-random-path').checked=false;
   const urls=accessConfig.https?[accessConfig.domain,...(accessConfig.aliases||[])].map(domain=>'https://'+domain+(accessConfig.base_path||'/')):[];
   $('access-addresses').replaceChildren(...urls.map(url=>{const field=document.createElement('input');field.readOnly=true;field.dir='ltr';field.value=url;field.setAttribute('aria-label',panelText("آدرس ورود پنل"));return field;}));
   $('access-https').checked=true;$('access-https').disabled=!!accessConfig.https;syncDomain();
  }
 }
 async function watchJob(job){
  pendingJob(job.id);showJob(job);if(watching)return;watching=true;
  try{for(let i=0;i<150;i++){
   await delay(2000);
   try{const state=await api('/maintenance');if(state.job?.id===job.id){showJob(state.job);if(state.job.state!=='running'){pendingJob('');return;}}}
   catch{if(!csrf){$('login-notice').textContent=panelText("سرور دوباره در دسترس است. برای مشاهدهٔ نتیجهٔ عملیات، با رمز فعلی وارد شوید.");return;}}
  }for(const el of document.querySelectorAll('[data-maintenance-state]'))el.textContent=panelText("وضعیت هنوز نهایی نشده است. پس از ورود دوباره، نتیجهٔ آخرین عملیات را ببینید.");
  }finally{watching=false;}
 }
 $('restore-file').onchange=()=>{restoreToken='';$('restore-preview').hidden=true;info('restore-feedback','');};
 $('restore-form').onsubmit=e=>{e.preventDefault();withFeedback(e.submitter,'restore-feedback',async()=>{
  restoreToken='';$('restore-preview').hidden=true;const file=$('restore-file').files[0];if(!file)throw Error(panelText("فایل پشتیبان را انتخاب کنید."));if(file.size>32*1024**2)throw Error(panelText("حجم فایل بیشتر از ۳۲ MiB است."));info('restore-feedback',panelText("در حال بررسی فایل…"));
  const response=await fetch(apiPath('/restore/preview'),{method:'POST',headers:{'Content-Type':'application/octet-stream','X-CSRF-Token':csrf},body:file,credentials:'same-origin'});const result=await response.json();if(!response.ok){info('restore-feedback',result.error||panelText("فایل قابل بازیابی نیست."),true);return;}
  restoreToken=result.token;$('restore-summary').textContent=number(result.summary.accounts)+panelText(" حساب · ")+result.summary.base_zone+' · '+bytes(result.summary.bytes);$('restore-preview').hidden=false;$('restore-confirm').checked=false;$('restore-password').value='';info('restore-feedback',panelText("فایل معتبر است. برای جایگزینی حساب‌ها، رمز فعلی را وارد و بازیابی را تأیید کنید."));
 });};
 $('restore-apply').onclick=e=>withFeedback(e.currentTarget,'restore-feedback',async()=>{
  if(!restoreToken)throw Error(panelText("ابتدا فایل را بررسی کنید."));if(!$('restore-confirm').checked)throw Error(panelText("جایگزینی اطلاعات با پشتیبان را تأیید کنید."));
  const password=$('restore-password').value;try{const job=await api('/restore/apply','POST',{token:restoreToken,password});restoreToken='';$('restore-preview').hidden=true;info('restore-feedback',panelText("بازیابی آغاز شد؛ رمز و آدرس ورود فعلی حفظ می‌شوند."));void watchJob(job);}finally{$('restore-password').value='';}
 });
 $('update-check').onclick=e=>withFeedback(e.currentTarget,'update-feedback',async()=>{
  updateVersion='';$('update-preview').hidden=true;info('update-feedback',panelText("در حال بررسی مخزن رسمی…"));const result=await api('/updates/check','POST',{});$('update-current').textContent=result.current;
  if(result.available){updateVersion=result.latest;$('update-latest').textContent=result.latest;$('update-release').href=result.release_url;$('update-preview').hidden=false;$('update-install').disabled=!available;info('update-feedback',panelText("نسخهٔ جدید آمادهٔ نصب است."));}else info('update-feedback',panelText("نسخهٔ فعلی به‌روز است. آخرین انتشار: ")+result.latest);
 });
 $('update-install').onclick=e=>withFeedback(e.currentTarget,'update-feedback',async()=>{
  const password=$('update-password').value;try{if(!updateVersion)throw Error(panelText("ابتدا نسخهٔ جدید را بررسی کنید."));const job=await api('/updates/install','POST',{version:updateVersion,password});info('update-feedback',panelText("دریافت و بررسی بسته آغاز شد. تا پایان بررسی، سرویس فعلی کار می‌کند."));void watchJob(job);}finally{$('update-password').value='';}
 });
 function syncDomain(){$('access-domain').required=$('access-https').checked;for(const id of ['access-domain','access-email','access-aliases','access-keep-old'])$(id).disabled=!$('access-https').checked;}
 const splitDomains=value=>value.split(/[,\s،]+/).filter(Boolean).map(v=>v.toLowerCase().replace(/\.$/,''));
 function syncTunnelPrimary(){const previous=$('tunnel-primary').value;const zones=[...new Set(splitDomains($('tunnel-domains').value))];$('tunnel-primary').replaceChildren(...zones.map(zone=>new Option(zone,zone)));if(zones.includes(previous))$('tunnel-primary').value=previous;panelUI.syncSelects();}
 async function loadTunnelDomains(){const data=await api('/tunnel-domains');$('tunnel-domains').value=data.zones.join('\n');syncTunnelPrimary();$('tunnel-primary').value=data.primary;panelUI.syncSelects();
  $('tunnel-record-help').textContent=panelText("رکوردهای زیر را در سرویس DNS بسازید؛ Proxy خاموش باشد. نام‌سرور دامنهٔ جایگزین را روی همان دامنهٔ جایگزین نگه دارید تا به دامنهٔ مسدودشده وابسته نشود. اگر نام‌سرور فعلی متفاوت است، مقدار پیشنهادی را اصلاح و دوباره ذخیره کنید.");
  $('tunnel-domain-checks').replaceChildren(...data.zones.map(zone=>{const group=document.createElement('section');group.className='domain-record';const row=document.createElement('div');row.className='domain-check-row';const name=document.createElement('bdi');name.dir='ltr';name.textContent=zone;const button=document.createElement('button');button.type='button';button.className='secondary';button.textContent=panelText("بررسی DNS");button.onclick=()=>withFeedback(button,'tunnel-domains-feedback',async()=>{info('tunnel-domains-feedback',panelText("در حال بررسی مسیر DNS…"));await api('/tunnel-domains/check','POST',{zone});info('tunnel-domains-feedback',zone+' — '+panelText("مسیر DNS از سرور تأیید شد. اتصال از اینترنت کاربر را هم آزمایش کنید."));});row.append(name,button);const label=document.createElement('label');label.textContent=panelText("نام‌سرور این دامنه");const field=document.createElement('input');field.dir='ltr';field.value=data.nameservers[zone]||'';field.placeholder='ns1.example.com';field.maxLength=253;field.dataset.tunnelNameserver=zone;field.setAttribute('form','tunnel-domains-form');label.append(field);const records=document.createElement('pre');records.dir='ltr';const target=field.value||panelText("نام‌سرور فعلی در سرویس DNS");records.textContent='A   '+target+'  →  '+(data.public_ip||panelText("IP عمومی سرور"))+'\nNS  '+zone+'  →  '+target;group.append(row,label,records);return group;}));
 }
 $('tunnel-domains').oninput=syncTunnelPrimary;
 $('tunnel-domains-form').onsubmit=e=>{e.preventDefault();withFeedback(e.submitter,'tunnel-domains-feedback',async()=>{const nameservers=Object.fromEntries([...document.querySelectorAll('[data-tunnel-nameserver]')].map(field=>[field.dataset.tunnelNameserver,field.value]));await api('/tunnel-domains','PUT',{zones:splitDomains($('tunnel-domains').value),primary:$('tunnel-primary').value,nameservers});await loadTunnelDomains();info('tunnel-domains-feedback',panelText("دامنه‌ها ذخیره شدند؛ DNS را بررسی کنید و ساب کاربران را تازه‌سازی کنید. حساب‌ها و مصرف حفظ شدند."));});};
 $('access-https').onchange=syncDomain;
 $('access-form').onsubmit=e=>{e.preventDefault();withFeedback(e.submitter,'access-feedback',async()=>{
  accessPlan='';$('access-preview').hidden=true;info('access-feedback',panelText("در حال آماده‌سازی؛ صدور گواهی ممکن است چند دقیقه طول بکشد. آدرس فعلی همچنان کار می‌کند."));
  try{
   const domain=$('access-https').checked?$('access-domain').value.trim().toLowerCase().replace(/\.$/,''):'';
   let aliases=$('access-https').checked?splitDomains($('access-aliases').value):[];
   if($('access-keep-old').checked&&accessConfig.https&&accessConfig.domain!==domain)aliases.push(accessConfig.domain);
   aliases=[...new Set(aliases)].filter(alias=>alias!==domain);
   const started=await api('/access/prepare','POST',{domain,aliases,https:$('access-https').checked,email:$('access-email').value,generate_path:$('access-random-path').checked||!accessConfig.base_path,base_path:accessConfig.base_path||'',password:$('access-password').value});
   for(let i=0;i<150;i++){const plan=(await api('/access')).plan;if(!plan||plan.id!==started.id)throw Error(panelText("بررسی آدرس منقضی شد؛ دوباره آماده‌سازی کنید."));if(plan.state==='failed'){info('access-feedback',plan.error,true);return;}if(plan.state==='ready'){accessPlan=plan.id;$('access-next-url').value=plan.config.https?[plan.config.domain,...(plan.config.aliases||[])].map(host=>'https://'+host+(plan.config.base_path||'/')).join('\n'):plan.url;$('access-confirm').checked=false;$('access-preview').hidden=false;info('access-feedback',panelText("آدرس جدید آماده است. ابتدا آن را ذخیره کنید؛ هنوز تنظیم قبلی فعال است."));return;}await delay(2000);}
   info('access-feedback',panelText("صدور گواهی هنوز تمام نشده است. وضعیت DNS و پورت‌های ۸۰ و ۴۴۳ را بررسی کنید."),true);
  }finally{$('access-password').value='';}
 });};
 $('access-copy').onclick=e=>withButton(e.currentTarget,async()=>{
  const field=$('access-next-url');let copied=false;if(navigator.clipboard&&isSecureContext){try{await navigator.clipboard.writeText(field.value);copied=true;}catch{}}
  if(!copied){field.focus();field.select();copied=document.execCommand('copy');}if(!copied)throw Error(panelText("مرورگر اجازهٔ کپی نداد؛ آدرس انتخاب‌شده را از منوی مرورگر کپی کنید."));toast(panelText("آدرس جدید کپی شد."),'success');
 });
 $('access-apply').onclick=e=>withFeedback(e.currentTarget,'access-feedback',async()=>{
  if(!accessPlan||!$('access-confirm').checked)throw Error(panelText("ابتدا آدرس جدید را ذخیره و گزینهٔ تأیید را انتخاب کنید."));
  const result=await api('/access/apply','POST',{id:accessPlan});location.assign(result.url);
 });
 function afterLogin(){let id;try{id=sessionStorage.getItem('soar-maintenance-job');}catch{}if(id){$('login-notice').textContent='';api('/maintenance').then(state=>{showJob(state.job);if(state.job?.id===id&&state.job.state==='running')void watchJob(state.job);else pendingJob('');}).catch(()=>{});}}
 if(csrf&&['backup','updates','security'].includes(currentPage))load(currentPage).catch(e=>toast(message(e),'error'));
 return {load,afterLogin,messages:{
  'use at most eight tunnel domains':panelText("حداکثر ۸ دامنهٔ تونل وارد کنید."),
  'keep at least one tunnel domain':panelText("حداقل یک دامنهٔ تونل باید باقی بماند."),
  'tunnel domains must be unique ASCII domains of 3–24 characters':panelText("نام دامنه‌ها باید یکتا، انگلیسی و بین ۳ تا ۲۴ نویسه باشد."),
  'tunnel domains cannot be nested':panelText("دامنهٔ تونل نباید زیرمجموعهٔ دامنهٔ تونل دیگری در همین فهرست باشد."),
  'primary tunnel domain must be in the domain list':panelText("دامنهٔ ترجیحی را از همین فهرست انتخاب کنید."),
  'use at most eight panel domains':panelText("مجموع دامنه‌های ورود پنل حداکثر ۸ مورد است."),
  'panel aliases must be unique HTTPS domains':panelText("دامنه‌های جایگزین پنل باید یکتا و دارای HTTPS باشند."),
  'invalid tunnel nameserver':panelText("نام‌سرور باید نام کامل دامنه باشد؛ مانند ns1.example.net."),
  "DNS route to this server could not be verified. Check the NS delegation, nameserver A record and UDP/TCP 53. This does not establish whether a resolver or domain is filtered on the user's network":panelText("مسیر DNS به این سرور تأیید نشد. رکورد NS، رکورد A نام‌سرور و پورت‌های UDP/TCP 53 را بررسی کنید. این نتیجه مشخص نمی‌کند که دامنه یا ریزالور در اینترنت کاربر فیلتر است یا نه."),
  'current administrator password is incorrect':panelText("رمز فعلی مدیر درست نیست."),
  'too many password checks; retry after five minutes':panelText("تعداد بررسی رمز زیاد است؛ پنج دقیقه دیگر امتحان کنید."),
  'invalid or unsupported panel backup':panelText("فایل خراب است یا پشتیبان سازگارِ این پنل نیست."),
  'backup preview expired; upload the file again':panelText("بررسی فایل منقضی شده است؛ فایل را دوباره انتخاب و بررسی کنید."),
  'choose a newer official panel version':panelText("ابتدا یک نسخهٔ رسمی جدیدتر را بررسی کنید."),
  'invalid panel domain':panelText("نام دامنه درست نیست؛ مانند panel.example.com وارد کنید."),
  'panel domain must contain ASCII letters, numbers or hyphens':panelText("دامنه را فقط با حروف انگلیسی، عدد، خط تیره و نقطه وارد کنید."),
  'enter a full domain such as panel.example.com':panelText("نام کامل دامنه را وارد کنید؛ مانند panel.example.com."),
  'access preview expired or is not ready':panelText("آدرس جدید آماده نیست یا مهلت آن گذشته است؛ دوباره آماده‌سازی کنید."),
  'another maintenance operation is running':panelText("یک عملیات نگهداری در حال انجام است؛ تا پایان آن صبر کنید."),
  'maintenance service unavailable':panelText("سرویس نگهداری در دسترس نیست. نصب‌کنندهٔ نسخهٔ فعلی را اجرا کنید.")
 }};
})();
