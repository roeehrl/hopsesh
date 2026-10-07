import {test,expect} from '@playwright/test';
import {fresh,row,details} from './helpers';
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
 await expect(page.locator('#sheet')).toContainText('new portable conversation',{timeout:30000});
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
 await expect(page.locator('#sheet')).toContainText('new portable conversation');await page.locator('#sheet').getByRole('button',{name:/Continue in Codex/}).click();
 await expect(page.getByText('Codex session written',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await row(page,'Find the codeword (from Claude Code)').click();await details(page).locator('#act-chevron').click();
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
 await expect(notice).toBeVisible();await expect(notice).toContainText('SSH is connected');
 await expect(notice).toContainText('hopsesh accounts scan --machine local');
 await page.screenshot({path:testInfo.outputPath('remote-account-setup.png'),fullPage:true});
 await notice.getByRole('button',{name:'Manage machines'}).click();
 await expect(page.getByRole('heading',{name:'Machines',exact:true})).toBeVisible();
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 initialized=true;await page.getByRole('button',{name:'Scan accounts',exact:true}).click();
 await expect(notice).toHaveCount(0);
});
