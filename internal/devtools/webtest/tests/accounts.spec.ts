import {test,expect,Page} from '@playwright/test';
import {fresh,row,agentRow,details} from './helpers';
test.beforeEach(async({page})=>fresh(page));

const accounts=async(page:Page)=>{await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();await expect(page.getByRole('heading',{name:'Accounts',exact:true})).toBeVisible();};
const idle=async(page:Page)=>expect(page.getByRole('button',{name:'Check all machines',exact:true})).toBeEnabled({timeout:30000});
// add makes an account folder on this machine through the dialog (its sign-in opens in a tab).
async function add(page:Page,name:string,agent='Claude Code'){
 await page.getByRole('button',{name:'Add an account…',exact:true}).click();const d=page.locator('#dlg');
 await d.getByRole('radio',{name:agent,exact:true}).check();await d.getByLabel('Name',{exact:true}).fill(name);
 await d.getByRole('button',{name:'Create and sign in',exact:true}).click();await expect(d).toBeHidden({timeout:30000});await idle(page);
}
const folder=(page:Page,name:string)=>page.locator('.folder-card').filter({hasText:name+' · '});

test('a new account folder lives in ~/.hopsesh/accounts, can be renamed and is a transfer destination',async({page},testInfo)=>{
 await accounts(page);
 await add(page,'Research lab');
 const f=folder(page,'Research lab');await expect(f).toBeVisible();await expect(f).toContainText('~/.hopsesh/accounts/claude/research-lab');
 await expect(f).toContainText('Run it yourself with claude-research-lab');
 await f.getByRole('button',{name:'Edit',exact:true}).click();const d=page.locator('#dlg');
 await d.getByLabel('Name',{exact:true}).fill('Research');await d.getByLabel('Tags').fill('Personal, Side projects');await d.getByRole('button',{name:'Save',exact:true}).click();
 await expect(folder(page,'Research')).toBeVisible();
 await page.screenshot({path:testInfo.outputPath('accounts-wide.png'),fullPage:true});
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/Move to another account/}).click();
 const choice=page.getByLabel('Destination account');await expect(choice).toBeVisible();const id=await choice.locator('option').filter({hasText:'Research'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation',{timeout:30000});
});

test('the add dialog fits a narrow window and says what it creates',async({page},testInfo)=>{
 await page.setViewportSize({width:760,height:650});await accounts(page);
 await page.getByRole('button',{name:'Add an account…',exact:true}).click();const d=page.locator('#dlg');await expect(d).toBeVisible();
 const b=await d.boundingBox();expect(b!.x).toBeGreaterThanOrEqual(0);expect(b!.x+b!.width).toBeLessThanOrEqual(760);
 await d.getByLabel('Name',{exact:true}).fill('Client A');await expect(d).toContainText('~/.hopsesh/accounts/claude/client-a');await expect(d).toContainText('claude-client-a');
 await d.getByRole('radio',{name:'Codex',exact:true}).check();await expect(d).toContainText('~/.hopsesh/accounts/codex/client-a');await expect(d).toContainText('the way your default folder does');
 await page.screenshot({path:testInfo.outputPath('account-add-narrow.png'),fullPage:true});
 await d.getByRole('button',{name:'Cancel'}).click();await expect(d).toBeHidden();
});

test('Codex desktop action remains visible and explains why a custom account uses a terminal',async({page})=>{
 await accounts(page);await add(page,'Research Codex','Codex');
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/^Continue with Codex…/}).click();
 const choice=page.getByLabel('Destination account');const id=await choice.locator('option').filter({hasText:'Research Codex'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation');await page.locator('#sheet').getByRole('button',{name:/Continue in Codex/}).click();
 await expect(page.getByText('Codex session written',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await agentRow(page,'Find the codeword','Codex').click();await details(page).locator('#act-chevron').click();
 const app=page.getByRole('menuitem',{name:/Resume in Codex app/});await expect(app).toBeVisible();await expect(app).toHaveAttribute('aria-disabled','true');await expect(app).toContainText(/account (root|profile)/);
});

// view is an Accounts screen as the backend would give it, for the states a demo home has not.
function view(base:any){
 const here=base.machines.find((m:any)=>m.local)?.machine||'here';
 return {...base,
  people:[{id:'email:me@gmail.com',email:'me@gmail.com',initials:'M',hue:170,labels:['Claude Max','ChatGPT Pro'],where:['This machine: Claude desktop, Codex in the terminal','studio: Claude Code in the terminal'],sessions:64,machines:2},
   {id:'email:roy@conduct.security',email:'roy@conduct.security',initials:'RC',hue:260,labels:['Claude Team'],where:['This machine: Claude Code in the terminal'],sessions:41,machines:2},
   {id:'unknown:claude:x',initials:'?',hue:0,labels:[],where:[],sessions:5,machines:1,unknown:true,agent:'claude'}],
  machines:[{machine:here,local:true,os:'darwin',scanned:true,checked:new Date().toISOString(),cells:[
    {tool:'claude-terminal',installed:true,version:'Claude Code 2.1.299',signIns:[{person:'email:roy@conduct.security',email:'roy@conduct.security',folder:'default',path:'~/.claude',status:'signed-in'},{person:'email:me@gmail.com',email:'me@gmail.com',folder:'personal',path:'~/.hopsesh/accounts/claude/personal',status:'signed-in'}]},
    {tool:'claude-desktop',installed:true,version:'Claude desktop 2.31226.1, running its own Claude Code 2.1.295',signIns:[{person:'email:me@gmail.com',email:'me@gmail.com',folder:'default',path:'~/.claude',status:'signed-in'}]},
    {tool:'codex-terminal',installed:true,version:'Codex 0.162.0',signIns:[{person:'email:me@gmail.com',email:'me@gmail.com',folder:'default',path:'~/.codex',status:'signed-in'}]},
    {tool:'codex-app',installed:true,version:'ChatGPT desktop 26.1007, running Codex 0.162.0-alpha.17.2',note:'same sign-in as the terminal',signIns:[{person:'email:me@gmail.com',email:'me@gmail.com',folder:'default',path:'~/.codex',status:'signed-in'}]}]},
   {machine:'lurus',local:false,os:'darwin',presence:'ssh',scanned:true,cells:[]},
   {machine:'roys-mbp',local:false,os:'darwin',presence:'login',hopsesh:'0.5.0',scanned:true,checked:new Date().toISOString(),cells:[
    {tool:'claude-terminal',installed:true,version:'Claude Code 2.1.299',signIns:[{folder:'default',path:'~/.claude',status:'signed-out'}]},
    {tool:'claude-desktop',installed:false,signIns:[]},
    {tool:'codex-terminal',installed:true,note:"No Codex CLI; the app's Codex 0.162.0-alpha.17.2 is used",signIns:[{folder:'default',path:'~/.codex',status:'failed',detail:'codex app-server exited 1'}]},
    {tool:'codex-app',installed:true,version:'ChatGPT desktop 26.1007, running Codex 0.162.0-alpha.17.2',signIns:[]}]}],
  folders:[{machine:here,local:true,profile:'in',generation:1,agent:'claude',agentName:'Claude Code',name:'default',path:'~/.claude',default:true,tools:'Terminal (roy@conduct.security) and Claude desktop (me@gmail.com)',counts:[{person:'email:me@gmail.com',n:26},{person:'email:roy@conduct.security',n:10},{person:'unknown:claude:x',n:5}],unrecorded:9,signedIn:true,canRun:true,tags:[]},
   {machine:here,local:true,profile:'out',generation:1,agent:'claude',agentName:'Claude Code',name:'personal',path:'~/.hopsesh/accounts/claude/personal',default:false,command:'claude-personal',tools:'Terminal only (not signed in)',counts:[],unrecorded:0,signedIn:false,canRun:true,tags:[]},
   {machine:here,local:true,profile:'bad',generation:1,agent:'codex',agentName:'Codex',name:'work',path:'~/.hopsesh/accounts/codex/work',default:false,tools:'Terminal only (not signed in)',counts:[],unrecorded:0,signedIn:false,canRun:true,error:'codex app-server exited 1',tags:[]}],
  notes:[{machine:here,title:'This machine: two accounts write to ~/.claude',text:'The terminal is roy@conduct.security and Claude desktop is me@gmail.com, and both keep sessions in ~/.claude.'}],
  move:[{id:'old',name:'Personal',agent:'claude',from:'/state/profiles/old/claude',to:'/h/.hopsesh/accounts/claude/personal'}]};
}
async function routeView(page:Page,moved:{n:number}={n:0}){
 await page.route('**/call',async route=>{
  const m=route.request().postDataJSON().m;
  if(m==='MoveAccountFolders'){moved.n++;return route.fulfill({json:{result:[{id:'old'}]}});}
  if(m!=='AccountsView')return route.continue();
  const response=await route.fetch(),body=await response.json();body.result=view(body.result);if(moved.n)body.result.move=[];
  await route.fulfill({json:body});
 });
}

test('accounts by email, the machines × tools grid, and an account only seen in records',async({page},testInfo)=>{
 await routeView(page);await accounts(page);
 const me=page.locator('.person-card').filter({hasText:'me@gmail.com'});
 await expect(me).toContainText('Claude Max · ChatGPT Pro');await expect(me).toContainText('Ran 64 sessions on 2 machines');await expect(me).toContainText('studio: Claude Code in the terminal');
 const unknown=page.locator('.person-card').filter({hasText:'A Claude account not signed in anywhere'});
 await expect(unknown).toContainText('Ran 5 sessions');await expect(unknown.getByRole('button',{name:'Sign it in on a machine…'})).toBeVisible();
 const grid=page.getByRole('table',{name:"Where you're signed in"});
 await expect(grid).toContainText('Claude desktop 2.31226.1, running its own Claude Code 2.1.295');
 await expect(grid).toContainText('in personal · ~/.hopsesh/accounts/claude/personal');
 await expect(grid).toContainText("hopsesh reads lurus's sessions over SSH, but can't see who is signed in there");
 await expect(grid.getByRole('button',{name:'Set up hopsesh on lurus…'})).toBeVisible();
 await expect(grid).toContainText("No Codex CLI; the app's Codex 0.162.0-alpha.17.2 is used");
 await expect(grid).toContainText('Signed out');await expect(grid).toContainText('Check failed');
 await expect(page.getByRole('note').filter({hasText:'two accounts write to ~/.claude'})).toBeVisible();
 const def=folder(page,'default');await expect(def).toContainText('9 unrecorded');await expect(def).toContainText('26');
 await page.screenshot({path:testInfo.outputPath('accounts-grid.png'),fullPage:true});
 await grid.getByRole('button',{name:'Set up hopsesh on lurus…'}).click();await expect(page.getByRole('heading',{name:'Machines',exact:true})).toBeVisible();
});

test('a signed-in folder offers no Sign in; a signed-out or failed one does',async({page})=>{
 await routeView(page);await accounts(page);
 await expect(folder(page,'default').getByRole('button',{name:/^Sign in/})).toHaveCount(0);
 await expect(folder(page,'default').getByRole('button',{name:/Check sign-in of default/})).toBeVisible();
 await expect(folder(page,'personal').getByRole('button',{name:'Sign in',exact:true})).toBeVisible();
 await expect(folder(page,'personal')).toContainText('Run it yourself with claude-personal');
 await expect(folder(page,'work').getByRole('button',{name:'Sign in again',exact:true})).toBeVisible();
 await expect(folder(page,'work')).toContainText('codex app-server exited 1');
});

test('folders left in the state folder move after the user agrees',async({page})=>{
 const moved={n:0};await routeView(page,moved);await accounts(page);
 const banner=page.getByRole('status').filter({hasText:"hopsesh's state folder"});await expect(banner).toBeVisible();
 await banner.getByRole('button',{name:'Move them'}).click();await expect(page.locator('#dlg')).toContainText('/state/profiles/old/claude → /h/.hopsesh/accounts/claude/personal');
 expect(moved.n).toBe(0);
 await page.locator('#dlg').getByRole('button',{name:'Move them'}).click();await expect(banner).toBeHidden({timeout:30000});expect(moved.n).toBe(1);
});

test('account grouping shows retained source and destination copies after a transfer',async({page})=>{
 await accounts(page);await add(page,'Second personal');
 await page.getByRole('button',{name:'Back to sessions',exact:true}).click();await row(page,'Find the codeword').click();
 await details(page).getByRole('button',{name:'Move',exact:true}).click();await page.getByRole('menuitem',{name:/Move to another account/}).click();
 const choice=page.getByLabel('Destination account'),id=await choice.locator('option').filter({hasText:'Second personal'}).getAttribute('value');await choice.selectOption(id!);
 await expect(page.locator('#sheet')).toContainText('portable conversation');await page.locator('#sheet').getByRole('button',{name:/Continue in Claude Code/}).click();
 await expect(page.getByText('Claude Code session written',{exact:true})).toBeVisible({timeout:30000});await page.getByRole('button',{name:'Back to sessions',exact:true}).click();
 await page.locator('#btn-display').click();await page.locator('#dp-group').selectOption('account');await page.keyboard.press('Escape');
 await expect(page.locator('.gname').filter({hasText:'Second personal'})).toHaveCount(1);
 await expect(row(page,'Find the codeword')).toHaveCount(2);
 const second=page.locator('[role="treeitem"][aria-level="1"]').filter({has:page.locator('.gname',{hasText:'Second personal'})});
 await expect(second.locator('.row').filter({has:page.locator('.t').getByText('Find the codeword',{exact:true})})).toHaveCount(1);
});
