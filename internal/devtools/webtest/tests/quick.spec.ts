import { test, expect } from '@playwright/test';
import { fresh, menu } from './helpers';

test.beforeEach(async ({page})=>fresh(page));

test('Quick access selects before acting, searches and routes exact identity',async({page})=>{
 await page.goto('/quick.html');
 await page.getByRole('button',{name:'Recent',exact:true}).click();
 const row=page.locator('.quick-row').first();await expect(row).toBeVisible();
 const selected=await row.getAttribute('data-session');await row.click();
 await expect(page.getByRole('heading',{name:'Recent conversation'})).toBeVisible();
 await expect(page.locator('.quick-message').first()).toBeVisible();
 const tabs=await page.request.post('/call',{data:{m:'TerminalTabs',args:[]}});expect((await tabs.json()).result).toHaveLength(0);
 await page.getByRole('button',{name:'Open session details',exact:true}).click();
 const route=await page.request.post('/call',{data:{m:'TakeQuickRoute',args:[]}});const r=(await route.json()).result;
 expect(r.machine+'\0'+r.key).toBe(selected);
 await page.getByRole('button',{name:'← Quick access',exact:true}).click();
 await page.getByRole('searchbox').fill('codeword');await expect(page.locator('.quick-row').first()).toBeVisible();
 await page.getByRole('searchbox').press('Enter');await expect(page.getByRole('heading',{name:'Recent conversation'})).toBeVisible();
});

test('unsupported-desktop fallback fits a narrow window',async({page})=>{
 await page.setViewportSize({width:320,height:580});await page.goto('/quick.html');
 await page.getByRole('button',{name:'Desktop presence settings',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Desktop presence',exact:true})).toBeVisible();
 await expect(page.locator('input[value=tray]')).toBeDisabled();await expect(page.locator('input[value=both]')).toBeDisabled();
 expect(await page.evaluate(()=>document.documentElement.scrollWidth)).toBe(320);
 await page.getByRole('button',{name:'Check support again',exact:true}).click();
 await expect(page.getByText('Desktop integration is available in the installed app.',{exact:true})).toBeVisible();
});

test('main Settings exposes the same Desktop presence choices',async({page})=>{
 await menu(page,'settings');await page.getByRole('tab',{name:'Desktop presence',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Desktop presence',exact:true})).toBeVisible();
 await expect(page.getByLabel('Start Hopsesh at login')).toBeVisible();await expect(page.getByLabel('When the main window closes')).toBeVisible();
});

test('hostile preview text cannot execute',async({page})=>{
 await page.route('**/call',async route=>{
  const req=route.request().postDataJSON();if(req.m==='QuickPreview'){
   await route.fulfill({json:{result:{items:[{role:'user',text:'**User request** <img src=x onerror="window.compromised=true">'},{role:'agent',text:'Agent **answer**'}]}}});return;
  }await route.continue();
 });
 await page.goto('/quick.html');await page.getByRole('button',{name:'Recent',exact:true}).click();await page.locator('.quick-row').first().click();
 await expect(page.locator('.quick-message.user strong')).toHaveText('User request');await expect(page.locator('.quick-message.agent strong')).toHaveText('answer');
 await expect(page.locator('.quick-message img')).toHaveCount(0);expect(await page.evaluate(()=>Boolean((window as any).compromised))).toBe(false);
});


test('cached remote state is labelled and never counted as live local attention',async({page})=>{
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot'){await route.continue();return}
  const response=await route.fetch();const body=await response.json();const d=body.result;
  const source=d.scan.groups[0].entries[0];
  d.scan.machines.push({name:'remote-fixture',local:false});d.scan.elsewhere='2020-01-01T12:00:00Z';
  d.scan.groups=[{name:'demo',entries:[{...source,machine:'remote-fixture',needs:true,live:true,status:'Working',profile:{name:'Personal',tags:['personal']}}]}];
  d.presence=null;d.tabs=[];await route.fulfill({json:body});
 });
 await page.goto('/quick.html');
 await expect(page.locator('.quick-summary')).toHaveText('0 need you · 0 open terminal tabs');
 await expect(page.locator('.quick-row')).toHaveCount(0);
 await page.getByRole('button',{name:'Recent',exact:true}).click();
 await expect(page.locator('.quick-state')).toContainText('last scanned');
 await page.locator('.quick-row').click();
 await expect(page.getByText('Open session details to read the conversation on its machine.',{exact:true})).toBeVisible();
 await expect(page.getByRole('button',{name:/Open in .* app/})).toHaveCount(0);
});

test('refresh preserves keyboard focus and duplicate tags never identify a session',async({page})=>{
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot'){await route.continue();return}
  const response=await route.fetch();const body=await response.json();
  for(const g of body.result.scan.groups)for(const e of g.entries)e.profile={name:'Personal',tags:['personal']};
  await route.fulfill({json:body});
 });
 await page.goto('/quick.html');await page.getByRole('button',{name:'Recent',exact:true}).click();
 const rows=page.locator('.quick-row');expect(await rows.count()).toBeGreaterThan(1);
 expect(await rows.nth(0).getAttribute('data-session')).not.toBe(await rows.nth(1).getAttribute('data-session'));
 await rows.nth(1).focus();const identity=await rows.nth(1).getAttribute('data-session');
 await page.evaluate(()=>(window as any).__emit('hopsesh:quick',null));
 await expect.poll(()=>page.evaluate(()=> (document.activeElement as HTMLElement)?.dataset.session)).toBe(identity);
 await page.keyboard.press('Enter');await expect(page.getByRole('heading',{name:'Recent conversation'})).toBeVisible();
});

test('Quick access uses the same loaded agent icons as the main window',async({page},testInfo)=>{
 const response=await page.request.post('/call',{data:{m:'Info',args:[]}});
 const agents=(await response.json()).result.agents;
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot'){await route.continue();return;}
  const response=await route.fetch(),body=await response.json();
  const source=body.result.scan.groups[0].entries[0];
  body.result.scan.groups=[{name:'demo',entries:['claude','codex'].map(id=>({...source,key:id+'/icon-test',agent:id,agentName:agents.find(a=>a.id===id).name}))}];
  await route.fulfill({json:body});
 });
 await page.setViewportSize({width:420,height:640});await page.goto('/quick.html');
 await page.getByRole('button',{name:'Recent',exact:true}).click();
 for(const id of ['claude','codex']){
  const agent=agents.find(a=>a.id===id);expect(agent.icon).toBeTruthy();
  const icon=page.locator('.quick-row').filter({has:page.getByRole('img',{name:agent.name,exact:true})}).locator('img.agent-ico');
  await expect(icon).toHaveAttribute('src',agent.icon);
  await expect.poll(()=>icon.evaluate((img:HTMLImageElement)=>img.complete&&img.naturalWidth>0)).toBe(true);
 }
 await page.screenshot({path:testInfo.outputPath('quick-agent-icons.png')});
});

test('background close is the default on every platform even without a tray',async({page})=>{
 for(const os of ['darwin','windows','linux']){
  await page.route('**/call',async route=>{
   if(route.request().postDataJSON().m!=='QuickSnapshot'){await route.continue();return;}
   const response=await route.fetch(),body=await response.json();
   body.result.os=os;body.result.desktop.preferences={mode:'app',close:''};body.result.desktop.capabilities={tray:false,hideApp:false};body.result.desktop.effective='app';
   await route.fulfill({json:body});
  });
  await page.goto('/quick.html');await page.getByRole('button',{name:'Desktop presence settings',exact:true}).click();
  const close=page.getByLabel('When the main window closes');await expect(close).toHaveValue('keep');
  await expect(close.locator('option[value=keep]')).toHaveText('Keep running in background');
  await expect(close.locator('option[value=keep]')).toBeEnabled();
  await expect(page.getByText(/Open Hopsesh from your app launcher/)).toBeVisible();
  await page.unrouteAll({behavior:'wait'});
 }
});
