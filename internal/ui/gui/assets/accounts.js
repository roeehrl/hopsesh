import {api,h,fill,view,state,screen,go,here,toast,fail,ask,errText,current,navigationID,sys,on,ago} from './core.js';
const checking=new Set(), scanningMachines=new Set(), lastAttempt=new Map();
let accounts=[], query='', tag='', group='machine', busy=false, problem='';
const expanded=new Map();
const localMachine=name=>(state.scan?.machines||[]).some(m=>m.local&&m.name===name);
const byMachine=(a,b)=>Number(localMachine(b))-Number(localMachine(a))||a.localeCompare(b);
const text=(p)=>[p.name,p.agent,p.machine,p.root,p.account?.email,...(p.tags||[])].join(' ').toLowerCase();
const status=p=>p.error ? `Check failed: ${p.error}${p.account?.email ? " · Last known: "+p.account.email : ""}` : !p.account ? 'Not checked yet' : `${p.account.loggedIn ? "Signed in · "+(p.account.email || p.account.label || p.account.provider || "Account name unavailable") : p.identitySource==='ssh' ? "No sign-in visible over SSH · Sign-in on the machine may differ" : 'Signed out'}${p.stale ? ' · Last known' : ''}`;
async function check(p){if(checking.has(p.id))return;lastAttempt.set(p.id,Date.now());checking.add(p.id);render();try{await api('RefreshAccount',p.id);state.scan=await api('ScanSnapshot');await reload()}catch(e){problem=errText(e);await reload()}finally{checking.delete(p.id);if(current==='accounts')render()}}
on('hopsesh:accounts',()=>{reload().catch(fail)});
let refreshing=false;
setInterval(async()=>{if(current!=='accounts'||document.hidden||refreshing||busy||document.querySelector('dialog[open]'))return;refreshing=true;try{const result=await api('Accounts');if(JSON.stringify(result)!==JSON.stringify(accounts)){accounts=result;render()}}catch{}finally{refreshing=false}},2000);

// Refresh stale sign-in metadata one profile at a time. Watcher-driven local
// scans must not starve remote identities; failed checks retry after five minutes.
setInterval(()=>{if(current!=='accounts'||document.hidden||busy||checking.size||scanningMachines.size||document.querySelector('dialog[open]')||document.activeElement?.matches('input,select'))return;const p=accounts.find(p=>p.stale&&Date.now()-(lastAttempt.get(p.id)||0)>=300000);if(p)check(p).catch(fail)},15000);

let read=0;
async function reload(){const n=++read,visit=navigationID();const result=await api('Accounts');if(n!==read||visit!==navigationID())return;accounts=result;if(current==='accounts')render();}
async function scan(){if(busy)return;busy=true;problem='';render();try{state.scan=await api('ScanAccounts');state.stale=false;await reload();}catch(e){problem=errText(e)}finally{busy=false;if(current==='accounts')render()}}
function editor(p){
 const dlg=document.createElement('dialog');dlg.className='account-editor';
 const name=h('input',{class:'field',value:p?.name||'',required:true,maxlength:120});
 const tags=h('input',{class:'field',value:(p?.tags||[]).join(', '),placeholder:'Personal, Research, Client A'});
 const agent=h('select',{class:'field','aria-label':'Agent'},h('option',{value:'claude'},'Claude Code'),h('option',{value:'codex'},'Codex'));
 const machine=h('select',{class:'field','aria-label':'Machine'},[...(state.scan?.machines||[{name:here(),local:true}])].sort((a,b)=>Number(b.local)-Number(a.local)||a.name.localeCompare(b.name)).map(m=>h('option',{value:m.name},m.local?'This machine':m.name)));
 const root=h('input',{class:'field',placeholder:'Leave empty to create a new local profile'});
 const error=h('p',{class:'err',role:'alert'});
 const submit=h('button',{class:'btn primary',type:'submit'},p?'Save changes':'Add account');
 const form=h('form',{onsubmit:async ev=>{ev.preventDefault();error.textContent="";try{const ts=tags.value.split(',');if(p)await api('EditAccount',p.id,name.value,ts,p.generation);else await api('RegisterAccount',machine.value,agent.value,name.value,root.value,ts);state.stale=true;dlg.close();await scan();}catch(e){error.textContent=errText(e)}}},
 h('h2',{},p?'Edit account':'Add an account'),h('p',{class:'muted'},'Names and tags are yours. Two personal accounts, multiple work accounts, or any combination.'),
 h('label',{},'Name',name),!p?h('label',{},'Agent',agent):null,!p?h('label',{},'Machine',machine):null,
 !p?h('label',{},'Existing state root (optional)',root):null,h('label',{},'Tags, separated by commas',tags),
 !p?h('p',{class:'muted'},'A new profile starts empty. Sign in using the agent’s own login flow. Claude Console logins without API keys cannot be isolated. New Codex profiles require an OS keyring. For a remote account, supply its existing absolute state root.'):null,error,
 h('div',{class:'account-actions'},h('button',{class:'btn',type:'button',onclick:()=>dlg.close()},'Cancel'),submit));
 dlg.append(form);document.body.append(dlg);dlg.addEventListener('close',()=>dlg.remove());dlg.showModal();name.focus();
}
function row(p){return h('article',{class:'account-card','data-profile':p.id},h('div',{},h('h3',{},p.name),h('div',{class:'muted'},`${p.agent==='claude'?'Claude Code':'Codex'} · ${p.machine||'Machine unavailable'}`),h('p',{class:p.error?'err':'muted'},status(p)),h('div',{class:'account-tags'},(p.tags||[]).map(t=>h('span',{class:'pill'},t))),h('details',{},h('summary',{},'Profile details'),h('p',{class:'mono'},p.root),h('p',{class:'mono'},p.id),h('p',{class:'muted'},`Last checked ${p.checkedAt && !p.checkedAt.startsWith('0001-') ? ago(p.checkedAt) : 'never'} · ${p.identitySource==='owner'?'Reported by Hopsesh on '+p.machine:p.identitySource==='ssh'?'Checked over SSH':p.identitySource==='local'?'Checked on this machine':'Last saved observation'}`),h('p',{class:'muted'},'The agent reports its current sign-in. Email and organization are not a verified identity or proof of who created older sessions.'),p.account?.isolationWhy?h('p',{class:'warn'},p.account.isolationWhy):null)),
 h('div',{class:'account-actions'},h('button',{class:'btn small',disabled:busy||checking.has(p.id),'aria-label':`Check sign-in for ${p.name} on ${p.machine}`,onclick:()=>check(p)},checking.has(p.id)?'Checking…':'Check sign-in'),h('button',{class:'btn small',onclick:()=>editor(p)},'Edit'),h('button',{class:'btn small',disabled:!p.local||busy,title:p.local?'Sign in using the vendor CLI':'Sign in on this account’s machine',onclick:async()=>{try{await api('LoginAccount',p.id);toast('Complete sign-in in the terminal. Your account refreshes automatically when it finishes.')}catch(e){fail(e)}}},'Sign in'),h('button',{class:'btn small',onclick:async()=>{if(!await ask({title:`Forget ${p.name}?`,body:'This removes the registration. Vendor files and credentials stay on the machine. A default root may be discovered again.',ok:'Forget'}))return;try{await api('ForgetAccount',p.id,p.generation);await reload();state.stale=true}catch(e){fail(e)}}},'Forget')))}
function setupNotice(m){return h('aside',{class:'account-card account-setup','aria-label':`Account setup on ${m.name}`},
 h('p',{},'SSH is connected and sessions can be read. Set up Hopsesh on this machine to discover its accounts.'),
 h('p',{class:'muted'},`Install Hopsesh on ${m.name} if needed, then run this command there:`),h('code',{},'hopsesh accounts scan --machine local'),
 h('p',{class:'muted'},'Then click Scan this machine here. Scanning from this computer does not install or initialize Hopsesh remotely.'),
 h('button',{class:'btn small',onclick:()=>go('machines')},'Manage machines'));}
function accountGroup(name,ps,m=null){
 const machine=group==='machine'||!!m, k=(machine?'machine':group)+':'+name;
 const local=machine&&localMachine(name), setup=!!m?.accountSetupRequired;
 const label=local?`${sys.Here} · ${name}`:name;
 return h('details',{class:'account-group','data-group':k,open:expanded.get(k)??!setup,ontoggle:ev=>{if(ev.target.isConnected)expanded.set(k,ev.target.open)}},
  h('summary',{},h('span',{},label),h('span',{class:setup?'pill warn':'muted'},setup?'Account setup required':`${ps.length} account${ps.length===1?'':'s'}`),machine?h('button',{class:'btn small',disabled:busy||scanningMachines.has(name),onclick:async ev=>{ev.preventDefault();ev.stopPropagation();scanningMachines.add(name);render();try{if(local)state.scan=await api('RefreshHere');else{await api('ScanMachine',name);state.scan=await api('ScanSnapshot')}state.stale=false;await reload()}catch(e){problem=errText(e)}finally{scanningMachines.delete(name);if(current==='accounts')render()} }},scanningMachines.has(name)?'Scanning…':'Scan this machine'):null),
  ps.length?h('div',{class:'account-grid'},ps.map(row)):null,setup?setupNotice(m):null);
}
function render(){
 if(current!=='accounts')return;
 const tagNames=new Map();for(const p of accounts)for(const t of p.tags||[])if(!tagNames.has(t.toLowerCase()))tagNames.set(t.toLowerCase(),t);
 const tags=[...tagNames.values()].sort((a,b)=>a.localeCompare(b));
 const visible=[...accounts].sort((a,b)=>byMachine(a.machine||'',b.machine||'')||a.name.localeCompare(b.name)).filter(p=>text(p).includes(query.toLowerCase())&&(!tag||(tag==='__untagged'?!p.tags?.length:p.tags?.some(t=>t.toLowerCase()===tag.toLowerCase()))));
 const groups=new Map();for(const p of visible){const keys=group==='tag'?(p.tags?.length?p.tags.map(t=>tagNames.get(t.toLowerCase())):['Untagged']):[group==='agent'?p.agent:group==='none'?'All accounts':p.machine||'Machine unavailable'];for(const k of keys){if(!groups.has(k))groups.set(k,[]);groups.get(k).push(p)}}
 const setupMachines=(state.scan?.machines||[]).filter(m=>m.accountSetupRequired&&m.name.toLowerCase().includes(query.toLowerCase())&&!tag).sort((a,b)=>byMachine(a.name,b.name));
 const setupByName=new Map(setupMachines.map(m=>[m.name,m]));
 if(group==='machine')for(const m of setupMachines)if(!groups.has(m.name))groups.set(m.name,[]);
 const sections=[...groups].sort(([a],[b])=>group==='machine'?byMachine(a,b):a.localeCompare(b)).map(([name,ps])=>accountGroup(name,ps,group==='machine'?setupByName.get(name):null));
 if(group!=='machine')sections.push(...setupMachines.map(m=>accountGroup(m.name,[],m)));
 fill(view,h('div',{class:'page'},h('div',{class:'page-in accounts-page'},h('div',{class:'account-toolbar'},h('h1',{},'Accounts'),h('span',{class:'spacer'}),h('button',{class:'btn',disabled:busy,onclick:scan},busy?'Scanning…':'Scan accounts'),h('button',{class:'btn primary',onclick:()=>editor()},'Add account')),
 h('p',{class:'muted'},'Independent Claude Code and Codex profiles. Tags organize accounts; they do not change logins or transfer permissions.'),
 h('div',{class:'account-toolbar'},h('input',{class:'field',type:'search','aria-label':'Search accounts',placeholder:'Name, email, tag or machine',value:query,oninput:ev=>{query=ev.target.value;render();const input=view.querySelector('[aria-label="Search accounts"]');input.focus();input.setSelectionRange(query.length,query.length)}}),
 h('select',{'aria-label':'Filter accounts by tag',onchange:ev=>{tag=ev.target.value;render()}},h('option',{value:'',selected:!tag},'All tags'),h('option',{value:'__untagged',selected:tag==='__untagged'},'Untagged'),tags.map(t=>h('option',{value:t,selected:tag===t},t))),
 h('select',{'aria-label':'Group accounts',onchange:ev=>{group=ev.target.value;render()}},['machine','agent','tag','none'].map(v=>h('option',{value:v,selected:group===v},`Group: ${v}`)))),
 problem?h('p',{class:'err',role:'alert'},problem):null,
 h('div',{'aria-live':'polite'},`${visible.length} accounts`),sections,
 !visible.length&&!setupMachines.length?h('p',{class:'muted'},'No matching accounts. Scan known roots or add an account.'):null,
 h('p',{class:'muted'},'Discovery checks known default roots and registered roots on allowed machines with an initialized Hopsesh identity. Each card identifies the current sign-in for a separate state root. Emails do not establish ownership of older sessions.'))));
}
screen('accounts',async()=>{const visit=navigationID();await reload();if(visit===navigationID()&&!accounts.length)await scan()});
