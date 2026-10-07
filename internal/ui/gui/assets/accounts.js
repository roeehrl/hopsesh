import {api,h,fill,view,state,screen,go,here,toast,fail,ask,errText,current,navigationID} from './core.js';
let accounts=[], query='', tag='', group='machine', busy=false, problem='';
const collapsed=new Set();
const text=(p)=>[p.name,p.agent,p.machine,p.root,p.account?.email,...(p.tags||[])].join(' ').toLowerCase();
const status=(p)=>p.error || p.account?.isolationWhy || (p.stale?'Machine has not been checked':p.account?.loggedIn ? `${p.account.email || p.account.label || p.account.provider} · ${p.account.confidence || 'unverified'} identity`:'Sign-in not verified');
let read=0;
async function reload(){const n=++read,visit=navigationID();const result=await api('Accounts');if(n!==read||visit!==navigationID())return;accounts=result;if(current==='accounts')render();}
async function scan(){if(busy)return;busy=true;problem='';render();try{state.scan=await api('ScanAccounts');state.stale=true;await reload();}catch(e){problem=errText(e)}finally{busy=false;if(current==='accounts')render()}}
function editor(p){
 const dlg=document.createElement('dialog');dlg.className='account-editor';
 const name=h('input',{class:'field',value:p?.name||'',required:true,maxlength:120});
 const tags=h('input',{class:'field',value:(p?.tags||[]).join(', '),placeholder:'Personal, Research, Client A'});
 const agent=h('select',{class:'field','aria-label':'Agent'},h('option',{value:'claude'},'Claude Code'),h('option',{value:'codex'},'Codex'));
 const machine=h('select',{class:'field','aria-label':'Machine'},(state.scan?.machines||[{name:here(),local:true}]).map(m=>h('option',{value:m.name},m.local?'This machine':m.name)));
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
function row(p){return h('article',{class:'account-card','data-profile':p.id},h('div',{},h('h3',{},p.name),h('div',{class:'muted'},`${p.agent==='claude'?'Claude Code':'Codex'} · ${p.machine||'Machine unavailable'}`),h('p',{class:p.error?'err':'muted'},status(p)),h('div',{class:'account-tags'},(p.tags||[]).map(t=>h('span',{class:'pill'},t))),h('details',{},h('summary',{},'Profile details'),h('p',{class:'mono'},p.root),h('p',{class:'mono'},p.id))),
 h('div',{class:'account-actions'},h('button',{class:'btn small',onclick:()=>editor(p)},'Edit'),h('button',{class:'btn small',disabled:!p.local||busy,title:p.local?'Sign in using the vendor CLI':'Sign in on this account’s machine',onclick:async()=>{try{await api('LoginAccount',p.id);toast('Complete sign-in in the terminal, then scan accounts.')}catch(e){fail(e)}}},'Sign in'),h('button',{class:'btn small',onclick:async()=>{if(!await ask({title:`Forget ${p.name}?`,body:'This removes the registration. Vendor files and credentials stay on the machine. A default root may be discovered again.',ok:'Forget'}))return;try{await api('ForgetAccount',p.id,p.generation);await reload();state.stale=true}catch(e){fail(e)}}},'Forget')))}
function render(){
 if(current!=='accounts')return;
 const tagNames=new Map();for(const p of accounts)for(const t of p.tags||[])if(!tagNames.has(t.toLowerCase()))tagNames.set(t.toLowerCase(),t);
 const tags=[...tagNames.values()].sort((a,b)=>a.localeCompare(b));
 const visible=accounts.filter(p=>text(p).includes(query.toLowerCase())&&(!tag||(tag==='__untagged'?!p.tags?.length:p.tags?.some(t=>t.toLowerCase()===tag.toLowerCase()))));
 const groups=new Map();for(const p of visible){const keys=group==='tag'?(p.tags?.length?p.tags.map(t=>tagNames.get(t.toLowerCase())):['Untagged']):[group==='agent'?p.agent:group==='none'?'All accounts':p.machine||'Machine unavailable'];for(const k of keys){if(!groups.has(k))groups.set(k,[]);groups.get(k).push(p)}}
 fill(view,h('div',{class:'page'},h('div',{class:'page-in accounts-page'},h('div',{class:'account-toolbar'},h('h1',{},'Accounts'),h('span',{class:'spacer'}),h('button',{class:'btn',disabled:busy,onclick:scan},busy?'Scanning…':'Scan accounts'),h('button',{class:'btn primary',onclick:()=>editor()},'Add account')),
 h('p',{class:'muted'},'Independent Claude Code and Codex profiles. Tags organize accounts; they do not change logins or transfer permissions.'),
 h('div',{class:'account-toolbar'},h('input',{class:'field',type:'search','aria-label':'Search accounts',placeholder:'Name, email, tag or machine',value:query,oninput:ev=>{query=ev.target.value;render();const input=view.querySelector('[aria-label="Search accounts"]');input.focus();input.setSelectionRange(query.length,query.length)}}),
 h('select',{'aria-label':'Filter accounts by tag',onchange:ev=>{tag=ev.target.value;render()}},h('option',{value:'',selected:!tag},'All tags'),h('option',{value:'__untagged',selected:tag==='__untagged'},'Untagged'),tags.map(t=>h('option',{value:t,selected:tag===t},t))),
 h('select',{'aria-label':'Group accounts',onchange:ev=>{group=ev.target.value;render()}},['machine','agent','tag','none'].map(v=>h('option',{value:v,selected:group===v},`Group: ${v}`)))),
 problem?h('p',{class:'err',role:'alert'},problem):null,
 h('div',{'aria-live':'polite'},`${visible.length} accounts`),[...groups].map(([name,ps])=>h('details',{class:'account-group',open:!collapsed.has(group+':'+name),ontoggle:ev=>{const k=group+':'+name;if(ev.target.open)collapsed.delete(k);else collapsed.add(k)}},h('summary',{},`${name} · ${ps.length}`),h('div',{class:'account-grid'},ps.map(row)))),
 !visible.length?h('p',{class:'muted'},'No matching accounts. Scan known roots or add an account.'):null,
 h('p',{class:'muted'},'Discovery checks known default roots and registered roots on allowed machines. Sign-in metadata can be limited; a matching email is never treated as proof of account identity.'))));
}
screen('accounts',async()=>{const visit=navigationID();await reload();if(visit===navigationID()&&!accounts.length)await scan()});
