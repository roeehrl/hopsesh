import {test,expect} from '@playwright/test';
import {menu,row} from './helpers';

test.beforeEach(async({page})=>{
 await page.request.post('/reset?terminal=here');await page.goto('/');
 await expect(page.getByRole('heading',{name:'All sessions'})).toBeVisible({timeout:30000});
});

test('embedded workspace uses an isolated origin, resizes, hides and preserves the running tab',async({page})=>{
 await page.request.post('/terminal-test/open?title=Docking%20proof');
 await menu(page,'settings');await page.getByRole('tab',{name:'Terminal',exact:true}).click();
 await page.getByRole('radio',{name:'Bottom panel',exact:true}).click();
 await expect(page.locator('#terminal-workspace')).toBeVisible();
 const frame=page.frameLocator('#terminal-workspace iframe');
 await expect(frame.locator('#connection')).toBeHidden({timeout:15000});
 await expect(frame.getByRole('tab',{name:/Docking proof/})).toBeVisible();
 const src=await page.locator('#terminal-workspace iframe').getAttribute('src');
 expect(new URL(src!).origin).not.toBe(new URL(page.url()).origin);
 await expect(page.locator('#terminal-workspace iframe')).toHaveAttribute('sandbox','allow-scripts');
 const original=await frame.locator('.term').getAttribute('id');
 await page.getByRole('separator',{name:'Terminal size'}).focus();await page.keyboard.press('ArrowUp');
 await page.getByRole('button',{name:'Hide terminal',exact:true}).click();
 await expect(page.locator('#terminal-workspace')).toBeHidden();
 await page.locator('#btn-terminal').click();await expect(page.locator('#terminal-workspace')).toBeVisible();
 await expect(frame.locator('.term')).toHaveAttribute('id',original!);
 await frame.getByLabel('Terminal placement').selectOption('right');
 await expect(page.getByRole('radio',{name:'Right panel',exact:true})).toHaveAttribute('aria-checked','true');
 await frame.getByLabel('Group terminal tabs').selectOption('session');
 await expect(page.getByRole('radio',{name:'Session',exact:true})).toHaveAttribute('aria-checked','true');
 await page.setViewportSize({width:1800,height:1000});
 await expect(page.locator('body')).toHaveClass(/terminal-right/);
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await page.getByLabel('Filter sessions',{exact:true}).fill('Feature flag');
 await row(page,'Feature flag').click();
 await page.getByRole('button',{name:'Session details',exact:true}).click();
 // A visible AX/DOM node is insufficient: paint containment must not put the
 // actual inspector under the terminal panel. Its actions must receive clicks.
 await page.locator('#inspector').getByRole('button',{name:'More actions',exact:true}).click();
 await expect(page.getByRole('menuitem',{name:'Open transcript',exact:true})).toBeVisible();
 await page.keyboard.press('Escape');
 await page.getByRole('button',{name:'Back to terminal',exact:true}).click();
 await page.setViewportSize({width:1024,height:600});
 await expect(page.locator('body')).toHaveClass(/terminal-bottom/);
});

test('dock transfer preserves screen and rejects old view capability',async({page,context})=>{
 await page.request.post('/terminal-test/open?title=Transfer%20proof');
 const ws=await page.evaluate(async()=> (await import('/core.js')).api('TerminalWorkspace'));
 const detached=await context.newPage();await detached.goto(ws.url+'&renderer=dom');
 await expect(detached.locator('#connection')).toBeHidden();
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('termfake: ready');
 const before=await detached.locator('.term').getAttribute('id');
 await detached.getByLabel('Terminal placement').selectOption('bottom');
 await expect(page.locator('#terminal-workspace')).toBeVisible();
 const embedded=page.frameLocator('#terminal-workspace iframe');
 await expect(embedded.locator('#connection')).toBeHidden();
 await expect(embedded.locator('.term')).toHaveAttribute('id',before!);
 await expect.poll(async()=>{const f=page.frames().find(f=>f.url().includes('host='));return f?.evaluate(()=>window.hopseshTerminal?.text());}).toContain('termfake: ready');
 const old=await page.request.get(ws.url);expect(old.status()).toBe(403);
});

test('alternate screen, cursor, PID and later input survive repeated dock and detach',async({page,context})=>{
 await page.request.post('/terminal-test/open?title=Full%20screen');
 const workspace=()=>page.evaluate(async()=> (await import('/core.js')).api('TerminalWorkspace'));
 let ws=await workspace();let detached=await context.newPage();await detached.goto(ws.url+'&renderer=dom');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('termfake: ready');
 await detached.locator('.xterm-helper-textarea').focus();await detached.keyboard.press('P');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toMatch(/pid=\d+/);
 const pid=(await detached.evaluate(()=>window.hopseshTerminal?.text())).match(/pid=\d+/)![0];
 await detached.keyboard.press('A');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.buffer())).toMatchObject({type:'alternate',x:11,y:4});
 for(let i=0;i<2;i++) {
  await detached.getByLabel('Terminal placement').selectOption('bottom');
  const frame=page.frameLocator('#terminal-workspace iframe');
  await expect(frame.locator('.term')).toBeVisible();
  await expect(frame.locator('#connection')).toBeHidden();
  let embedded=page.frames().find(f=>f.url().includes('host='))!;
  await expect.poll(()=>embedded.evaluate(()=>window.hopseshTerminal?.buffer())).toMatchObject({type:'alternate',x:11,y:4});
  await expect.poll(()=>embedded.evaluate(()=>window.hopseshTerminal?.text())).toContain('alternate-screen-proof');
  await frame.getByLabel('Terminal placement').selectOption('separate');
  await expect.poll(async()=>(await workspace()).placement).toBe('separate');
  ws=await workspace();await detached.close();detached=await context.newPage();await detached.goto(ws.url+'&renderer=dom');
  await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.buffer())).toMatchObject({type:'alternate',x:11,y:4});
 }
 await detached.locator('.xterm-helper-textarea').focus();await detached.keyboard.press('N');await detached.keyboard.press('P');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain(pid);
 await detached.keyboard.insertText('after move');await detached.keyboard.press('Enter');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('typed=after move');
 await detached.keyboard.press('F');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('stream complete');
});

test('failed replacement keeps the source usable and composing text postpones a move',async({page,context})=>{
 await page.request.post('/terminal-test/open?title=Recovery');
 const ws=await page.evaluate(async()=> (await import('/core.js')).api('TerminalWorkspace'));
 const detached=await context.newPage();await detached.goto(ws.url+'&renderer=dom');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('termfake: ready');
 const input=detached.locator('.xterm-helper-textarea');
 await input.dispatchEvent('compositionstart');await detached.getByLabel('Terminal placement').selectOption('bottom');
 await expect(detached.getByText('Finish composing your text before moving the terminal.')).toBeVisible();
 await expect(page.locator('#terminal-workspace')).toHaveCount(0);
 await input.dispatchEvent('compositionend');
 await page.route('**/terminal/?host=*',route=>route.abort());
 await detached.getByLabel('Terminal placement').selectOption('bottom');
 await expect(detached.getByText(/Couldn’t load the new terminal view/)).toBeVisible({timeout:15000});
 await expect(page.locator('#terminal-workspace')).toBeHidden();
 // The checkpoint deadline releases input on the surviving source view.
 await expect(detached.getByText(/Moving the terminal timed out/)).toBeVisible({timeout:15000});
 await input.focus();await detached.keyboard.insertText('still here');await detached.keyboard.press('Enter');
 await expect.poll(()=>detached.evaluate(()=>window.hopseshTerminal?.text())).toContain('typed=still here');
});

test('shared observation during a press does not swallow the shell grouping menu',async({page})=>{
 await page.request.post('/terminal-test/open?title=Build%20shell&kind=shell');
 await row(page,'Find the codeword').click();
 let release!:()=>void;
 const responseGate=new Promise<void>(resolve=>release=resolve);
 let reached!:()=>void;
 const requested=new Promise<void>(resolve=>reached=resolve);
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='QuickSnapshot')return route.continue();
  reached();await responseGate;
  const response=await route.fetch();const body=await response.json();
  body.result.presence={entries:{'test-presence':[]}};
  await route.fulfill({json:body});
 });
 await page.evaluate(()=>window.__emit('hopsesh:quick',null));
 await requested;
 const button=page.getByRole('button',{name:'More actions',exact:true});
 // Resolve a stable action target after any already-scheduled repaint. A
 // separate visibility check and bounding-box read race that earlier repaint.
 await button.hover();
 await page.mouse.down();release();
 await expect.poll(()=>page.evaluate(async()=>Boolean((await import('/core.js')).state.presence['test-presence']))).toBe(true);
 await page.mouse.up();
 await expect(page.getByRole('menuitem',{name:'Organize a shell with this conversation…'})).toBeVisible();
});

test('a shell can be organized explicitly without becoming a running agent session',async({page,context})=>{
 await page.request.post('/terminal-test/open?title=Build%20shell&kind=shell');
 const ws=await page.evaluate(async()=> (await import('/core.js')).api('TerminalWorkspace'));
 const terminal=await context.newPage();await terminal.goto(ws.url);
 await expect(terminal.getByRole('tab',{name:/Build shell/})).toBeVisible();
 const row=page.locator('.row').filter({has:page.locator('.t').getByText('Find the codeword',{exact:true})});await row.click();
 await page.getByRole('button',{name:'More actions',exact:true}).click();
 await page.getByRole('menuitem',{name:'Organize a shell with this conversation…'}).click();
 await page.getByRole('button',{name:'Group shell here',exact:true}).click();
 await expect(terminal.locator('#runs')).toContainText('Find the codeword');
 await expect(page.locator('#act-primary')).toContainText('Resume');
 await page.getByRole('button',{name:'More actions',exact:true}).click();
 await page.getByRole('menuitem',{name:'Organize a shell with this conversation…'}).click();
 await page.getByRole('button',{name:'Move to Other terminals',exact:true}).click();
 await expect(terminal.locator('#runs')).not.toContainText('Find the codeword');
});

// A native launch may emit placement before the page installs event listeners.
test('startup recovers an embedded terminal whose initial presentation event was missed',async({page})=>{
 await page.goto('about:blank');
 await page.request.post('/terminal-test/open?title=Early%20terminal');
 const changed=await page.request.post('/call',{data:{m:'TerminalPlacement',args:['bottom']}});
 expect(changed.ok()).toBeTruthy();
 await page.goto('/');
 const frame=page.frameLocator('#terminal-workspace iframe');
 await expect(frame.getByRole('tab',{name:/Early terminal/})).toBeVisible();
 await expect(frame.locator('#connection')).toBeHidden();
 await expect.poll(async()=>{
  const response=await page.request.post('/call',{data:{m:'TerminalSettings',args:[]}});
  return (await response.json()).result.placement;
 }).toBe('bottom');
});
