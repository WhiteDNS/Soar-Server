'use strict';
const panelUI = (() => {
 const get=id=>document.getElementById(id), ns='http://www.w3.org/2000/svg';
 const fmt=new Intl.NumberFormat('fa-IR',{maximumFractionDigits:1});
 const readingFmt=new Intl.NumberFormat('en-US',{minimumFractionDigits:1,maximumFractionDigits:1});
 const icons={
  dashboard:'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  users:'M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2 M9 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8 M22 21v-2a4 4 0 0 0-3-3.87 M16 3.13a4 4 0 0 1 0 7.75',
  settings:'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M9 3l1-2h4l1 2 3 2 2 1v4l-2 1v2l2 1v4l-3 1-2 2h-4l-1-2-3-2-2-1v-4l2-1v-2L3 8V5l3-1z',
  activity:'M3 12h4l3-8 4 16 3-8h4',
  backup:'M12 3v12 m-4-4 4 4 4-4 M4 15v5h16v-5',
  logout:'M9 21H4V3h5 M9 12h12 m-4-4 4 4-4 4',
  refresh:'M20 7V3l-3 3a8 8 0 1 0 3 10 M20 3h-5',
  plus:'M12 5v14 M5 12h14',
  search:'M10.5 3a7.5 7.5 0 1 0 0 15 7.5 7.5 0 0 0 0-15 M16 16l5 5',
  cpu:'M6 6h12v12H6z M9 9h6v6H9z M9 2v4 M15 2v4 M9 18v4 M15 18v4 M2 9h4 M2 15h4 M18 9h4 M18 15h4',
  memory:'M3 6h18v12H3z M7 10v4 M12 10v4 M17 10v4 M7 18v3 M12 18v3 M17 18v3',
  disk:'M4 3h16v18H4z M4 15h16 M8 18h.01 M12 18h4 M8 7h8 M8 10h8',
  clock:'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18 M12 7v5l3 2',
  server:'M3 3h18v7H3z M3 14h18v7H3z M7 6.5h.01 M7 17.5h.01 M11 6.5h6 M11 17.5h6',
  network:'M3 5h6v6H3z M15 5h6v6h-6z M9 17h6v5H9z M6 11v3h12v-3 M12 14v3',
  moon:'M20.9 13a9 9 0 0 1-9.9-9.9A9 9 0 1 0 20.9 13z',
  sun:'M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M12 2v2 M12 20v2 M2 12h2 M20 12h2 M5 5l1.5 1.5 M17.5 17.5 19 19 M5 19l1.5-1.5 M17.5 6.5 19 5',
  eye:'M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12 M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6',
  user:'M12 3a4 4 0 1 0 0 8 4 4 0 0 0 0-8 M4 21v-2a8 8 0 0 1 16 0v2',
  lock:'M5 10h14v11H5z M8 10V7a4 4 0 0 1 8 0v3 M12 14v3',
  menu:'M4 6h16 M4 12h16 M4 18h16',
  close:'M6 6l12 12 M18 6 6 18',
  arrow:'M19 12H5 m6-6-6 6 6 6',
 };
 function icon(name){const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('class','icon');svg.setAttribute('aria-hidden','true');const path=document.createElementNS(ns,'path');path.setAttribute('d',icons[name]||icons.activity);svg.append(path);return svg;}
 for(const el of document.querySelectorAll('[data-icon]'))el.replaceChildren(icon(el.dataset.icon));
 function setTheme(theme){document.documentElement.dataset.theme=theme;try{localStorage.setItem('soar-theme',theme);}catch{}for(const button of [get('theme-toggle'),get('login-theme')]){button.replaceChildren(icon(theme==='dark'?'sun':'moon'));button.setAttribute('aria-label',theme==='dark'?'فعال کردن تم روشن':'فعال کردن تم تیره');}}
 let theme='dark';try{theme=localStorage.getItem('soar-theme')==='light'?'light':'dark';}catch{}setTheme(theme);
 get('login-theme').onclick=()=>setTheme(document.documentElement.dataset.theme==='dark'?'light':'dark');
 get('password-toggle').onclick=()=>{const input=get('login-password'),shown=input.type==='password';input.type=shown?'text':'password';get('password-toggle').setAttribute('aria-pressed',String(shown));get('password-toggle').setAttribute('aria-label',shown?'پنهان کردن رمز عبور':'نمایش رمز عبور');};
 function closeMenu(){const opened=document.body.classList.contains('menu-open');document.body.classList.remove('menu-open');get('menu-toggle').setAttribute('aria-expanded','false');if(opened)get('menu-toggle').focus();}
 get('nav-backdrop').onclick=closeMenu;get('rail-close').onclick=closeMenu;
 document.addEventListener('keydown',event=>{if(!document.body.classList.contains('menu-open'))return;if(event.key==='Escape'){closeMenu();get('menu-toggle').focus();}if(event.key==='Tab'){const links=[...document.querySelectorAll('.sidebar a,.sidebar button')].filter(el=>el.getClientRects().length);const first=links[0],last=links.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}}});
 function size(n){if(n==null)return '—';if(n===0)return '0 B';const units=['B','KiB','MiB','GiB','TiB'];const i=Math.max(0,Math.min(4,Math.floor(Math.log(n)/Math.log(1024))));return `${(n/1024**i).toLocaleString('en-US',{maximumFractionDigits:i?2:0})} ${units[i]}`;}
 function text(id,value){get(id).textContent=value;}
 function svgEl(tag,attrs){const el=document.createElementNS(ns,tag);for(const [key,val] of Object.entries(attrs))el.setAttribute(key,val);return el;}
 let samples=[],last=null;
 function chart(id,keys,fixedMax,spark=false){
  const container=get(id),w=600,h=spark?80:180,pad=5;
  const values=samples.flatMap(s=>keys.map(k=>s[k])).filter(Number.isFinite);
  container.replaceChildren();
  if(!values.length){const empty=document.createElement('div');empty.className='spark-empty';empty.textContent=spark?'داده‌ای در دسترس نیست':'در انتظار دو نمونهٔ واقعی از سرور';container.append(empty);return;}
  const svg=svgEl('svg',{viewBox:`0 0 ${w} ${h}`,preserveAspectRatio:'none','aria-hidden':'true'});
  if(!spark)for(let i=0;i<4;i++){const y=pad+i*(h-2*pad)/3;svg.append(svgEl('line',{x1:0,x2:w,y1:y,y2:y,class:'chart-gridline'}));}
  const max=fixedMax||Math.max(...values,1)*1.18;
  const first=samples[0].at,span=Math.max(1,samples.at(-1).at-first);
  keys.forEach((key,index)=>{
   let segments=[],part=[];for(const s of samples){if(!Number.isFinite(s[key])){if(part.length)segments.push(part);part=[];continue;}part.push([samples.length===1?w:pad+(s.at-first)/span*(w-2*pad),h-pad-Math.min(s[key],max)/max*(h-2*pad)]);}if(part.length)segments.push(part);
   for(const points of segments){const path=points.map((p,i)=>`${i?'L':'M'}${p[0].toFixed(2)},${p[1].toFixed(2)}`).join(' ');if(points.length>1){if(index===0)svg.append(svgEl('path',{d:`${path} L${points.at(-1)[0]},${h} L${points[0][0]},${h} Z`,class:'chart-area'}));svg.append(svgEl('path',{d:path,class:'chart-line'+(index?' secondary':'')}));}else{svg.append(svgEl('circle',{cx:points[0][0],cy:points[0][1],r:2,class:'chart-point'}));}}
  });
  container.append(svg);
 }
 function setConnection(ok){const chip=get('service-chip');chip.dataset.live=String(ok);chip.textContent=ok?'پنل در دسترس':'بررسی ارتباط';}
 function renderDashboard(d){
  setConnection(true);const h=d.host,now=performance.now(),up=d.dns_received_bytes,down=d.dns_sent_bytes;
  if(last&&(last.uptime>d.uptime_seconds||up<last.up||down<last.down)){samples=[];last=null;}
  const dt=last?(now-last.at)/1000:0;
  const ram=h.memory_total?100*h.memory_used/h.memory_total:null,disk=h.disk_total?100*h.disk_used/h.disk_total:null;
  const sample={at:now,cpu:h.cpu_percent,ram,disk,streams:d.active_streams,send:dt>0?(down-last.down)/dt:null,receive:dt>0?(up-last.up)/dt:null};
  samples.push(sample);samples=samples.filter(s=>now-s.at<=600000).slice(-120);last={at:now,up,down,uptime:d.uptime_seconds};
  text('cpu-reading',h.cpu_percent==null?'—':readingFmt.format(h.cpu_percent));text('ram-reading',ram==null?'—':readingFmt.format(ram));text('disk-reading',disk==null?'—':readingFmt.format(disk));text('streams',String(d.active_streams));text('connection-count',String(d.active_streams));
  text('ram-label',h.memory_total?size(h.memory_used)+' / '+size(h.memory_total):'اطلاعات حافظه در دسترس نیست');
  text('disk-label',h.disk_total?size(h.disk_used)+' / '+size(h.disk_total):'اطلاعات دیسک در دسترس نیست');
  text('cpu-label',h.cpu_percent==null?(h.platform==='linux'?'پس از نمونهٔ بعدی مشخص می‌شود':'اطلاعات پردازنده در دسترس نیست'):'درصد استفاده از پردازنده');
  text('stat-online',fmt.format(d.accounts_with_streams)+' حساب دارای جریان باز');
  text('send-rate',sample.send==null?'—':size(sample.send)+'/s');text('receive-rate',sample.receive==null?'—':size(sample.receive)+'/s');
  text('dns-send-total',size(down));text('dns-receive-total',size(up));text('dns-traffic',size(up+down));
  text('uptime',fmt.format(Math.floor(d.uptime_seconds/3600))+' ساعت و '+fmt.format(Math.floor(d.uptime_seconds/60)%60)+' دقیقه');
  text('platform',h.platform);text('panel-version',d.version);text('footer-version',d.version);
  text('stat-accounts',fmt.format(d.accounts));text('stat-active',fmt.format(d.states.active)+' حساب فعال');text('stat-up',size(d.upload_bytes));text('stat-down',size(d.download_bytes));
  const states={active:'فعال',disabled:'غیرفعال',expired:'منقضی',exhausted:'حجم تمام‌شده'};
  get('status-breakdown').replaceChildren(...Object.entries(states).map(([key,label])=>{const row=document.createElement('div'),badge=document.createElement('span'),value=document.createElement('strong');badge.className='badge '+key;badge.textContent=label;value.textContent=fmt.format(d.states[key]);row.append(badge,value);return row;}));
  for(const [id,key,max] of [['cpu-spark','cpu',100],['ram-spark','ram',100],['disk-spark','disk',100],['streams-spark','streams',0]])chart(id,[key],max,true);
  chart('traffic-chart',['send','receive']);chart('connections-chart',['streams']);
  const elapsed=Math.floor((samples.at(-1).at-samples[0].at)/1000);
  text('chart-window',elapsed<60?'نمونه‌های این نشست':fmt.format(Math.floor(elapsed/60))+' دقیقهٔ اخیر');
  text('chart-start',new Date(Date.now()-(now-samples[0].at)).toLocaleTimeString('fa-IR',{hour:'2-digit',minute:'2-digit'}));
  text('chart-now','اکنون');
 }
 return {icon,renderDashboard,setConnection,closeMenu,toggleTheme(){setTheme(document.documentElement.dataset.theme==='dark'?'light':'dark');}};
})();
