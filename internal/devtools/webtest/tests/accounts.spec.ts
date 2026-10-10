import {test,expect} from '@playwright/test';
import {fresh,row,agentRow,details} from './helpers';
test.beforeEach(async({page})=>fresh(page));
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
 await agentRow(page,'Find the codeword','Codex').click();await details(page).locator('#act-chevron').click();
 const app=page.getByRole('menuitem',{name:/Resume in Codex app/});await expect(app).toBeVisible();await expect(app).toHaveAttribute('aria-disabled','true');await expect(app).toContainText(/account (root|profile)/);
});

test('connected remote without identity explains account setup and clears after initialization',async({page},testInfo)=>{
 let initialized=false;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='ScanAccounts'){await route.continue();return;}
  const response=await route.fetch();const body=await response.json();
  body.result.machines.push({name:'remote-laptop',status:'ok',local:false,agents:['Claude Code'],sessions:12,hopsesh:'',accountSetupRequired:!initialized});
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
 // Both copies keep the title; one is listed under each account.
 await expect(row(page,'Find the codeword')).toHaveCount(2);
 const second=page.locator('[role="treeitem"][aria-level="1"]').filter({has:page.locator('.gname',{hasText:'Second personal'})});
 await expect(second.locator('.row').filter({has:page.locator('.t').getByText('Find the codeword',{exact:true})})).toHaveCount(1);
});
