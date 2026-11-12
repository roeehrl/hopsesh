import {test,expect,Page} from '@playwright/test';
import {fresh} from './helpers';
test.beforeEach(async({page})=>fresh(page));

// The Accounts screen with two machines, account folders with long paths, a sign-in hopsesh
// can't read, and two accounts seen only in session records: how it looks, and that nothing
// overflows or stretches.
const long='~/.local/state/hopsesh/profiles/9ed8bd2002abb3c51ee867454de32790/claude';
function view(base:any){
 const here=base.machines.find((m:any)=>m.local)?.machine||'here';
 const me='email:roeehrl@gmail.com',de='email:roeeharde@gmail.com',u1='unknown:claude:a',u2='unknown:claude:b';
 const si=(person:string,email:string,folder:string,path:string)=>({person,email,folder,path,status:'signed-in'});
 return {...base,
  people:[{id:me,email:'roeehrl@gmail.com',initials:'R',hue:80,labels:['ChatGPT Plus','Claude Team','Claude Max'],where:['This machine: Claude desktop, Codex app, Codex in the terminal','laptop: Codex app, Claude Code in the terminal, Claude Code in the terminal in the personal folder, Codex in the terminal'],sessions:161,machines:2},
   {id:de,email:'roeeharde@gmail.com',initials:'R',hue:200,labels:['Claude Max'],where:['This machine: Claude Code in the terminal in the de folder','laptop: Claude Code in the terminal in the de folder'],sessions:10,machines:1},
   {id:u1,initials:'1',number:1,hue:0,labels:[],where:[],sessions:16,machines:1,ranOn:['laptop'],unknown:true,agent:'claude'},
   {id:u2,initials:'2',number:2,hue:0,labels:[],where:[],sessions:1,machines:1,ranOn:['laptop'],unknown:true,agent:'claude'}],
  machines:[{machine:here,local:true,os:'darwin',scanned:true,checked:new Date().toISOString(),cells:[
    {tool:'claude-terminal',installed:true,version:'Claude Code 2.1.314',signIns:[{folder:'default',path:'~/.claude',status:'signed-out'},si(de,'roeeharde@gmail.com','de','~/.hopsesh/accounts/claude/de')]},
    {tool:'claude-desktop',installed:true,version:'Claude desktop 4.4212.0, running its own Claude Code 2.1.309',signIns:[si(me,'roeehrl@gmail.com','default','~/.claude')]},
    {tool:'codex-terminal',installed:true,version:'Codex 0.160.1',signIns:[si(me,'roeehrl@gmail.com','default','~/.codex')]},
    {tool:'codex-app',installed:true,version:'ChatGPT desktop 26.1007, running Codex 0.162.0-alpha.17.2',note:'same sign-in as the terminal',signIns:[si(me,'roeehrl@gmail.com','default','~/.codex')]}]},
   {machine:'laptop',local:false,os:'darwin',presence:'login',hopsesh:'0.7.0',scanned:true,checked:new Date().toISOString(),cells:[
    {tool:'claude-terminal',installed:true,version:'Claude Code 2.1.316',signIns:[si(me,'roeehrl@gmail.com','default','~/.claude'),si(me,'roeehrl@gmail.com','claude code personal',long),si(de,'roeeharde@gmail.com','de','~/.hopsesh/accounts/claude/de')]},
    {tool:'claude-desktop',installed:true,version:'Claude desktop 4.4212.0, running its own Claude Code 2.1.309',signIns:[{folder:'default',path:'~/.claude',status:'unknown'}]},
    {tool:'codex-terminal',installed:true,version:'Codex 0.162.0',signIns:[si(me,'roeehrl@gmail.com','default','~/.codex')]},
    {tool:'codex-app',installed:true,version:'ChatGPT desktop 26.1007, running Codex 0.162.0-alpha.17.2',note:'same sign-in as the terminal',signIns:[si(me,'roeehrl@gmail.com','default','~/.codex')]}]}],
  folders:[
   {machine:here,local:true,profile:'h1',generation:1,agent:'claude',agentName:'Claude Code',name:'default',path:'~/.claude',default:true,tools:'Terminal (not signed in) and Claude desktop (roeehrl@gmail.com)',counts:[{person:me,n:121}],unrecorded:52,signedIn:false,canRun:true,tags:[]},
   {machine:here,local:true,profile:'h2',generation:1,agent:'claude',agentName:'Claude Code',name:'de',path:'~/.hopsesh/accounts/claude/de',default:false,command:'claude-de',tools:'Terminal only (roeeharde@gmail.com)',counts:[],unrecorded:0,signedIn:true,canRun:true,tags:[]},
   {machine:here,local:true,profile:'h3',generation:1,agent:'codex',agentName:'Codex',name:'default',path:'~/.codex',default:true,tools:'Terminal and ChatGPT desktop share one sign-in (roeehrl@gmail.com)',counts:[{person:me,n:17}],unrecorded:35,signedIn:true,canRun:true,tags:[]},
   {machine:'laptop',local:false,profile:'l1',generation:1,agent:'claude',agentName:'Claude Code',name:'default',path:'~/.claude',default:true,tools:'Terminal (roeehrl@gmail.com) and Claude desktop (its own sign-in)',counts:[{person:me,n:19},{person:u1,n:16},{person:u2,n:1}],unrecorded:4,signedIn:true,canRun:true,tags:[]},
   {machine:'laptop',local:false,profile:'l2',generation:1,agent:'claude',agentName:'Claude Code',name:'claude code personal',path:long,default:false,tools:'Terminal only (roeehrl@gmail.com)',counts:[],unrecorded:1,signedIn:true,canRun:true,tags:[]},
   {machine:'laptop',local:false,profile:'l3',generation:1,agent:'claude',agentName:'Claude Code',name:'de',path:'~/.hopsesh/accounts/claude/de',default:false,tools:'Terminal only (roeeharde@gmail.com)',counts:[{person:de,n:10}],unrecorded:4,signedIn:true,canRun:true,tags:[]},
   {machine:'laptop',local:false,profile:'l4',generation:1,agent:'codex',agentName:'Codex',name:'default',path:'~/.codex',default:true,tools:'Terminal and ChatGPT desktop share one sign-in (roeehrl@gmail.com)',counts:[{person:me,n:4}],unrecorded:27,signedIn:true,canRun:true,tags:[]}],
  notes:[],move:[]};
}

async function open(page:Page){
 await page.route('**/call',async route=>{
  if(route.request().postDataJSON().m!=='AccountsView')return route.continue();
  const response=await route.fetch(),body=await response.json();body.result=view(body.result);await route.fulfill({json:body});
 });
 await page.locator('#btn-settings').click();await page.getByRole('button',{name:'Accounts',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Accounts',exact:true})).toBeVisible();
 await expect(page.locator('.folder-card').first()).toBeVisible();
}

for(const width of [1280,900]){
 test(`accounts at ${width}px: round avatars, nothing overflows its box`,async({page},testInfo)=>{
  await page.setViewportSize({width,height:900});if(width>1000)await page.emulateMedia({colorScheme:'dark'});await open(page);
  await page.screenshot({path:testInfo.outputPath(`accounts-${width}.png`),fullPage:true});
  for(const s of ['where-h','folders-h'])await page.locator(`section[aria-labelledby="${s}"]`).screenshot({path:testInfo.outputPath(`accounts-${width}-${s}.png`)});
  // Every avatar is a circle.
  for(const b of await page.locator('.accounts-page .avatar').evaluateAll(els=>els.map(e=>{const r=e.getBoundingClientRect();return [r.width,r.height];}))){
   expect(Math.abs(b[0]-b[1]),`avatar ${b}`).toBeLessThanOrEqual(1);expect(b[1]).toBeLessThanOrEqual(36);
  }
  // No text runs out of its card or grid cell.
  const over=await page.locator('.accounts-page').evaluate(root=>{
   const out:string[]=[];
   for(const box of root.querySelectorAll('.tcell, .tmach, .folder-card, .person-card')){
    const b=box.getBoundingClientRect();
    for(const el of box.querySelectorAll('*')){const r=el.getBoundingClientRect();if(r.width&&(r.right>b.right+1||r.left<b.left-1))out.push((el.textContent||'').slice(0,60));}
   }
   return out;
  });
  expect(over).toEqual([]);
  // The page itself doesn't scroll sideways.
  expect(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBeTruthy();
 });
}

test('accounts seen only in records are one card, each told apart',async({page})=>{
 await open(page);
 const others=page.locator('.person-card').filter({hasText:'not signed in anywhere'});
 await expect(others).toHaveCount(1);await expect(others).toContainText('2 accounts not signed in anywhere');
 await expect(others).toContainText('Claude account 1 · 16 sessions on laptop');await expect(others).toContainText('Claude account 2 · 1 session on laptop');
});
