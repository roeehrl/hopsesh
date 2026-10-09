import {test,expect} from '@playwright/test';
import {fresh,row,details} from './helpers';
const gate=()=>{let release!:()=>void;const promise=new Promise<void>(r=>release=r);return {promise,release};};
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
