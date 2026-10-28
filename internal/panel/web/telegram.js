'use strict';
window.panelTelegram=(()=>{
 const form=$('telegram-form');let current=null,refreshing=false,loading=false;
 const labels={awaiting_pairing:panelText('در انتظار اتصال حساب تلگرام'),disconnected:panelText('متصل نشده'),disabled:panelText('غیرفعال'),connecting:panelText('در حال اتصال…'),connected:panelText('متصل'),error:panelText('خطای اتصال')};
 function status(v){
  current=v;const label=labels[v.status]||labels.error;
  $('telegram-dashboard-status').textContent=label;
  $('telegram-status').textContent=[label,v.username?'@'+v.username:'',v.last_contact?panelText('آخرین ارتباط: ')+date(v.last_contact):'',v.error||''].filter(Boolean).join(' · ');
	$('telegram-deliveries').textContent=[panelText('آخرین گزارش: ')+(v.last_report||'—'),panelText('آخرین پشتیبان: ')+(v.last_backup||'—'),v.report_error,v.backup_error].filter(Boolean).join(' · ');
  $('telegram-test').disabled=!v.enabled||!v.paired;
  $('telegram-pairing').hidden=!v.enabled||v.paired;
  $('telegram-pair-link').hidden=!v.pair_url;if(v.pair_url)$('telegram-pair-link').href=v.pair_url;else $('telegram-pair-link').removeAttribute('href');
  $('telegram-pair-renew').disabled=!v.enabled||v.paired;$('telegram-disable').disabled=!v.enabled;$('telegram-disconnect').disabled=!v.configured;
 }
 function fill(v){status(v);$('telegram-token').value='';$('telegram-language').value=v.language||'auto';$('telegram-alerts').checked=v.alerts;$('telegram-daily').checked=v.daily_report;$('telegram-backup').checked=!!v.auto_backup;$('telegram-backup-time').value=v.backup_time||'03:00';$('telegram-report-time').value=v.report_time||'09:00';$('telegram-timezone').value=v.timezone||'Asia/Tehran';panelUI.syncSelects();}
 async function load(){const session=csrf,ticket=navigationId;try{const value=await api('/telegram');if(csrf===session&&ticket===navigationId&&currentPage==='settings')fill(value);}catch(e){if(csrf===session&&ticket===navigationId)$('telegram-feedback').textContent=message(e);}}
 async function refresh(){if(refreshing)return;refreshing=true;const session=csrf;try{const value=await api('/telegram');if(csrf===session)status(value);}catch{if(csrf===session)$('telegram-dashboard-status').textContent=panelText('در دسترس نیست');}finally{refreshing=false;}}
 async function act(button,fn){for(const b of form.querySelectorAll('button'))b.disabled=true;$('telegram-feedback').textContent='';button.setAttribute('aria-busy','true');try{await fn();}catch(e){$('telegram-feedback').textContent=message(e);}finally{button.removeAttribute('aria-busy');$('telegram-save').disabled=false;if(current)status(current);}}
 form.addEventListener('submit',e=>{e.preventDefault();act(e.submitter,async()=>{
  const body={token:$('telegram-token').value.trim(),enabled:true,language:$('telegram-language').value,alerts:$('telegram-alerts').checked,daily_report:$('telegram-daily').checked,auto_backup:$('telegram-backup').checked,backup_time:$('telegram-backup-time').value,report_time:$('telegram-report-time').value,timezone:$('telegram-timezone').value.trim()};
  const saved=await api('/telegram','PUT',body);fill(saved);$('telegram-feedback').textContent=saved.paired?panelText('تنظیمات تلگرام ذخیره شد.'):panelText('توکن ذخیره شد. با لینک اتصال، حساب تلگرام خود را متصل کنید.');
 });});
 $('telegram-pair-renew').onclick=e=>act(e.currentTarget,async()=>{fill(await api('/telegram/pair','POST',{}));});
 $('telegram-test').onclick=e=>act(e.currentTarget,async()=>{status(await api('/telegram/test','POST',{}));$('telegram-feedback').textContent=panelText('پیام آزمایش ارسال شد.');});
 $('telegram-disable').onclick=e=>act(e.currentTarget,async()=>{if(!await confirmAction(panelText('غیرفعال کردن ربات'),panelText('مدیریت تلگرام و گزارش‌ها متوقف می‌شوند. توکن برای اتصال دوباره حفظ می‌شود.')))return;fill(await api('/telegram','PUT',{enabled:false}));});
 $('telegram-disconnect').onclick=e=>act(e.currentTarget,async()=>{if(!await confirmAction(panelText('حذف اتصال تلگرام'),panelText('توکن و شناسهٔ ذخیره‌شده حذف می‌شوند. حساب‌های سرور حفظ می‌شوند.')))return;fill(await api('/telegram','DELETE'));});
 return {load,refresh,clear(){form.reset();$('telegram-token').value='';$('telegram-pair-link').removeAttribute('href');$('telegram-pair-link').hidden=true;$('telegram-pairing').hidden=true;$('telegram-status').textContent='';$('telegram-feedback').textContent='';current=null;}};
})();
