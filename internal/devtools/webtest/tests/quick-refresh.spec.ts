import { test, expect } from '@playwright/test';
import { fresh, row } from './helpers';

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
