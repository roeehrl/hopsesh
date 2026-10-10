import { test,expect } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { row,details,action } from './helpers';

test('real service resolves hidden original and appends return work to that exact session',async({page},info)=>{
 test.setTimeout(120000);
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
 // Rows are usable before return-candidate verification finishes.
 await expect(details(page).locator('#act-primary')).toContainText('Open existing session in Claude Code',{timeout:30000});
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
 expect(plan.blockers || []).toEqual([]);
 expect(plan.reviewNewSession || false).toBe(false);
 await expect(page.locator('#sheet')).toContainText(source.key);
 await expect(page.locator('#sheet #go')).toBeEnabled();
 const originalBytes=await readFile(original.path);
 await page.screenshot({path:info.outputPath('real-move-back-original-plan.png'),fullPage:true});
 await page.locator('#sheet #go').click();
 await expect(page.getByRole('heading',{name:/is prepared for Claude Code/})).toBeVisible({timeout:30000});
 const after=await readFile(original.path);
 expect(after.subarray(0,originalBytes.length)).toEqual(originalBytes);
 expect(after.subarray(originalBytes.length).toString()).toContain('Fixture return checkpoint completed.');
 expect(after.subarray(originalBytes.length).toString()).not.toContain('PLUM-7');
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 const scan=await page.request.post('/call',{data:{m:'Scan',args:[]}});
 const entries=(await scan.json()).result.groups.flatMap(g=>g.entries);
 const returned=[];
 for(const e of entries.filter(e=>e.agent==='claude' && e.key===source.key && e.path)) {
  if((await readFile(e.path,'utf8')).includes('Fixture return checkpoint completed.')) returned.push(e);
 }
 expect(returned).toHaveLength(1);
 expect(returned[0].key).toBe(source.key);
 const originalGraph=JSON.parse(await readFile(original.path+'.hopsesh.json','utf8'));
 const returnedGraph=JSON.parse(await readFile(returned[0].path+'.hopsesh.json','utf8'));
 expect(returnedGraph.family).toBe(originalGraph.family);
 expect(returnedGraph.branch).toBe(originalGraph.branch);
 expect(returnedGraph.hops).toHaveLength(2);
 expect(returnedGraph.hops.every(h=>!h.fork)).toBe(true);
 expect(returned[0].returns).toEqual(expect.arrayContaining([expect.objectContaining({key:converted.key})]));
 await row(page,returned[0].title).click();
 await expect(details(page).getByLabel('Return destinations')).toBeVisible();
 await expect(details(page).locator('.skel')).toHaveCount(0);
 await expect(page.locator('#toast')).not.toHaveClass(/show/);
 await page.screenshot({path:info.outputPath('real-returned-same-branch.png'),fullPage:true});
});
