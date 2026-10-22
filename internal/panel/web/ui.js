'use strict';
const panelUI = (() => {
 const get=id=>document.getElementById(id), ns='http://www.w3.org/2000/svg';
 const fmt=new Intl.NumberFormat(panelI18n.locale,{maximumFractionDigits:1});
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
  more:'M5 12h.01 M12 12h.01 M19 12h.01',
  chevron:'M7 10l5 5 5-5',
  power:'M12 2v10 M5.6 5.6a9 9 0 1 0 12.8 0',
  unlink:'M9 15l6-6 M8 5l1-1a5 5 0 0 1 7 7l-1 1 M9 12l-1 1a5 5 0 0 0 7 7l1-1 M3 3l18 18',
  key:'M8 15a5 5 0 1 1 4-4l-8 8H1v-3l7-7 M15 6h.01',
  trash:'M3 6h18 M9 6V3h6v3 M5 6l1 15h12l1-15 M10 10v7 M14 10v7',
  check:'M5 12l4 4L19 6',
 };
 function icon(name){const svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('class','icon');svg.setAttribute('aria-hidden','true');const path=document.createElementNS(ns,'path');path.setAttribute('d',icons[name]||icons.activity);svg.append(path);return svg;}
 for(const el of document.querySelectorAll('[data-icon]'))el.replaceChildren(icon(el.dataset.icon));
 // Native selects retain the form values; one themed listbox renders every choice.
 const selectMenu=document.createElement('div');selectMenu.id='panel-select-options';selectMenu.className='select-menu';selectMenu.hidden=true;selectMenu.setAttribute('role','listbox');
 const selectPopover=typeof selectMenu.showPopover==='function';if(selectPopover)selectMenu.popover='auto';document.body.append(selectMenu);
 const selects=new Map();let currentSelect=null,activeOption=-1,searchText='',searchAt=0;
 function syncSelects(){for(const [select,button] of selects){button.querySelector('.select-value').textContent=select.selectedOptions[0]?.textContent||panelText("انتخاب کنید");button.disabled=select.disabled;}}
 function resetSelect(){if(currentSelect){const button=selects.get(currentSelect);button.setAttribute('aria-expanded','false');button.removeAttribute('aria-activedescendant');}currentSelect=null;activeOption=-1;searchText='';selectMenu.hidden=true;}
 function closeSelectMenu(){if(selectPopover&&selectMenu.matches(':popover-open'))selectMenu.hidePopover();resetSelect();}
 function positionSelectMenu(){
  if(!currentSelect)return;const r=selects.get(currentSelect).getBoundingClientRect(),pad=12,gap=6;
  if(r.bottom<0||r.top>innerHeight||!r.width){closeSelectMenu();return;}
  selectMenu.style.width=Math.min(innerWidth-2*pad,Math.max(r.width,240))+'px';
  const below=innerHeight-pad-r.bottom-gap,above=r.top-pad-gap,up=below<Math.min(selectMenu.scrollHeight,260)&&above>below;
  selectMenu.style.maxHeight=Math.max(44,Math.min(360,up?above:below))+'px';
  const rect=selectMenu.getBoundingClientRect();selectMenu.style.left=Math.max(pad,Math.min(r.right-rect.width,innerWidth-pad-rect.width))+'px';
  selectMenu.style.top=Math.max(pad,up?r.top-gap-rect.height:r.bottom+gap)+'px';
 }
 function markOption(index){
  if(!currentSelect||index<0)return;activeOption=index;
  for(const item of selectMenu.children)item.classList.toggle('is-active',Number(item.dataset.index)===index);
  const item=selectMenu.children[index];selects.get(currentSelect).setAttribute('aria-activedescendant',item.id);item.scrollIntoView({block:'nearest'});
 }
 function chooseOption(index){
  const select=currentSelect;if(!select||select.options[index]?.disabled)return;const changed=select.selectedIndex!==index;select.selectedIndex=index;syncSelects();closeSelectMenu();
  if(changed){select.dispatchEvent(new Event('input',{bubbles:true}));select.dispatchEvent(new Event('change',{bubbles:true}));}
 }
 function openSelect(select){
  closeSelectMenu();closeActionMenu(false);currentSelect=select;const button=selects.get(select);button.setAttribute('aria-expanded','true');selectMenu.setAttribute('aria-labelledby',button.getAttribute('aria-labelledby'));
  selectMenu.replaceChildren(...[...select.options].map((option,index)=>{
   const item=document.createElement('div');item.id=select.id+'-option-'+index;item.dataset.index=index;item.className='select-option';item.setAttribute('role','option');item.setAttribute('aria-selected',String(option.selected));if(option.disabled)item.setAttribute('aria-disabled','true');
   const label=document.createElement('span');label.textContent=option.textContent;item.append(label,icon('check'));
   item.addEventListener('pointerdown',e=>e.preventDefault());item.addEventListener('click',()=>chooseOption(index));item.addEventListener('pointermove',e=>{if(e.pointerType==='mouse'&&(e.movementX||e.movementY)&&!option.disabled)markOption(index);});return item;
  }));
  selectMenu.hidden=false;if(selectPopover)selectMenu.showPopover();positionSelectMenu();markOption(select.selectedIndex>=0?select.selectedIndex:0);
 }
 for(const select of document.querySelectorAll('select')){
  const button=document.createElement('button');button.type='button';button.id=select.id+'-trigger';button.className='select-trigger';button.setAttribute('role','combobox');button.setAttribute('aria-haspopup','listbox');button.setAttribute('aria-expanded','false');button.setAttribute('aria-controls',selectMenu.id);
  const labels=[...select.labels];for(const [i,label] of labels.entries()){label.id ||=select.id+'-label-'+i;label.htmlFor=button.id;}button.setAttribute('aria-labelledby',labels.map(label=>label.id).join(' '));
  if(select.hasAttribute('aria-describedby'))button.setAttribute('aria-describedby',select.getAttribute('aria-describedby'));
  const value=document.createElement('span');value.className='select-value';button.append(value,icon('chevron'));select.after(button);select.hidden=true;select.tabIndex=-1;selects.set(select,button);
  button.addEventListener('click',()=>currentSelect===select?closeSelectMenu():openSelect(select));
  button.addEventListener('blur',closeSelectMenu);select.addEventListener('change',syncSelects);
  button.addEventListener('keydown',e=>{
   if(e.key==='Tab'){closeSelectMenu();return;}
   if(e.key==='Escape'){if(currentSelect===select){e.preventDefault();closeSelectMenu();}return;}
   if(e.key==='Enter'||e.key===' '){e.preventDefault();if(currentSelect===select)chooseOption(activeOption);else openSelect(select);return;}
   const enabled=[...select.options].map((option,i)=>option.disabled?-1:i).filter(i=>i>=0);if(!enabled.length)return;
   if(['ArrowDown','ArrowUp','Home','End'].includes(e.key)){
    e.preventDefault();if(currentSelect!==select){openSelect(select);if(e.key==='ArrowDown'||e.key==='ArrowUp')return;}
    const i=enabled.indexOf(activeOption);markOption(e.key==='Home'?enabled[0]:e.key==='End'?enabled.at(-1):enabled[(i+(e.key==='ArrowDown'?1:-1)+enabled.length)%enabled.length]);
   }else if(e.key.length===1&&!e.ctrlKey&&!e.metaKey&&!e.altKey){
    e.preventDefault();if(currentSelect!==select)openSelect(select);searchText=performance.now()-searchAt>700?e.key:searchText+e.key;searchAt=performance.now();
    const index=enabled.find(i=>select.options[i].textContent.trim().toLocaleLowerCase().startsWith(searchText.toLocaleLowerCase()));if(index!==undefined)markOption(index);
   }
  });
 }
 syncSelects();
 selectMenu.addEventListener('toggle',()=>{if(selectPopover&&!selectMenu.matches(':popover-open'))resetSelect();});
 document.addEventListener('pointerdown',e=>{if(currentSelect&&!selectMenu.contains(e.target)&&!selects.get(currentSelect).contains(e.target))closeSelectMenu();});
 window.addEventListener('resize',positionSelectMenu);window.addEventListener('hashchange',closeSelectMenu);
 document.addEventListener('scroll',e=>{if(!selectMenu.contains(e.target))positionSelectMenu();},{capture:true});
 document.addEventListener('reset',()=>setTimeout(syncSelects,0));
 const actionMenu=document.createElement('div');actionMenu.id='account-actions-menu';actionMenu.className='account-actions-menu';
 actionMenu.setAttribute('role','menu');actionMenu.setAttribute('aria-label',panelText("عملیات حساب"));actionMenu.hidden=true;
 const nativePopover=typeof actionMenu.showPopover==='function';if(nativePopover)actionMenu.popover='auto';document.body.append(actionMenu);
 let actionTrigger=null;
 function resetActionMenu(){if(actionTrigger)actionTrigger.setAttribute('aria-expanded','false');actionTrigger=null;actionMenu.hidden=true;}
 function closeActionMenu(restoreFocus=true){const trigger=actionTrigger;if(nativePopover&&actionMenu.matches(':popover-open'))actionMenu.hidePopover();resetActionMenu();if(restoreFocus&&trigger?.isConnected)trigger.focus({preventScroll:true});}
 function positionActionMenu(){
  if(!actionTrigger)return;
  const r=actionTrigger.getBoundingClientRect(),m=actionMenu.getBoundingClientRect(),gap=8,pad=12;
  const left=Math.max(pad,Math.min(r.right-m.width,innerWidth-m.width-pad));
  const below=r.bottom+gap,top=below+m.height<=innerHeight-pad?below:Math.max(pad,r.top-m.height-gap);
  actionMenu.style.left=left+'px';actionMenu.style.top=top+'px';
 }
 function openActionMenu(trigger,client,onAction,last=false){
  if(actionTrigger===trigger){closeActionMenu();return;}
  closeSelectMenu();closeActionMenu(false);actionTrigger=trigger;trigger.setAttribute('aria-expanded','true');
  const heading=document.createElement('div');heading.className='action-menu-heading';heading.textContent=client.name;actionMenu.replaceChildren(heading);
  for(const [key,label,glyph] of [['toggle',client.enabled?panelText("غیرفعال کردن حساب"):panelText("فعال کردن حساب"),'power'],['disconnect',panelText("قطع اتصال‌های باز"),'unlink'],['reset',panelText("صفر کردن مصرف"),'refresh'],['rotate',panelText("تعویض لینک و کلید"),'key'],['delete',panelText("حذف حساب"),'trash']]){
   const item=document.createElement('button');item.type='button';item.className='action-menu-item'+(key==='delete'?' destructive':'');item.dataset.action=key;item.setAttribute('role','menuitem');item.tabIndex=-1;
   const labelNode=document.createElement('span');labelNode.textContent=label;item.append(icon(glyph),labelNode);
   item.onclick=()=>{closeActionMenu();onAction(key,trigger);};actionMenu.append(item);
  }
  actionMenu.hidden=false;if(nativePopover)actionMenu.showPopover();positionActionMenu();
  const items=actionMenu.querySelectorAll('[role=menuitem]');items[last?items.length-1:0].focus({preventScroll:true});
 }
 function accountActions(client,onAction){
  const button=document.createElement('button');button.type='button';button.className='account-actions-trigger';button.dataset.clientId=client.id;
  button.setAttribute('aria-haspopup','menu');button.setAttribute('aria-expanded','false');button.setAttribute('aria-controls',actionMenu.id);button.setAttribute('aria-label',panelText("عملیات بیشتر برای ")+client.name);
  const text=document.createElement('span');text.textContent=panelText("بیشتر");button.append(icon('more'),text,icon('chevron'));
  button.onclick=()=>openActionMenu(button,client,onAction);
  button.onkeydown=e=>{if(e.key==='ArrowDown'||e.key==='ArrowUp'){e.preventDefault();openActionMenu(button,client,onAction,e.key==='ArrowUp');}};
  return button;
 }
 actionMenu.addEventListener('keydown',e=>{
  const items=[...actionMenu.querySelectorAll('[role=menuitem]')],i=items.indexOf(document.activeElement);
  let next;if(e.key==='ArrowDown')next=(i+1)%items.length;else if(e.key==='ArrowUp')next=(i+items.length-1)%items.length;else if(e.key==='Home')next=0;else if(e.key==='End')next=items.length-1;
  if(next!==undefined){e.preventDefault();items[next].focus();}
  if(e.key==='Escape'){e.preventDefault();closeActionMenu();}
  if(e.key==='Tab'){e.preventDefault();const trigger=actionTrigger;closeActionMenu();if(!e.shiftKey){const focusable=[...document.querySelectorAll('a[href],button,input,select,textarea')].filter(el=>!el.disabled&&!el.closest('[hidden],[inert],dialog:not([open])')&&el.getClientRects().length);focusable[focusable.indexOf(trigger)+1]?.focus();}}
 });
 actionMenu.addEventListener('toggle',()=>{if(nativePopover&&!actionMenu.matches(':popover-open'))resetActionMenu();});
 document.addEventListener('pointerdown',e=>{if(actionTrigger&&!actionMenu.contains(e.target)&&!actionTrigger.contains(e.target))closeActionMenu(false);});
 function trackActionMenu(){if(!actionTrigger)return;const r=actionTrigger.getBoundingClientRect();if(r.bottom<0||r.top>innerHeight||r.right<0||r.left>innerWidth)closeActionMenu(false);else positionActionMenu();}
 window.addEventListener('resize',trackActionMenu);
 document.addEventListener('scroll',e=>{if(!actionMenu.contains(e.target))trackActionMenu();},{capture:true});
 function setTheme(theme){document.documentElement.dataset.theme=theme;try{localStorage.setItem('soar-theme',theme);}catch{}for(const button of [get('theme-toggle'),get('login-theme')]){button.replaceChildren(icon(theme==='dark'?'sun':'moon'));button.setAttribute('aria-label',theme==='dark'?panelText("فعال کردن تم روشن"):panelText("فعال کردن تم تیره"));}}
 let theme='dark';try{theme=localStorage.getItem('soar-theme')==='light'?'light':'dark';}catch{}setTheme(theme);
 get('login-theme').onclick=()=>setTheme(document.documentElement.dataset.theme==='dark'?'light':'dark');
 get('password-toggle').onclick=()=>{const input=get('login-password'),shown=input.type==='password';input.type=shown?'text':'password';get('password-toggle').setAttribute('aria-pressed',String(shown));get('password-toggle').setAttribute('aria-label',shown?panelText("پنهان کردن رمز عبور"):panelText("نمایش رمز عبور"));};
 function syncDrawer(){if(innerWidth>800){document.body.classList.remove('menu-open');get('menu-toggle').setAttribute('aria-expanded','false');}const closed=innerWidth<=800&&!document.body.classList.contains('menu-open'),rail=document.querySelector('.sidebar');rail.inert=closed;if(closed)rail.setAttribute('aria-hidden','true');else rail.removeAttribute('aria-hidden');}
 function closeMenu(){const opened=document.body.classList.contains('menu-open');document.body.classList.remove('menu-open');get('menu-toggle').setAttribute('aria-expanded','false');if(opened)get('menu-toggle').focus();syncDrawer();}
 function toggleMenu(){const open=document.body.classList.toggle('menu-open');get('menu-toggle').setAttribute('aria-expanded',String(open));syncDrawer();if(open)document.querySelector('nav a[aria-current=page]').focus();}
 window.addEventListener('resize',syncDrawer);syncDrawer();
 get('nav-backdrop').onclick=closeMenu;get('rail-close').onclick=closeMenu;
 document.querySelectorAll('.sidebar a[href^="#"]').forEach(link=>link.addEventListener('click',closeMenu));
 document.addEventListener('keydown',event=>{if(!document.body.classList.contains('menu-open'))return;if(event.key==='Escape'){closeMenu();get('menu-toggle').focus();}if(event.key==='Tab'){const links=[...document.querySelectorAll('.sidebar a,.sidebar button')].filter(el=>el.getClientRects().length);const first=links[0],last=links.at(-1);if(event.shiftKey&&document.activeElement===first){event.preventDefault();last.focus();}else if(!event.shiftKey&&document.activeElement===last){event.preventDefault();first.focus();}}});
 function size(n){if(n==null)return '—';if(n===0)return '0 B';const units=['B','KiB','MiB','GiB','TiB'];const i=Math.max(0,Math.min(4,Math.floor(Math.log(n)/Math.log(1024))));return `${(n/1024**i).toLocaleString('en-US',{maximumFractionDigits:i?2:0})} ${units[i]}`;}
 function text(id,value){get(id).textContent=value;}
 function svgEl(tag,attrs){const el=document.createElementNS(ns,tag);for(const [key,val] of Object.entries(attrs))el.setAttribute(key,val);return el;}
 let samples=[],last=null;
 function chart(id,keys,fixedMax,spark=false){
  const container=get(id),w=600,h=spark?80:180,pad=5;
  const values=samples.flatMap(s=>keys.map(k=>s[k])).filter(Number.isFinite);
  container.replaceChildren();
  if(!values.length){const empty=document.createElement('div');empty.className='spark-empty';empty.textContent=spark?panelText("داده‌ای در دسترس نیست"):panelText("در انتظار دو نمونهٔ واقعی از سرور");container.append(empty);return;}
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
 function setConnection(ok){const chip=get('service-chip');chip.dataset.live=String(ok);chip.textContent=ok?panelText("پنل در دسترس"):panelText("بررسی ارتباط");}
 function renderDashboard(d){
  setConnection(true);const h=d.host,now=performance.now(),up=d.dns_received_bytes,down=d.dns_sent_bytes;
  if(last&&(last.uptime>d.uptime_seconds||up<last.up||down<last.down)){samples=[];last=null;}
  const dt=last?(now-last.at)/1000:0;
  const ram=h.memory_total?100*h.memory_used/h.memory_total:null,disk=h.disk_total?100*h.disk_used/h.disk_total:null;
  const sample={at:now,cpu:h.cpu_percent,ram,disk,streams:d.active_streams,send:dt>0?(down-last.down)/dt:null,receive:dt>0?(up-last.up)/dt:null};
  samples.push(sample);samples=samples.filter(s=>now-s.at<=600000).slice(-120);last={at:now,up,down,uptime:d.uptime_seconds};
  text('cpu-reading',h.cpu_percent==null?'—':readingFmt.format(h.cpu_percent));text('ram-reading',ram==null?'—':readingFmt.format(ram));text('disk-reading',disk==null?'—':readingFmt.format(disk));text('streams',String(d.active_streams));text('connection-count',String(d.active_streams));
  text('ram-label',h.memory_total?size(h.memory_used)+' / '+size(h.memory_total):panelText("اطلاعات حافظه در دسترس نیست"));
  text('disk-label',h.disk_total?size(h.disk_used)+' / '+size(h.disk_total):panelText("اطلاعات دیسک در دسترس نیست"));
  text('cpu-label',h.cpu_percent==null?(h.platform==='linux'?panelText("پس از نمونهٔ بعدی مشخص می‌شود"):panelText("اطلاعات پردازنده در دسترس نیست")):panelText("درصد استفاده از پردازنده"));
  text('stat-online',fmt.format(d.accounts_with_streams)+panelText(" حساب دارای جریان باز"));
  text('send-rate',sample.send==null?'—':size(sample.send)+'/s');text('receive-rate',sample.receive==null?'—':size(sample.receive)+'/s');
  text('dns-send-total',size(down));text('dns-receive-total',size(up));text('dns-traffic',size(up+down));
  text('uptime',fmt.format(Math.floor(d.uptime_seconds/3600))+panelText(" ساعت و ")+fmt.format(Math.floor(d.uptime_seconds/60)%60)+panelText(" دقیقه"));
  text('platform',h.platform);text('panel-version',d.version);text('footer-version',d.version);
  const diagnostics=d.diagnostics||{},accounting=d.accounting||{},metric=n=>Number.isFinite(n)?fmt.format(n):'—',ratio=(used,limit)=>metric(used)+' / '+metric(limit);
  text('diag-udp-errors',metric(diagnostics.udp_write_errors));text('diag-queue-drops',metric((diagnostics.soar_queue_drops||0)+(diagnostics.cotten_queue_drops||0)));
  text('diag-dial-failures',metric(diagnostics.dial_failures));text('diag-dial-time',diagnostics.dial_attempts?((diagnostics.dial_latency_microseconds||0)/diagnostics.dial_attempts/1000).toFixed(1)+' ms':'—');
  text('diag-fds',ratio(h.file_descriptors_used,h.file_descriptors_limit));text('diag-conntrack',ratio(h.conntrack_used,h.conntrack_limit));text('diag-accounting',ratio(accounting.requests,accounting.commits));text('diag-accept-retries',metric(diagnostics.tcp_accept_retries));
  text('stat-accounts',fmt.format(d.accounts));text('stat-active',fmt.format(d.states.active)+panelText(" حساب فعال"));text('stat-up',size(d.upload_bytes));text('stat-down',size(d.download_bytes));
  const states={active:panelText("فعال"),disabled:panelText("غیرفعال"),expired:panelText("منقضی"),exhausted:panelText("حجم تمام‌شده")};
  get('status-breakdown').replaceChildren(...Object.entries(states).map(([key,label])=>{const row=document.createElement('div'),badge=document.createElement('span'),value=document.createElement('strong');badge.className='badge '+key;badge.textContent=label;value.textContent=fmt.format(d.states[key]);row.append(badge,value);return row;}));
  for(const [id,key,max] of [['cpu-spark','cpu',100],['ram-spark','ram',100],['disk-spark','disk',100],['streams-spark','streams',0]])chart(id,[key],max,true);
  chart('traffic-chart',['send','receive']);chart('connections-chart',['streams']);
  const elapsed=Math.floor((samples.at(-1).at-samples[0].at)/1000);
  text('chart-window',elapsed<60?panelText("نمونه‌های این نشست"):fmt.format(Math.floor(elapsed/60))+panelText(" دقیقهٔ اخیر"));
  text('chart-start',new Date(Date.now()-(now-samples[0].at)).toLocaleTimeString(panelI18n.locale,{hour:'2-digit',minute:'2-digit'}));
  text('chart-now',panelText("اکنون"));
 }
 return {icon,renderDashboard,setConnection,closeMenu,toggleMenu,accountActions,closeActionMenu,closeSelectMenu,syncSelects,accountMenuOpen:()=>!!actionTrigger,toggleTheme(){setTheme(document.documentElement.dataset.theme==='dark'?'light':'dark');}};
})();
