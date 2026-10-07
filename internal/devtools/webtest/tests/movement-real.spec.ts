import { test,expect } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { row,details,action } from './helpers';

test('real service resolves hidden original and reviews a safe separate return when native append is blocked',async({page},info)=>{
 const reset=await page.request.post('/reset?movement=1');expect(reset.ok()).toBeTruthy();
 await page.goto('/');await expect(page.getByRole('heading',{name:'All sessions',exact:true})).toBeVisible();
 await row(page,'Find the codeword').click();
 const source=await page.evaluate(async()=>{
  const {entries}=await import('/core.js');const e=entries().find(e=>e.title==='Find the codeword');return {machine:e.machine,key:e.key};
 });
 await action(page,'move',/^Continue with Codex/);
 await page.locator('#sheet').getByRole('button',{name:/Continue in Codex/}).click();
 await expect(page.getByRole('heading',{name:/is prepared for Codex/})).toBeVisible({timeout:30000});
 await expect(page.getByText('Movement notice',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await row(page,'Find the codeword (from Claude Code)').click();
 const converted=await page.evaluate(async()=>{
  const {selected}=await import('/core.js');const e=selected();return {machine:e.machine,key:e.key,returns:e.returns};
 });
 expect(converted.returns).toEqual(expect.arrayContaining([expect.objectContaining({key:source.key,status:'same'})]));
 // The original is collapsed into the branch's displayed Codex copy.
 await expect(row(page,'Find the codeword')).toHaveCount(0);
 const resolve=await page.request.post('/call',{data:{m:'ResolveEntry',args:[source.machine,source.key]}});
 const original=(await resolve.json()).result;
 expect(original.key).toBe(source.key);
 // Use the history's Show copy to inspect it without starting an agent.
 await details(page).getByRole('button',{name:/Copies & history/}).click();
 await details(page).getByRole('button',{name:'Show copy',exact:true}).first().click();
 await expect(details(page).getByRole('heading',{name:'Find the codeword',exact:true})).toBeVisible();
 await expect(details(page).getByLabel('Movement notice')).toContainText('prepared');
 await expect(details(page).locator('#act-primary')).toHaveText('Show destination');
 await expect(details(page).locator('#act-primary')).toBeEnabled();
 await expect(details(page).getByLabel('Movement notice')).not.toContainText('Last checked');
 await expect(details(page).locator('.skel')).toHaveCount(0);
 await page.screenshot({path:info.outputPath('real-source-prepared-hidden-copy.png'),fullPage:true});
 const separatePlan=page.waitForResponse(r=>r.url().endsWith('/call') && r.request().postDataJSON().m==='Plan');
 await details(page).getByLabel('Movement notice').getByRole('button',{name:'Continue separately here…',exact:true}).click();
 const separate=(await (await separatePlan).json()).result;
 await info.attach('separate-plan.json',{body:JSON.stringify(separate,null,2),contentType:'application/json'});
 await page.screenshot({path:info.outputPath('real-separate-plan.png'),fullPage:true});
 expect.soft(separate.blockers || [],'same-agent fork plan must preserve original and permit a new session').toEqual([]);
 await page.locator('#sheet').getByRole('button',{name:'Cancel',exact:true}).click();
 await details(page).locator('#act-primary').click();
 await expect(details(page).getByRole('heading',{name:'Find the codeword (from Claude Code)',exact:true})).toBeVisible();
 // Same/behind opens the exact prior native session, not the branch representative.
 const resume=page.waitForRequest(r=>r.url().endsWith('/call') && r.postDataJSON().m==='ResumeSession');
 await details(page).locator('#act-primary').click();
 expect((await resume).postDataJSON().args.slice(0,2)).toEqual([source.machine,source.key]);
 const work=await page.request.post('/movement-work',{params:{machine:converted.machine,key:converted.key}});
 expect(work.ok(),await work.text()).toBeTruthy();
 await page.locator('#btn-refresh').click();
 await row(page,'Find the codeword (from Claude Code)').click();
 await expect(details(page).locator('#act-primary')).toContainText('Move back to Claude Code');
 const choice=details(page).getByLabel('Return destinations').getByRole('button');
 const dimensions=await choice.evaluate(el=>({height:el.clientHeight,content:el.scrollHeight}));
 expect(dimensions.content).toBeLessThanOrEqual(dimensions.height);
 await page.screenshot({path:info.outputPath('real-move-back.png'),fullPage:true});
 const planned=page.waitForResponse(r=>r.url().endsWith('/call') && r.request().postDataJSON().m==='Plan');
 await details(page).locator('#act-primary').click();
 const plan=(await (await planned).json()).result;
 await info.attach('return-plan.json',{body:JSON.stringify(plan,null,2),contentType:'application/json'});
 await page.screenshot({path:info.outputPath('real-move-back-plan.png'),fullPage:true});
 expect(plan.blockers).toEqual([expect.stringContaining('cannot append to a native replica under an unverified account binding')]);
 await expect(page.locator('#sheet')).toContainText(source.key);
 await expect(page.locator('#sheet #go')).toBeDisabled();
 await page.locator('#sheet').getByText(/Cannot append to a native replica/).scrollIntoViewIfNeeded();
 await page.screenshot({path:info.outputPath('real-move-back-blocker.png'),fullPage:true});
 const originalBytes=await readFile(original.path);
 const replanned=page.waitForResponse(r=>r.url().endsWith('/call') && r.request().postDataJSON().m==='Plan');
 await page.locator('#sheet').getByRole('button',{name:'Review keeping both as separate sessions',exact:true}).click();
 const response=await replanned;
 expect(response.request().postDataJSON().args[3]).toMatchObject({targetSession:'',fork:true,newReplica:true,conflict:'keep-both'});
 const fork=(await response.json()).result;
 await info.attach('return-fork-plan.json',{body:JSON.stringify(fork,null,2),contentType:'application/json'});
 expect(fork.blockers || []).toEqual([]);
 await expect(page.locator('#sheet')).toContainText('New separate branch; the original session will be preserved');
 await expect(page.locator('#sheet #go')).toBeEnabled({timeout:30000});
 await page.screenshot({path:info.outputPath('real-move-back-fork-plan.png'),fullPage:true});
 await page.locator('#sheet #go').click();
 await expect(page.getByRole('heading',{name:/is prepared for Claude Code/})).toBeVisible({timeout:30000});
 await page.screenshot({path:info.outputPath('real-move-back-fork-done.png'),fullPage:true});
 expect(await readFile(original.path)).toEqual(originalBytes);
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 const scan=await page.request.post('/call',{data:{m:'Scan',args:[]}});
 const entries=(await scan.json()).result.groups.flatMap(g=>g.entries);
 const returned=[];
 for(const e of entries.filter(e=>e.agent==='claude' && e.key!==source.key && e.path)) {
  if((await readFile(e.path,'utf8')).includes('Fixture return checkpoint completed.')) returned.push(e);
 }
 expect(returned).toHaveLength(1);
 expect(returned[0].returns || []).toEqual([]);
 await row(page,returned[0].title).click();
 await expect(details(page).getByLabel('Return destinations')).toHaveCount(0);
 await expect(details(page).locator('.skel')).toHaveCount(0);
 await expect(page.locator('#toast')).not.toHaveClass(/show/);
 await page.screenshot({path:info.outputPath('real-returned-separate-branch.png'),fullPage:true});
});
