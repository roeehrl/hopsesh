import { test, expect, Page } from '@playwright/test';
import { fresh, row, details } from './helpers';

test.afterEach(async({page})=>{await page.unrouteAll({behavior:'wait'})});

const candidate = (patch={}) => ({replica:'original',machine:'studio',agent:'codex',agentName:'Codex',profile:'work',profileLabel:'Work',key:'codex@work/original',status:'available',reason:'Same branch; new work can return',local:false,...patch});
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

test('offline return reviews before verification; divergence preserves both branches',async({page})=>{
 const calls=await fixtures(page,[candidate({status:'verify'})]);
 await details(page).locator('#act-primary').click();
 await expect(page.getByRole('dialog').last()).toContainText('has not been verified');expect(calls).toHaveLength(0);
 await page.getByRole('button',{name:'Verify and review plan',exact:true}).click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].args[3].targetSession).toBe('codex@work/original');
});

test('diverged return plans with keep-both and cannot overwrite a branch',async({page})=>{
 const calls=await fixtures(page,[candidate({status:'diverged',local:true})]);
 await details(page).locator('#act-primary').click();
 await expect(page.getByRole('dialog').last()).toContainText('Both copies changed');
 await page.getByRole('button',{name:'Review plan keeping both branches'}).click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].args[3].conflict).toBe('keep-both');
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

test('open original explains how to exit and rechecks the exact original only on request',async({page},info)=>{
 const calls=await fixtures(page,[candidate({agent:'claude',agentName:'Claude Code',profile:'personal',profileLabel:'roee@example.com',key:'claude@personal/original',status:'live',local:true})]);
 const launches:any[]=[];
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();
  if(req.m==='ResolveEntry') return route.fulfill({json:{result:{machine:req.args[0],key:req.args[1],title:'oarbank',agent:'claude',agentName:'Claude Code',app:'Claude',live:true,profile:{id:'personal'}}}});
  if(['ShowPlace','ResumeEntry','Apply','PushApply'].includes(req.m)) { launches.push(req); return route.fulfill({json:{result:null}}); }
  return route.fallback();
 });
 const card=details(page).getByLabel('Return destinations');
 await expect(card).toContainText('Original conversation still open');
 await expect(card).toContainText('roee@example.com');
 await expect(card).not.toContainText('claude@personal/original');
 await details(page).getByRole('button',{name:'Move',exact:true}).click();
 const item=page.getByRole('menuitem',{name:/^Move back to Claude Code/});
 await expect(item).toContainText('Exit it first');
 await item.click();
 const d=page.getByRole('dialog').filter({has:page.getByRole('heading',{name:'Move back to Claude Code',exact:true})});
 await expect(d).toContainText('oarbank');
 await expect(d).toContainText('Code tab');
 await expect(d).toContainText('⌘W on Mac or Ctrl+W on Windows');
 await expect(d).toContainText('Esc only stops');
 await expect(d).toContainText('Saved history is preserved');
 expect(launches).toHaveLength(0);expect(calls).toHaveLength(0);
 for(const theme of ['light','dark']) {
  await page.evaluate(theme=>document.documentElement.dataset.theme=theme,theme);
  await expect(d).toBeInViewport();
  expect(await d.evaluate(el=>el.scrollWidth<=el.clientWidth)).toBeTruthy();
  await page.screenshot({path:info.outputPath(`open-original-${theme}.png`),fullPage:true});
 }
 await d.getByRole('button',{name:'Check again and review return',exact:true}).click();
 await expect.poll(()=>calls.length).toBe(1);
 expect(calls[0].args[3]).toMatchObject({targetProfile:'personal',targetSession:'claude@personal/original',fork:false,newReplica:false,stopLocal:false});
 expect(launches).toHaveLength(0);
});

test('remote original instructions name the destination and terminal exit without starting it',async({page})=>{
 const calls=await fixtures(page,[candidate({agent:'claude',agentName:'Claude Code',status:'live',machine:'remote-studio'})]);
 await details(page).getByLabel('Return destinations').getByRole('button',{name:'How to move back…',exact:true}).click();
 const d=page.getByRole('dialog').last();
 await expect(d).toContainText('remote-studio');
 await expect(d).toContainText('type /exit');
 await expect(d).toContainText('exit it in each place');
 expect(calls).toHaveLength(0);
 await d.getByRole('button',{name:'Cancel',exact:true}).click();
 await expect(d).not.toBeVisible();
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
 await expect(page.getByRole('dialog').last()).toContainText('does not reuse or restore the missing original');
 await page.getByRole('button',{name:'Review new session there'}).click();
 await expect.poll(()=>calls.length).toBe(1);expect(calls[0].args[3]).toMatchObject({targetSession:'',targetProfile:'work',newReplica:true,fork:false});
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
