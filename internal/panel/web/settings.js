'use strict';
// The supported WhiteDNS settings are exported as-is; the Go API validates them
// independently. Descriptions are text nodes so none can introduce HTML.
const soarSettingsForm = (() => {
 const defaults = {transport:'auto',discovery:true,discoveryWant:2,discoveryConcurrency:2,discoveryPaceMs:1000,safeQName:101,maxQName:253,maxAnswer:1232,holdMs:500,maxInflight:128,maxPathWindow:32,maxStreams:128,streamWindowKiB:256,sendBufferKiB:64,sessionIdleSeconds:90,dialTimeoutSeconds:90};
 const limited = {...defaults,discoveryWant:1,discoveryConcurrency:1,discoveryPaceMs:1500,maxQName:101,maxAnswer:512,maxInflight:32,maxPathWindow:8};
 const presets = {
  adaptive:{...defaults},
  stability:{...limited},
  balanced:{...limited,maxQName:180,maxAnswer:896,maxInflight:64,maxPathWindow:16},
  performance:{...limited,maxQName:253,maxAnswer:1232,maxInflight:128,maxPathWindow:32},
 };
 const fields = [
  ['safeQName',panelText("طول امن اولیهٔ پرس‌وجو"),101,253,panelText("کاراکتر"),panelText("طول نامی که هر مسیر با آن شروع می‌شود. مقدار ۱۰۱ برای مسیرهایی که نام بلند را حذف می‌کنند مناسب‌تر است. افزایش آن شروع اتصال را به بسته‌های بزرگ‌تر وابسته می‌کند؛ نباید از MaxQName بیشتر باشد.")],
  ['maxQName',panelText("سقف طول پرس‌وجو"),101,253,panelText("کاراکتر"),panelText("سقف طول نام DNS و یکی از عوامل ظرفیت آپلود است. مقدار ۲۵۳ در حالت خودکار سقف یادگیری است، نه اندازهٔ اجباری روی همهٔ مسیرها. قفل‌کردن سقف روی ۱۰۱ ظرفیت ارسال پیام آغاز TLS را محدود می‌کند؛ فقط وقتی مسیر به آن نیاز دارد استفاده کنید. مقدار بزرگ‌تر جا برای دادهٔ بیشتری می‌دهد، ولی بعضی مسیرها نام بلند را حذف می‌کنند. موتور از طول امن شروع می‌کند و فقط تا این سقف افزایش را آزمایش می‌کند.")],
  ['maxAnswer',panelText("سقف اندازهٔ پاسخ DNS"),512,1232,panelText("بایت"),panelText("سقف اندازهٔ پاسخ و یکی از عوامل ظرفیت دانلود است. مقدار ۵۱۲ محافظه‌کارانه است؛ پاسخ بزرگ‌تر می‌تواند سریع‌تر باشد ولی احتمال حذف یا تکه‌تکه‌شدن آن در مسیر بیشتر است. موتور اندازه‌های پاسخ را در گام‌های ۴۸ بایتی به کار می‌گیرد.")],
  ['maxInflight',panelText("کل درخواست‌های در حال انتظار"),8,1024,panelText("درخواست"),panelText("حداکثر درخواست‌های DNS بی‌پاسخ در تمام مسیرهاست. افزایش آن روی مسیر پرظرفیت ممکن است سرعت را بیشتر کند؛ مقدار زیاد روی Resolver محدود باعث افت بسته، تأخیر و مصرف بیشتر می‌شود. این عدد تعداد سایت‌ها یا دستگاه‌ها نیست.")],
  ['maxPathWindow',panelText("پنجرهٔ هر مسیر"),2,128,panelText("درخواست"),panelText("سقف درخواست‌های در حال انتظار روی یک مسیر DNS است. مقدار پایین بار هر Resolver را محدود می‌کند. افزایش آن فقط وقتی مفید است که همان مسیر تحمل درخواست‌های بیشتر را داشته باشد.")],
  ['holdMs',panelText("مهلت نگه‌داشتن پرس‌وجوی بیکار"),100,1500,panelText("میلی‌ثانیه"),panelText("سرور می‌تواند پرس‌وجوی بیکار را تا این مدت برای رسیدن داده نگه دارد. این مقدار تأخیر ثابتِ همهٔ بسته‌ها نیست؛ دادهٔ آماده زودتر برمی‌گردد. کاهش زیاد آن تعداد پرس‌وجوها و مصرف شبکه را بالا می‌برد.")],
  ['transport',panelText("روش ارتباط با Resolver"),null,null,'',panelText("این گزینه ارتباط DNS گوشی با Resolver را تعیین می‌کند، نه نوع VPN. خودکار ابتدا UDP را استفاده می‌کند و برای Resolver بی‌پاسخ مسیر TCP/53 می‌افزاید. روشن، TCP را از ابتدا هم آزمایش می‌کند؛ فقط TCP، UDP را کنار می‌گذارد؛ خاموش یعنی TCP جایگزین اضافه نشود.")],
  ['discovery',panelText("کشف خودکار مسیر جایگزین"),null,null,'',panelText("اگر فعال باشد، موتور علاوه بر Resolverهای شروع، مسیرهای جایگزین را جست‌وجو می‌کند. خاموش‌کردن آن اتصال را به فهرست دستی محدود می‌کند و ممکن است روی شبکهٔ دیگر اتصال را از بین ببرد. انتخاب محدودهٔ ایران فقط اولویت جست‌وجوست و تضمین اتصال نیست.")],
  ['discoveryWant',panelText("تعداد مسیر سالم موردنظر"),1,32,panelText("مسیر"),panelText("وقتی این تعداد مسیر تأییدشده و سالم وجود داشته باشد، افزودن مسیر جدید متوقف می‌شود؛ با خراب‌شدن مسیر جست‌وجو ادامه می‌یابد. عدد بیشتر الزاماً سریع‌تر نیست و ممکن است مسیرهای ضعیف بیشتری وارد انتقال کند. Resolverهای شروع همچنان بررسی می‌شوند.")],
  ['discoveryConcurrency',panelText("بررسی هم‌زمان مسیرهای تازه"),1,16,panelText("مسیر"),panelText("چند Resolver ناشناخته می‌توانند هم‌زمان در حال آزمایش باشند. عدد بیشتر پیدا کردن مسیر را در بعضی شبکه‌ها سریع‌تر می‌کند، ولی بار اولیه و احتمال محدودشدن درخواست‌های DNS را افزایش می‌دهد.")],
  ['discoveryPaceMs',panelText("فاصلهٔ افزودن مسیر تازه"),100,5000,panelText("میلی‌ثانیه"),panelText("حداقل فاصلهٔ زمانی میان افزودن دو مسیر برای بررسی است؛ زمان انتظار پاسخ هر بسته نیست. عدد کوچک‌تر جست‌وجو را سریع‌تر و شلوغ‌تر می‌کند. عدد بزرگ‌تر بار جست‌وجو را کاهش می‌دهد ولی پیدا کردن جایگزین ممکن است طولانی‌تر شود.")],
  ['maxStreams',panelText("سقف اتصال هم‌زمان در پروفایل"),8,128,panelText("جریان TCP"),panelText("تعداد جریان‌های هم‌زمان قابل استفاده در موتور گوشی است. برنامه‌های پس‌زمینه هم جریان مصرف می‌کنند؛ عدد پایین می‌تواند بازشدن سایت را متوقف کند. سقف حساب در سرور جداگانه اعمال می‌شود؛ حد مؤثر از هر دو بیشتر نمی‌شود و این عدد محدودیت دستگاه نیست.")],
  ['streamWindowKiB',panelText("پنجرهٔ دریافت هر جریان"),64,1024,'KiB',panelText("مقدار داده‌ای که هر جریان اجازه دارد پیش از تأیید جلو بیفتد. افزایش آن روی مسیر با تأخیر زیاد ممکن است مفید باشد، ولی نیاز حافظه و مقدار دادهٔ در انتظار را بیشتر می‌کند؛ محدودیت سرعت یا حجم حساب نیست.")],
  ['sendBufferKiB',panelText("بافر ارسال هر جریان"),32,1024,'KiB',panelText("ظرفیت صف ارسال هر جریان در موتور است. بافر بزرگ‌تر می‌تواند وقفه‌های کوتاه را جذب کند، ولی مصرف حافظه و زمان ماندن داده در صف را افزایش می‌دهد؛ به تنهایی ظرفیت DNS را بیشتر نمی‌کند.")],
  ['sessionIdleSeconds',panelText("پایان نشست بیکار"),15,110,panelText("ثانیه"),panelText("پس از این مدت بی‌کاری بدون جریان باز و پاسخ معتبر، کلاینت نشست قدیمی را کنار می‌گذارد و برای اتصال بعدی نشست تازه می‌سازد. باید از مهلت دو دقیقه‌ای سرور کمتر بماند تا نشست منقضی دوباره استفاده نشود.")],
  ['dialTimeoutSeconds',panelText("مهلت برقراری اتصال"),5,120,panelText("ثانیه"),panelText("حداکثر زمان مجاز برای برقراری اتصال از طریق تونل است. افزایش آن فرصت بیشتری به مسیر کند می‌دهد ولی آن را سریع‌تر نمی‌کند. مقدار بسیار کم می‌تواند اتصال سالم اما کند را زود قطع کند.")],
 ];
 const get = id => document.getElementById(id);
 function element(tag,text,cls){const el=document.createElement(tag);if(text!=null)el.textContent=text;if(cls)el.className=cls;return el;}
 function attachHelp(wrapper,control,label,text){
  const description=element('span',text,'sr-only');description.id=control.id+'-help';
  const help=element('button',panelText("؟"),'help-trigger');help.type='button';help.setAttribute('aria-label',panelText("راهنمای ")+label);help.setAttribute('aria-describedby',description.id);help.setAttribute('aria-controls','field-tooltip');help.setAttribute('aria-expanded','false');
  control.setAttribute('aria-describedby',description.id);wrapper.dataset.help=description.id;
  wrapper.querySelector('.field-label').append(help);wrapper.append(description);
 }
 for(const [key,label,min,max,unit,help] of fields){
  const wrapper=element('div',null,'setting-field'),heading=element('div',null,'field-label'),caption=element('label',label);caption.htmlFor='soar-'+key;heading.append(caption);
  const technical=element('span',key,'setting-key');technical.dir='ltr';
  let input;
  if(key==='transport'||key==='discovery'){
   input=element('select');const choices=key==='transport'?[['auto',panelText("خودکار — UDP با جایگزین TCP")],['on',panelText("UDP و TCP از ابتدا")],['only',panelText("فقط TCP")],['off',panelText("UDP؛ بدون جایگزین خودکار TCP")]]:[['true',panelText("فعال")],['false',panelText("خاموش؛ فقط Resolverهای دستی")]];
   for(const [value,text] of choices){const option=element('option',text);option.value=value;input.append(option);}
  }else{input=element('input');input.type='number';input.min=min;input.max=max;input.step='1';input.dir='ltr';input.required=true;}
  input.id='soar-'+key;input.name=key;wrapper.append(heading,technical,input);
  if(min!=null)wrapper.append(element('small',`${min}–${max} ${unit}`,'setting-range'));
  attachHelp(wrapper,input,label,help);
  const section=['safeQName','maxQName','maxAnswer','maxInflight','maxPathWindow','holdMs'].includes(key)?'dns':(['transport','discovery','discoveryWant','discoveryConcurrency','discoveryPaceMs'].includes(key)?'discovery':'streams');
  get('soar-fields-'+section).append(wrapper);
 }
 const generalHelp={
  'server-stream-limit':panelText("مجموع جریان‌های TCP هم‌زمان همهٔ حساب‌ها در Soar، Cotten و MasterDNS است؛ سقف هر حساب جداگانه اعمال می‌شود. صفر یعنی نامحدود؛ برای سقف مشخص، عدد صحیح بین ۱ تا ۲۱۴۷۴۸۳۶۴۷ وارد کنید. این عدد تعداد دستگاه یا نشست تونل نیست و تنظیم maxStreams پروفایل گوشی را تغییر نمی‌دهد."),
  'base-zone':panelText("دامنهٔ اصلی واگذارشده به این سرور است. پس از نصب ثابت می‌ماند، چون حساب‌ها زیر همین دامنه ساخته شده‌اند. برای هر حساب رکورد DNS جداگانه لازم نیست."),
  country:panelText("اولویت فهرست جست‌وجوی کلاینت را تعیین می‌کند. ایران یعنی بررسی Resolverهای ایران پیش از فهرست جهانی؛ این گزینه کشور IP خروجی سرور را تغییر نمی‌دهد. روی لینک‌های دوباره صادرشده اعمال می‌شود."),
  resolvers:panelText("یک تا هشت آدرس IP یا IP:port، هر کدام در یک خط. این‌ها مسیرهای شروع اتصال‌اند؛ انتخاب بیشتر الزاماً سرعت را بیشتر نمی‌کند. با کشف خودکار فعال، کلاینت مسیرهای جایگزین را نیز بررسی می‌کند."),
  'allow-ports':panelText("پورت‌های مقصد TCP که کاربران تونل اجازه دارند به آن‌ها وصل شوند؛ با ویرگول جدا کنید. ۸۰ و ۴۴۳ برای وب هستند. این گزینه پورت ورود به پنل یا پورت DNS سرور را تغییر نمی‌دهد و برای اتصال‌های جدید اعمال می‌شود."),
 };
 for(const [id,help] of Object.entries(generalHelp)){
  const control=get(id),label=document.querySelector(`label[for="${id}"]`),wrapper=control.closest('.setting-field');attachHelp(wrapper,control,label.textContent,help);
 }
 const tooltip=get('field-tooltip');let active=null,pinned=false,closeTimer;
 function closeHelp(){clearTimeout(closeTimer);if(active)active.querySelector('.help-trigger').setAttribute('aria-expanded','false');active=null;pinned=false;tooltip.hidden=true;}
 function placeHelp(){
  if(!active)return;const anchor=active.querySelector('.help-trigger').getBoundingClientRect();
  if(anchor.bottom<0||anchor.top>innerHeight){closeHelp();return;}
  const gap=10,margin=12,rect=tooltip.getBoundingClientRect();
  tooltip.style.left=Math.max(margin,Math.min(innerWidth-rect.width-margin,anchor.right-rect.width))+'px';
  const below=anchor.bottom+gap;
  tooltip.style.top=Math.max(margin,Math.min(innerHeight-rect.height-margin,below+rect.height>innerHeight-margin?anchor.top-rect.height-gap:below))+'px';
 }
 function showHelp(wrapper){
  clearTimeout(closeTimer);if(active!==wrapper){closeHelp();active=wrapper;}
  tooltip.textContent=get(wrapper.dataset.help).textContent;tooltip.hidden=false;wrapper.querySelector('.help-trigger').setAttribute('aria-expanded','true');placeHelp();
 }
 function laterClose(){if(!pinned)closeTimer=setTimeout(()=>{if(active?.querySelector('.help-trigger')===document.activeElement||tooltip.matches(':hover'))return;closeHelp();},160);}
 for(const wrapper of document.querySelectorAll('.setting-field[data-help]')){
  const button=wrapper.querySelector('.help-trigger');
  button.addEventListener('pointerenter',event=>{if(event.pointerType==='mouse'&&!pinned)showHelp(wrapper);});
  button.addEventListener('pointerleave',laterClose);
  button.addEventListener('focus',()=>{if(active!==wrapper||tooltip.hidden)showHelp(wrapper);});
  button.addEventListener('blur',()=>{pinned=false;laterClose();});
  button.addEventListener('click',()=>{if(active===wrapper&&pinned){closeHelp();return;}showHelp(wrapper);pinned=true;});
 }
 tooltip.addEventListener('pointerenter',()=>clearTimeout(closeTimer));tooltip.addEventListener('pointerleave',laterClose);
 document.addEventListener('keydown',event=>{if(event.key==='Escape')closeHelp();});
 document.addEventListener('pointerdown',event=>{if(active&&!active.querySelector('.help-trigger').contains(event.target)&&!tooltip.contains(event.target))closeHelp();});
 window.addEventListener('resize',placeHelp);window.addEventListener('scroll',placeHelp,true);window.addEventListener('hashchange',closeHelp);
 function fill(settings){for(const [key] of fields)get('soar-'+key).value=String(settings[key]);panelUI.syncSelects();}
 function read(){const settings={};for(const [key] of fields){const value=get('soar-'+key).value;settings[key]=key==='transport'?value:key==='discovery'?value==='true':Number(value);}return settings;}
 function validate(){
  const safe=get('soar-safeQName');safe.setCustomValidity(Number(safe.value)>Number(get('soar-maxQName').value)?panelText("طول امن اولیه نباید از سقف طول پرس‌وجو بیشتر باشد."):'');
  return get('settings-form').reportValidity();
 }
 get('settings-form').addEventListener('input',()=>{get('soar-safeQName').setCustomValidity('');get('preset-state').textContent=panelText("فرم تغییر کرده؛ برای اعمال روی لینک‌ها ذخیره کنید.");get('settings-feedback').textContent=panelText("تغییرات هنوز ذخیره نشده‌اند.");});
 for(const button of document.querySelectorAll('[data-soar-preset]'))button.addEventListener('click',()=>{
  fill(presets[button.dataset.soarPreset]);get('soar-safeQName').setCustomValidity('');get('preset-state').textContent=`«${button.textContent}${panelText("» در فرم قرار گرفت؛ هنوز ذخیره نشده است.")}`;get('settings-feedback').textContent=panelText("تغییرات هنوز ذخیره نشده‌اند.");closeHelp();
 });
 return {load(settings){fill(settings);get('soar-safeQName').setCustomValidity('');get('preset-state').textContent=panelText("مقادیر ذخیره‌شدهٔ سرور");},read,validate};
})();
