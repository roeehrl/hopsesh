import {api,on,h,fill,view,state,setSystem,sys,errText,agentBadge} from './core.js';
import {markdown} from './markdown.js';
import {desktopSettings} from './desktop-settings.js';

let data=null,screen='home',tab='focus',query='',selected=null,preview=null,request=0,pending=false,again=false,scopedTabs=null;
const key=e=>e.machine+'\0'+e.key;
const local=e=>data?.scan?.machines?.some(m=>m.local&&m.name===e.machine);
const entries=()=>data?.scan?.groups?.flatMap(g=>g.entries.map(e=>({...e,...data.presence?.entries?.[key(e)],repository:g.name})))||[];
const sessionTabs=e=>(data?.tabs||[]).filter(t=>t.machine===e.machine&&t.key===e.key);
const needs=e=>local(e)&&(e.needs||sessionTabs(e).some(t=>t.attention));
const active=e=>local(e)&&(e.live||sessionTabs(e).some(t=>t.state!=='exited'));
const age=iso=>{const n=Date.parse(iso);if(!Number.isFinite(n)||n<1000)return 'not yet scanned';const s=Math.max(0,Math.floor((Date.now()-n)/1000));return s<60?'just now':s<3600?Math.floor(s/60)+'m ago':s<86400?Math.floor(s/3600)+'h ago':Math.floor(s/86400)+'d ago';};
const account=e=>[e.profile?.account?.email||e.profile?.name,...e.profile?.tags||[]].filter(Boolean).join(' · ');
const status=e=>!local(e)?`${e.status||'Status unknown'} · last scanned ${age(data.scan.elsewhere)}`:needs(e)?'Needs you':sessionTabs(e).length?`${sessionTabs(e).length} terminal tab${sessionTabs(e).length===1?'':'s'} · ${e.status||'Open'}`:e.status||'Status unknown';
function error(e){let el=document.querySelector('#quick-error');if(!el){el=h('div',{id:'quick-error',class:'desktop-note',role:'alert'});view.append(el)}el.textContent=errText(e);}
async function action(fn){try{await fn()}catch(e){error(e)}}
const open=(e=null)=>action(()=>api('QuickOpen','sessions',e?.machine||'',e?.key||''));
function head(back=false){return h('header',{class:'quick-head'},back?h('button',{class:'btn',onclick:()=>{screen='home';request++;render()}},'← Quick access'):h('h1',{},'Hopsesh'),h('span',{class:'spacer'}),!back?h('button',{class:'btn','aria-label':'Desktop presence settings',onclick:()=>{screen='settings';render()}},'Settings'):null,!back?h('button',{class:'btn','aria-label':'More options',onclick:()=>{screen='menu';render()}},'⋯'):null,h('button',{class:'btn','aria-label':'Close Quick access',onclick:()=>api('QuickClose')},'×'));}
function row(e){return h('button',{class:'quick-row','data-session':key(e),onclick:()=>select(e)},agentBadge(e.agent,e.agentName),h('span',{class:'quick-row-copy'},h('span',{class:'quick-title',title:e.title},e.title),h('span',{class:'quick-meta'},`${e.repository} · ${local(e)?sys.Here:e.machine}${account(e)?' · '+account(e):''}`),h('span',{class:'quick-state'+(needs(e)?' needs':'')},status(e))),h('span',{'aria-hidden':'true'},'›'));}
function render(){
 if(!data){fill(view,head(),h('p',{class:'quick-empty'},'Reading sessions…'));return}
 const es=entries();
 if(screen==='settings'){fill(view,head(true),h('div',{class:'quick-body'},h('h2',{},'Desktop presence'),desktopSettings({...data.desktop,os:data.os})));return}
 if(screen==='menu'){fill(view,head(true),h('div',{class:'quick-body quick-actions'},h('button',{class:'btn',onclick:()=>open()},'Open Hopsesh'),h('button',{class:'btn',onclick:()=>{scopedTabs=null;screen='tabs';render()}},`Open terminals · ${data.tabs.length} tabs`),h('button',{class:'btn',onclick:()=>{screen='settings';render()}},'Desktop presence…'),h('button',{class:'btn',onclick:()=>api('QuickQuit')},'Quit Hopsesh…')));return}
 if(screen==='tabs'){const ts=scopedTabs?data.tabs.filter(t=>t.machine===scopedTabs.machine&&t.key===scopedTabs.key):data.tabs;fill(view,head(true),h('div',{class:'quick-body'},h('h2',{},'Open terminal tabs'),ts.length?ts.map(t=>h('button',{class:'quick-row',onclick:()=>action(async()=>{await api('TerminalFocus',t.id);await api('QuickClose')})},h('span',{class:'quick-row-copy'},h('b',{},t.title||t.command||'Terminal'),h('span',{class:'muted'},t.state==='exited'?'Ended':t.attention?'Needs you':'Open')))):h('p',{class:'muted'},'No matching terminal tabs.')));return}
 if(screen==='detail'){
  const e=es.find(e=>key(e)===key(selected));if(!e){screen='home';render();return}selected=e;const ts=sessionTabs(e);
  const content=h('div',{class:'quick-body'},h('h2',{},e.title),h('p',{class:'muted'},`${e.agentName} · ${e.repository}`),h('p',{class:'quick-meta'},`${local(e)?sys.Here:e.machine} · ${account(e)||'Account not identified'}`),h('p',{class:needs(e)?'desktop-note':'quick-status'},status(e)));
  if(e.lineageError)content.append(h('p',{class:'desktop-note'},'Transfer history needs review. Open session details to resolve it.'));
  else if(e.journey){const j=e.journey;content.append(h('p',{class:'quick-meta'},[j.fork?'Fork':null,`${j.transfers} transfers`,`${j.roundTrips} round trips`,`${j.returns} returns`].filter(Boolean).join(' · ')));}
  if(data.desktop.preferences.previews!==false){content.append(h('h3',{},'Recent conversation'));if(preview){for(const m of preview.items||[])content.append(h('section',{class:'quick-message '+(m.role==='user'?'user':'agent')},h('b',{},m.role==='user'?'You':m.role==='agent'?e.agentName:m.role),markdown(m.text)));if(preview.note)content.append(h('p',{class:'muted'},preview.note));}else content.append(h('p',{class:'muted'},'Reading conversation…'));}else content.append(h('p',{class:'muted'},'Message previews are hidden.'));
  const actions=h('div',{class:'quick-actions'});
  if(ts.length)actions.append(h('button',{class:'btn primary',onclick:()=>{if(ts.length===1)action(async()=>{await api('TerminalFocus',ts[0].id);await api('QuickClose')});else{scopedTabs=e;screen='tabs';render()}}},ts.length===1?'Show existing terminal tab':`Choose from ${ts.length} open terminal tabs…`));
  else if(local(e)&&e.canApp&&!e.live)actions.append(h('button',{class:'btn primary',onclick:()=>action(async()=>{await api('ResumeEntry',e.machine,e.key,true);await api('QuickClose')})},`Open in ${e.agent==='codex'?'Codex':e.agent==='claude'?'Claude':e.agentName} app`));
  actions.append(h('button',{class:'btn',onclick:()=>open(e)},'Open session details'));content.append(actions);fill(view,head(true),content);return;
 }
 const waiting=es.filter(needs).length;const input=h('input',{class:'field quick-search',type:'search','aria-label':'Search sessions, accounts or machines',placeholder:'Search sessions, accounts, machines…',value:query,oninput:e=>{query=e.target.value;renderList()},onkeydown:e=>{if(e.key==='Enter'){e.preventDefault();document.querySelector('.quick-list .quick-row')?.click()}}});
 fill(view,head(),h('div',{class:'quick-summary'},`${waiting} need you · ${data.tabs.length} open terminal tabs`),input,h('div',{class:'quick-switch'},['focus','recent'].map(t=>h('button',{class:'btn','aria-pressed':tab===t?'true':'false',onclick:()=>{tab=t;render()}},t==='focus'?'Focus':'Recent'))),h('div',{class:'quick-list','aria-label':'Sessions'}),h('footer',{class:'quick-foot'},h('button',{class:'btn',onclick:()=>open()},'Open Hopsesh ↗'),h('button',{class:'btn quick-refresh',disabled:data.refreshing,onclick:async()=>{data.refreshing=true;render();await action(()=>api('QuickRefresh'))}},data.refreshing?'Checking…':`Refresh · ${age(data.scan?.updated)}`)));
 if(data.error)error(data.error);renderList();
}
function renderList(){const list=document.querySelector('.quick-list');if(!list)return;const q=query.toLocaleLowerCase();const filtered=entries().filter(e=>q?[e.title,e.repository,e.agentName,e.machine,account(e)].join(' ').toLocaleLowerCase().includes(q):tab==='focus'?needs(e)||active(e):true).sort((a,b)=>q||tab==='recent'?Date.parse(b.lastActive)-Date.parse(a.lastActive):Number(needs(b))-Number(needs(a))||Date.parse(b.lastActive)-Date.parse(a.lastActive));let last='';const rows=[];for(const e of filtered.slice(0,60)){const group=q?'Search results':tab==='recent'?'Recent sessions':needs(e)?'Needs you':'Running on this device';if(group!==last){rows.push(h('div',{class:'quick-group'},group));last=group}rows.push(row(e))}fill(list,rows.length?rows:h('div',{class:'quick-empty'},q?'No matching sessions.':tab==='focus'?'No sessions need attention or are running here.':'No sessions found. Refresh to scan this device.'),filtered.length>60?h('button',{class:'btn',onclick:()=>open()},'See all matches in Hopsesh'):null);}
async function select(e){selected=e;screen='detail';preview=null;const n=++request;render();if(data.desktop.preferences.previews===false)return;try{const p=await api('QuickPreview',e.machine,e.key);if(n===request&&screen==='detail'){preview=p;render()}}catch(err){if(n===request){preview={note:errText(err)};render()}}}
async function refresh(){
 if(pending){again=true;return}pending=true;
 try{
  data=await api('QuickSnapshot');setSystem(data.os,'Terminal');
  state.info={agents:[...new Map(entries().map(e=>[e.agent,{id:e.agent,name:e.agentName}])).values()]};
  if(screen!=='settings'){
   const focused=document.activeElement,search=focused?.matches('.quick-search'),session=focused?.dataset.session;
   const label=focused?.getAttribute('aria-label'),text=focused?.matches('button')?focused.textContent:null;
   const scroll=document.querySelector('.quick-list,.quick-body')?.scrollTop||0;
   render();
   const body=document.querySelector('.quick-list,.quick-body');if(body)body.scrollTop=scroll;
   const buttons=[...document.querySelectorAll('button')];
   const next=search?document.querySelector('.quick-search'):session?buttons.find(b=>b.dataset.session===session):label?buttons.find(b=>b.getAttribute('aria-label')===label):text?buttons.find(b=>b.textContent===text):null;
   next?.focus({preventScroll:true});
  }
 }catch(e){error(e)}finally{pending=false;if(again){again=false;refresh()}}
}

on('hopsesh:quick',refresh);on('hopsesh:terminal',refresh);
document.addEventListener('visibilitychange',()=>{if(!document.hidden){refresh();api('QuickRefresh')}});
document.addEventListener('keydown',e=>{if(e.key==='Escape')api('QuickClose');if(['ArrowDown','ArrowUp'].includes(e.key)&&!e.target.matches('input,select,textarea')){const rows=[...document.querySelectorAll('.quick-list .quick-row')];const i=rows.indexOf(document.activeElement),n=Math.max(0,Math.min(rows.length-1,i+(e.key==='ArrowDown'?1:-1)));rows[n]?.focus();e.preventDefault()}});
render();await refresh();if(!data?.scan)api('QuickRefresh');
