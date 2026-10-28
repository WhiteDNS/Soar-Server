'use strict';
window.panelEgress=(()=>{
 const engines=[['soar','Soar'],['cotten','Cotten'],['masterdns','MasterDNS']];
 let revision=null,settingsSession='',reportSession='',offset=0,nextOffset=-1,loadedReport=false,loadingReport=null,requestSession='',appliedQuery=null,reportDirty=false;
 const timestampFormatter=new Intl.DateTimeFormat(panelI18n.locale,{dateStyle:'short',timeStyle:'medium'}),when=value=>value?timestampFormatter.format(new Date(value)):'—';
 const text=panelText;
 const states={active:text('فعال'),completed:text('پایان‌یافته'),failed:text('ناموفق'),denied:text('ردشده'),interrupted:text('پایان نامشخص')};
 for(const [key] of engines){
  const mode=$('egress-'+key+'-mode'),address=$('egress-'+key+'-address'),username=$('egress-'+key+'-username'),password=$('egress-'+key+'-password'),ports=$('egress-'+key+'-ports');
  function update(){const direct=mode.value==='direct';for(const el of [address,username,password,$('egress-'+key+'-clear'),$('egress-'+key+'-unrestricted')])el.disabled=direct;if(direct)$('egress-'+key+'-unrestricted').checked=false;ports.disabled=direct||$('egress-'+key+'-unrestricted').checked;address.required=!direct;ports.required=!direct&&!$('egress-'+key+'-unrestricted').checked;}
  mode.addEventListener('change',update);$('egress-'+key+'-unrestricted').addEventListener('change',update);update();
 }
 async function loadSettings(){
  const session=csrf;settingsSession=session;revision=null;$('egress-save').disabled=true;
  const cfg=await api('/egress');if(csrf!==session||currentPage!=='settings')return;
  revision=cfg.revision;
  for(const [key] of engines){const route=cfg[key];for(const name of ['mode','address','username','ports'])$('egress-'+key+'-'+name).value=route[name]||'';$('egress-'+key+'-password').value='';$('egress-'+key+'-password').placeholder=route.password_configured?text('رمز ذخیره شده است'):'';$('egress-'+key+'-clear').checked=false;$('egress-'+key+'-unrestricted').checked=!!route.unrestricted;$('egress-'+key+'-mode').dispatchEvent(new Event('change'));}
  $('outbound-logging').checked=cfg.logs.enabled;$('outbound-retention').value=cfg.logs.retention_hours;$('outbound-storage').value=cfg.logs.max_mib;$('egress-save').disabled=false;panelUI.syncSelects();
 }
 $('egress-form').addEventListener('submit',event=>{event.preventDefault();withButton(event.submitter,async()=>{
  if(revision===null||csrf!==settingsSession)throw Error(text('تنظیمات را دوباره دریافت کنید.'));
  const cfg={revision,logs:{enabled:$('outbound-logging').checked,retention_hours:Number($('outbound-retention').value),max_mib:Number($('outbound-storage').value)}};
  for(const [key] of engines){const clear=$('egress-'+key+'-clear').checked;cfg[key]={mode:$('egress-'+key+'-mode').value,address:$('egress-'+key+'-address').value.trim(),username:clear?'':$('egress-'+key+'-username').value,password:clear?'':$('egress-'+key+'-password').value,ports:$('egress-'+key+'-ports').value.trim(),unrestricted:$('egress-'+key+'-unrestricted').checked,clear_password:clear};}
  const session=csrf;await api('/egress','PUT',cfg);if(csrf!==session)return;loadedReport=false;await loadSettings();$('egress-feedback').textContent=text('مسیر خروجی ذخیره شد و موتورهای فعال دوباره راه‌اندازی شدند.');toast(text('مسیر خروجی ذخیره شد و موتورهای فعال دوباره راه‌اندازی شدند.'));return true;
 });});
 function localTime(value){const d=new Date(value);return new Date(value-d.getTimezoneOffset()*60000).toISOString().slice(0,19);}
 function query(){
  const from=new Date($('outbound-from').value).getTime(),until=new Date($('outbound-until').value).getTime();
  if(!Number.isFinite(from)||!Number.isFinite(until)||until<from)throw Error(text('تاریخ و ساعت شروع و پایان معتبر انتخاب کنید.'));
  return new URLSearchParams({from:String(from),until:String(until),engine:$('outbound-engine').value,account:$('outbound-account').value.trim(),route:$('outbound-route').value,state:$('outbound-status').value});
 }
 function cell(primary,secondary,ltr=false){const td=node('td'),top=node('span',primary),bottom=node('small',secondary);if(ltr){top.dir='ltr';bottom.dir='ltr';}td.append(top,bottom);return td;}
 function syncPagination(){$('outbound-previous').disabled=!!loadingReport||reportDirty||offset===0;$('outbound-next').disabled=!!loadingReport||reportDirty||nextOffset<0;}
 function render(report){
  nextOffset=report.next_offset;
  const rows=report.entries.map(r=>{const tr=node('tr'),partial=r.state==='interrupted'&&r.bytes_known===false;tr.append(cell(when(r.started_at),when(r.ended_at)),cell(r.engine,r.account_id,true),cell(r.host,r.destination_ip||'—',true),cell(String(r.port),r.network+' · '+r.purpose,true),cell(r.route,r.proxy_endpoint||r.proxy||'',true),cell((partial?'≥ ':'')+bytes(r.upload_bytes),(partial?'≥ ':'')+bytes(r.download_bytes),true),cell(states[r.state]||r.state,r.reason||''));return tr;});
  if(!rows.length){const tr=node('tr'),td=node('td',text('رکورد خروجی در این بازه وجود ندارد.'),'empty-outbound');td.colSpan=7;tr.append(td);rows.push(tr);}
  $('outbound-body').replaceChildren(...rows);
  const range=new URLSearchParams(appliedQuery);$('outbound-summary').textContent=number(report.total)+' '+text('اتصال')+' · '+text('آپلود')+' '+bytes(report.upload_bytes)+' · '+text('دانلود')+' '+bytes(report.download_bytes)+' · '+when(Number(range.get('from')))+' — '+when(Number(range.get('until')));
  $('outbound-state').textContent=report.logging.enabled?text('ثبت مقصد فعال است'):text('ثبت مقصد خاموش است');
  const loss=report.dropped||report.storage_errors;$('outbound-warning').hidden=!loss;$('outbound-warning').textContent=loss?text('گزارش ممکن است ناقص باشد؛ حذف از صف / خطای ذخیره: ')+number(report.dropped)+' / '+number(report.storage_errors):'';
  syncPagination();$('outbound-page').textContent=report.total?number(offset+1)+'–'+number(offset+report.entries.length)+' / '+number(report.total):'';
 }
 async function fetchReport(fresh=false){
  if(loadingReport){const pending=loadingReport;if(requestSession===csrf)return pending;try{await pending;}catch{}return fetchReport(fresh);}
  const session=csrf,base=fresh||!appliedQuery?query():new URLSearchParams(appliedQuery),signature=base.toString(),parameters=new URLSearchParams(base);parameters.set('offset',String(offset));parameters.set('limit','200');$('outbound-state').textContent=text('در حال دریافت…');
  requestSession=session;
  loadingReport=(async()=>{const report=await api('/outbound?'+parameters);if(csrf!==session||currentPage!=='logs')return;appliedQuery=signature;try{reportDirty=query().toString()!==signature;}catch{reportDirty=true;}render(report);if(reportDirty)$('outbound-state').textContent=text('فیلتر تغییر کرده؛ نمایش گزارش را بزنید.');loadedReport=true;reportSession=session;})().finally(()=>{loadingReport=null;syncPagination();});
  syncPagination();
  return loadingReport;
 }
 async function loadReport(){if(!$('outbound-from').value){const now=Date.now();$('outbound-from').value=localTime(now-3600000);$('outbound-until').value=localTime(now);}if(!loadedReport||reportSession!==csrf)await fetchReport(true);}
 $('outbound-query').addEventListener('input',()=>{reportDirty=true;syncPagination();$('outbound-state').textContent=text('فیلتر تغییر کرده؛ نمایش گزارش را بزنید.');});
 $('outbound-query').addEventListener('change',()=>{reportDirty=true;syncPagination();$('outbound-state').textContent=text('فیلتر تغییر کرده؛ نمایش گزارش را بزنید.');});
 $('outbound-query').addEventListener('submit',event=>{event.preventDefault();offset=0;withButton(event.submitter,()=>fetchReport(true));});
 async function changePage(value){const previous=offset;offset=value;try{await fetchReport();}catch(error){offset=previous;throw error;}}
 $('outbound-previous').onclick=event=>withButton(event.currentTarget,()=>changePage(Math.max(0,offset-200))).finally(syncPagination);
 $('outbound-next').onclick=event=>withButton(event.currentTarget,()=>nextOffset<0?undefined:changePage(nextOffset)).finally(syncPagination);
 async function exportReport(format){const session=csrf,parameters=query();parameters.set('format',format);parameters.set('download','1');const response=await fetch(apiPath('/outbound?'+parameters),{credentials:'same-origin',cache:'no-store'});if(!response.ok){if(response.status===401&&csrf===session)signOut();let detail;try{detail=(await response.json()).error;}catch{}throw Error(detail||text('دریافت گزارش خروجی ناموفق بود.'));}const blob=await response.blob();if(csrf!==session)return;download(blob,'whitedns-outbound.'+format);return true;}
 $('outbound-csv').onclick=event=>withButton(event.currentTarget,()=>exportReport('csv'));
 $('outbound-json').onclick=event=>withButton(event.currentTarget,()=>exportReport('json'));
 function clear(){revision=null;settingsSession='';reportSession='';loadedReport=false;offset=0;nextOffset=-1;appliedQuery=null;reportDirty=false;$('outbound-body').replaceChildren();for(const [key]of engines)$('egress-'+key+'-password').value='';}
 return {loadSettings,loadReport,clear};
})();
