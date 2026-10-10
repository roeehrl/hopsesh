import {test,expect,type Page} from '@playwright/test';
import {fresh,row,details} from './helpers';
test.beforeEach(async({page})=>fresh(page));
// The account fixture patches background snapshots too. Drain its in-flight
// requests before closing the page so teardown cannot abort route.fetch().
test.afterEach(async({page})=>page.unrouteAll({behavior:'wait'}));
test('arbitrary account names, tags, edits, and a scoped transfer destination',async({page},testInfo)=>{
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 for(const name of ['First personal','Second personal']){
  await page.getByRole('button',{name:'Add account',exact:true}).click();const d=page.locator('dialog.account-editor');
  await d.getByLabel('Name',{exact:true}).fill(name);await d.getByLabel('Tags, separated by commas').fill(' Personal, personal, Research ');
  await d.getByRole('button',{name:'Add account',exact:true}).click();await expect(d).toHaveCount(0);
  await expect(page.getByRole('button',{name:'Scan accounts',exact:true})).toBeEnabled({timeout:30000});
  await expect(page.getByRole('heading',{name,exact:true})).toBeVisible();
 }
 await page.getByLabel('Filter accounts by tag').selectOption('Personal');await expect(page.locator('.account-card')).toHaveCount(2);
 const card=page.locator('.account-card').filter({has:page.getByRole('heading',{name:'Second personal',exact:true})});
 await card.getByRole('button',{name:'Edit',exact:true}).click();const d=page.locator('dialog.account-editor');
 await d.getByLabel('Name',{exact:true}).fill('Research lab');await d.getByLabel('Tags, separated by commas').fill('Personal, Side projects');
 await d.getByRole('button',{name:'Save changes'}).click();await expect(d).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Scan accounts',exact:true})).toBeEnabled({timeout:30000});await expect(page.getByRole('heading',{name:'Research lab',exact:true})).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('accounts-wide.png'),fullPage:true});
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/Move to another account/}).click();
 const choice=page.getByLabel('Destination account');await expect(choice).toBeVisible();const id=await choice.locator('option').filter({hasText:'Research lab'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation',{timeout:30000});
});
test('account setup fits a narrow window',async({page},testInfo)=>{
 await page.setViewportSize({width:760,height:650});await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await page.getByRole('button',{name:'Add account',exact:true}).click();const d=page.locator('dialog.account-editor');await expect(d).toBeVisible();const b=await d.boundingBox();expect(b!.x).toBeGreaterThanOrEqual(0);expect(b!.x+b!.width).toBeLessThanOrEqual(760);
 await page.screenshot({path:testInfo.outputPath('account-setup-narrow.png'),fullPage:true});
 await d.getByRole('button',{name:'Cancel'}).click();await expect(page.getByRole('button',{name:'Back to sessions',exact:true})).toBeVisible();
});

test('Codex desktop action remains visible and explains why a custom account uses a terminal',async({page})=>{
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await page.getByRole('button',{name:'Add account',exact:true}).click();const d=page.locator('dialog.account-editor');
 await d.getByLabel('Name',{exact:true}).fill('Research Codex');await d.getByLabel('Agent',{exact:true}).selectOption('codex');
 await d.getByRole('button',{name:'Add account',exact:true}).click();await expect(d).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Scan accounts',exact:true})).toBeEnabled({timeout:30000});
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/^Continue with Codex…/}).click();
 const choice=page.getByLabel('Destination account');const id=await choice.locator('option').filter({hasText:'Research Codex'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation');await page.locator('#sheet').getByRole('button',{name:/Continue in Codex/}).click();
 await expect(page.getByText('Codex session written',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await row(page,'Find the codeword (from Claude Code)').click();await details(page).locator('#act-chevron').click();
 const app=page.getByRole('menuitem',{name:/Resume in Codex app/});await expect(app).toBeVisible();await expect(app).toHaveAttribute('aria-disabled','true');await expect(app).toContainText(/account (root|profile)/);
});

test('connected remote without identity explains account setup and clears after initialization',async({page},testInfo)=>{
 let initialized=false;
 await page.route('**/call',async route=>{
  const method=route.request().postDataJSON().m;
  if(!['ScanAccounts','QuickSnapshot'].includes(method)){await route.continue();return;}
  const response=await route.fetch();const body=await response.json();
  const scan=method==='QuickSnapshot'?body.result?.scan:body.result;
  scan?.machines.push({name:'remote-laptop',status:'ok',local:false,agents:['Claude Code'],sessions:12,hopsesh:'',accountSetupRequired:!initialized});
  await route.fulfill({json:body});
 });
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await page.getByRole('button',{name:'Scan accounts',exact:true}).click();
 const notice=page.getByRole('complementary',{name:'Account setup on remote-laptop'});
 const remote=page.locator('[data-group="machine:remote-laptop"]');
 await expect(remote).not.toHaveAttribute('open','');
 await expect(page.locator('.account-group').first().locator('summary').first()).toContainText('This');
 await remote.locator('summary').click();
 await expect(notice).toBeVisible();await expect(notice).toContainText('SSH is connected');
 await remote.locator('summary').click();await expect(notice).toBeHidden();
 // A filter render before the native toggle event is delivered must preserve
 // the newest collapsed state, rather than the previous cached open state.
 await page.evaluate(()=>{
  const details=document.querySelector('[data-group="machine:remote-laptop"]') as HTMLDetailsElement;
  details.open=true;
  details.dispatchEvent(new Event('toggle'));
  details.open=false;
  const search=document.querySelector('[aria-label="Search accounts"]') as HTMLInputElement;
  search.dispatchEvent(new Event('input',{bubbles:true}));
 });
 await expect(notice).toBeHidden();
 await page.getByRole('button',{name:'Scan accounts',exact:true}).click();
 await expect(page.getByRole('button',{name:'Scan accounts',exact:true})).toBeEnabled({timeout:30000});
 await expect(notice).toBeHidden();await remote.locator('summary').click();
 await expect(notice).toContainText('hopsesh accounts scan --machine local');
 await page.screenshot({path:testInfo.outputPath('remote-account-setup.png'),fullPage:true});
 await page.getByLabel('Search accounts').fill('no-such-machine');await expect(remote).toHaveCount(0);
 await page.getByLabel('Search accounts').fill('');await expect(notice).toBeVisible();
 await notice.getByRole('button',{name:'Manage machines'}).click();
 await expect(page.getByRole('heading',{name:'Machines',exact:true})).toBeVisible();
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 initialized=true;await page.getByRole('button',{name:'Scan accounts',exact:true}).click();
 await expect(notice).toHaveCount(0);
});

test('public identity states are clear and account groups use email without merging profiles',async({page})=>{
 let profiles:any[]=[];
 await page.route('**/call',async route=>{
 const method=route.request().postDataJSON().m;
 if(method!=='Accounts')return route.continue();
 const response=await route.fetch(),body=await response.json();profiles=body.result;
 for(const p of profiles){p.account={loggedIn:true,email:'two.personal@example.com',confidence:'limited',provider:p.agent};p.stale=false;p.error='';p.identitySource=p.local?'local':'owner'}
 await route.fulfill({json:body});
 });
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await expect(page.locator('.account-card').first()).toContainText('Signed in · two.personal@example.com');
 await expect(page.locator('.accounts-page')).not.toContainText('limited identity');
 const card=page.locator('.account-card').first();await card.getByText('Profile details',{exact:true}).click();await expect(card).toContainText('not a verified identity');
 await page.evaluate(async p=>{const path='/core.js',core=await import(path),scan=structuredClone(core.state.scan);const all=scan.groups.flatMap((g:any)=>g.entries);all[0].profile={...p,id:'first',name:'First personal'};all[1].profile={...p,id:'second',name:'Second personal'};scan.revision+=100;core.state.scan=scan;},profiles[0]);
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await page.locator('#btn-display').click();await page.locator('#dp-group').selectOption('account');await page.keyboard.press('Escape');
 await expect(page.locator('.gname').filter({hasText:'two.personal@example.com'})).toHaveCount(2);
 await expect(page.locator('.account-chip').filter({hasText:'two.personal@example.com'})).toHaveCount(2);
});

test('scanning an account machine does not show every machine as scanning',async({page})=>{
 await page.route('**/call',async route=>{
  const m=route.request().postDataJSON().m;
  if(m==='Accounts'){
   const response=await route.fetch(),body=await response.json();body.result.push({...body.result[0],id:'remote-profile',machine:'remote-laptop',local:false});
   return route.fulfill({json:body});
  }
  if(m==='ScanMachine')return route.fulfill({json:{result:null}});
  return route.continue();
 });
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 const local=page.locator('.account-group').first(),remote=page.locator('[data-group="machine:remote-laptop"]');
 await expect(remote).toBeVisible();
 let release!:()=>void;const wait=new Promise<void>(r=>release=r);
 await page.route('**/call',async route=>{if(route.request().postDataJSON().m!=='ScanMachine')return route.fallback();await wait;await route.fulfill({json:{result:null}})});
 await remote.getByRole('button',{name:'Scan this machine'}).click();
 await expect(remote.getByRole('button',{name:'Scanning…'})).toBeDisabled();
 await expect(local.getByRole('button',{name:'Scan this machine'})).toBeEnabled();
 release();await expect(remote.getByRole('button',{name:'Scan this machine'})).toBeEnabled();
});


test('account grouping shows retained source and destination copies after a transfer',async({page})=>{
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await page.getByRole('button',{name:'Add account',exact:true}).click();const editor=page.locator('dialog.account-editor');
 await editor.getByLabel('Name',{exact:true}).fill('Second personal');await editor.getByRole('button',{name:'Add account',exact:true}).click();
 await expect(editor).toHaveCount(0);await expect(page.getByRole('button',{name:'Scan accounts',exact:true})).toBeEnabled({timeout:30000});
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/Move to another account/}).click();
 const choice=page.getByLabel('Destination account'),id=await choice.locator('option').filter({hasText:'Second personal'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation');await page.locator('#sheet').getByRole('button',{name:/Continue in Claude Code/}).click();
 await expect(page.getByText('Claude Code session written',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await page.locator('#btn-display').click();await page.locator('#dp-group').selectOption('account');await page.keyboard.press('Escape');
 await expect(page.locator('.gname').filter({hasText:'Second personal'})).toHaveCount(1);
 await expect(row(page,'Find the codeword')).toHaveCount(1);await expect(row(page,'Find the codeword (from Claude Code)')).toHaveCount(1);
});

// Freeze unrelated source publications so these checks exercise one explicit
// notification and the client's idle/deadline behavior deterministically.
async function accountNotificationFixture(page:Page){
 await page.clock.install();await page.reload();await expect(page.locator("#fresh")).toContainText("updated",{timeout:30000});
 let profile={id:'notification-profile',agent:'claude',name:'Notification account',machine:'test-mac',root:'/disposable/account',local:true,generation:1,tags:[],checkedAt:new Date().toISOString(),stale:false,account:{loggedIn:true,email:'before@example.com'}};
 let checks=0;
 await page.route('**/call',async route=>{
  const method=route.request().postDataJSON().m;
  if(method==='Accounts')return route.fulfill({json:{result:[profile]}});
  if(method==='RefreshAccount'){checks++;profile={...profile,checkedAt:new Date().toISOString()};return route.fulfill({json:{result:profile}})}
  return route.continue();
 });
 await page.evaluate(()=>{
  const win=window as any,emit=win.__emit,fetch=window.fetch.bind(window);
  win.__accountEmit=emit;
  win.__emit=(name:string,data:any)=>{if(!['hopsesh:runtime','hopsesh:accounts','hopsesh:discovery'].includes(name))emit(name,data)};
  win.__accountReads=0;
  window.fetch=(input,init)=>{if(typeof init?.body==='string'&&JSON.parse(init.body).m==='Accounts')win.__accountReads++;return fetch(input,init)};
 });
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Notification account',exact:true})).toBeVisible();
 return {changeEmail:(email:string)=>{profile={...profile,account:{...profile.account,email}}},makeStale:()=>{profile={...profile,checkedAt:new Date(Date.now()-600000).toISOString(),stale:true}},checks:()=>checks};
}

test('idle accounts use notifications instead of periodic backend reads',async({page})=>{
 const fixture=await accountNotificationFixture(page);
 const before=await page.evaluate(()=>(window as any).__accountReads);
 await page.clock.fastForward(10000);
 expect(await page.evaluate(()=>(window as any).__accountReads)).toBe(before);
 fixture.changeEmail('after@example.com');
 await page.evaluate(()=>{for(let i=0;i<20;i++)(window as any).__accountEmit('hopsesh:runtime',null)});
 await expect(page.locator('.account-card')).toContainText('after@example.com');
 expect(await page.evaluate(()=>(window as any).__accountReads)).toBe(before+1);
});

test('account notifications preserve a focused filter and apply after blur',async({page})=>{
 const fixture=await accountNotificationFixture(page);
 const search=page.getByLabel('Search accounts');await search.fill('account');
 fixture.changeEmail('updated@example.com');
 await page.evaluate(()=>(window as any).__accountEmit('hopsesh:accounts',null));
 // Crossing the old polling interval also catches an unsolicited repaint.
 await page.clock.fastForward(3000);
 await expect(search).toBeFocused();await expect(search).toHaveValue('account');
 await page.getByRole('heading',{name:'Accounts',exact:true}).click();
 await expect(page.locator('.account-card')).toContainText('updated@example.com');
});

test('account sign-in checks follow freshness deadlines and stop off screen',async({page})=>{
 const fixture=await accountNotificationFixture(page);
 await page.clock.fastForward(4*60*1000);
 expect(fixture.checks()).toBe(0);
 await page.clock.fastForward(75000);
 await expect.poll(fixture.checks).toBe(1);
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await page.clock.fastForward(6*60*1000);
 expect(fixture.checks()).toBe(1);
});

test('account notifications defer reads while an editor is open and refresh after closing',async({page})=>{
 const fixture=await accountNotificationFixture(page);
 await page.getByRole('button',{name:'Add account',exact:true}).click();
 const editor=page.locator('dialog.account-editor');await editor.getByLabel('Name',{exact:true}).fill('Unfinished name');
 const before=await page.evaluate(()=>(window as any).__accountReads);
 fixture.changeEmail('after-dialog@example.com');
 await page.evaluate(()=>(window as any).__accountEmit('hopsesh:runtime',null));
 await page.clock.fastForward(3000);
 expect(await page.evaluate(()=>(window as any).__accountReads)).toBe(before);
 await expect(editor.getByLabel('Name',{exact:true})).toBeFocused();
 await expect(editor.getByLabel('Name',{exact:true})).toHaveValue('Unfinished name');
 await editor.getByRole('button',{name:'Cancel'}).click();
 await expect(page.locator('.account-card')).toContainText('after-dialog@example.com');
});

test('frequent account notifications do not postpone an overdue sign-in check',async({page})=>{
 const fixture=await accountNotificationFixture(page);fixture.makeStale();
 for(let i=0;i<4;i++){
  const before=await page.evaluate(()=>(window as any).__accountReads);
  await page.evaluate(()=>(window as any).__accountEmit('hopsesh:runtime',null));
  await expect.poll(()=>page.evaluate(()=>(window as any).__accountReads)).toBeGreaterThan(before);
  await page.clock.fastForward(5000);
 }
 await expect.poll(fixture.checks).toBe(1);
});
