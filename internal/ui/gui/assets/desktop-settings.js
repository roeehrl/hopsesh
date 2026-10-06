import { api, h, fill } from './core.js';

// Shared by Settings and Quick access: one settings form and one native API.
export function desktopSettings(initial) {
 const root=h('section',{class:'desktop-settings'});let s=initial,busy=false;
 const paint=()=>{
  const p=s.preferences,os=s.os||initial.os,mac=os==='darwin',win=os==='windows';
  const app=mac?'Dock':win?'taskbar':'app launcher',tray=mac?'menu bar':'system tray';
  const error=h('p',{class:'err',role:'alert'},s.error||s.loginError||'');
  const modes=h('fieldset',{},h('legend',{},'Where would you like Hopsesh?'));
  for(const [id,label,desc] of [['app',mac?'Dock only':win?'Taskbar only':'App launcher only',`Normal ${app}. No ${tray} icon.`],['tray',mac?'Menu bar only':'System tray only',`Use the ${tray} to return. Hopsesh is removed from the app switcher.`],['both','Both',`Quick access in the ${tray}, plus the normal ${app}.`]]) {
   const disabled=busy||(id!=='app'&&!s.capabilities.tray)||(id==='tray'&&!s.capabilities.hideApp);
   modes.append(h('label',{class:'desktop-mode'},h('input',{type:'radio',name:'desktop-mode',value:id,checked:(p.mode||'app')===id,disabled,onchange:()=>{p.mode=id;if(id==='app'&&!mac&&p.close==='keep')p.close='quit';paint();}}),h('span',{},h('b',{},label),h('span',{class:'muted'},desc))));
  }
  const toggle=(key,label,value)=>h('label',{class:'desktop-pref'},h('span',{},label),h('input',{type:'checkbox',checked:value,disabled:busy,onchange:e=>{if(key==='login')s.login=e.target.checked;else p[key]=e.target.checked;}}));
  const keepAllowed=mac||s.capabilities.tray&&(p.mode||'app')!=='app';
  const close=h('label',{class:'desktop-pref'},h('span',{},'When the main window closes'),h('select',{'aria-label':'When the main window closes',disabled:busy,onchange:e=>p.close=e.target.value},h('option',{value:'keep',selected:p.close==='keep',disabled:!keepAllowed},`Keep in ${p.mode==='app'?app:tray}`),h('option',{value:'quit',selected:p.close!=='keep'},'Quit Hopsesh')));
  const save=h('button',{class:'btn primary',disabled:busy,onclick:async()=>{
   busy=true;paint();try{await api('SaveDesktop',{mode:p.mode||'app',close:p.close||'quit',attention:p.attention!==false,previews:p.previews!==false,login:!!s.login});s={...await api('DesktopSettings'),os};busy=false;paint();root.querySelector('[role=status]').textContent='Preferences saved.';}catch(e){busy=false;paint();root.querySelector('[role=alert]').textContent=String(e.message||e);}
  }},busy?'Saving…':'Save preferences');
  fill(root,modes,s.capabilities.reason?h('p',{class:'desktop-note'},s.capabilities.reason):null,
   p.mode&&p.mode!==s.effective?h('p',{class:'muted'},`Currently using ${s.effective==='app'?app:'both'}; the saved preference is unavailable or not yet applied.`):null,
   toggle('login','Start Hopsesh at login',s.login),close,
   toggle('attention','Show an attention indicator',p.attention!==false),toggle('previews','Show message previews in Quick access',p.previews!==false),
   win?h('p',{class:'muted'},'Windows may place the icon under Show hidden icons. Pinned shortcuts stay under your control.'):null,
   h('div',{class:'desktop-buttons'},save,s.effective!=='app'?h('button',{class:'btn',onclick:()=>api('QuickShow').catch(e=>{error.textContent=String(e.message||e)})},'Open Quick access'):null,h('button',{class:'btn',disabled:busy,onclick:async()=>{try{s={...await api('RecheckDesktop'),os};paint();}catch(e){error.textContent=String(e.message||e)}}},'Check support again')),error,h('p',{role:'status','aria-live':'polite'}));
 };
 paint();return root;
}
