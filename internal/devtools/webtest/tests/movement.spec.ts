import { test, expect, Page, Route } from '@playwright/test';
import { fresh, row, details } from './helpers';
import { appendFile, readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';

test.afterEach(async({page})=>{await page.unrouteAll({behavior:'wait'})});

const candidate = (patch={}) => ({replica:'original',machine:'studio',agent:'codex',agentName:'Codex',profile:'work',profileLabel:'Work',key:'codex@work/original',status:'available',reason:'Same branch; new work can return',local:false,...patch});
// Preserve every field of the real conversion/repository/capability report.
// Only the conflict evidence or live destination is injected by each test.
async function realPlan(route:Route, req:any) {
 const response=await route.fetch({postData:JSON.stringify({...req,args:[req.args[0],req.args[1],'codex',{...req.args[3],targetProfile:'',targetSession:'',app:false}]})});
 const body=await response.json();
 expect(body.error).toBeFalsy();expect(body.result.continue).toBeTruthy();
 return body.result;
}
const changingCall = (method:string) => /^(Apply|PushApply|Resume|Stop|EndReturnDestination|OpenResult|ShowPlace)/.test(method);
const comparisonFixture = () => ({
 classification:'diverged',verified:true,reason:'Both sessions contain verified independent conversation work.',sharedRevisions:17,
 source:{identity:{agent:'codex',agentName:'Codex',profile:'work',profileName:'Source work account',machine:'source-studio',machineId:'source-endpoint',title:'Actual incoming conversation',key:{agent:'codex',profile:'work',session:'incoming-session'}},status:'verified',exclusiveKnown:true,revisions:8,
  counts:{nodes:8,messages:6,userMessages:3,assistantMessages:3,tools:1,other:1},
  preview:[{kind:'message',role:'user',text:'Earlier incoming checkpoint',time:'2026-10-09T08:00:00Z',truncated:false},{kind:'tool_call',tool:'Bash',time:'2026-10-09T08:01:00Z',truncated:false},{kind:'message',role:'assistant',text:'Incoming checkpoint completed',truncated:false},{kind:'message',role:'user',text:'Review **incoming** changes',truncated:false},{kind:'message',role:'assistant',text:'Incoming work is ready',truncated:false}],truncated:true,previewOmitted:2},
 destination:{identity:{agent:'claude',agentName:'Claude Code',profile:'personal',profileName:'Destination personal account',machine:'destination-mbp',machineId:'destination-endpoint',title:'Actual original conversation',key:{agent:'claude',profile:'personal',session:'original-session'}},status:'verified',exclusiveKnown:true,revisions:3,
  counts:{nodes:3,messages:2,userMessages:1,assistantMessages:1,tools:1,other:0},
  preview:[{kind:'message',role:'user',text:'Keep the destination-only decision',truncated:false},{kind:'tool_call',tool:'Read',truncated:false},{kind:'message',role:'assistant',text:'Destination-only decision retained',truncated:false}],truncated:false,previewOmitted:0}
});
async function conflictFixture(page:Page, comparison=comparisonFixture()) {
 await fixtures(page,[candidate({status:'diverged',agent:'claude',agentName:'Claude Code',profile:'personal',profileLabel:'Stale candidate account',title:'Stale candidate title',key:'claude@personal/original-session',local:true})]);
 const plans:any[]=[], changes:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='Plan') {
   plans.push(req);
   const plan=await realPlan(route,req);
   Object.assign(plan,{agent:'Claude Code',fromAgent:'Codex',sourceAgent:'codex',conflict:'destination has independent work',blockers:req.args[3].conflict==='keep-both'?[]:['destination has independent work; choose --keep-both'],endDestinationToken:undefined});
   Object.assign(plan.continue,{relation:req.args[3].conflict==='keep-both'?'new':'diverged',comparison});
   return route.fulfill({json:{result:plan}});
  }
  if(changingCall(req.m)) {changes.push(req);return route.fulfill({json:{error:'Review must not change or launch either session'}})}
  return route.fallback();
 });
 await details(page).locator('#act-primary').click();
 await expect(page.locator('.conflict-review')).toBeVisible();
 return {plans,changes};
}
async function fixtures(page:Page, returns:any[], movement:any=null) {
 const calls:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(['Plan','PushPlan'].includes(req.m)) {
   calls.push(req);return route.fulfill({json:{error:'Test stopped after read-only plan request'}});
  }
  if(req.m==='AccountDestinations') return route.fulfill({json:{result:[]}});
  if(['InitialScan','Scan','RefreshHere'].includes(req.m)) {
   const response=await route.fetch();const body=await response.json();
   for(const g of body.result?.groups||[]) for(const e of g.entries||[]) if(e.title==='Find the codeword') {
    e.returns=returns.map(r=>({...r,machine:r.local?e.machine:r.machine}));e.movement=movement;
   }
   return route.fulfill({json:body});
  }
  return route.continue();
 });
 await fresh(page);await row(page,'Find the codeword').click();
 return calls;
}

test('remote return plans the exact account and native destination; prepared is not working',async({page},info)=>{
 const calls=await fixtures(page,[candidate()],{status:'prepared',text:'Prepared in Codex on studio',agent:'codex',agentName:'Codex',machine:'studio',profile:'work',key:'codex@work/original',checkedAt:'2026-01-02T03:04:05Z',delivery:'pending'});
 await expect(details(page).getByLabel('Movement notice')).toContainText('new work has not been observed');
 await expect(details(page).locator('#act-primary')).toContainText('Move back to Codex');
 await page.screenshot({path:info.outputPath('return-prepared.png'),fullPage:true});
 await details(page).locator('#act-primary').click();
 await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0].m).toBe('PushPlan');expect(calls[0].args[1]).toBe('studio');
 expect(calls[0].args[3]).toMatchObject({targetProfile:'work',targetSession:'codex@work/original',notify:true});
});

test('multiple returns require explicit selection and replace generic Continue with',async({page})=>{
 const calls=await fixtures(page,[candidate({local:true}),candidate({replica:'other',key:'codex@personal/other',profile:'personal',profileLabel:'Personal',local:true})]);
 await details(page).getByRole('button',{name:'Move',exact:true}).click();
 await expect(page.getByRole('menuitem',{name:/^Continue with Codex/})).toHaveCount(0);await page.keyboard.press('Escape');
 await details(page).locator('#act-primary').click();
 const d=page.getByRole('dialog').filter({has:page.getByRole('heading',{name:'Choose where to move back'})});
 await expect(d).toBeVisible();expect(calls).toHaveLength(0);
 await d.getByRole('button',{name:/Move back to Codex · Personal/}).click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].args[3]).toMatchObject({targetProfile:'personal',targetSession:'codex@personal/other'});
});

test('unverified return directly reviews the exact destination in one dialog',async({page})=>{
 const calls=await fixtures(page,[candidate({status:'verify'})]);
 await details(page).locator('#act-primary').click();
 await expect.poll(()=>calls.length).toBe(1);
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
 expect(calls[0]).toMatchObject({m:'PushPlan',args:[expect.any(String),'studio','codex',expect.objectContaining({targetSession:'codex@work/original',targetProfile:'work',conflict:'',stopLocal:false})]});
 await expect(page.locator('#sheet')).toContainText('Test stopped after read-only plan request');
});

test('diverged return directly requests comparison without selecting keep-both',async({page})=>{
 const calls=await fixtures(page,[candidate({status:'diverged',local:true})]);
 await details(page).locator('#act-primary').click();
 await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0].m).toBe('Plan');
 expect(calls[0].args[3]).toMatchObject({targetProfile:'work',targetSession:'codex@work/original',conflict:'',fork:false,newReplica:false,stopLocal:false});
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
 await expect(page.locator('#sheet')).toContainText('Test stopped after read-only plan request');
});

test('conflict review shows actual identities, causal counts and expandable changed-message previews',async({page})=>{
 const {plans,changes}=await conflictFixture(page);
 const review=page.getByLabel('Conversation differences');
 await expect(page.getByRole('dialog')).toHaveCount(1);
 for(const text of ['Codex','Source work account','source-studio','Actual incoming conversation','Claude Code','Destination personal account','destination-mbp','Actual original conversation','codex@work/incoming-session','claude@personal/original-session']) await expect(review).toContainText(text);
 await expect(review).not.toContainText('Stale candidate');
 await expect(review).toContainText(/17.*shared|shared.*17/i);
 await expect(review.getByLabel('Incoming conversation').locator('.comparison-counts')).toContainText('6 messages · 1 tool call');
 await expect(review.getByLabel('Existing destination conversation').locator('.comparison-counts')).toContainText('2 messages · 1 tool call');
 await expect(review).toContainText('Incoming work is ready');
 await expect(review).toContainText('Destination-only decision retained');
 await expect(review.locator('strong')).toContainText(['incoming']);
 await expect(review.getByText('Earlier incoming checkpoint',{exact:true})).not.toBeVisible();
 await review.getByLabel('Incoming conversation').getByText('View more changed messages and tool activity',{exact:true}).click();
 await expect(review.getByText('Earlier incoming checkpoint',{exact:true})).toBeVisible();
 await expect(review).toContainText(/shorten|omitted|bounded|preview/i);
 expect(plans[0].args[3]).toMatchObject({conflict:'',targetSession:'claude@personal/original-session',stopLocal:false});
 await expect(page.locator('#sheet #go, #sheet #end-original')).toHaveCount(0);
 expect(changes).toEqual([]);
});

test('review separate Claude Code session only replans with keep-both before explicit creation',async({page})=>{
 const {plans,changes}=await conflictFixture(page);
 const sheet=page.locator('#sheet');
 await expect(sheet.locator('#go, #end-original')).toHaveCount(0);
 const before=plans.length;
 await sheet.getByRole('button',{name:'Review separate Claude Code session',exact:true}).click();
 await expect(sheet.locator('#go')).toContainText('Create separate Claude Code session');
 await expect(sheet.locator('#go')).toBeEnabled();
 expect(plans.length).toBeGreaterThan(before);
 for(const req of plans.slice(before)) expect(req.args[3]).toMatchObject({conflict:'keep-both',targetSession:'claude@personal/original-session',targetProfile:'personal',fork:false,newReplica:false,stopLocal:false});
 expect(changes).toEqual([]);
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(sheet).toContainText('existing destination conversation is unchanged');
 await sheet.getByRole('button',{name:'Cancel return',exact:true}).click();
 await expect(sheet).not.toBeVisible();
 expect(changes).toEqual([]);
});

test('Cancel return preserves both existing native conversations without applying, resuming or stopping',async({page})=>{
 const {plans,changes}=await conflictFixture(page);
 const scan=await page.request.post('/call',{data:{m:'Scan',args:[]}});
 const body=await scan.json();expect(body.error).toBeFalsy();
 const entries=body.result.groups.flatMap((g:any)=>g.entries);
 const originals=[entries.find((e:any)=>e.title==='Find the codeword'),entries.find((e:any)=>e.agent==='codex' && e.path)];
 expect(originals.every(e=>!!e?.path)).toBe(true);
 const before=await Promise.all(originals.map(e=>readFile(e.path)));
 const reviewed=plans.length;
 await page.locator('#sheet').getByRole('button',{name:'Cancel return',exact:true}).click();
 await expect(page.getByRole('dialog')).toHaveCount(0);
 expect(plans).toHaveLength(reviewed);expect(changes).toEqual([]);
 const after=await Promise.all(originals.map(e=>readFile(e.path)));
 expect(after).toEqual(before);
});

test('hostile comparison Markdown stays inert and never fetches remote images or opens links',async({page})=>{
 const comparison=comparisonFixture();
 comparison.source.preview[4].text=[
  '**Safe formatting**',
  '<script>window.conflictExecuted=true</script>',
  '<img src="https://example.com/conflict-tracker.png" onerror="window.conflictExecuted=true">',
  '[Unsafe](javascript:alert(1))',
  '[Encoded unsafe](jav&#x61;script:alert(1))',
  '![Tracking image](https://example.com/conflict-image.png)',
  '[Documentation](https://example.com/conflict-docs)',
  '```html\n<iframe src="https://example.com/conflict-frame"></iframe>\n```'
 ].join('\n\n');
 const network:string[]=[];
 page.on('request',req=>{if(new URL(req.url()).hostname==='example.com')network.push(req.url())});
 const {changes}=await conflictFixture(page,comparison);
 const review=page.getByLabel('Conversation differences');
 await expect(review.locator('strong')).toContainText(['Safe formatting']);
 await expect(review).toContainText('<script>window.conflictExecuted=true</script>');
 await expect(review.locator('img, script, iframe, [onerror], [onclick]')).toHaveCount(0);
 // Recent excerpts also occur in the collapsed full-preview list.
 await expect(review.locator('a')).toHaveCount(2);
 await expect(review.locator('a:not([href="https://example.com/conflict-docs"])')).toHaveCount(0);
 await expect(review.getByRole('link',{name:'Documentation',exact:true}).first()).toHaveAttribute('href','https://example.com/conflict-docs');
 expect(await page.evaluate(()=>(window as any).conflictExecuted)).toBeUndefined();
 expect(network).toEqual([]);expect(changes).toEqual([]);
 await expect(page.getByRole('dialog')).toHaveCount(1);
});

test('unavailable comparison reports unknown changes without claiming independent work',async({page})=>{
 const comparison=comparisonFixture();
 Object.assign(comparison,{classification:'unavailable',verified:false,reason:'Destination conversation or causal evidence could not be verified.',sharedRevisions:0});
 for(const side of [comparison.source,comparison.destination]) Object.assign(side,{exclusiveKnown:false,status:'unavailable',reason:'Changes could not be verified; counts are unavailable.',preview:[],truncated:false,previewOmitted:0});
 const {changes}=await conflictFixture(page,comparison);
 const review=page.getByLabel('Conversation differences');
 await expect(review).toContainText('This does not prove that both gained new work');
 await expect(review).toContainText(comparison.reason);
 await expect(review.locator('.comparison-counts')).toHaveCount(0);
 await expect(review.locator('.comparison-message')).toHaveCount(0);
 await expect(page.locator('#sheet #go, #sheet #end-original')).toHaveCount(0);
 await expect(page.locator('#sheet #review-separate')).toBeEnabled();
 await page.locator('#sheet').getByRole('button',{name:'Cancel return',exact:true}).click();
 expect(changes).toEqual([]);
});

test('saved history remains inspectable when causal receipt verification is unavailable',async({page})=>{
 const comparison=comparisonFixture();
 Object.assign(comparison,{classification:'unavailable',verified:false,reason:'An older move may have omitted existing history.',sharedRevisions:0});
 for(const side of [comparison.source,comparison.destination]) Object.assign(side,{exclusiveKnown:false,status:'unavailable',reason:'Exclusive counts could not be verified.',previewBasis:'saved-history',counts:{},truncated:true,previewOmitted:4});
 const {changes}=await conflictFixture(page,comparison);
 const review=page.getByLabel('Conversation differences');
 await expect(review.locator('.comparison-counts')).toHaveCount(0);
 await expect(review.getByText('Recent saved messages · relationship unverified. These excerpts are not proof of unique work.',{exact:true})).toHaveCount(2);
 await expect(review.locator('.comparison-message').first()).toBeVisible();
 await expect(review).toContainText('exclusive counts are unavailable');
 await expect(review).not.toContainText('Counts include all verified changes');
 await expect(review).toContainText('This does not prove that both gained new work');
 await page.locator('#sheet').getByRole('button',{name:'Cancel return',exact:true}).click();
 expect(changes).toEqual([]);
});

test('real backend comparison omits private reasoning from both sides before sending its DTO',async({page})=>{
 test.setTimeout(120000);
 const reset=await page.request.post('/reset?movement=1');expect(reset.ok()).toBeTruthy();
 const call=async(m:string,args:any[]=[])=>{
  const response=await page.request.post('/call',{data:{m,args}});
  expect(response.ok()).toBeTruthy();const body=await response.json();expect(body.error).toBeFalsy();return body.result;
 };
 const scan=await call('Scan');
 const original=scan.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.title==='Find the codeword');
 expect(original.path).toContain('hopsesh-webtest-');
 const plan=await call('Plan',[original.machine,original.key,'codex',{app:false,notify:true,worktree:'auto',mark:false,conflict:''}]);
 expect(plan.blockers || []).toEqual([]);
 await call('Apply'); // Fixture setup only: writes to the isolated demo home, no launch.
 const convertedScan=await call('Scan');
 const converted=convertedScan.groups.flatMap((g:any)=>g.entries).find((e:any)=>e.agent==='codex' && e.title==='Find the codeword (from Claude Code)');
 expect(converted.path).toContain('hopsesh-webtest-');
 const destinationPrivate='DESTINATION_PRIVATE_REASONING_'+randomUUID(),sourcePrivate='SOURCE_PRIVATE_REASONING_'+randomUUID();
 const records=(await readFile(original.path,'utf8')).trim().split('\n').map(line=>JSON.parse(line));
 const last=records.filter(r=>r.type==='assistant').at(-1);
 const destinationRecord={...last,parentUuid:last.uuid,uuid:randomUUID(),timestamp:new Date().toISOString(),message:{...last.message,id:randomUUID(),content:[{type:'thinking',thinking:destinationPrivate,signature:'private-fixture-signature'},{type:'text',text:'Public destination-only checkpoint'}]}};
 await appendFile(original.path,JSON.stringify(destinationRecord)+'\n');
 await appendFile(converted.path,JSON.stringify({timestamp:new Date().toISOString(),type:'response_item',payload:{type:'reasoning',id:randomUUID(),summary:[{type:'summary_text',text:sourcePrivate}],encrypted_content:'private-fixture-ciphertext'}})+'\n');
 const work=await page.request.post('/movement-work',{params:{machine:converted.machine,key:converted.key}});expect(work.ok(),await work.text()).toBeTruthy();
 const before=await Promise.all([readFile(original.path),readFile(converted.path)]);
 const returned=await call('Plan',[converted.machine,converted.key,'claude',{targetProfile:original.profile?.id || '',targetSession:original.key,app:false,notify:true,worktree:'auto',mark:false,conflict:''}]);
 const comparison=returned.continue.comparison;
 expect(comparison).toBeTruthy();expect(comparison.verified).toBe(true);expect(comparison.classification).toBe('diverged');
 expect(comparison.source.counts.other).toBeGreaterThan(0);expect(comparison.destination.counts.other).toBeGreaterThan(0);
 expect(comparison.source.preview).toEqual(expect.arrayContaining([expect.objectContaining({kind:'message',text:'Fixture return checkpoint completed.'})]));
 expect(comparison.destination.preview).toEqual(expect.arrayContaining([expect.objectContaining({kind:'message',text:'Public destination-only checkpoint'})]));
 const payload=JSON.stringify(returned);
 for(const secret of [destinationPrivate,sourcePrivate,'private-fixture-signature','private-fixture-ciphertext']) expect(payload).not.toContain(secret);
 for(const side of [comparison.source,comparison.destination]) for(const preview of side.preview) {
  expect(['message','tool_call']).toContain(preview.kind);
  expect(Object.keys(preview).every(key=>['kind','role','text','tool','time','truncated'].includes(key))).toBe(true);
 }
 expect(await Promise.all([readFile(original.path),readFile(converted.path)])).toEqual(before);
});

test('narrow conflict review wraps evidence and keeps its footer visible while the body scrolls',async({page},info)=>{
 await page.setViewportSize({width:640,height:560});
 const comparison=comparisonFixture();
 comparison.source.identity.title='Actual incoming conversation '+ 'long-title-'.repeat(16);
 comparison.source.preview[4].text='long-unbroken-evidence-'.repeat(35);
 await conflictFixture(page,comparison);
 const sheet=page.locator('#sheet'),body=sheet.locator('.sheet-body'),footer=sheet.locator('.sheet-foot');
 const review=footer.getByRole('button',{name:'Review separate Claude Code session',exact:true}),cancel=footer.getByRole('button',{name:'Cancel return',exact:true});
 await expect(page.getByRole('dialog')).toHaveCount(1);
 for(const target of [sheet,sheet.locator('.conflict-review')]) expect(await target.evaluate(el=>el.scrollWidth<=el.clientWidth+1)).toBe(true);
 await expect(review).toBeInViewport();await expect(cancel).toBeInViewport();
 const first=await footer.boundingBox();expect(first).toBeTruthy();
 await body.evaluate(el=>el.scrollTop=el.scrollHeight);
 expect(await body.evaluate(el=>el.scrollTop)).toBeGreaterThan(0);
 await expect(review).toBeInViewport();await expect(cancel).toBeInViewport();
 const last=await footer.boundingBox();expect(last).toBeTruthy();expect(Math.abs(last!.y-first!.y)).toBeLessThan(2);
 await page.screenshot({path:info.outputPath('return-conflict-narrow-footer.png')});
});

test('same and behind candidates never plan a transfer',async({page})=>{
 const calls=await fixtures(page,['same','behind'].map(status=>candidate({status,replica:status})));
 for(const status of ['same','behind']) {
  await page.evaluate(async status=>{
   const core=await import('/core.js');const {model}=await import('/actions.js');
   const e=core.entries().find(e=>e.title==='Find the codeword');model(e).returns.find(a=>a.candidate.status===status).run();
  },status);
  const dialog=page.getByRole('dialog').last();await expect(dialog).toBeVisible();
  await expect(dialog.getByRole('button',{name:/review plan/i})).toHaveCount(0);
  await dialog.getByRole('button',{name:'Close',exact:true}).click();
 }
 expect(calls).toHaveLength(0);
});

test('open original opens one review directly and pins the exact original without stopping it',async({page})=>{
 const calls=await fixtures(page,[candidate({agent:'claude',agentName:'Claude Code',title:'oarbank',profile:'personal',profileLabel:'roee@example.com',key:'claude@personal/original',status:'live',local:true})]);
 const launches:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='ResolveEntry') return route.fulfill({json:{result:{machine:req.args[0],key:req.args[1],title:'oarbank',agent:'claude',agentName:'Claude Code',app:'Claude',live:true,profile:{id:'personal'}}}});
  if(['ShowPlace','ResumeEntry','Apply','PushApply','EndReturnDestination'].includes(req.m)) { launches.push(req); return route.fulfill({json:{result:null}}); }
  return route.fallback();
 });
 const card=details(page).getByLabel('Return destinations');
 await expect(card).toContainText('Original conversation still open');
 await expect(card).toContainText('oarbank');
 await expect(card).toContainText('roee@example.com');
 await expect(card).not.toContainText('claude@personal/original');
 await details(page).getByRole('button',{name:'Move',exact:true}).click();
 const item=page.getByRole('menuitem',{name:/^Move back to Claude Code/});
 await expect(item).toContainText('Review ending it');
 await item.click();
 await expect.poll(()=>calls.length).toBe(1);
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
 expect(calls[0].args[3]).toMatchObject({targetProfile:'personal',targetSession:'claude@personal/original',fork:false,newReplica:false,stopLocal:false});
 expect(launches).toHaveLength(0);
});

test('remote open original goes directly to its destination review without starting it',async({page})=>{
 const calls=await fixtures(page,[candidate({agent:'claude',agentName:'Claude Code',status:'live',machine:'remote-studio',key:'claude@work/original'})]);
 await details(page).getByLabel('Return destinations').getByRole('button',{name:'Review move back…',exact:true}).click();
 await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0]).toMatchObject({m:'PushPlan',args:[expect.any(String),'remote-studio','claude',expect.objectContaining({targetSession:'claude@work/original'})]});
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
});

for(const outcome of ['ended','timeout','unsupported','diverged']) test(`ending the reviewed original is explicit and never applies the return: ${outcome}`,async({page})=>{
 await fixtures(page,[candidate({agent:'claude',agentName:'Claude Code',status:'live',local:true,key:'claude/original'})]);
 const actions:any[]=[],planOptions:any[]=[];let plans=0;let ended=false;
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='Plan') {
   plans++;
   planOptions.push(req.args[3]);
   // Keep the real service's complete conversion report; only inject the live
   // destination and the stop result. No real user's process can be targeted.
   const plan=await realPlan(route,req);
   Object.assign(plan,{agent:'Claude Code',blockers:ended?[]:['the destination copy is open; quit it first'],endDestinationToken:!ended && outcome!=='unsupported'?'review-token':undefined});
   Object.assign(plan.continue,{relation:'append',appendTo:'Original codeword conversation'});
   if(ended && outcome==='diverged') {
    Object.assign(plan,{conflict:'destination has independent work',blockers:['destination has independent work; choose --keep-both']});
    Object.assign(plan.continue,{relation:'diverged',comparison:comparisonFixture()});
   }
   return route.fulfill({json:{result:plan}});
  }
  if(req.m==='EndReturnDestination') {
   actions.push(req);
   await new Promise(resolve=>setTimeout(resolve,250));
   ended=outcome==='ended' || outcome==='diverged';
   return route.fulfill({json:ended?{result:null}:{error:'The original session did not exit in time'}});
  }
  if(changingCall(req.m)){actions.push(req);return route.fulfill({json:{result:null}})}
  return route.fallback();
 });
 await details(page).locator('#act-primary').click();
 const sheet=page.locator('#sheet');
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
 await expect(sheet).toContainText('leave its agent running in the background');
 if(outcome==='unsupported') await expect(sheet.locator('#go')).toBeDisabled();
 else await expect(sheet.locator('#go')).toHaveCount(0);
 expect(actions).toHaveLength(0);
 const end=sheet.getByRole('button',{name:'End original session and check again',exact:true});
 if(outcome==='unsupported') {
  await expect(end).toHaveCount(0);await expect(sheet).toContainText('unavailable for this destination');return;
 }
 await expect(sheet).toContainText('other conversations are unaffected');
 await expect(end).toBeInViewport();
 await expect(sheet.locator('.sheet-foot #end-original')).toBeVisible();
 await sheet.locator('.sheet-body').evaluate(el=>el.scrollTop=el.scrollHeight);
 if(outcome==='diverged') expect(await sheet.locator('.sheet-body').evaluate(el=>el.scrollTop)).toBeGreaterThan(0);
 await expect(end).toBeInViewport();
 const reviewedPlans=plans;
 await end.click();
 await expect(sheet.getByRole('button',{name:'Ending original session…',exact:true})).toBeDisabled();
 await expect.poll(()=>actions.length).toBe(1);
 expect(actions[0]).toMatchObject({m:'EndReturnDestination',args:['review-token']});
 if(outcome==='diverged') {
  await expect(sheet.getByLabel('Conversation differences')).toBeVisible();
  await expect(sheet.getByLabel('Conversation differences').getByRole('heading',{name:'The conversations have different work',exact:true})).toBeInViewport();
  await expect.poll(()=>sheet.locator('.sheet-body').evaluate(el=>el.scrollTop)).toBe(0);
  await expect(sheet.locator('#review-separate')).toBeInViewport();
  await expect(sheet.locator('#go, #end-original')).toHaveCount(0);
  await expect(page.getByRole('dialog')).toHaveCount(1);
  expect(plans).toBeGreaterThan(reviewedPlans);
  for(const opts of planOptions) expect(opts).toMatchObject({conflict:'',targetSession:'claude/original',fork:false,newReplica:false,stopLocal:false});
 } else if(outcome==='ended') {
  await expect(sheet).toContainText('Original session ended. Review the refreshed plan');
  expect(plans).toBeGreaterThan(reviewedPlans);
  await expect(sheet.locator('#go')).toBeEnabled();
  await expect(end).toHaveCount(0);
 } else {
  await expect(sheet).toContainText('did not exit in time');
  await expect(sheet.locator('#go')).toHaveCount(0);
  await sheet.getByRole('button',{name:'Check again and review',exact:true}).click();
  await expect(end).toBeVisible();
  await expect(sheet.locator('#go')).toHaveCount(0);
 }
 expect(actions).toHaveLength(1); // No Apply or agent launch without another user action.
});

test('a fork with no candidates never infers a return to its parent',async({page})=>{
 await fixtures(page,[]);
 const actions=await page.evaluate(async()=>{
  const core=await import('/core.js');const {model}=await import('/actions.js');
  const e={...core.entries().find(e=>e.title==='Find the codeword'),journey:{fork:true,parentBranch:'parent'}};
  return model(e).returns;
 });
 expect(actions).toEqual([]);
 await expect(details(page).getByLabel('Return destinations')).toHaveCount(0);
});

test('movement notice setting persists and same-agent plans expose the override',async({page})=>{
 await fresh(page);await page.locator('#btn-settings').click();
 const toggle=page.getByRole('checkbox',{name:/Record movement notices/});await expect(toggle).toBeChecked();
 // Hold the real post-save Info response: a fast click after "Saved" must not
 // create a plan from the previous cached defaults, regardless of network timing.
 let saving=false;
 let releaseInfo!:()=>void, infoHeld!:()=>void;
 const gate=new Promise<void>(resolve=>{releaseInfo=resolve});
 const held=new Promise<void>(resolve=>{infoHeld=resolve});
 const plans:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='SaveSettings') saving=true;
  if(req.m==='Plan') plans.push(req.args[3]);
  if(req.m==='Info' && saving) {
   saving=false;
   const response=await route.fetch();
   infoHeld();await gate;
   return route.fulfill({response});
  }
  return route.continue();
 });
 try {
  await toggle.uncheck();await held;
  await expect(page.getByText('Saved',{exact:true})).not.toBeVisible();
 } finally {releaseInfo()}
 await expect(page.getByText('Saved',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/Move to another account/}).click();
 const notice=page.getByRole('checkbox',{name:/Record a movement notice/});await expect(notice).not.toBeChecked();
 await expect.poll(()=>plans.at(-1)?.notify).toBe(false);
 await notice.check();await expect(notice).toBeChecked();
 await expect.poll(()=>plans.at(-1)?.notify).toBe(true);
 await page.locator('#sheet').getByRole('button',{name:'Cancel',exact:true}).click();
 await page.reload();await page.locator('#btn-settings').click();
 await expect(toggle).not.toBeChecked(); // The per-move override did not change the saved default.
});

test('missing destination reviews a new session without reusing its original key',async({page})=>{
 const calls=await fixtures(page,[candidate({status:'missing',local:true})]);
 await details(page).locator('#act-primary').click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].args[3]).toMatchObject({targetSession:'',targetProfile:'work',newReplica:true,fork:false,conflict:'',stopLocal:false});
 await expect(page.getByRole('dialog')).toHaveCount(1);
 await expect(page.locator('.return-guide')).toHaveCount(0);
 await expect(page.locator('#sheet')).toContainText('Test stopped after read-only plan request');
});

test('source notice has destination, journey and explicit separate continuation controls',async({page})=>{
 const calls=await fixtures(page,[],{status:'prepared',text:'Prepared elsewhere',machine:'studio',agent:'codex',key:'codex/original',checkedAt:'0001-01-01T00:00:00Z'});
 const notice=details(page).getByLabel('Movement notice');
 await expect(notice).not.toContainText('Checked');
 await notice.getByRole('button',{name:'Show destination',exact:true}).click();
 await expect(page.getByRole('dialog').last()).toContainText('Scan studio');await page.getByRole('dialog').last().getByRole('button',{name:'Close',exact:true}).click();
 await notice.getByRole('button',{name:'View journey',exact:true}).click();
 await expect(details(page).locator('#sec-copies')).toBeVisible();
 await expect(details(page).locator('#sec-copies')).toContainText('Movement operation:');
 await notice.getByRole('button',{name:'Continue separately here…',exact:true}).click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].m).toBe('Plan');expect(calls[0].args[2]).toBe('claude');expect(calls[0].args[3]).toMatchObject({fork:true,newReplica:true,targetSession:''});
});

test('cross-agent plan exposes durable notice override',async({page})=>{
 await fresh(page);await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/^Continue with Codex/}).click();
 const sheet=page.locator('#sheet');
 await expect(sheet.getByText('Adds the destination to its title. This is a visual reminder; it does not lock the conversation or block further work.',{exact:true})).toBeVisible();
 await expect(sheet.getByText('Records where this conversation went. Installed agent hooks can supply a reminder on resume or a new prompt; this does not block further work in the original.',{exact:true})).toBeVisible();
 const notice=page.getByRole('checkbox',{name:/Record a movement notice/});await expect(notice).toBeChecked();await notice.uncheck();await expect(notice).not.toBeChecked();
 await expect(page.locator('#sheet')).not.toContainText('asks the agent to tell the old one');
});

test('notice hook setup is explicit and installation is distinct from delivery',async({page})=>{
 const installs:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='InstallNoticeHooks'){installs.push(req);return route.fulfill({json:{result:[]}})}
  if(req.m==='Settings'){
   const res=await route.fetch();const body=await res.json();
   body.result.noticeHooks=[{agent:'claude',profile:'work',profileLabel:'Work · alice@example.com',supported:true,installed:false,enabled:true,path:'/home/alice/.claude/settings.json'}];body.result.noticeHooksError='';
   return route.fulfill({json:body});
  }
  return route.continue();
 });
 await fresh(page);await page.locator('#btn-settings').click();
 await expect(page.getByText(/Installation does not prove delivery/)).toBeVisible();expect(installs).toHaveLength(0);
 await expect(page.getByText('claude · Work · alice@example.com',{exact:true})).toBeVisible();
 await page.getByRole('button',{name:'Install hook',exact:true}).click();
 await expect.poll(()=>installs.length).toBe(1);expect(installs[0].args).toEqual(['claude','work']);
});


test('source notice selects the exact listed destination without planning',async({page})=>{
 const calls=await fixtures(page,[],null);
 const title=await page.evaluate(async()=>{
  const core=await import('/core.js');const {inspector}=await import('/inspector.js');
  const source=core.entries().find(e=>e.title==='Find the codeword');const destination=core.entries().find(e=>e.agent==='codex');
  source.movement={status:'prepared',text:'Prepared in Codex',machine:destination.machine,agent:destination.agent,key:destination.key,profile:destination.profile?.id || ''};
  document.querySelector('#inspector').replaceWith(inspector(source));return destination.title;
 });
 await expect(details(page).locator('#act-primary')).toHaveText('Show destination');
 await expect(details(page).locator('#act-primary')).toBeEnabled();
 await details(page).locator('#act-primary').click();
 await expect(details(page).getByRole('heading',{name:title,exact:true})).toBeVisible();expect(calls).toHaveLength(0);
});

test('remote Codex source offers move back into the exact local Claude replica',async({page},info)=>{
 const calls=await fixtures(page,[]);
 await page.evaluate(async()=>{
  const {selected,here}=await import('/core.js');const {inspector}=await import('/inspector.js');
  const e={...selected(),machine:'remote-fixture',agent:'codex',agentName:'Codex',returns:[{replica:'original',machine:here(),agent:'claude',agentName:'Claude Code',profile:'personal',profileLabel:'Personal',key:'claude@personal/original',status:'available',reason:'New work can return',local:true}]};
  document.querySelector('#inspector').replaceWith(inspector(e));
 });
 await expect(details(page).locator('#act-primary')).toContainText('Move back to Claude Code');
 await page.screenshot({path:info.outputPath('remote-source-local-return.png'),fullPage:true});
 await details(page).locator('#act-primary').click();
 await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0].m).toBe('Plan');expect(calls[0].args[0]).toBe('remote-fixture');
 expect(calls[0].args[2]).toBe('claude');expect(calls[0].args[3]).toMatchObject({targetProfile:'personal',targetSession:'claude@personal/original'});
});
