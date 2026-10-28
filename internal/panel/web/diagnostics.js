'use strict';
window.panelDiagnostics=(()=>{
 const tr=panelText;
 let config=null,logsBusy=false,refreshPending=false,live=true,entries=[],proposal=null,undo=null,controller=null,generation=0,searchTimer;
 let modelsController=null;
 function selectedModel(){return $('assistant-model-manual').checked?$('assistant-model-custom').value.trim():$('assistant-model').value;}
 function modelOptions(models=[],selected=''){
  const options=[new Option(tr(models.length?'مدل را انتخاب کنید':'ابتدا مدل‌ها را دریافت کنید'),'')];
  if(selected&&!models.some(m=>m.id===selected))options.push(new Option(selected,selected));
  for(const model of models)options.push(new Option(model.name===model.id?model.id:model.name+' · '+model.id,model.id));
  $('assistant-model').replaceChildren(...options);$('assistant-model').value=selected;panelUI.syncSelects();
 }
 function modelMode(){const manual=$('assistant-model-manual').checked;$('assistant-model').disabled=manual;$('assistant-model-custom').disabled=!manual;$('assistant-model-custom').required=manual;$('assistant-model-custom-field').hidden=!manual;panelUI.syncSelects();}
 function invalidateModels(clear=false){modelsController?.abort();modelsController=null;$('assistant-fetch-models').disabled=false;$('assistant-fetch-models').removeAttribute('aria-busy');$('assistant-models-state').textContent='';modelOptions([],clear?'':$('assistant-model').value);}
 const codeNames={manager_started:'شروع مدیریت سرور',engines_restarted:'راه‌اندازی مجدد موتورها',engine_restart_failed:'راه‌اندازی مجدد موتورها ناموفق بود',reconcile_failed:'همگام‌سازی موتور ناموفق بود',engine_started:'موتور شروع شد',engine_stopped:'موتور متوقف شد',engine_unexpected_exit:'خروج غیرمنتظرهٔ موتور',engine_start_failed:'شروع موتور ناموفق بود',engine_close_failed:'بستن موتور ناموفق بود',engine_warning:'هشدار داخلی موتور',engine_error:'خطای داخلی موتور',connection_opened:'اتصال خروجی برقرار شد',connection_failed:'اتصال خروجی ناموفق بود',resolver_connection_failed:'اتصال به resolver ناموفق بود',connection_rejected:'اتصال رد شد',connection_close_failed:'بستن اتصال ناموفق بود',dns_queue_full:'صف DNS پُر است',dns_udp_write_failed:'ارسال پاسخ UDP ناموفق بود',dns_tcp_accept_retry:'تلاش مجدد برای پذیرش TCP',resolver_query_failed:'پرس‌وجوی resolver ناموفق بود',resolver_refused_or_servfail:'resolver پاسخ REFUSED یا SERVFAIL داد',resolver_invalid_response:'پاسخ resolver معتبر نیست',stream_read_failed:'خواندن جریان ناموفق بود',stream_write_failed:'نوشتن جریان ناموفق بود',assistant_provider_failed:'ارتباط با ارائه‌دهندهٔ هوش مصنوعی ناموفق بود'};
 const reasons={timeout:'مهلت اتصال تمام شد',dns_timeout:'مهلت DNS تمام شد',dns_not_found:'نام DNS پیدا نشد',dns_failure:'خطای حل نام DNS',connection_refused:'مقصد اتصال را رد کرد',connection_reset:'اتصال از سمت مقابل قطع شد',network_unreachable:'مسیر شبکه در دسترس نیست',file_descriptor_limit:'سقف فایل‌های باز سیستم پُر است',account_inactive:'حساب غیرفعال، منقضی یا حجم آن تمام شده است',account_stream_limit:'سقف جریان هم‌زمان حساب پُر است',server_stream_limit:'سقف جریان هم‌زمان سرور پُر است',destination_policy:'سیاست دسترسی مقصد اجازهٔ اتصال نمی‌دهد',antiflood:'کنترل ضدفلاد اتصال را محدود کرد',cancelled:'عملیات لغو شد',closed:'اتصال بسته است',internal_error:'خطای داخلی؛ شمارنده‌ها و وضعیت موتور را بررسی کنید'};
 const levels={info:'اطلاعات',warn:'هشدار',error:'خطا'};
 function label(code){
  if(codeNames[code])return tr(codeNames[code]);
  if(code.endsWith('_request_failed'))return tr('خطای پردازش درخواست پنل');
  if(code.endsWith('_request_rejected'))return tr('درخواست پنل رد شد');
  if(code.endsWith('_request_completed'))return tr('عملیات پنل انجام شد');
  return code;
 }
 function logRow(e){
  const li=node('li',null,'diagnostic-entry');li.dataset.level=e.level;
  const top=node('div',null,'diagnostic-entry-top'),time=node('time',new Date(e.last_at).toLocaleString(panelI18n.locale));time.dateTime=new Date(e.last_at).toISOString();time.title=time.dateTime;
  top.append(node('span',tr(levels[e.level]||e.level),'diagnostic-level'),node('bdi',e.source),time);
  li.append(top,node('p',label(e.code)+(e.reason?' · '+tr(reasons[e.reason]||e.reason):'')));
  const meta=node('div',null,'diagnostic-entry-meta');meta.append(node('code',e.code+(e.reason?' / '+e.reason:'')));
  if(e.account_id){const account=clients.find(c=>c.id===e.account_id);meta.append(node('span',account?account.name:e.account_id.slice(0,8)));}
  if(e.count>1)meta.append(node('span','× '+number(e.count)));
  if(e.duration_ms)meta.append(node('bdi',e.duration_ms+' ms'));
  if(e.http_status)meta.append(node('bdi',(e.operation||'')+' HTTP '+e.http_status));
  li.append(meta);return li;
 }
 function filters(){return {source:$('logs-source').value,level:$('logs-level').value,account:$('logs-account').value,q:$('logs-search').value};}
 function query(){const p=new URLSearchParams({...filters(),limit:'500'});const range=Number($('logs-range').value);if(range)p.set('since',String(Date.now()-range));return p;}
 function syncAccounts(){
  const select=$('logs-account'),value=select.value,signature=clients.map(c=>c.id+':'+c.name).join('|');
  if(select.dataset.accounts===signature)return;
  select.replaceChildren(new Option(tr('همهٔ حساب‌ها'),''),...clients.map(c=>new Option(c.name,c.id)));select.value=value;select.dataset.accounts=signature;panelUI.syncSelects();
 }
 function errorBox(id,error){const el=$(id);el.textContent=error?message(error):'';el.hidden=!error;}
 async function refresh(force=false){
  if(!csrf||!force&&!live)return;if(logsBusy){refreshPending ||=force;return;}
  logsBusy=true;const ticket=generation;
  try{
   syncAccounts();const report=await api('/logs?'+query());if(ticket!==generation||!csrf)return;
   entries=report.entries;$('diagnostic-entries').replaceChildren(...entries.map(logRow));$('logs-empty').hidden=entries.length>0;
   $('logs-state').textContent=(live?tr('زنده'):tr('متوقف'))+' · '+number(entries.length)+' / '+number(report.retained);
   $('logs-storage-error').hidden=!report.storage_error;errorBox('logs-error',null);
  }catch(e){if(ticket===generation)errorBox('logs-error',e);}finally{logsBusy=false;if(refreshPending){refreshPending=false;if(csrf&&currentPage==='logs')void refresh(true);}}
 }
 function showConfig(value){
  invalidateModels();
  config=value;
  $('assistant-provider').replaceChildren(...value.providers.map(p=>new Option(p.name,p.id)));
  for(const [id,key] of [['provider','provider'],['protocol','protocol'],['base-url','base_url'],['max-tokens','max_tokens']])$('assistant-'+id).value=value[key];
  modelOptions([],value.model||'');$('assistant-model-manual').checked=false;$('assistant-model-custom').value='';modelMode();
  $('assistant-enabled').checked=value.enabled;$('assistant-json-mode').checked=value.json_mode;$('assistant-api-key').value='';$('assistant-clear-key').checked=false;$('assistant-consent').checked=false;
  $('assistant-key-state').textContent=tr(value.has_key?'کلید ذخیره شده است؛ برای حفظ آن خالی بگذارید.':'کلیدی ذخیره نشده است؛ سرویس‌های محلی ممکن است به کلید نیاز نداشته باشند.');
  $('assistant-destination').textContent=value.base_url+' · '+(value.model||'—');
  $('assistant-status').textContent=tr(value.enabled?'دستیار آماده است؛ ارسال گزارش فقط با اقدام شما انجام می‌شود.':'دستیار غیرفعال است؛ اتصال را تنظیم و ذخیره کنید.');
  $('assistant-send').disabled=!value.enabled;
  panelUI.syncSelects();
  if(!value.enabled||value.load_error)$('assistant-setup').open=true;
  if(value.load_error)errorBox('assistant-error',Error(tr('فایل تنظیمات هوش مصنوعی خوانده نشد؛ اتصال را دوباره ذخیره کنید.')));
 }
 async function load(){const ticket=generation,route=navigationId;await refresh(true);const [value,conversation]=await Promise.all([api('/assistant/config'),api('/assistant/conversation')]);if(ticket!==generation||route!==navigationId||currentPage!=='logs'||!csrf)return;showConfig(value);if(!controller){if(conversation.messages?.length){$('assistant-chat').replaceChildren();for(const m of conversation.messages)appendTurn(m.role,m.content);}renderProposal(conversation.proposal);undo=conversation.undo;$('assistant-undo-panel').hidden=!undo;}}
 $('assistant-provider').addEventListener('change',()=>{
  const preset=config?.providers.find(p=>p.id===$('assistant-provider').value);if(!preset)return;
  $('assistant-protocol').value=preset.protocol;$('assistant-base-url').value=preset.base_url;$('assistant-model').value='';$('assistant-api-key').value='';$('assistant-clear-key').checked=true;
  invalidateModels(true);$('assistant-model-manual').checked=false;$('assistant-model-custom').value='';modelMode();
  panelUI.syncSelects();
 });
 for(const id of ['assistant-base-url','assistant-api-key'])$(id).addEventListener('input',()=>invalidateModels(id==='assistant-base-url'));
 $('assistant-protocol').addEventListener('change',()=>invalidateModels(true));
 $('assistant-clear-key').addEventListener('change',()=>invalidateModels());
 $('assistant-model-manual').addEventListener('change',()=>{invalidateModels();if($('assistant-model-manual').checked)$('assistant-model-custom').value=$('assistant-model').value;modelMode();});
 $('assistant-fetch-models').onclick=async()=>{
  if(!config||modelsController)return;
  const active=new AbortController();modelsController=active;const ticket=generation,button=$('assistant-fetch-models');
  button.disabled=true;button.setAttribute('aria-busy','true');$('assistant-models-state').textContent=tr('در حال دریافت مدل‌ها…');errorBox('assistant-error',null);
  try{
   const response=await fetch(apiPath('/assistant/models'),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({provider:$('assistant-provider').value,protocol:$('assistant-protocol').value,base_url:$('assistant-base-url').value.trim(),api_key:$('assistant-api-key').value.trim(),clear_key:$('assistant-clear-key').checked&&!$('assistant-api-key').value.trim(),revision:config.revision}),signal:active.signal,credentials:'same-origin',cache:'no-store'});
   const value=await response.json();if(modelsController!==active||ticket!==generation||!csrf)return;
   if(!response.ok){if(response.status===401)signOut();throw Error(value.error||`HTTP ${response.status}`);}
   modelOptions(value.models,selectedModel());$('assistant-model-manual').checked=false;modelMode();
   $('assistant-models-state').textContent=tr('مدل‌های دریافت‌شده: ')+number(value.models.length);
  }catch(error){if(modelsController===active&&error.name!=='AbortError'){$('assistant-models-state').textContent=tr('دریافت مدل‌ها ناموفق بود؛ دوباره تلاش کنید یا ورود دستی را فعال کنید.');errorBox('assistant-error',error);}}
  finally{if(modelsController===active){modelsController=null;button.disabled=false;button.removeAttribute('aria-busy');}}
 };
 $('assistant-config-form').addEventListener('submit',e=>{e.preventDefault();withButton(e.submitter,async()=>{
  if(!selectedModel()){errorBox('assistant-error',Error(tr('ابتدا یک مدل انتخاب کنید.')));return false;}
  const value=await api('/assistant/config','PUT',{provider:$('assistant-provider').value,protocol:$('assistant-protocol').value,base_url:$('assistant-base-url').value.trim(),model:selectedModel(),api_key:$('assistant-api-key').value.trim(),clear_key:$('assistant-clear-key').checked&&!$('assistant-api-key').value.trim(),enabled:$('assistant-enabled').checked,json_mode:$('assistant-json-mode').checked,max_tokens:Number($('assistant-max-tokens').value),revision:config.revision});
  resetConversation();showConfig(value);$('assistant-setup').open=false;errorBox('assistant-error',null);toast(tr('اتصال هوش مصنوعی ذخیره شد.'),'success');return true;
 });});
 function appendTurn(role,text){const turn=node('div',null,'assistant-turn');turn.dataset.role=role;turn.append(node('strong',tr(role==='user'?'شما':'دستیار')),node('p',text));const chat=$('assistant-chat');chat.querySelector('.assistant-welcome')?.remove();chat.append(turn);while(chat.children.length>12)chat.firstElementChild.remove();chat.scrollTop=chat.scrollHeight;return turn;}
 function renderProposal(value){
  proposal=value;$('assistant-proposal').hidden=!value;if(!value)return;
  $('assistant-proposal-reason').textContent=value.reason;$('assistant-diff').replaceChildren(...value.changes.map(c=>{const row=node('div');row.append(node('dt',c.field),node('dd',JSON.stringify(c.before)+' → '+JSON.stringify(c.after)));return row;}));
  $('assistant-proposal-expiry').textContent=tr('مهلت تأیید: ')+new Date(value.expires_at).toLocaleTimeString(panelI18n.locale);
 }
 async function contextPreview(){const value=await api('/assistant/context?'+new URLSearchParams(filters()));$('assistant-context-preview').textContent=JSON.stringify(value,null,2);}
 $('assistant-context-refresh').onclick=e=>withButton(e.currentTarget,contextPreview);
 $('assistant-context-details').addEventListener('toggle',()=>{if($('assistant-context-details').open)contextPreview().catch(e=>errorBox('assistant-error',e));});
 $('assistant-chat-form').addEventListener('submit',async e=>{
  e.preventDefault();if(controller||!config)return;
  if(!$('assistant-consent').checked){errorBox('assistant-error',Error(tr('ابتدا ارسال به مقصد نمایش‌داده‌شده را تأیید کنید.')));return;}
  const question=$('assistant-question').value.trim();if(!question)return;
  const ticket=generation;controller=new AbortController();const active=controller;
  $('assistant-send').disabled=true;$('assistant-send').setAttribute('aria-busy','true');$('assistant-cancel').hidden=false;errorBox('assistant-error',null);
  const turn=appendTurn('user',question);turn.dataset.pending='true';renderProposal(null);
  try{
   const response=await fetch(apiPath('/assistant/chat'),{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf},body:JSON.stringify({message:question,include_context:$('assistant-include-context').checked,consent:true,revision:config.revision,destination:config.base_url,...filters()}),signal:active.signal,credentials:'same-origin',cache:'no-store'});
   const value=await response.json();if(ticket!==generation||!csrf)return;
   if(!response.ok){if(response.status===401)signOut();throw Error(value.error||`HTTP ${response.status}`);}
   delete turn.dataset.pending;appendTurn('assistant',value.answer);$('assistant-question').value='';renderProposal(value.proposal);if(value.proposal_rejected)errorBox('assistant-error',Error(tr('پیشنهاد قابل اعمال نیست: ')+value.proposal_rejected));
  }catch(err){if(ticket===generation){turn.remove();errorBox('assistant-error',err.name==='AbortError'?Error(tr('درخواست لغو شد.')):err);}}
  finally{if(controller===active){controller=null;$('assistant-cancel').hidden=true;$('assistant-send').removeAttribute('aria-busy');$('assistant-send').disabled=!config?.enabled;}}
 });
 $('assistant-cancel').onclick=()=>controller?.abort();
 $('assistant-apply').onclick=e=>withButton(e.currentTarget,async()=>{
  const p=proposal;if(!p)return;
  const diff=p.changes.map(c=>c.field+': '+JSON.stringify(c.before)+' → '+JSON.stringify(c.after)).join('\n');
  if(!await confirmAction(tr('تأیید تغییر تنظیمات توسط دستیار'),diff+'\n\n'+tr('موتورها راه‌اندازی مجدد و جریان‌های فعال قطع می‌شوند.')))return;
  const result=await api('/assistant/apply','POST',{id:p.id,confirm:true});renderProposal(null);undo=result.undo;$('assistant-undo-panel').hidden=!undo;
  if(!result.runtime_synchronized)errorBox('assistant-error',Error(tr('تنظیمات ذخیره شد اما همگام‌سازی موتور ناموفق بود؛ لاگ‌ها را بررسی کنید.')));
  toast(tr('تغییرات تأییدشده اعمال شد.'),'success');await refresh(true);return true;
 });
 $('assistant-undo').onclick=e=>withButton(e.currentTarget,async()=>{
  if(!undo)return;const diff=undo.changes.map(c=>c.field+': '+JSON.stringify(c.after)+' → '+JSON.stringify(c.before)).join('\n');
  if(!await confirmAction(tr('بازگردانی تغییر قبلی'),diff+'\n\n'+tr('تنظیمات قبلی بازگردانده می‌شود و موتورهای فعال دوباره راه‌اندازی می‌شوند.')))return;
  const result=await api('/assistant/undo','POST',{id:undo.id,confirm:true});undo=null;$('assistant-undo-panel').hidden=true;renderProposal(null);
  if(!result.runtime_synchronized)errorBox('assistant-error',Error(tr('تنظیمات ذخیره شد اما همگام‌سازی موتور ناموفق بود؛ لاگ‌ها را بررسی کنید.')));
  toast(tr('تنظیمات قبلی بازگردانده شد.'),'success');await refresh(true);return true;
 });
 $('assistant-dismiss').onclick=e=>withButton(e.currentTarget,async()=>{if(!proposal)return;await api('/assistant/proposal','DELETE',{id:proposal.id});renderProposal(null);return true;});
 function resetConversation(){controller?.abort();controller=null;generation++;proposal=null;undo=null;$('assistant-chat').replaceChildren(node('p',tr('گفت‌وگوی تازه آماده است.'),'assistant-welcome'));$('assistant-context-preview').textContent='';$('assistant-proposal').hidden=true;$('assistant-undo-panel').hidden=true;$('assistant-cancel').hidden=true;$('assistant-send').removeAttribute('aria-busy');$('assistant-send').disabled=!config?.enabled;}
 $('assistant-clear').onclick=e=>withButton(e.currentTarget,async()=>{await api('/assistant/conversation','DELETE');resetConversation();errorBox('assistant-error',null);return true;});
 $('logs-live').onclick=()=>{live=!live;$('logs-live').setAttribute('aria-pressed',String(live));$('logs-live').textContent=tr(live?'توقف نمایش زنده':'ادامهٔ نمایش زنده');if(live)refresh(true);else $('logs-state').textContent=tr('متوقف');};
 for(const id of ['logs-source','logs-level','logs-account','logs-range'])$(id).addEventListener('change',()=>refresh(true));
 $('logs-search').addEventListener('input',()=>{clearTimeout(searchTimer);searchTimer=setTimeout(()=>refresh(true),250);});
 $('logs-download').onclick=e=>withButton(e.currentTarget,async()=>{const q=query();q.set('limit','2000');const report=await api('/logs?'+q);download(new Blob([JSON.stringify(report,null,2)],{type:'application/json'}),'whitedns-diagnostics-'+new Date().toISOString().slice(0,10)+'.json');return true;});
 function dashboard(summary){if(!summary)return;$('dashboard-log-state').textContent=tr('۵ دقیقهٔ اخیر')+' · '+tr('خطا')+': '+number(summary.errors)+' · '+tr('هشدار')+': '+number(summary.warnings);$('dashboard-log-latest').replaceChildren(...summary.latest.map(e=>node('p',e.source+' · '+label(e.code)+(e.reason?' · '+tr(reasons[e.reason]||e.reason):''))));}
 function clear(){invalidateModels(true);resetConversation();config=null;entries=[];$('diagnostic-entries').replaceChildren();$('assistant-question').value='';$('assistant-api-key').value='';$('assistant-consent').checked=false;$('assistant-destination').textContent='';errorBox('assistant-error',null);}
 return {load,refresh,dashboard,clear};
})();
