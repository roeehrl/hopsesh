import { test, expect } from '@playwright/test';
import { fresh, row } from './helpers';

test('a queued Quick route survives default account registration before the main window handles it', async ({page}) => {
 await fresh(page);
 const scan=await page.evaluate(async()=>structuredClone((await import('/core.js')).state.scan));
 const entry=scan.groups.flatMap(g=>g.entries).find(e=>e.profile?.default&&e.path);
 expect(entry).toBeTruthy();
 const oldKey=entry.agent+'/'+entry.session;
 expect(oldKey).not.toBe(entry.key);
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='TakeQuickRoute')return route.fallback();
  await route.fulfill({json:{result:{screen:'sessions',machine:entry.machine,key:oldKey,path:entry.path}}});
 });
 await page.evaluate(()=>(window as any).__emit('hopsesh:quick-route',null));
 await expect(row(page,entry.title)).toHaveAttribute('aria-selected','true');
 await expect(row(page,entry.title)).toHaveClass(/flash/);
 expect((await (await page.request.post('/call',{data:{m:'TerminalTabs',args:[]}})).json()).result).toHaveLength(0);
});

test('a selected native session remains selected when background discovery registers its default account', async ({page}) => {
 await fresh(page);
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='Preview')return route.fallback();
  await route.fulfill({json:{result:{items:[],more:false}}});
 });
 const expected=await page.evaluate(async()=>{
  const {state}=await import('/core.js');
  const {render,queueScan}=await import('/sessions.js');
  const fresh=structuredClone(state.scan);
  const current=fresh.groups.flatMap(g=>g.entries).find(e=>e.profile?.default&&e.path);
  const old=structuredClone(current);old.key=old.agent+'/'+old.session;old.profile=null;
  state.scan={...fresh,groups:[{...fresh.groups[0],entries:[old]}]};
  state.sel={machine:old.machine,key:old.key};render();
  fresh.revision++;queueScan(fresh);
  return {title:current.title,key:current.key,revision:fresh.revision};
 });
 await expect.poll(()=>page.evaluate(async()=>(await import('/core.js')).state.scan.revision)).toBe(expected.revision);
 await expect.poll(()=>page.evaluate(async()=>(await import('/core.js')).state.sel?.key)).toBe(expected.key);
 await expect(row(page,expected.title)).toHaveAttribute('aria-selected','true');
});

for (const groupBy of ['repository','account']) test(`an inspected family copy stays visible once in ${groupBy} grouping after account enrichment`, async ({page}) => {
 await fresh(page);
 const expected=await page.evaluate(async(groupBy)=>{
  const {state}=await import('/core.js');
  const {render,queueScan}=await import('/sessions.js');
  state.list.groupBy=groupBy;
  const scan=structuredClone(state.scan);
  const original=scan.groups.flatMap(g=>g.entries).find(e=>e.title==='Find the codeword');
  state.sel={machine:original.machine,key:original.key};render();
  const copy=structuredClone(original);
  copy.key='codex/other-copy';copy.agent='codex';copy.title='Family representative';
  copy.copies=[{machine:original.machine,key:original.key}];
  scan.groups=[{...scan.groups[0],entries:[copy]}];
  scan.profileCopies=[{...scan.groups[0],entries:[original]}];
  scan.revision++;queueScan(scan);
  return {title:original.title,key:original.key,revision:scan.revision};
 },groupBy);
 await expect.poll(()=>page.evaluate(async()=>(await import('/core.js')).state.scan.revision)).toBe(expected.revision);
 await expect(row(page,expected.title)).toHaveCount(1);
 await expect(row(page,expected.title)).toHaveAttribute('aria-selected','true');
 await expect(page.getByRole('complementary',{name:'Session details'}).getByRole('heading',{name:expected.title,exact:true})).toBeVisible();
 expect(await page.evaluate(async()=>(await import('/core.js')).selected()?.key)).toBe(expected.key);
});

test('browsing registration refuses another account, file, machine or uncertain candidate', async ({page}) => {
 await fresh(page);
 const results=await page.evaluate(async()=>{
  const {state}=await import('/core.js');
  const {browsingEntry}=await import('/sessions.js');
  const original=state.scan.groups.flatMap(g=>g.entries).find(e=>e.profile?.default&&e.path);
  const results={};
  for(const name of ['same-file','explicit-profile','different-file','non-default','cached','remote','cloud','ambiguous']){
   const e=structuredClone(original),selection={machine:e.machine,key:e.agent+'/'+e.session,path:e.path};
   const scan={...state.scan,machines:structuredClone(state.scan.machines),groups:[{entries:[e]}],profileCopies:[]};
   if(name==='explicit-profile')selection.key=e.agent+'@another-account/'+e.session;
   if(name==='different-file')e.path+='.another';
   if(name==='non-default')e.profile.default=false;
   if(name==='cached')e.cached=true;
   if(name==='remote')scan.machines.find(m=>m.name===e.machine).local=false;
   if(name==='cloud')e.location='cloud';
   if(name==='ambiguous'){
    const other=structuredClone(e);other.profile.id='another-default';other.key=other.agent+'@another-default/'+other.session;scan.groups[0].entries.push(other);
   }
   results[name]=browsingEntry(scan,selection)?.key||null;
  }
  return {results,expected:original.key};
 });
 expect(results.results['same-file']).toBe(results.expected);
 for(const [name,value] of Object.entries(results.results))if(name!=='same-file')expect(value,name).toBeNull();
});

test('Quick routes reveal the exact native copy when family grouping shows a different representative', async ({page}) => {
 await fresh(page);
 const snapshot=await page.evaluate(async()=>structuredClone((await import('/core.js')).state.scan));
 const [source,representative]=snapshot.groups.flatMap(g=>g.entries);
 expect(source.key).not.toBe(representative.key);
 representative.copies=[{machine:source.machine,key:source.key}];
 snapshot.groups=[{...snapshot.groups[0],entries:[representative]}];
 snapshot.revision+=100;
 await page.route('**/call',async route=>{
  const method=route.request().postDataJSON().m;
  if(method==='TakeQuickRoute')return route.fulfill({json:{result:{screen:'sessions',machine:source.machine,key:source.key,path:source.path}}});
  if(method==='ScanSnapshot')return route.fulfill({json:{result:snapshot}});
  return route.fallback();
 });
 await page.evaluate(()=>(window as any).__emit('hopsesh:quick-route',null));
 await expect(row(page,source.title)).toHaveAttribute('aria-selected','true');
 await expect(row(page,source.title)).toHaveClass(/flash/);
 expect(await page.evaluate(async()=>(await import('/core.js')).state.sel.key)).toBe(source.key);
});

test('duplicate quick publications preserve splitter capture and keyboard focus; new scans wait for interaction', async ({page}) => {
 await fresh(page);
 await row(page,'Find the codeword').click();
 const snapshot=await page.evaluate(async()=>{const {state}=await import('/core.js');return {scan:state.scan,presence:{entries:state.presence||{}}};});
 let reads=0;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot')return route.continue();
  reads++;return route.fulfill({json:{result:snapshot}});
 });
 const notify=async()=>{
  const before=reads;
  await page.evaluate(()=>(window as any).__emit('hopsesh:quick',null));
  await expect.poll(()=>reads).toBeGreaterThan(before);
  await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 };
 const divider=page.getByRole('separator',{name:'Resize sidebar'});
 await expect(divider).toBeVisible();
 const box=(await divider.boundingBox())!;
 await page.mouse.move(box.x+box.width/2,box.y+box.height/2);
 await page.mouse.down();
 await page.mouse.move(box.x+box.width/2+30,box.y+box.height/2);
 await notify();
 await expect(page.locator('body')).toHaveClass(/resizing/);
 await page.mouse.move(box.x+box.width/2+60,box.y+box.height/2);
 await page.mouse.up();
 await expect(divider).toHaveAttribute('aria-valuenow','280');
 await divider.focus();
 snapshot.scan.revision++;
 for(const group of snapshot.scan.groups)for(const entry of group.entries)if(entry.title==='Find the codeword')entry.title='Newly observed work';
 await notify();
 await expect(divider).toBeFocused();
 await expect(row(page,'Find the codeword')).toBeVisible();
 await page.keyboard.press('Enter');
 await page.keyboard.press('Enter');
 await expect(divider).toHaveAttribute('aria-valuenow','280');
 await page.locator('#btn-search').focus();
 await expect(row(page,'Newly observed work')).toBeVisible();
 snapshot.scan.revision--;
 for(const group of snapshot.scan.groups)for(const entry of group.entries)if(entry.title==='Newly observed work')entry.title='Stale work';
 await notify();
 await expect(row(page,'Newly observed work')).toBeVisible();
 await expect(row(page,'Stale work')).toHaveCount(0);
});

test('a new scan arriving between pointer down and click keeps the selected session', async ({page}) => {
 await fresh(page);
 const snapshot=await page.evaluate(async()=>{const {state}=await import('/core.js');return {scan:state.scan,presence:{entries:state.presence||{}}};});
 snapshot.scan.revision++;
 for(const group of snapshot.scan.groups)for(const entry of group.entries)if(entry.title==='Find the codeword')entry.contextOverflow=true;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot')return route.continue();
  return route.fulfill({json:{result:snapshot}});
 });
 const title=row(page,'Find the codeword').locator('.t');
 const box=(await title.boundingBox())!;
 await page.mouse.move(box.x+box.width/2,box.y+box.height/2);
 await page.mouse.down();
 await page.evaluate(()=>(window as any).__emit('hopsesh:quick',null));
 await expect.poll(()=>page.evaluate(async()=>{const {state}=await import('/core.js');return state.scan.revision;})).toBe(snapshot.scan.revision);
 await page.mouse.up();
 await expect(page.getByRole('complementary',{name:'Session details'})).toContainText('The agent reported a context limit');
 await expect(row(page,'Find the codeword')).toHaveAttribute('aria-selected','true');
});

test('background refresh retains expanded messages and controls while revalidating preview content', async ({page}) => {
 await fresh(page);
 // Drive publications explicitly below so the fixture's real watcher cannot
 // change unrelated session facts between capturing and comparing controls.
 await page.evaluate(async()=>{(await import('/core.js')).state.scanning=true;});
 let previewText=Array.from({length:15},(_,i)=>`Paragraph ${i}.`).join('\n\n');
 let reads=0;
 let release: (()=>void)|undefined;
 let hold=false;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='Preview')return route.fallback();
  const text=previewText;
  reads++;
  if(hold)await new Promise<void>(resolve=>{release=resolve;});
  await route.fulfill({json:{result:{items:[{role:'user',text:'Please explain'},{role:'agent',text}],more:false}}});
 });
 await row(page,'Find the codeword').click();
 const conv=page.locator('#inspector .conv');
 await conv.getByRole('button',{name:'Show more',exact:true}).click();
 const message=await conv.locator('.msg.agent').elementHandle();
 const divider=await page.getByRole('separator',{name:'Resize sidebar'}).elementHandle();
 const action=await page.locator('#act-more').elementHandle();
 // An unchanged publication must keep the actual controls, not just replacements
 // with identical labels. The preview still goes to the backend for fresh bytes.
 hold=true;
 const before=reads;
 await page.evaluate(async()=>(await import('/sessions.js')).render());
 await expect.poll(()=>reads).toBeGreaterThan(before);
 expect(await message!.evaluate(el=>el.isConnected)).toBe(true);
 expect(await divider!.evaluate(el=>el.isConnected)).toBe(true);
 expect(await action!.evaluate(el=>el.isConnected)).toBe(true);
 await expect(conv.getByRole('button',{name:'Show less'})).toBeVisible();
 release!(); hold=false;
 await expect(conv.locator('.skel')).toHaveCount(0);
 // Changed metadata updates the inspector, while its conversation stays readable
 // until the new backend preview arrives; scan timestamps never cache messages.
 previewText='A newly written answer.';
 hold=true;
 const previousReads=reads;
 await page.evaluate(async()=>{
  const {state}=await import('/core.js');
  for(const g of state.scan.groups)for(const e of g.entries)if(e.machine===state.sel.machine && e.key===state.sel.key)e.title='Changed title';
  (await import('/sessions.js')).render();
 });
 await expect.poll(()=>reads).toBeGreaterThan(previousReads);
 await expect(page.locator('#inspector')).toContainText('Changed title');
 expect(await message!.evaluate(el=>el.isConnected)).toBe(true);
 await expect(conv.getByRole('button',{name:'Show less'})).toBeVisible();
 release!(); hold=false;
 await expect(conv).toContainText('A newly written answer.');
 await expect(conv).not.toContainText('Paragraph 14.');
 // Turning previews off drops the retained conversation immediately.
 await page.evaluate(async()=>{
  (await import('/core.js')).state.info.previews=false;
  (await import('/sessions.js')).render();
 });
 await expect(page.locator('#inspector .conv')).toHaveCount(0);
});
