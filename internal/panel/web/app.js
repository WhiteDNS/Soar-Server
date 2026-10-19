'use strict';
const $ = id => document.getElementById(id);
const apiPath = path => new URL('api'+path,document.baseURI).href;
const states = {active:panelText("فعال"), disabled:panelText("غیرفعال"), expired:panelText("منقضی"), exhausted:panelText("حجم تمام‌شده")};
const pages = {dashboard:panelText("نمای کلی"), clients:panelText("حساب‌های کاربری"), events:panelText("رویدادهای مدیریت"), settings:panelText("تنظیمات سرور"), backup:panelText("پشتیبان و بازیابی"), updates:panelText("به‌روزرسانی پنل"), security:panelText("دامنه و امنیت"), admin:panelText("حساب مدیر")};
const events = {'admin.credentials_updated':panelText("تغییر اطلاعات ورود مدیر"),'backup.restored':panelText("بازیابی پشتیبان"),'maintenance.update_requested':panelText("درخواست به‌روزرسانی پنل"),'maintenance.restore_requested':panelText("درخواست بازیابی پشتیبان"),'access.updated':panelText("تغییر دامنه و آدرس امن"),'admin.login':panelText("ورود مدیر"),'backup.exported':panelText("دریافت پشتیبان"),'settings.updated':panelText("تغییر تنظیمات"),'client.created':panelText("ساخت حساب"),'client.updated':panelText("ویرایش حساب"),'client.deleted':panelText("حذف حساب"),'client.rotated':panelText("تعویض لینک حساب"),'client.usage_reset':panelText("صفر کردن مصرف"),'client.disconnected':panelText("قطع اتصال‌های باز"),'client.subscription_rotated':panelText("تعویض لینک ساب")};
let shareClient=null,shareMode='profile',directLink='',cottenLink='',subscriptionLink='',subscriptionURLs=[];
let csrf = '', clients = [], connections = {}, currentPage = 'dashboard', editing = null, sharedLink = '', busy = false, toastTimer;
const number = n => new Intl.NumberFormat(panelI18n.locale).format(n);
function bytes(n){if(n == null)return '—';if(!n)return '0 B';const units=['B','KiB','MiB','GiB','TiB'];const i=Math.min(4,Math.floor(Math.log(n)/Math.log(1024)));return `${(n/1024**i).toLocaleString('en-US',{maximumFractionDigits:i?2:0})} ${units[i]}`;}
function date(n){return n?new Date(n).toLocaleString(panelI18n.locale,{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit'}):panelText("بدون انقضا");}
function status(c){if(!c.enabled)return 'disabled';if(c.expires_at&&Date.now()>=c.expires_at)return 'expired';if(c.quota_bytes&&c.upload_bytes+c.download_bytes>=c.quota_bytes)return 'exhausted';return 'active';}
function node(tag,text,cls){const el=document.createElement(tag);if(text!=null)el.textContent=text;if(cls)el.className=cls;return el;}
function toast(text,kind='info'){
 const el=$('toast'),dialogs=[...document.querySelectorAll('dialog[open]')];
 // Native modal dialogs live above the document, including its z-index layers.
 (dialogs.at(-1)||document.body).append(el);
 el.textContent=text;el.dataset.kind=kind;el.hidden=false;
 clearTimeout(toastTimer);toastTimer=setTimeout(()=>el.hidden=true,4500);
}
document.querySelectorAll('dialog').forEach(d=>d.addEventListener('close',()=>{
 if(d.contains($('toast'))){$('toast').hidden=true;document.body.append($('toast'));}
}));
function message(err){const text=err.message||String(err);const map={...adminErrors,'Failed to fetch':panelText("ارتباط با پنل قطع شد. اتصال و آدرس پنل را بررسی کنید."),'invalid username or password':panelText("نام مدیر یا رمز عبور درست نیست."),'account changed; refresh and retry':panelText("این حساب هم‌زمان تغییر کرده است. فرم را ببندید و دوباره باز کنید."),'too many login attempts; retry after five minutes':panelText("تعداد تلاش‌ها زیاد است؛ پنج دقیقه دیگر امتحان کنید.")};if(text.startsWith('Certificate setup failed.'))return panelText("آماده‌سازی گواهی انجام نشد. رکوردهای A/AAAA و پورت‌های ۸۰ و ۴۴۳ را بررسی کنید؛ آدرس قبلی همچنان فعال است.");return map[text]||window.panelMaintenance?.messages?.[text]||text;}
async function api(path,method='GET',body){const response=await fetch(apiPath(path),{method,headers:{...(body!==undefined?{'Content-Type':'application/json'}:{}),...(method!=='GET'?{'X-CSRF-Token':csrf}:{})},body:body===undefined?undefined:JSON.stringify(body),credentials:'same-origin',cache:'no-store'});let value;try{value=await response.json();}catch{throw Error(panelText("پاسخ نامعتبر از پنل"));}if(!response.ok){if(response.status===401&&path!=='/login')signOut();throw Error(value.error||`HTTP ${response.status}`);}return value;}
async function withButton(button,fn){button.disabled=true;button.setAttribute('aria-busy','true');delete button.dataset.state;try{if(await fn()===true)button.dataset.state='success';}catch(e){button.dataset.state='error';toast(message(e),'error');}finally{button.disabled=false;button.removeAttribute('aria-busy');if(button.dataset.state)setTimeout(()=>delete button.dataset.state,2500);if(button.dataset.clientId){const target=document.querySelector('.account-actions-trigger[data-client-id="'+CSS.escape(button.dataset.clientId)+'"]');if(target?.getClientRects().length)target.focus({preventScroll:true});}}}
function signOut(){const wasSignedIn=!!csrf;selectedClients.clear();resetAdminForm();clients=[];connections={};$('clients-body').replaceChildren();updateSelectionControls();panelUI.closeActionMenu(false);panelUI.closeSelectMenu();csrf='';$('app').hidden=true;$('login-view').hidden=false;for(const d of document.querySelectorAll('dialog[open]'))d.close();if(wasSignedIn)$('login-password').value='';}
async function enter(token){csrf=token;$('login-view').hidden=true;$('app').hidden=false;$('login-password').value='';navigate();await refresh();window.panelMaintenance?.afterLogin();}
$('login-form').addEventListener('submit',async e=>{e.preventDefault();const button=e.submitter;button.disabled=true;$('login-error').textContent='';try{const v=await api('/login','POST',{name:$('login-name').value,password:$('login-password').value});await enter(v.csrf);}catch(err){$('login-error').textContent=message(err);}finally{button.disabled=false;}});
$('logout').onclick=e=>withButton(e.currentTarget,async()=>{await api('/logout','POST');signOut();});
function navigate(){if(currentPage==='admin')resetAdminForm();panelUI.closeActionMenu(false);panelUI.closeSelectMenu();currentPage=Object.hasOwn(pages,location.hash.slice(1))?location.hash.slice(1):'dashboard';$('page-title').textContent=pages[currentPage];for(const [key] of Object.entries(pages))$('page-'+key).hidden=key!==currentPage;for(const a of document.querySelectorAll('nav a')){if(a.dataset.page===currentPage)a.setAttribute('aria-current','page');else a.removeAttribute('aria-current');}panelUI.closeMenu();if(csrf&&(['settings','events','backup','updates','security','admin'].includes(currentPage)))loadPage().catch(e=>toast(message(e)));}
window.addEventListener('hashchange',navigate);
$('menu-toggle').onclick=()=>panelUI.toggleMenu();
$('open-clients').onclick=()=>location.hash='clients';
$('theme-toggle').onclick=()=>panelUI.toggleTheme();
async function refresh(){if(!csrf||busy)return;busy=true;try{const [d,c]=await Promise.all([api('/dashboard'),api('/clients')]);clients=c.clients;connections=c.connections;$('nav-count').textContent=number(clients.length);renderDashboard(d);renderClients();if(currentPage==='events')await loadPage();$('sync-state').textContent=panelText("به‌روز · ")+new Date().toLocaleTimeString(panelI18n.locale,{hour:'2-digit',minute:'2-digit'});$('sync-state').classList.remove('stale');$('page-error').hidden=true;}catch(e){$('sync-state').textContent=panelText("ارتباط برقرار نیست");$('sync-state').classList.add('stale');panelUI.setConnection(false);$('page-error').textContent=message(e);$('page-error').hidden=false;}finally{busy=false;}}
$('refresh').onclick=e=>withButton(e.currentTarget,refresh);
setInterval(()=>{if(!document.hidden)refresh();},5000);
function renderDashboard(d){panelUI.renderDashboard(d);}
function actionButton(label,fn){const b=node('button',label);b.type='button';b.onclick=e=>withButton(e.currentTarget,fn);return b;}
const selectedClients=new Set();
let bulkDeleting=false;
function visibleClients(){const query=$('search').value.trim().toLowerCase(),filter=$('status-filter').value;return clients.filter(c=>(!filter||status(c)===filter)&&(!query||(c.name+' '+c.note+' '+c.zone).toLowerCase().includes(query)));}
function updateSelectionControls(){
 const rows=visibleClients(),count=rows.filter(c=>selectedClients.has(c.id)).length,all=$('select-all-clients');
 all.checked=rows.length>0&&count===rows.length;all.indeterminate=count>0&&count<rows.length;all.disabled=!rows.length||bulkDeleting;
 for(const check of $('clients-body').querySelectorAll('[data-selection-id]'))check.disabled=bulkDeleting;
 $('bulk-toolbar').hidden=!count;$('selection-count').textContent=number(count)+panelText(" انتخاب‌شده");$('bulk-delete').disabled=!count||bulkDeleting;
}
function renderClients(){
 if(panelUI.accountMenuOpen())return;
 const rows=visibleClients(),visible=new Set(rows.map(c=>c.id));
 for(const id of selectedClients)if(!visible.has(id))selectedClients.delete(id);
 const focused=document.activeElement?.dataset.selectionId;
 $('filtered-count').textContent=number(rows.length)+panelText(" حساب");$('clients-empty').hidden=rows.length!==0;
 $('clients-body').replaceChildren(...rows.map(c=>{
  const tr=node('tr'),selection=node('td',null,'selection-cell'),check=node('input');check.type='checkbox';check.dataset.selectionId=c.id;check.checked=selectedClients.has(c.id);check.setAttribute('aria-label',panelText("انتخاب حساب ")+c.name);tr.classList.toggle('selected',check.checked);
  check.onchange=()=>{if(check.checked)selectedClients.add(c.id);else selectedClients.delete(c.id);tr.classList.toggle('selected',check.checked);updateSelectionControls();};selection.append(check);
  const name=node('td');name.append(node('strong',c.name),node('span',c.connect_zone||c.zone,'domain'),node('small',c.engines==='both'?'Soar + Cotten':c.engines==='cotten'?'Cotten':'Soar','domain'));
  const state=node('td');state.append(node('span',states[status(c)],'badge '+status(c)));
  const usage=node('td'),used=c.upload_bytes+c.download_bytes;usage.append(node('span',bytes(used)+' / '+(c.quota_bytes?bytes(c.quota_bytes):'∞'),'usage'));
  if(c.quota_bytes){const p=node('progress');p.max=c.quota_bytes;p.value=used;p.setAttribute('aria-label',panelText("مصرف حجم ")+c.name);usage.append(p);}
  const count=node('td',number(connections[c.id]||0)),actions=node('td'),bar=node('div',null,'row-actions');
  bar.append(actionButton(panelText("لینک / QR"),()=>share(c)),actionButton(panelText("ویرایش"),async()=>openClient(c)));bar.append(panelUI.accountActions(c,(value,button)=>withButton(button,()=>clientAction(clients.find(current=>current.id===c.id)||c,value))));actions.append(bar);
  tr.append(selection,name,state,usage,node('td',date(c.expires_at)),count,actions);return tr;
 }));
 updateSelectionControls();if(focused)$('clients-body').querySelector('[data-selection-id="'+CSS.escape(focused)+'"]')?.focus({preventScroll:true});
}
function clearSelection(){selectedClients.clear();panelUI.closeActionMenu(false);renderClients();}
$('search').addEventListener('input',clearSelection);$('status-filter').onchange=clearSelection;
$('clear-selection').onclick=clearSelection;
$('select-all-clients').onchange=e=>{selectedClients.clear();if(e.currentTarget.checked)for(const c of visibleClients())selectedClients.add(c.id);renderClients();};
$('bulk-delete').onclick=async e=>{
 if(bulkDeleting)return;bulkDeleting=true;updateSelectionControls();
 await withButton(e.currentTarget,async()=>{
  const selection=visibleClients().filter(c=>selectedClients.has(c.id)),ids=selection.map(c=>c.id);if(!ids.length)return;
  const names=selection.slice(0,5).map(c=>c.name).join('، ')+(ids.length>5?' …':'');
  if(!await confirmAction(panelText("حذف حساب‌های انتخاب‌شده")+' ('+number(ids.length)+')',names+'\n'+panelText("حساب‌های انتخاب‌شده، کلیدها، لینک‌های ساب و مصرف آن‌ها حذف و اتصال‌هایشان قطع می‌شوند. این عمل برگشت‌پذیر نیست.")))return;
  try{await api('/clients/delete','POST',{ids});selectedClients.clear();toast(panelText("حساب‌های انتخاب‌شده حذف شدند."),'success');}finally{await refresh();}
 });bulkDeleting=false;updateSelectionControls();
};
const adminErrors={"current administrator credentials are incorrect":panelText("نام کاربری یا رمز فعلی مدیر درست نیست."),"administrator credentials changed; sign in again":panelText("اطلاعات مدیر هم‌زمان تغییر کرده است؛ دوباره وارد شوید."),"administrator name must be 1-64 letters, numbers, dots, underscores or hyphens":panelText("نام کاربری باید ۱ تا ۶۴ حرف انگلیسی، عدد، نقطه، خط تیره یا زیرخط باشد."),"new password must be 12-72 printable ASCII characters":panelText("رمز جدید باید ۱۲ تا ۷۲ نویسهٔ قابل چاپ انگلیسی باشد."),"cannot read administrator account":panelText("اطلاعات حساب مدیر دریافت نشد. دوباره تلاش کنید."),"select between 1 and 512 accounts":panelText("بین ۱ تا ۵۱۲ حساب انتخاب کنید."),"invalid or duplicate account ID":panelText("فهرست حساب‌ها معتبر نیست؛ صفحه را به‌روز و دوباره انتخاب کنید."),"account not found":panelText("یکی از حساب‌ها دیگر وجود ندارد؛ هیچ حسابی از این درخواست حذف نشد. صفحه را به‌روز کنید."),"accounts deleted; runtime synchronization failed":panelText("حساب‌ها حذف شدند، اما همگام‌سازی اتصال‌ها کامل نشد. وضعیت سرویس را بررسی کنید.")};
function resetAdminForm(){
 $('admin-form').reset();$('admin-current-name').value='';$('admin-new-name').value='';$('admin-feedback').textContent='';$('admin-save').disabled=true;
 for(const button of document.querySelectorAll('[data-admin-password]')){$(button.dataset.adminPassword).type='password';button.setAttribute('aria-pressed','false');button.setAttribute('aria-label',panelText("نمایش رمز عبور"));}
}
async function loadAdmin(){
 resetAdminForm();const v=await api('/admin');if(!csrf||currentPage!=='admin')return;
 $('admin-current-name').value=v.name;$('admin-new-name').value=v.name;$('admin-save').disabled=false;
}
for(const button of document.querySelectorAll('[data-admin-password]'))button.onclick=()=>{const input=$(button.dataset.adminPassword),shown=input.type==='password';input.type=shown?'text':'password';button.setAttribute('aria-pressed',String(shown));button.setAttribute('aria-label',shown?panelText("پنهان کردن رمز عبور"):panelText("نمایش رمز عبور"));};
$('admin-form').onsubmit=async e=>{
 e.preventDefault();$('admin-feedback').textContent='';const password=$('admin-new-password').value;
 if(password!==$('admin-repeat-password').value){$('admin-feedback').textContent=panelText("رمز جدید و تکرار آن یکسان نیستند.");return;}
 if(password&&!/^[\x20-\x7E]{12,72}$/.test(password)){$('admin-feedback').textContent=panelText("رمز جدید باید ۱۲ تا ۷۲ نویسهٔ قابل چاپ انگلیسی باشد.");return;}
 const name=$('admin-new-name').value;
 await withButton(e.submitter,async()=>{
  try{await api('/admin','PUT',{current_name:$('admin-current-name').value,current_password:$('admin-current-password').value,new_name:name,new_password:password});
   signOut();$('login-name').value=name;$('login-notice').textContent=panelText("اطلاعات ورود تغییر کرد. با اطلاعات جدید وارد شوید.");$('login-password').focus();
  }catch(err){$('admin-feedback').textContent=message(err);throw err;}
 });
};
function openClient(c){editing=c;const f=$('client-form');f.reset();$('client-form-error').textContent='';$('client-dialog-title').textContent=c?panelText("ویرایش حساب"):panelText("حساب جدید");f.elements.name.value=c?.name||'';f.elements.engines.value=c?(c.engines||'soar'):'both';panelUI.syncSelects();f.elements.quota.value=c?Number((c.quota_bytes/1024**3).toFixed(9)):0;f.elements.rate.value=c?c.rate_bytes/1024:0;f.elements.streams.value=c?.stream_limit||128;f.elements.note.value=c?.note||'';f.elements.enabled.checked=c?c.enabled:true;if(c?.expires_at){const local=new Date(c.expires_at-new Date(c.expires_at).getTimezoneOffset()*60000);f.elements.expires.value=local.toISOString().slice(0,16);}$('client-dialog').showModal();}
document.querySelectorAll('.new-client').forEach(b=>b.onclick=()=>openClient(null));
document.querySelectorAll('.close-dialog').forEach(b=>b.onclick=()=>b.closest('dialog').close());
$('client-form').onsubmit=async e=>{e.preventDefault();const button=e.submitter;button.disabled=true;$('client-form-error').textContent='';const f=e.currentTarget,old=editing;const body={engines:f.elements.engines.value,name:f.elements.name.value,note:f.elements.note.value,enabled:f.elements.enabled.checked,quota_bytes:Math.round(Number(f.elements.quota.value)*1024**3),rate_bytes:Math.round(Number(f.elements.rate.value)*1024),stream_limit:Number(f.elements.streams.value),expires_at:f.elements.expires.value?new Date(f.elements.expires.value).getTime():0,revision:old?.revision||0};try{await api('/clients'+(old?'/'+old.id:''),old?'PUT':'POST',body);$('client-dialog').close();await refresh();toast(old?panelText("تغییرات ذخیره شد."):panelText("حساب ساخته شد؛ لینک آن را از جدول دریافت کنید."));}catch(err){$('client-form-error').textContent=message(err);}finally{button.disabled=false;}};
function confirmAction(title,text){return new Promise(resolve=>{const d=$('confirm-dialog');$('confirm-title').textContent=title;$('confirm-text').textContent=text;let answer=false;$('confirm-ok').onclick=()=>{answer=true;d.close();};$('confirm-cancel').onclick=()=>d.close();d.addEventListener('close',()=>resolve(answer),{once:true});d.showModal();$('confirm-cancel').focus();});}
async function clientAction(c,action){const titles={toggle:c.enabled?panelText("غیرفعال کردن حساب"):panelText("فعال کردن حساب"),disconnect:panelText("قطع اتصال‌های باز"),reset:panelText("صفر کردن مصرف"),rotate:panelText("تعویض لینک و کلید"),delete:panelText("حذف حساب")};const details={toggle:c.enabled?panelText("تمام اتصال‌های این حساب بسته می‌شوند. لینک تا فعال‌سازی دوباره قابل استفاده نیست."):panelText("این حساب در صورت داشتن حجم و اعتبار زمانی اجازهٔ اتصال خواهد داشت."),disconnect:panelText("جریان‌های باز بسته می‌شوند. حساب فعال می‌تواند دوباره وصل شود."),reset:panelText("مصرف آپلود و دانلود صفر و اتصال‌های فعلی بسته می‌شوند. این عمل برگشت‌پذیر نیست."),rotate:panelText("لینک قبلی و اتصال‌های آن باطل می‌شوند. باید لینک جدید را به صاحب حساب بدهید. مصرف صفر نمی‌شود."),delete:panelText("حساب، کلید و سابقهٔ مصرف آن حذف می‌شوند. این عمل برگشت‌پذیر نیست.")};if(!await confirmAction(titles[action]+' «'+c.name+'»',details[action]))return false;if(action==='toggle'){await api('/clients/'+c.id,'PUT',{engines:c.engines||'soar',name:c.name,note:c.note,enabled:!c.enabled,quota_bytes:c.quota_bytes,rate_bytes:c.rate_bytes,stream_limit:c.stream_limit,expires_at:c.expires_at,revision:c.revision});}else await api('/clients/'+c.id+(action==='delete'?'':'/'+action),action==='delete'?'DELETE':'POST');await refresh();toast(panelText("انجام شد."),'success');return true;}
function showShareMode(mode){
 shareMode=mode;const sub=mode==='subscription';sharedLink=sub?subscriptionLink:mode==='cotten'?cottenLink:directLink;
 $('share-link').value=sharedLink;$('share-qr').alt=sub?panelText("لینک ساب"):(mode==='cotten'?panelText("QR پروفایل Cotten"):panelText("QR پروفایل Soar"));$('share-qr').src=apiPath('/clients/'+shareClient.id+(sub?'/subscription-qr.png':'/qr.png?engine='+(mode==='cotten'?'cotten':'soar')));
 $('share-link-label').textContent=sub?panelText("لینک ساب"):panelText("لینک ورود پروفایل");
 $('share-hint').textContent=sub?panelText("این آدرس را در WhiteDNS 1.6.8 یا جدیدتر وارد کنید. با تازه‌سازی ساب، تغییرات تنظیمات همین حساب دریافت می‌شوند. این لینک محرمانه است."):panelText("این لینک را در بخش Import اپ WhiteDNS وارد کنید. لینک را فقط در اختیار صاحب حساب قرار دهید.");
 $('rotate-subscription').hidden=!sub;$('share-alternatives-label').hidden=!sub||subscriptionURLs.length<2;$('share-alternatives').value=subscriptionURLs.join('\n');
 for(const [id,active] of [['share-profile-mode',mode==='profile'],['share-cotten-mode',mode==='cotten'],['share-sub-mode',sub]]){$(id).className=active?'secondary':'quiet';$(id).setAttribute('aria-pressed',String(active));}
}
async function share(c){const [v,sub]=await Promise.all([api('/clients/'+c.id+'/link'),api('/clients/'+c.id+'/subscription')]);shareClient=c;directLink=v.links?.soar||(v.link.startsWith('soar://')?v.link:'');cottenLink=v.links?.cotten||'';subscriptionLink=sub.url;subscriptionURLs=sub.urls||[sub.url];$('share-profile-mode').hidden=!directLink;$('share-cotten-mode').hidden=!cottenLink;$('share-title').textContent=c.name;showShareMode(directLink?'profile':'cotten');$('share-dialog').showModal();}
$('share-profile-mode').onclick=()=>showShareMode('profile');
$('share-cotten-mode').onclick=()=>showShareMode('cotten');
$('share-sub-mode').onclick=()=>showShareMode('subscription');
$('rotate-subscription').onclick=e=>withButton(e.currentTarget,async()=>{if(!await confirmAction(panelText("تعویض لینک ساب"),panelText("آدرس ساب قبلی باطل می‌شود. پروفایل‌های قبلاً دریافت‌شده و کلید تونل تغییر نمی‌کنند. لینک تازه را به صاحب حساب بدهید.")))return;const v=await api('/clients/'+shareClient.id+'/subscription-rotate','POST',{});subscriptionLink=v.url;subscriptionURLs=v.urls||[v.url];showShareMode('subscription');toast(panelText("لینک ساب تعویض شد."),'success');});
async function copyProfileLink(){
 if(navigator.clipboard&&window.isSecureContext){
  try{await navigator.clipboard.writeText(sharedLink);return;}catch{/* Try the HTTP-compatible path if clipboard permission is denied. */}
 }
 // Keep the source inside the active modal: body-level fields are inert here.
 const field=$('share-link'),start=field.selectionStart,end=field.selectionEnd;
 field.focus({preventScroll:true});field.select();field.setSelectionRange(0,field.value.length);
 let copied=false;
 try{copied=document.execCommand('copy');}catch{/* Report failure below, never a false success. */}
 if(!copied)throw Error(panelText("مرورگر اجازهٔ کپی نداد. لینک را انتخاب کرده‌ایم؛ از منوی مرورگر کپی کنید یا «دانلود لینک» را بزنید."));
 field.setSelectionRange(start,end);
}
$('copy-link').onclick=e=>withButton(e.currentTarget,async()=>{await copyProfileLink();toast(panelText("لینک کپی شد."),'success');});
function download(blob,name){const url=URL.createObjectURL(blob),a=node('a');a.href=url;a.download=name;document.body.append(a);a.click();a.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);}
$('save-profile').onclick=()=>download(new Blob([sharedLink+'\n'],{type:'text/plain'}),shareMode==='subscription'?'whitedns-subscription-url.txt':shareMode==='cotten'?'cottendns-profile.txt':'soar-profile.txt');
$('download-backup').onclick=e=>withButton(e.currentTarget,async()=>{if(!await confirmAction(panelText("دانلود پشتیبان کامل"),panelText("این فایل شامل کلیدهای خصوصی حساب‌هاست. آیا آن را در جای امن نگهداری می‌کنید؟")))return;const response=await fetch(apiPath('/backup'),{credentials:'same-origin',cache:'no-store'});if(!response.ok)throw Error(panelText("پشتیبان دریافت نشد. دوباره وارد پنل شوید."));download(await response.blob(),'soar-panel-'+new Date().toISOString().slice(0,10)+'.db');toast(panelText("پشتیبان دریافت شد."));});
$('export-csv').onclick=()=>{const fields=['Name','Zone','Status','Upload bytes','Download bytes','Quota bytes','Expires UTC','Stream limit'];const cell=v=>'"'+String(v).replaceAll('"','""')+'"';const csv=[fields,...clients.map(c=>[c.name,c.zone,status(c),c.upload_bytes,c.download_bytes,c.quota_bytes,c.expires_at?new Date(c.expires_at).toISOString():'',c.stream_limit])].map(row=>row.map(v=>cell(/^[=+@\-\t\r]/.test(String(v))?"'"+v:v)).join(',')).join('\r\n');download(new Blob(['\uFEFF'+csv],{type:'text/csv;charset=utf-8'}),'soar-accounts.csv');};
async function loadPage(){if(currentPage==='admin'){await loadAdmin();return;}if(['backup','updates','security'].includes(currentPage)){await window.panelMaintenance?.load(currentPage);return;}if(currentPage==='settings'){const s=await api('/settings');$('base-zone').value=s.base_zone;$('country').value=s.country;$('resolvers').value=s.resolvers.join('\n');$('allow-ports').value=s.allow_ports.join(', ');soarSettingsForm.load(s.soarSettings);}else if(currentPage==='events'){const list=await api('/events');$('events-list').replaceChildren(...list.map(ev=>{const li=node('li'),title=node('div',events[ev.action]||ev.action);if(ev.client_id){const c=clients.find(c=>c.id===ev.client_id);title.append(node('small',c?c.name:ev.client_id));}const t=node('time',date(ev.at));t.dateTime=new Date(ev.at).toISOString();li.append(title,t);return li;}));if(!list.length)$('events-list').append(node('li',panelText("هنوز رویدادی ثبت نشده است.")));}}
$('settings-form').onsubmit=e=>{e.preventDefault();if(!soarSettingsForm.validate())return;withButton(e.submitter,async()=>{const allow=$('allow-ports').value.split(/[,\s،]+/).filter(Boolean).map(Number);if(allow.some(n=>!Number.isInteger(n)||n<1||n>65535))throw Error(panelText("پورت باید عددی بین ۱ تا ۶۵۵۳۵ باشد."));await api('/settings','PUT',{base_zone:$('base-zone').value,country:$('country').value,resolvers:$('resolvers').value.split(/[,\s،]+/).filter(Boolean),allow_ports:allow,soarSettings:soarSettingsForm.read()});$('settings-feedback').textContent=panelText("ذخیره شد. لینک همان حساب را دوباره بگیرید و در اپ Import کنید.");$('preset-state').textContent=panelText("مقادیر ذخیره‌شدهٔ سرور");toast(panelText("تنظیمات ذخیره شد؛ لینک حساب را دوباره Import کنید."));});};
(async()=>{try{const s=await api('/session');await enter(s.csrf);}catch{signOut();}})();
