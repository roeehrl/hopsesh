// Main-window chrome only. Terminal bytes and capabilities stay inside the
// cross-origin sandboxed frame; no messages from it are treated as app calls.
import {api,on,h,fail} from './core.js';

let panel, frame, placement='bottom', maximized=false, details=false;
function showDetails(){details=!details;layout();if(details){const ins=document.querySelector('#inspector');if(ins){ins.inert=false;ins.tabIndex=-1;ins.focus();}}}
addEventListener('terminal-session-details',showDetails);
let height=Number(localStorage.getItem('terminal-height'))||320;
let width=Number(localStorage.getItem('terminal-width'))||640;
function layout(){
 if(!panel)return;
 const side=placement==='right' && innerWidth>=1400 && !maximized;
 document.body.classList.toggle('terminal-right',side&&!panel.hidden);
 document.body.classList.toggle('terminal-bottom',!side&&!panel.hidden);
 document.body.classList.toggle('terminal-maximized',maximized&&!panel.hidden);
 panel.dataset.side=side;
 const size=side?Math.min(width,innerWidth*.6):Math.min(height,innerHeight-180);
 document.documentElement.style.setProperty('--terminal-size',Math.max(side?420:180,size)+'px');
 document.body.classList.toggle('terminal-details',side&&details&&!panel.hidden);
 const detailsButton=panel.querySelector('#terminal-details-button');
 detailsButton.hidden=!side||!document.querySelector('#inspector');detailsButton.textContent=details?'Back to terminal':'Session details';
 detailsButton.setAttribute('aria-expanded',String(details));
 panel.style.left=maximized?'0':side?'':document.querySelector('#sidebar')?'var(--sb-w)':'0';
 document.documentElement.style.setProperty('--terminal-details-top',(panel.querySelector('.terminal-workspace-bar').getBoundingClientRect().bottom)+'px');
 const handle=panel.querySelector('[role=separator]');
 handle.setAttribute('aria-orientation',side?'vertical':'horizontal');
 handle.setAttribute('aria-valuenow',String(Math.round(size)));
}
export function hideWorkspace(){if(panel){panel.hidden=true;layout();api("TerminalWorkspaceVisible",false).catch(fail);document.querySelector('#btn-terminal')?.focus();}}
function build(){
 if(panel)return;
 const divider=h('div',{class:'terminal-divider',role:'separator',tabindex:0,'aria-label':'Terminal size','aria-valuemin':180,'aria-valuemax':1200});
 const maximize=h('button',{class:'btn',onclick:()=>{maximized=!maximized;maximize.textContent=maximized?'Restore layout':'Maximize';layout();}},'Maximize');
 panel=h('section',{id:'terminal-workspace','aria-label':'Terminal workspace'},divider,
 h('div',{class:'terminal-workspace-bar'},h('strong',{},'Terminal'),h('button',{id:'terminal-details-button',class:'btn',onclick:showDetails},'Session details'),h('span',{class:'muted'},'Hiding keeps programs running'),h('span',{class:'spacer'}),maximize,
 h('button',{class:'btn',onclick:()=>api('TerminalPlacement','separate').catch(fail)},'Separate window'),h('button',{class:'btn',onclick:hideWorkspace},'Hide terminal')));
 frame=h('iframe',{title:'Hopsesh Terminal',sandbox:'allow-scripts',referrerpolicy:'no-referrer'});
 panel.append(frame);document.body.append(panel);
 let start;
 divider.onpointerdown=e=>{start={x:e.clientX,y:e.clientY,w:width,h:height};divider.setPointerCapture(e.pointerId);panel.classList.add('resizing');};
 divider.onpointermove=e=>{if(!start)return;if(panel.dataset.side==='true')width=Math.max(420,start.w+start.x-e.clientX);else height=Math.max(180,start.h+start.y-e.clientY);layout();};
 divider.onpointerup=()=>{start=null;panel.classList.remove('resizing');saveSizes();};
 divider.onpointercancel=()=>{start=null;panel.classList.remove('resizing');};
 divider.onkeydown=e=>{if(!['ArrowUp','ArrowDown','ArrowLeft','ArrowRight','Home','End'].includes(e.key))return;e.preventDefault();const side=panel.dataset.side==='true';const delta=['ArrowUp','ArrowLeft'].includes(e.key)?24:-24;let value=side?width:height;value=e.key==='Home'?(side?420:180):e.key==='End'?(side?innerWidth*.6:innerHeight-180):value+delta;if(side)width=Math.max(420,value);else height=Math.max(180,value);layout();saveSizes();};
}
function saveSizes(){localStorage.setItem('terminal-height',String(height));localStorage.setItem('terminal-width',String(width));}
export async function showWorkspace(data){
 if(!data)data=await api('TerminalWorkspace');
 if(data.placement==='separate'){hideWorkspace();return;}
 build();placement=data.placement||'bottom';
 if(data.url && frame.src!==data.url)frame.src=data.url;
 panel.hidden=false;layout();api("TerminalWorkspaceVisible",true).catch(fail);
}
on('hopsesh:terminal-workspace',data=>showWorkspace(data).catch(fail));
addEventListener('resize',layout);

new MutationObserver(()=>{if(panel&&!panel.hidden)layout();}).observe(document.querySelector('#view'),{childList:true});

on("hopsesh:terminal-main",()=>{(document.querySelector('.row[aria-selected="true"]')||document.querySelector("#list-filter")||document.querySelector("#btn-terminal"))?.focus();});
