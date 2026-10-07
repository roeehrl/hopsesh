import {test,expect} from '@playwright/test';
import {fresh,row,action} from './helpers';
test.beforeEach(async({page})=>fresh(page));
async function plan(page){await row(page,'Find the codeword').click();await action(page,'move',/^Continue with Codex…/);await expect(page.locator('#sheet #go')).toBeEnabled();}

test('one destination shows its identity without a redundant default picker',async({page},testInfo)=>{
 await plan(page);
 const accounts=page.locator('.transfer-accounts');await expect(accounts).toContainText('Destination account');
 await expect(accounts.getByRole('combobox')).toHaveCount(0);await expect(accounts).toContainText('Default profile');
 await expect(page.locator('.fromto')).toContainText('studio (this Mac)');
 await expect(page.locator('#sheet')).not.toContainText('is signed in to another');
 await expect(page.locator('#sheet')).not.toContainText('Account identity continuity is unverified');
 await page.setViewportSize({width:760,height:700});
 expect(await page.locator('#sheet').evaluate(e=>e.scrollWidth<=e.clientWidth)).toBe(true);
 expect(await page.locator('#go').evaluate(e=>{const outer=e.getBoundingClientRect(),label=e.querySelector('small')!.getBoundingClientRect();return label.bottom<outer.bottom&&label.top>outer.top})).toBe(true);
 await page.screenshot({path:testInfo.outputPath('transfer-review-narrow.png')});
});

test('launch choice is adjacent to Continue, remembered and honored on apply',async({page},testInfo)=>{
 const opened:string[]=[];
 await page.route('**/call',async route=>{const r=route.request().postDataJSON();if(r.m==='Plan'){const response=await route.fetch(),body=await response.json();body.result.can.app=false;body.result.can.appWhy='Desktop app not installed in this scenario';await route.fulfill({json:body});return;}if(r.m==='OpenResult'){opened.push(r.args[0]);await route.fulfill({json:{result:{where:r.args[0]}}});return;}await route.continue();});
 await plan(page);const sheet=page.locator('#sheet');
 await expect(sheet.getByRole('checkbox',{name:/Open it in/})).toHaveCount(0);
 await sheet.getByRole('button',{name:'Choose where to open the continued session'}).click();
 const menu=page.getByRole('menu',{name:'Open continued session in'});await expect(menu).toBeVisible();
 await expect(menu.getByRole('menuitemradio',{name:/Open in Codex app/})).toHaveAttribute('aria-disabled','true');
 await menu.getByRole('menuitemradio',{name:'Open in Hopsesh Terminal',exact:true}).click();
 await expect(sheet.locator('#go')).toContainText('Open in Hopsesh Terminal');
 await sheet.getByRole('button',{name:'Cancel',exact:true}).click();await action(page,'move',/^Continue with Codex…/);
 await expect(sheet.locator('#go')).toContainText('Open in Hopsesh Terminal');
 await sheet.getByRole('button',{name:'Choose where to open the continued session'}).click();
 await page.screenshot({path:testInfo.outputPath('transfer-launch-menu.png')});
 await page.keyboard.press('Escape');await expect(menu).toHaveCount(0);await expect(sheet).toBeVisible();
 await sheet.locator('#go').click();await expect.poll(()=>opened).toEqual(['here']);
});

test('instruction paths, previews, individual selection and round-trip explanation',async({page},testInfo)=>{
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();if(req.m!=='Plan'){await route.continue();return;}
  const response=await route.fetch(),body=await response.json(),o=req.args[3];
  body.result.continue.instructions=[
   {path:'/fixture/.claude/CLAUDE.md',scope:'Global',text:'GLOBAL rule <script>not executable</script>',bytes:48},
   {path:'/fixture/project/CLAUDE.md',scope:'Project directory',text:'PROJECT rule',bytes:12}
  ].map(f=>({...f,selected:o.carryRules&&o.ruleFiles?.includes(f.path)}));
  body.result.continue.briefing=body.result.continue.instructions.filter(f=>f.selected).map(f=>f.text).join('\n');
  await route.fulfill({json:body});
 });
 await plan(page);const picker=page.locator('.instruction-picker');await picker.locator('summary').first().click();
 await expect(picker).toContainText('never created or overwritten');await expect(picker).toContainText('edits are not synchronized back');
 const project=picker.getByRole('checkbox',{name:'Include /fixture/project/CLAUDE.md',exact:true});
 await project.scrollIntoViewIfNeeded();
 const previousScroll=await page.locator('#sheet .sheet-body').evaluate(e=>e.scrollTop);expect(previousScroll).toBeGreaterThan(0);
 await project.check();await expect(picker.locator('summary').first()).toHaveText('Source instructions · 1 of 2 files selected');
 expect(Math.abs(await page.locator('#sheet .sheet-body').evaluate(e=>e.scrollTop)-previousScroll)).toBeLessThan(4);
 await expect(picker.getByRole('checkbox',{name:'Include /fixture/.claude/CLAUDE.md',exact:true})).not.toBeChecked();
 await picker.locator('.instruction-file').first().getByText('View instructions',{exact:true}).click();
 await expect(picker.locator('pre').first()).toHaveText('GLOBAL rule <script>not executable</script>');await expect(picker.locator('script')).toHaveCount(0);
 await page.screenshot({path:testInfo.outputPath('instruction-file-selection.png')});
 await picker.getByRole('checkbox',{name:'Include all listed instruction files',exact:true}).check();
 await expect(picker.locator('summary').first()).toHaveText('Source instructions · 2 of 2 files selected');
 await picker.getByRole('checkbox',{name:'Include all listed instruction files',exact:true}).uncheck();
 await expect(picker.locator('summary').first()).toHaveText('Source instructions · 0 of 2 files selected');
});

// A launcher restriction can itself be the blocker: keep alternatives available.
test('a blocked plan still allows changing its launcher',async({page})=>{
 await page.route('**/call',async route=>{const r=route.request().postDataJSON();if(r.m!=='Plan'){await route.continue();return;}const response=await route.fetch(),body=await response.json();body.result.blockers=['Choose a compatible launcher'];await route.fulfill({json:body});});
 await row(page,'Find the codeword').click();await action(page,'move',/^Continue with Codex…/);
 await expect(page.locator('#sheet #go')).toBeDisabled();
 const choice=page.getByRole('button',{name:'Choose where to open the continued session'});await expect(choice).toBeEnabled();await choice.click();
 await expect(page.getByRole('menu',{name:'Open continued session in'})).toBeVisible();
});

test('missing source identity is explained without a duplicate profile label',async({page})=>{
 // Remove only the public observation, never the stable profile ID.
 await page.route('**/call',async route=>{const r=route.request().postDataJSON();if(!['InitialScan','Scan','RefreshHere'].includes(r.m)){await route.continue();return;}const response=await route.fetch(),body=await response.json();for(const g of body.result.groups||[])for(const e of g.entries||[])if(e.profile)e.profile.account=null;await route.fulfill({json:body});});
 await page.reload();await plan(page);
 const source=page.locator('.transfer-accounts>div').first();await expect(source).toContainText('Email unavailable');const name=await source.locator('b').innerText();expect((await source.innerText()).split(name).length-1).toBe(1);
});
