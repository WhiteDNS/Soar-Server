'use strict';
window.panelAntiFlood=(()=>{
 const tr=panelText;
 const reasons={account_rate:tr('نرخ اتصال حساب'),server_rate:tr('نرخ اتصال سرور'),target_rate:tr('نرخ اتصال به یک IP'),network_rate:tr('نرخ اتصال به یک شبکه'),account_streams:tr('جریان‌های هم‌زمان حساب'),server_streams:tr('جریان‌های هم‌زمان سرور'),target_streams:tr('جریان‌های هم‌زمان مقصد'),target_scan:tr('تعداد مقصدهای تازه'),dial_failures:tr('شکست‌های مکرر اتصال'),account_upload:tr('آپلود حساب'),target_upload:tr('آپلود به یک IP'),server_upload:tr('آپلود کل سرور'),resolver_rate:tr('درخواست DNS حساب'),server_resolver_rate:tr('درخواست DNS سرور'),capacity:tr('ظرفیت محافظت'),quarantine:tr('قرنطینه'),manual_release:tr('آزادسازی دستی')};
 const actions={observed:tr('فقط مشاهده'),denied:tr('رد شد'),quarantined:tr('قرنطینه'),released:tr('آزاد شد')};
 const fields=[
  ['account_rate','نرخ اتصال حساب',1,10000,'اتصال جدید در ثانیه؛ مجموع Soar، Cotten و MasterDNS'],
  ['account_burst','جهش کوتاه حساب',1,20000,'تعداد اتصال جدید مجاز در یک جهش کوتاه'],
  ['server_rate','نرخ اتصال سرور',1,100000,'اتصال جدید در ثانیه؛ مجموع همهٔ حساب‌ها'],
  ['server_burst','جهش کوتاه سرور',1,200000,'تعداد اتصال جدید مجاز در یک جهش کوتاه'],
  ['target_rate','نرخ اتصال به یک IP',1,10000,'اتصال جدید در ثانیه؛ همهٔ حساب‌ها و پورت‌ها مشترک'],
  ['target_burst','جهش کوتاه مقصد',1,20000,'تعداد اتصال جدید مجاز در یک جهش کوتاه'],
  ['network_rate','نرخ اتصال به یک شبکه',1,20000,'اتصال جدید در ثانیه؛ IPv4 /24 و IPv6 /64'],
  ['network_burst','جهش کوتاه شبکه',1,40000,'تعداد اتصال جدید مجاز در یک جهش کوتاه'],
  ['account_streams','جریان‌های هم‌زمان حساب',1,4096,'شامل اتصال‌های در حال برقراری؛ سقف حساب هم اعمال می‌شود'],
  ['server_streams','جریان‌های هم‌زمان سرور',1,32768,'شامل اتصال‌های در حال برقراری؛ سقف حساب هم اعمال می‌شود'],
  ['target_streams','جریان‌های هم‌زمان مقصد',1,4096,'همهٔ حساب‌ها، پورت‌ها و هر سه موتور مشترک'],
  ['targets_per_minute','مقصدهای متمایز در دقیقه',10,2000,'بر اساس IP واقعی اتصال، در پنجرهٔ متحرک یک دقیقه'],
  ['failure_threshold','حداقل شکست اتصال',10,10000,'شمار شکست‌ها در یک دقیقه؛ همراه با درصد شکست بررسی می‌شود'],
  ['failure_percent','درصد شکست اتصال',50,100,'درصد شکست لازم برای قرنطینه؛ خطای سیاست و لغو اتصال حساب نمی‌شوند'],
  ['violation_threshold','تخلف‌های پیاپی',10,10000,'رد درخواست حساب در ۱۰ ثانیه؛ شلوغی مشترک مقصد باعث قرنطینهٔ حساب نمی‌شود'],
  ['cooldown_seconds','زمان پایهٔ قرنطینه',30,3600,'ثانیه؛ با تکرار تخلف دو برابر، تا سقف یک ساعت'],
  ['account_upload_kib','آپلود حساب',32,524288,'KiB/s؛ جهش دوثانیه‌ای مجاز است. دانلود حساب نمی‌شود'],
  ['target_upload_kib','آپلود به یک IP',32,524288,'KiB/s؛ جهش دوثانیه‌ای مجاز است. دانلود حساب نمی‌شود'],
  ['server_upload_kib','آپلود کل سرور',32,524288,'KiB/s؛ جهش دوثانیه‌ای مجاز است. دانلود حساب نمی‌شود'],
  ['resolver_qps','درخواست DNS حساب',1,10000,'درخواست DNS خروجی Cotten و MasterDNS در ثانیه؛ سقف سرور چهار برابر است'],
 ];
 const groups={account_rate:'نرخ و جهش اتصال',account_streams:'ظرفیت و مقصدها',failure_threshold:'تشخیص و قرنطینه',account_upload_kib:'آپلود و DNS'};
 let group;
 for(const [key,label,min,max,help] of fields){
  if(groups[key]){const section=node('details',null,'flood-group');section.open=true;section.append(node('summary',tr(groups[key])));group=node('div',null,'settings-grid');section.append(group);$('flood-fields').append(section);}
  const wrap=node('div',null,'setting-field'),head=node('div',null,'field-label'),caption=node('label',tr(label)),input=node('input');
  caption.htmlFor='flood-'+key;head.append(caption);input.id=caption.htmlFor;input.name=key;input.type='number';input.min=min;input.max=max;input.step='1';input.required=true;input.dir='ltr';
  const description=node('small',tr(help),'setting-range');description.id=input.id+'-help';input.setAttribute('aria-describedby',description.id);
  wrap.append(head,input,description);group.append(wrap);
 }
 let savedPolicy=null;
 function dirtyState(){if(!savedPolicy)return;const dirty=$('flood-mode').value!==savedPolicy.mode||fields.some(([key])=>Number($('flood-'+key).value)!==savedPolicy[key]||!$('flood-'+key).validity.valid);$('flood-dirty').hidden=!dirty;if(dirty)$('flood-feedback').textContent='';}
 $('flood-form').addEventListener('input',dirtyState);$('flood-form').addEventListener('change',dirtyState);
 function policyForm(policy){savedPolicy={...policy};$('flood-mode').value=policy.mode;for(const [key] of fields)$('flood-'+key).value=policy[key];$('flood-dirty').hidden=true;panelUI.syncSelects();}
 function accountName(id){return clients.find(c=>c.id===id)?.name||id;}
 function render(data){
  $('flood-summary').replaceChildren(...[['ردشده',data.denied],['مشاهده‌شده',data.observed],['قرنطینه‌ها',data.quarantines],['جریان باز',data.active]].map(([label,value])=>{const metric=node('div',null,'flood-metric');metric.append(node('span',tr(label)),node('strong',number(value)));return metric;}));
  $('flood-runtime-mode').dataset.mode=data.policy.mode;
  $('flood-storage-error').hidden=!data.persistence_errors;
  const active=data.accounts.filter(a=>a.block.until>Date.now());
  $('flood-blocks').replaceChildren(...active.map(a=>{
   const row=node('li'),content=node('div',accountName(a.client_id));content.append(node('p',(reasons[a.block.reason]||a.block.reason)+' · '+tr('تا')+' '+date(a.block.until),'hint'));
   const button=node('button',tr('آزادسازی'),'secondary');button.type='button';button.onclick=()=>withButton(button,async()=>{
    if(!await confirmAction(tr('آزادسازی قرنطینه'),accountName(a.client_id)+' — '+tr('پیش از آزادسازی، علت تخلف و اشتراک‌گذاری لینک حساب را بررسی کنید. محدودیت نرخ باقی می‌ماند.')))return;
    render(await api('/antiflood/release','POST',{client_id:a.client_id}));toast(tr('قرنطینه آزاد شد.'));
   });row.append(content,button);return row;
  }));
  if(!active.length){const empty=node('li',null,'list-empty');empty.append(panelUI.icon('check'),node('span',tr('حسابی در قرنطینه نیست.')));$('flood-blocks').append(empty);}
  $('flood-events').replaceChildren(...data.events.map(ev=>{
   const row=node('li'),content=node('div',(actions[ev.action]||ev.action)+' · '+(reasons[ev.reason]||ev.reason));content.append(node('small',accountName(ev.client_id)));const stamp=node('time',date(ev.at));stamp.dateTime=new Date(ev.at).toISOString();row.append(content,stamp);return row;
  }));
  if(!data.events.length){const empty=node('li',null,'list-empty');empty.append(panelUI.icon('activity'),node('span',tr('هنوز رویدادی ثبت نشده است.')));$('flood-events').append(empty);}
  $('flood-reasons').replaceChildren(...Object.entries(data.reasons).map(([reason,count])=>node('li',(reasons[reason]||reason)+' · '+number(count))));
  $('flood-runtime-mode').textContent={off:tr('خاموش'),monitor:tr('فقط مشاهده'),enforce:tr('فعال؛ محدودسازی و قرنطینه')}[data.policy.mode];
 }
 async function load(){const data=await api('/antiflood');policyForm(data.policy);render(data);}
 async function refreshStatus(){if(currentPage==='antiflood')render(await api('/antiflood'));}
 $('flood-refresh').onclick=e=>withButton(e.currentTarget,refreshStatus);
 $('flood-form').onsubmit=e=>{e.preventDefault();withButton(e.submitter,async()=>{
  const mode=$('flood-mode').value;
  if(mode!=='enforce'&&!await confirmAction(tr('تغییر حالت ضدفلاد'),tr('در حالت خاموش یا فقط مشاهده، ضدفلاد جلوی ترافیک مشکوک را نمی‌گیرد.')))return;
  const policy={mode};for(const [key] of fields)policy[key]=Number($('flood-'+key).value);
  const data=await api('/antiflood','PUT',policy);policyForm(data.policy);render(data);$('flood-feedback').textContent=tr('سیاست ذخیره و روی سرور اعمال شد. نیازی به تازه‌سازی ساب نیست.');toast(tr('سیاست ضدفلاد ذخیره شد.'));
 });};
 return {load,refreshStatus};
})();
