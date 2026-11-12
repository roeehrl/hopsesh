import {test,expect} from '@playwright/test';
import {fresh,row,oldRow,details} from './helpers';
import {readFile,appendFile} from 'node:fs/promises';
const gate=()=>{let release!:()=>void;const promise=new Promise<void>(r=>release=r);return {promise,release};};
test('pending repository details make no claim about local checkout availability',async({page})=>{
 await page.request.post('/reset');
 const snapshot=(await (await page.request.post('/call',{data:{m:'Scan',a:[]}})).json()).result;
 const pending=structuredClone(snapshot);pending.revision+=100;pending.cached=true;pending.discovering=true;
 pending.groups=pending.groups.map((g:any)=>({...g,name:'Repository details pending',remote:'',local:'',noRepo:false,noRemote:false,repoPending:true}));
 const known=structuredClone(pending);known.revision+=100;known.cached=false;known.discovering=false;
 known.groups=known.groups.map((g:any)=>({...g,name:'Uncloned repository',remote:'https://example.test/uncloned.git',repoPending:false}));
 const wait=gate();
 await page.route('**/call',async route=>{
  const method=route.request().postDataJSON().m;
  if(method==='CachedScan')return route.fulfill({json:{result:pending}});
  if(method==='InitialScan'){await wait.promise;return route.fulfill({json:{result:known}});}
  return route.continue();
 });
 await page.goto('/');
 const group=page.getByRole('treeitem',{name:/Repository details pending/}).first();
 await expect(group).toBeVisible();await expect(group).not.toContainText(/Not on this/);await expect(group).not.toContainText('Cloned here');
 wait.release();
 await expect(page.getByRole('treeitem',{name:/Uncloned repository/}).first()).toContainText(/Not on this/);
});
test('found sessions can be searched and previewed before the final scan response',async({page})=>{
 await page.request.post('/reset');const wait=gate();
 await page.route('**/call',async route=>{if(route.request().postDataJSON().m!=='InitialScan')return route.continue();const response=await route.fetch();await wait.promise;await route.fulfill({response});});
 await page.goto('/');await expect(page.getByRole('heading',{name:'All sessions'})).toBeVisible();
 await expect(row(page,'Find the codeword')).toBeVisible();await expect(page.locator('#fresh')).toContainText('Checking for changes');
 await row(page,'Find the codeword').click();await expect(details(page)).toBeVisible();
 await expect(details(page)).not.toContainText('is not reached');
 const search=page.getByRole('textbox',{name:'Filter sessions'});await search.fill('codeword');wait.release();
 await expect(page.locator('#fresh')).toContainText('updated');await expect(search).toHaveValue('codeword');await expect(search).toBeFocused();await expect(row(page,'Find the codeword')).toHaveAttribute('aria-selected','true');
});
test('late discovery never overwrites a newer result or replaces a pressed row',async({page})=>{
 await fresh(page);const snapshot=await page.evaluate(async()=>{const path='/core.js';return structuredClone((await import(path)).state.scan);});
 const target=row(page,'Find the codeword');await target.hover();await page.mouse.down();
 const next=structuredClone(snapshot);next.revision+=100;next.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.title==='Find the codeword').title='Newest discovery';
 await page.evaluate(d=>(window as any).__emit('hopsesh:discovery',d),next);await expect(target).toBeVisible();await page.mouse.up();await expect(row(page,'Newest discovery')).toBeVisible();
 snapshot.revision=next.revision-1;await page.evaluate(d=>(window as any).__emit('hopsesh:discovery',d),snapshot);await expect(row(page,'Newest discovery')).toBeVisible();
});
test('10000 sessions mount bounded rows and support keyboard End and filtering',async({page})=>{
 await fresh(page);await page.locator('#btn-display').click();await page.locator('#dp-group').selectOption('none');await page.keyboard.press('Escape');
 await page.evaluate(async()=>{const path='/core.js',base=structuredClone((await import(path)).state.scan),sample=base.groups.flatMap((g:any)=>g.entries)[0];base.groups=[{...base.groups[0],entries:Array.from({length:10000},(_,i)=>({...sample,key:'claude/large-'+i,session:'large-'+i,title:'Large session '+String(i).padStart(5,'0'),lastActive:new Date(Date.now()-i*1000).toISOString()}))}];base.total=10000;base.revision+=100;(window as any).__emit('hopsesh:discovery',base);});
 await expect(row(page,'Large session 00000')).toBeVisible();await expect.poll(()=>page.locator('.row').count()).toBeLessThan(200);
 await row(page,'Large session 00000').focus();await page.keyboard.press('End');await expect(row(page,'Large session 09999')).toBeFocused();
 const search=page.getByRole('textbox',{name:'Filter sessions'});await search.fill('Large session 05432');await expect(row(page,'Large session 05432')).toBeVisible();await expect(page.locator('.row')).toHaveCount(1);
});

test('unrelated discovery and presence updates preserve the selected conversation DOM',async({page})=>{
 await fresh(page);await row(page,'Find the codeword').click();
 await expect(details(page).locator('.conv .msg').first()).toBeVisible();
 await details(page).getByRole('button',{name:'Recent conversation',exact:true}).focus();
 await page.evaluate(async()=>{const path='/core.js',core=await import(path);(window as any).__conversation=document.querySelector('.conv');(window as any).__inspector=document.querySelector('#inspector');const scan=structuredClone(core.state.scan);scan.revision+=100;scan.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.title==='Feature flag').title='Unrelated update';(window as any).__emit('hopsesh:discovery',scan)});
 await expect(row(page,'Unrelated update')).toBeVisible();
 expect(await page.evaluate(()=>document.querySelector('.conv')===(window as any).__conversation)).toBeTruthy();
 await expect(details(page).getByRole('button',{name:'Recent conversation',exact:true})).toBeFocused();
 expect(await page.evaluate(()=>document.querySelector('#inspector')===(window as any).__inspector)).toBeTruthy();
 await page.evaluate(async()=>{const path='/core.js',core=await import(path),scan=structuredClone(core.state.scan);scan.revision+=100;const selected=scan.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.title==='Find the codeword');selected.status='idle';selected.dirty=3;(window as any).__emit('hopsesh:discovery',scan)});
 await expect(details(page)).toContainText('3 uncommitted');
 expect(await page.evaluate(()=>document.querySelector('.conv')===(window as any).__conversation)).toBeTruthy();
 await expect(details(page).getByRole('button',{name:'Recent conversation',exact:true})).toBeFocused();
});

test('recent local discoveries do not postpone remote reconciliation',async({page})=>{
 await page.clock.install();await fresh(page);await page.bringToFront();let scans=0;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='Scan')return route.continue();
  scans++;const result=await page.evaluate(async()=>{const path='/core.js';return structuredClone((await import(path)).state.scan)});
  result.updated=result.elsewhere=new Date().toISOString();result.revision+=100;result.discovering=false;
  await route.fulfill({json:{result}});
 });
 await page.evaluate(async()=>{const path='/core.js',core=await import(path);core.state.scan.updated=new Date().toISOString();core.state.scan.elsewhere=new Date(Date.now()-11*60000).toISOString()});
 await page.clock.fastForward(16000);
 await expect.poll(()=>scans).toBe(1);
});


test('Quick and terminal updates cannot replace a pressed row or an open menu',async({page})=>{
 await fresh(page);await row(page,'Find the codeword').click();
 const scan=await page.evaluate(async()=>{const path='/core.js';return structuredClone((await import(path)).state.scan)});
 scan.revision+=100;scan.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.title==='Find the codeword').title='Quick publication';
 await page.route('**/call',route=>route.request().postDataJSON().m==='QuickSnapshot'?route.fulfill({json:{result:{scan,presence:{entries:{}}}}}):route.continue());
 const pressed=row(page,'Find the codeword');await pressed.hover();await page.mouse.down();
 await page.evaluate(()=>(window as any).__emit('hopsesh:quick',null));await page.waitForTimeout(250);await expect(pressed).toBeVisible();
 await page.mouse.up();await expect(row(page,'Quick publication')).toBeVisible();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();
 await page.evaluate(()=>{(window as any).__menu=document.querySelector('[role=menu]');(window as any).__emit('hopsesh:terminal',{id:'unrelated',state:'exited'})});
 await page.waitForTimeout(250);await expect(page.getByRole('menu')).toBeVisible();expect(await page.evaluate(()=>document.querySelector('[role=menu]')===(window as any).__menu)).toBeTruthy();
 await page.keyboard.press('Escape');
});

test('third-hop local metadata reparents existing rows while remote discovery remains pending',async({page})=>{
 await fresh(page);
 const result=await page.evaluate(async()=>{
  const {state,selected}=await import('/core.js');
  const {acceptScan,render,showEntry}=await import('/sessions.js');
  const {list}=await import('/listview.js');
  const base=structuredClone(state.scan),machine=base.machines.find(m=>m.local),sample=base.groups.flatMap(g=>g.entries)[0];
  const a={...sample,machine:machine.name,key:'claude/local-A',session:'local-A',agent:'claude',agentName:'Claude Code',title:'Local conversation',cached:false,
   observedAt:'2026-10-11T12:00:00Z',copies:[],journey:{transfers:2,branch:'fixture-branch',family:'fixture-family',roundTrips:1,returns:1},arrival:{kind:'returned',agentName:'Codex'},departure:null,guard:null};
  const b={...a,key:'codex/local-B',session:'local-B',agent:'codex',agentName:'Codex',arrival:null,departure:{agent:'claude',agentName:'Claude Code'},guard:{mode:'block',effective:true}};
  const archived={...a,key:'claude/archived',session:'archived',title:'Archived local',copies:[]};
  const remote={...sample,machine:'pending-remote',key:'claude/remote',session:'remote',title:'Retained remote',copies:[],cached:false};
  const copies=[{machine:a.machine,key:a.key,agent:a.agent,newest:true},{machine:b.machine,key:b.key,agent:b.agent,leftBehind:true}];
  a.copies=copies;b.copies=copies;
  const group={...base.groups[0],name:'Before transfer',entries:[a,archived]},remoteGroup={...group,name:'Remote repository',entries:[remote]};
  const before={...base,revision:base.revision+100,discovering:false,cached:false,groups:[group,remoteGroup],profileCopies:[{...group,entries:[b]}],total:4,
   machines:[{...machine,phase:'done',checkedAt:'2026-10-11T12:00:00.123Z',sessions:3},{...machine,name:remote.machine,local:false,phase:'done',sessions:1}]};
  acceptScan(before);list.groupBy='repository';list.text='';state.sel=null;render();showEntry(a);
  const node=[...document.querySelectorAll('.row')].find(n=>n.getAttribute('data-key')===a.machine+'\0'+a.key);
  node.focus();const original=JSON.stringify(before);
  const newCopies=[{machine:a.machine,key:a.key,agent:a.agent,leftBehind:true},{machine:b.machine,key:b.key,agent:b.agent,newest:true}];
  const source={...a,cached:true,journey:{...a.journey,transfers:3},arrival:null,departure:{operation:'hop-3',agent:'codex',agentName:'Codex'},guard:{mode:'block',effective:true},copies:newCopies};
  const target={...b,cached:true,journey:{...b.journey,transfers:3},arrival:{operation:'hop-3',kind:'returned',agentName:'Claude Code'},departure:null,guard:null,copies:newCopies};
  const afterGroup={...group,name:'After transfer',remote:'https://github.com/example/after-transfer.git'};
  const after={...before,revision:before.revision+1,discovering:true,groups:[{...afterGroup,entries:[target]}],profileCopies:[{...afterGroup,entries:[source]}],
   machines:[{...before.machines[0],checkedAt:'2026-10-11T12:00:00.123001Z',sessions:2},{...before.machines[1],phase:'reading'}]};
  const accepted=acceptScan(after);render();
  const chosen=selected(),liveNode=[...document.querySelectorAll('.row')].find(n=>n.getAttribute('data-key')===a.machine+'\0'+a.key);
  const rows=[...state.scan.groups,...state.scan.profileCopies].flatMap(g=>g.entries);
  const snapshot=structuredClone(state.scan),older={...before,revision:after.revision-1};
  const lateAccepted=acceptScan(older);
  return {accepted,lateAccepted,selection:state.sel.key,selected:chosen.key,selectedHops:chosen.journey.transfers,sourceBlocked:chosen.guard.mode,
   target:rows.find(e=>e.key===b.key),remote:rows.find(e=>e.key===remote.key),archived:rows.some(e=>e.key===archived.key),
   parents:state.scan.groups.flatMap(g=>g.entries).map(e=>e.key),copies:state.scan.profileCopies.flatMap(g=>g.entries).map(e=>e.key),
   total:state.scan.total,localCount:state.scan.machines[0].sessions,sameNode:node===liveNode,focused:document.activeElement===node,sourceRole:liveNode.textContent,
   previousUnchanged:original===JSON.stringify(before),lateUnchanged:JSON.stringify(snapshot)===JSON.stringify(state.scan)};
 });
 expect(result.accepted).toBe(true);expect(result.lateAccepted).toBe(false);expect(result.lateUnchanged).toBe(true);
 expect(result.selection).toBe('claude/local-A');expect(result.selected).toBe('claude/local-A');expect(result.selectedHops).toBe(3);
 expect(result.sourceBlocked).toBe('block');expect(result.target.departure).toBeNull();expect(result.target.arrival.operation).toBe('hop-3');
 expect(result.parents).toEqual(expect.arrayContaining(['codex/local-B','claude/remote']));expect(result.parents).not.toContain('claude/local-A');
 expect(result.copies).toEqual(['claude/local-A']);expect(result.archived).toBe(false);expect(result.remote.title).toBe('Retained remote');
 expect(result.total).toBe(3);expect(result.localCount).toBe(2);expect(result.sameNode).toBe(true);expect(result.focused).toBe(true);expect(result.previousUnchanged).toBe(true);
 expect(result.sourceRole).toContain('Moved to Codex');
});

test('partial local discovery updates ready rows but removes missing locals only after local completion',async({page})=>{
 await fresh(page);
 const result=await page.evaluate(async()=>{
  const {state}=await import('/core.js'),{acceptScan}=await import('/sessions.js');
  const before=structuredClone(state.scan),machine=before.machines.find(m=>m.local),locals=before.groups.flatMap(g=>g.entries).filter(e=>e.machine===machine.name);
  const first=locals[0],missing=locals[1];
  const incoming={...before,revision:before.revision+1,discovering:true,profileCopies:[],groups:[{...before.groups[0],entries:[{...first,cached:false,observedAt:'2099-01-01T00:00:00Z',title:'Ready local update'}]}],
   machines:before.machines.map(m=>m.local?{...m,phase:'reading'}:m)};
  const accepted=acceptScan(incoming),during=state.scan.groups.flatMap(g=>g.entries);
  const pendingRetained=during.some(e=>e.key===missing.key),readyUpdated=during.find(e=>e.key===first.key).title;
  const failed={...incoming,revision:incoming.revision+1,groups:[{...incoming.groups[0],entries:[{...incoming.groups[0].entries[0],cached:true,observedAt:'2099-01-01T00:01:00Z',title:'Ready despite unrelated profile error'}]}],
   machines:incoming.machines.map(m=>m.local?{...m,phase:'error',status:'ok',error:'Another profile is unreadable',checkedAt:'2099-01-01T00:01:00Z',sessions:1}:m)};
  const failedAccepted=acceptScan(failed),failedRows=state.scan.groups.flatMap(g=>g.entries);
  const failedRetained=failedRows.some(e=>e.key===missing.key),failedUpdated=failedRows.find(e=>e.key===first.key).title;
  const complete={...failed,revision:failed.revision+1,machines:incoming.machines.map(m=>m.local?{...m,phase:'done',status:'ok',error:'',checkedAt:'2099-01-01T00:02:00Z',sessions:1}:m)};
  const completed=acceptScan(complete),after=state.scan.groups.flatMap(g=>g.entries);
  return {accepted,pendingRetained,readyUpdated,failedAccepted,failedRetained,failedUpdated,completed,removed:!after.some(e=>e.key===missing.key),count:state.scan.total};
 });
 expect(result.accepted).toBe(true);expect(result.readyUpdated).toBe('Ready local update');expect(result.pendingRetained).toBe(true);
 expect(result.failedAccepted).toBe(true);expect(result.failedRetained).toBe(true);expect(result.failedUpdated).toBe('Ready despite unrelated profile error');
 expect(result.completed).toBe(true);expect(result.removed).toBe(true);expect(result.count).toBeGreaterThanOrEqual(1);
});

test('real cached movement survives startup partials, presence and bookkeeping without switching the current parent',async({page})=>{
 test.setTimeout(90000);
 expect((await page.request.post('/reset?movement=1&protection=blocked')).ok()).toBeTruthy();
 const seed=await page.request.post('/cached-movement-world?legacy=1');expect(seed.ok(),await seed.text()).toBeTruthy();
 const pair=await seed.json(),claude=pair.claude.session,codex=pair.codex.session;
 const nativeKey=(s:any)=>s.key.agent+(s.key.profile?'@'+s.key.profile:'')+'/'+s.key.session;
 const claudeKey=nativeKey(claude),codexKey=nativeKey(codex);
 expect(codex.path).toContain('hopsesh-webtest-');
 const ledger=await readFile(codex.path+'.hopsesh.json','utf8');expect(JSON.parse(ledger).hops).toHaveLength(4);
 const wait=gate();
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m==='InitialScan')await wait.promise;
  return route.continue();
 });
 await page.goto('/');await expect(row(page,claude.title)).toBeVisible();
 await expect(row(page,claude.title).locator('.role-chip')).toContainText('Moved back from Codex');
 const child=oldRow(page,codex.title);
 await expect(child.locator('.chip.st-away')).toContainText('Moved to Claude Code');await expect(child.locator('.chip.mark.st-error')).toHaveText('Diverged');
 await child.click();await expect(details(page).locator('#act-primary')).toHaveText('Go to the active copy here');
 await expect(details(page).getByLabel('Old copy',{exact:true})).toContainText('Both copies changed after the move');
 const before=await page.evaluate(async()=>{const {state}=await import('/core.js');return structuredClone(state.scan)});
 expect(before.cached).toBe(true);
 expect(before.groups.flatMap(g=>g.entries).find(e=>e.key===claudeKey).journey.transfers).toBe(4);
 const status=await page.evaluate(async({before,key})=>{
  const {state,selected}=await import('/core.js'),{acceptScan,render}=await import('/sessions.js');
  const {model}=await import('/actions.js');
  const snapshot=JSON.stringify(before);
  const partial={...before,cached:false,discovering:true,revision:before.revision+1,profileCopies:[],groups:before.groups.map(g=>({...g,entries:g.entries.map(e=>({...e,arrival:null,departure:null,movement:null,guard:null,copies:[]}))})),
   machines:before.machines.map(m=>({...m,phase:'reading'}))};
  const accepted=acceptScan(partial);
  state.presence={[key]:{live:true,status:'live idle',needs:false,places:[{kind:'iterm2',count:1}]}};
  render();const e=selected();
  return {accepted,primary:model(e).primary.id,movement:e.movement.status,parents:state.scan.groups.flatMap(g=>g.entries).map(e=>e.key),
   unchanged:snapshot===JSON.stringify(before),selected:e.key};
 },{before,key:pair.codex.machine+'\0'+codexKey});
 expect(status.accepted).toBe(false);expect(status.primary).toBe('destination');expect(status.movement).toBe('diverged');expect(status.unchanged).toBe(true);
 expect(status.parents).toContain(claudeKey);expect(status.parents).not.toContain(codexKey);
 await expect(child.locator('.chip.mark.st-error')).toHaveText('Diverged');
 const nativeBefore=await readFile(codex.path);
 await appendFile(codex.path,JSON.stringify({timestamp:'2099-01-01T00:00:00Z',type:'event_msg',payload:{type:'thread_settings_applied',plugins:[]}})+'\n');
 wait.release();await expect(page.locator('#fresh')).toContainText('updated',{timeout:30000});
 await page.locator('#btn-refresh').click();await expect(page.locator('#fresh')).toContainText('updated',{timeout:30000});
 await expect(row(page,claude.title)).toBeVisible();await expect(child.locator('.chip.st-away')).toContainText('Moved to Claude Code');
 const after=await page.evaluate(async()=>{const {state,selected}=await import('/core.js');return {parents:state.scan.groups.flatMap(g=>g.entries).map(e=>e.key),selected:selected()?.key};});
 expect(after.parents).toContain(claudeKey);expect(after.parents).not.toContain(codexKey);
 expect(after.selected).toBe(status.selected);
 expect(await readFile(codex.path+'.hopsesh.json','utf8')).toBe(ledger);
 expect((await readFile(codex.path)).subarray(0,nativeBefore.length)).toEqual(nativeBefore);
});
