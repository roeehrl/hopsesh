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
