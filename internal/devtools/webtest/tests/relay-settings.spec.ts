import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => {
  await fresh(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Internet delivery" }).click();
});

test("saved handoff ancestry requires explicit provider-matched choices and a fresh review", async ({page}) => {
 const task={id:"a".repeat(32),provider:"claude-hosted",session:"rebuilt-native-session"};
 const other={id:"b".repeat(32),provider:"codex-current",session:"other-provider-task"};
 const handoff={journal:"saved-handoff",provider:task.provider,title:"Original source",session:"outer-cloud-session",family:"verified-family",time:new Date().toISOString()};
 let reviews=0,saved=false;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){
   const response=await route.fetch(),body=await response.json();
   body.result.relay={initialized:true,enrolled:true,enabled:true,expires:Date.now()/1000+3600,peers:[],tasks:[task,other],admissions:[],taskLinks:saved?[{task:task.id,journal:handoff.journal,family:handoff.family,fork:true}]:[]};
   await route.fulfill({json:body});return;
  }
  if(request.m==="RelayCloudHandoffOptions"){await route.fulfill({json:{result:[handoff]}});return;}
  if(request.m==="RelayPlanCloudHandoffLink"){
   reviews++;expect(request.args).toEqual([task.id,handoff.journal,reviews>1]);
   await route.fulfill({json:{result:{task:task.id,handoff,fork:reviews>1,review:`review-${reviews}`}}});return;
  }
  if(request.m==="RelayLinkCloudHandoff"){
   expect(request.args).toEqual([task.id,handoff.journal,true,"review-2"]);saved=true;
   await route.fulfill({json:{result:{}}});return;
  }
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await page.getByRole("button",{name:"Link task to saved handoff…"}).click();
 const d=page.getByRole("dialog");
 await expect(d.getByLabel("Logical cloud task")).toHaveValue("");
 await expect(d.getByLabel("Saved cloud handoff")).toBeDisabled();
 await expect(d.getByRole("button",{name:"Link reviewed handoff"})).toBeDisabled();
 await d.getByLabel("Logical cloud task").selectOption(other.id);
 await expect(d.getByLabel("Saved cloud handoff")).toBeDisabled();
 await d.getByLabel("Logical cloud task").selectOption(task.id);
 await expect(d.getByLabel("Saved cloud handoff")).toHaveValue("");
 await d.getByLabel("Saved cloud handoff").selectOption(handoff.journal);
 await expect(d.getByLabel("Independent cloud fork")).not.toBeChecked();
 await d.getByRole("button",{name:"Review ancestry"}).click();
 await expect(d.getByRole("status")).toContainText("Continuation of this saved handoff");
 await expect(d.getByRole("button",{name:"Link reviewed handoff"})).toBeEnabled();
 await d.getByLabel("Independent cloud fork").check();
 await expect(d.getByRole("button",{name:"Link reviewed handoff"})).toBeDisabled();
 await expect(d.getByRole("status")).toBeEmpty();
 await d.getByRole("button",{name:"Review ancestry"}).click();
 await expect(d.getByRole("status")).toContainText("Independent fork: shared ancestry, separate trip counts");
 await d.getByRole("button",{name:"Link reviewed handoff"}).click();
 await expect(d.getByRole("status")).toContainText("Saved handoff linked");
 await d.getByRole("button",{name:"Close",exact:true}).click();
 await expect(page.getByRole("heading",{name:"Saved task ancestry"})).toBeVisible();
 await expect(page.getByText("Independent fork · saved handoff saved-handoff",{exact:true})).toBeVisible();
 expect(saved).toBe(true);
});

test("a stale saved handoff review preserves choices and cannot be applied again without review", async ({page}) => {
 const task={id:"c".repeat(32),provider:"claude-hosted",session:"task"};
 const handoff={journal:"saved-source",provider:task.provider,title:"Saved source",session:"outer-session",family:"family",time:new Date().toISOString()};
 let applies=0,reviews=0;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){
   const response=await route.fetch(),body=await response.json();
   body.result.relay={initialized:true,enrolled:true,enabled:true,expires:Date.now()/1000+3600,peers:[],tasks:[task],admissions:[]};
   await route.fulfill({json:body});return;
  }
  if(request.m==="RelayCloudHandoffOptions"){await route.fulfill({json:{result:[handoff]}});return;}
  if(request.m==="RelayPlanCloudHandoffLink"){
   reviews++;
   await route.fulfill({json:reviews===1?{result:{task:task.id,handoff,fork:false,review:"first-review"}}:{error:"this task already has a reviewed checkpoint"}});return;
  }
  if(request.m==="RelayLinkCloudHandoff"){
   applies++;await route.fulfill({json:{error:"saved handoff changed since review; review it again"}});return;
  }
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await page.getByRole("button",{name:"Link task to saved handoff…"}).click();
 const d=page.getByRole("dialog");
 await d.getByLabel("Logical cloud task").selectOption(task.id);
 await d.getByLabel("Saved cloud handoff").selectOption(handoff.journal);
 await d.getByRole("button",{name:"Review ancestry"}).click();
 await d.getByRole("button",{name:"Link reviewed handoff"}).click();
 await expect(d.getByRole("alert")).toContainText("saved handoff changed since review");
 await expect(d.getByLabel("Saved cloud handoff")).toHaveValue(handoff.journal);
 await expect(d.getByRole("button",{name:"Link reviewed handoff"})).toBeDisabled();
 await d.getByRole("button",{name:"Review ancestry"}).click();
 await expect(d.getByRole("alert")).toHaveText("this task already has a reviewed checkpoint");
 await expect(d.getByRole("button",{name:"Link reviewed handoff"})).toBeDisabled();
 expect(applies).toBe(1);
});

test("resuming cloud task identity is explicit and still requires a new claim and approval",async({page})=>{
 const task={id:"e".repeat(32),provider:"codex-current",created:Math.floor(Date.now()/1000)-3600,ownerFingerprint:"f".repeat(64)};
 let issued=0;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){
   const response=await route.fetch(),body=await response.json();
   body.result.relay={initialized:true,enrolled:true,enabled:true,expires:Date.now()/1000+3600,peers:[],tasks:[task],admissions:[],identity:{id:task.ownerFingerprint}};
   await route.fulfill({json:body});return;
  }
  if(request.m==="RelayIssueCloudAdmission"){
   expect(request.args).toEqual([task.provider,"rebuilt-native-id",3600,task.id]);issued++;
   await route.fulfill({json:{result:{id:"a".repeat(32),taskId:task.id,path:"/private/new-invitation.json",ownerFingerprint:task.ownerFingerprint,expires:Date.now()/1000+600}}});return;
  }
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await page.getByRole("button",{name:"Invite cloud session…"}).click();
 const d=page.getByRole("dialog");
 await expect(d.getByLabel("Cloud task continuity")).toHaveValue("");
 await expect(d).toContainText("Matching names or native IDs never join tasks automatically");
 await d.getByLabel("Cloud task continuity").selectOption(task.id);
 await expect(d.getByLabel("Cloud invitation provider")).toHaveValue(task.provider);
 await expect(d.getByLabel("Cloud invitation provider")).toBeDisabled();
 await d.getByLabel("Cloud task continuity").selectOption("");
 await expect(d.getByLabel("Cloud invitation provider")).toBeEnabled();
 await d.getByLabel("Cloud task continuity").selectOption(task.id);
 await d.getByLabel("Actual cloud session ID").fill("rebuilt-native-id");
 expect(issued).toBe(0);
 await d.getByRole("button",{name:"Create private invitation"}).click();
 await expect(d.getByRole("status")).toContainText(`Logical task: ${task.id}`);
 await expect(d.getByRole("status")).toContainText("does not approve a cloud endpoint or enable sharing");
 await expect(d.getByRole("status")).toContainText("approve the fresh session's public identity");
 expect(issued).toBe(1);
});

test("checkpoint cache cleanup stays explicit and keeps recovery errors reviewable",async({page})=>{
 let reads=0,removes=0;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="CloudCheckpointCache"){reads++;await route.fulfill({json:{result:[{operation:"frozen-review",bytes:2048,reviewed:new Date().toISOString(),started:true}]}});return;}
  if(request.m==="RemoveCloudCheckpoint"){removes++;expect(request.args).toEqual(["frozen-review"]);await route.fulfill({json:{error:"checkpoint belongs to an interrupted import or pending receipt; recover or undo it before cleanup"}});return;}
  await route.continue();
 });
 expect(reads).toBe(0);
 await page.getByRole("button",{name:"Manage cached checkpoints…"}).click();
 const d=page.getByRole("dialog");
 await expect(d).toContainText("frozen-review");
 await expect(d).toContainText("undo backups are retained separately");
 expect(removes).toBe(0);
 await d.getByRole("button",{name:"Remove cached checkpoint…"}).click();
 await expect(page.getByRole("dialog")).toContainText("Native sessions, lineage receipts and undo journals stay available");
 await page.getByRole("button",{name:"Remove cache",exact:true}).click();
 await expect(d.getByRole("alert")).toContainText("interrupted import or pending receipt");
 await expect(d.getByRole("button",{name:"Remove cache",exact:true})).toBeEnabled();
 await d.getByRole("button",{name:"Keep cache",exact:true}).click();
 await expect(d.getByRole("button",{name:"Remove cached checkpoint…"})).toBeEnabled();
 expect(removes).toBe(1);
});

test("scoped cloud inspection is explicit and unqualified export stays disabled",async({page})=>{
 let checked=0,exported=0;
 const fingerprint="a".repeat(64);
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){const response=await route.fetch(),body=await response.json();body.result.relay={initialized:true,enabled:true,enrolled:true,expires:Date.now()/1000+3600,peers:[{id:fingerprint,fingerprint,name:"One cloud task",kind:"cloud-session",sendMethods:["observe"],methods:[],expires:Date.now()/1000+3600}]};await route.fulfill({json:body});return;}
  if(request.m==="RelayCloudInspect"){checked++;expect(request.args).toEqual([fingerprint]);await route.fulfill({json:{result:{provider:"codex-current",session:"actual-task",workspace:"/workspace/repo",observedAt:new Date().toISOString(),leaseExpires:new Date(Date.now()+3600000).toISOString(),transcriptAvailable:false,exportAllowed:false}}});return;}
  if(request.m==="RelayCloudPreview"){exported++;await route.fulfill({json:{error:"must not export"}});return;}
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await expect(page.getByRole("button",{name:"Inspect session…"})).toBeVisible();
 expect(checked).toBe(0);
 await page.getByRole("button",{name:"Inspect session…"}).click();
 const d=page.getByRole("dialog");
 await expect(d).toContainText("Native conversation export is unavailable");
 await expect(d.getByRole("button",{name:"Preview conversation checkpoint"})).toBeDisabled();
 await expect(d.getByRole("button",{name:"Import conversation…"})).toBeDisabled();
 await d.getByRole("button",{name:"Check session now"}).click();
 await expect.poll(()=>checked).toBe(2);expect(exported).toBe(0);
});

test("checkpoint import reviews exact destination and invalidates approval after editing",async({page})=>{
 const fingerprint="d".repeat(64),now=new Date().toISOString();
 let planCalls=0,applyCalls=0,reviewed="";
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){const response=await route.fetch(),body=await response.json();body.result.relay={initialized:true,enabled:true,enrolled:true,expires:Date.now()/1000+3600,peers:[{id:fingerprint,fingerprint,name:"Approved task",kind:"cloud-session",expires:Date.now()/1000+3600}]};await route.fulfill({json:body});return;}
  if(request.m==="RelayCloudInspect"){await route.fulfill({json:{result:{provider:"claude-hosted",session:"actual-session",workspace:"/workspace/repo",observedAt:now,leaseExpires:now,transcriptAvailable:true,exportAllowed:true}}});return;}
  if(request.m==="AccountDestinations"){await route.fulfill({json:{result:[{id:"profile-one",name:"Research",default:true}]}});return;}
  if(request.m==="ChooseFolder"){await route.fulfill({json:{result:"/local/repo"}});return;}
  if(request.m==="RelayCloudPlan"){planCalls++;expect(request.args.slice(0,4)).toEqual([fingerprint,planCalls===1?"claude":"codex","/local/repo","profile-one"]);expect(request.args[5]).toBe(false);reviewed=request.args[4];await route.fulfill({json:{result:{operation:reviewed,sha256:"e".repeat(64),checkpoint:{records:12,omittedTailBytes:19},plan:{agent:planCalls===1?"Claude Code":"Codex",targetCwd:"/local/repo",warnings:["Portable conversation only"],blockers:[]}}}});return;}
  if(request.m==="RelayCloudApply"){applyCalls++;expect(request.args).toEqual([reviewed]);await route.fulfill({json:{error:"this cloud incarnation is not approved for conversation export"}});return;}
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await page.getByRole("button",{name:"Inspect session…"}).click();
 await page.getByRole("dialog").getByRole("button",{name:"Import conversation…"}).click();
 const d=page.getByRole("dialog");
 await expect(d.getByRole("button",{name:"Import reviewed checkpoint"})).toBeDisabled();
 await d.getByRole("button",{name:"Choose working directory…"}).click();
 await d.getByRole("button",{name:"Review import"}).click();
 await expect(d).toContainText("19 unfinished bytes excluded");
 await expect(d.getByRole("button",{name:"Import reviewed checkpoint"})).toBeEnabled();
 const first=reviewed;
 await d.getByLabel("Checkpoint destination agent").selectOption("codex");
 await expect(d.getByRole("button",{name:"Import reviewed checkpoint"})).toBeDisabled();
 await d.getByRole("button",{name:"Review import"}).click();
 expect(reviewed).not.toBe(first);
 await d.getByRole("button",{name:"Import reviewed checkpoint"}).click();
 await expect(d.getByRole("alert")).toContainText("not approved for conversation export");
 await expect(d).toBeVisible();expect(applyCalls).toBe(1);
});

test("cloud checkpoint preview renders roles and Markdown while disclosing unfinished writes",async({page})=>{
 const fingerprint="b".repeat(64),observedAt=new Date().toISOString(),leaseExpires=new Date(Date.now()+3600000).toISOString();
 const observation={provider:"claude-hosted",session:"actual-session",workspace:"/workspace/repo",observedAt,leaseExpires,transcriptAvailable:true,exportAllowed:true};
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){const response=await route.fetch(),body=await response.json();body.result.relay={initialized:true,enabled:true,enrolled:true,expires:Date.now()/1000+3600,peers:[{id:fingerprint,fingerprint,name:"One cloud session",kind:"cloud-session",sendMethods:["observe","export"],methods:[],expires:Date.now()/1000+3600}]};await route.fulfill({json:body});return;}
  if(request.m==="RelayCloudInspect"){await route.fulfill({json:{result:observation}});return;}
  if(request.m==="RelayCloudPreview"){await route.fulfill({json:{result:{...observation,sha256:"c".repeat(64),checkpoint:{created:observedAt,records:12,omittedTailBytes:31},preview:{more:true,items:[{role:"user",text:"**Review this**\n<script>window.cloudCheckpointInjected=true</script>"},{role:"agent",text:"**Result**: ready to inspect."}]}}}});return;}
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status",exact:true}).click();
 await page.getByRole("button",{name:"Inspect session…"}).click();
 const d=page.getByRole("dialog");
 await expect(d.getByRole("button",{name:"Preview conversation checkpoint"})).toBeEnabled();
 await d.getByRole("button",{name:"Preview conversation checkpoint"}).click();
 await expect(d).toContainText("31 unfinished bytes excluded");
 await expect(d.locator(".msg.user strong")).toHaveText("Review this");
 await expect(d.locator(".msg.agent strong")).toHaveText("Result");
 await expect(d).toContainText("Earlier conversation is not included");
 expect(await page.evaluate(()=> (window as any).cloudCheckpointInjected)).toBeUndefined();
 await expect(d.locator("script")).toHaveCount(0);
});

test("opening internet settings does not enroll or create an identity", async ({ page }) => {
  await expect(page.getByRole("status").filter({ hasText: "Not initialized" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Enroll endpoint…" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Pair machine…" })).toBeDisabled();
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Not enrolled" })).toBeVisible();
  await page.getByRole("button", { name: "Show public identity" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator("pre")).toContainText('"signing"');
  await expect(dialog.locator("pre")).not.toContainText("AGE-SECRET-KEY");
  await expect(dialog.locator("pre")).not.toContainText('"token"');
});

test("pairing keeps invalid input visible and separates transfer permissions", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Pair machine…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Allow sending sessions to this machine")).not.toBeChecked();
  await expect(dialog.getByLabel("Allow bringing conversations from this machine")).not.toBeChecked();
  await dialog.getByLabel("Public pairing identity").fill("{invalid}");
  await dialog.getByRole("button", { name: "Approve endpoint" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("invalid public identity JSON");
  await expect(dialog.getByLabel("Public pairing identity")).toHaveValue("{invalid}");
  await expect(dialog.getByRole("button", { name: "Approve endpoint" })).toBeEnabled();
});

test("cloud approval has bounded lifetime and no machine receive controls", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Approve cloud session…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Approval lifetime")).toHaveValue("3600");
  await expect(dialog.getByLabel("Approval lifetime").locator("option")).toHaveCount(4);
  await expect(dialog.getByLabel("Machine name")).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Choose repository…" })).toHaveCount(0);
  await expect(dialog.getByLabel("Allow conversation export")).not.toBeChecked();
});

test("enrollment hides credentials and preserves the dialog on validation failure", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Enroll endpoint…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Scoped enrollment credential JSON")).toHaveAttribute("type", "password");
  await dialog.getByLabel("Relay HTTPS origin").fill("https://relay.example.com");
  await dialog.getByLabel("Scoped enrollment credential JSON").fill("invalid");
  await dialog.getByRole("button", { name: "Enroll this endpoint" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("invalid scoped enrollment credential JSON");
  await expect(dialog.getByRole("button", { name: "Enroll this endpoint" })).toBeEnabled();
});

test("public pairing identity wraps within a compact window", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 650 });
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Show public identity" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Close", exact: true })).toBeInViewport();
  const dimensions = await dialog.evaluate(element => ({ width: element.scrollWidth, client: element.clientWidth }));
  expect(dimensions.width).toBeLessThanOrEqual(dimensions.client + 1);
});

test("browser enrollment keeps secrets out of the bridge and cancels waiting approval", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  let pending: any;
  let cancellations=0;
  await page.route("**/call", async route => {
    const request=route.request().postDataJSON();
    if(request.m==="RelayLogin") {
      expect(request.args).toEqual(["https://relay.hopsesh.codonic.dev"]);
      pending=route;return;
    }
    if(request.m==="RelayCancelLogin") {
      cancellations++;
      await route.fulfill({json:{}});
      if(pending)await pending.fulfill({json:{error:"relay login canceled or expired; start again when ready"}});
      return;
    }
    await route.continue();
  });
  await page.getByRole("button", { name: "Connect in browser…" }).click();
  const dialog=page.getByRole("dialog");
  await expect(dialog.getByLabel("Browser login relay origin")).toHaveValue("https://relay.hopsesh.codonic.dev");
  await expect(dialog.locator(".mono")).toHaveText(/^[a-f0-9]{64}$/);
  await dialog.getByRole("button",{name:"Open browser to connect"}).click();
  await expect(dialog.getByRole("status").filter({hasText:"Waiting for approval in your browser…"})).toBeVisible();
  await expect(dialog.getByRole("button",{name:"Open browser to connect"})).toBeDisabled();
  await expect(dialog.getByLabel("Browser login relay origin")).toBeDisabled();
  await dialog.getByRole("button",{name:"Cancel",exact:true}).click();
  await expect(dialog).not.toBeVisible();
  await expect.poll(()=>cancellations).toBe(1);
  await expect(page.getByRole("status").filter({hasText:"Not enrolled"})).toBeVisible();
});

test("cloud invitations require enrollment and show provisional routing separately from approval",async({page})=>{
 await expect(page.getByRole("button",{name:"Invite cloud session…"})).toBeDisabled();
 await page.getByRole("button",{name:"Create endpoint identity"}).click();
 await expect(page.getByRole("button",{name:"Invite cloud session…"})).toBeDisabled();
 const record={id:"a".repeat(32),path:"/private/invitations/one.json",provider:"codex-current",session:"actual-task-123",ownerFingerprint:"b".repeat(64),origin:"https://relay.example.com",expires:Math.floor(Date.now()/1000)+600,leaseSeconds:3600,revoked:false};
 let created=false,attempts=0;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="Settings"){
   const response=await route.fetch(),body=await response.json();
   body.result.relay={initialized:true,enrolled:true,enabled:true,expires:record.expires+3600,peers:[],admissions:created?[record]:[],identity:{id:record.ownerFingerprint}};
   await route.fulfill({json:body});return;
  }
  if(request.m==="RelayIssueCloudAdmission"){
   expect(request.args).toEqual(["codex-current","actual-task-123",3600,""]);attempts++;
   if(attempts===1){await route.fulfill({json:{error:"relay temporarily unavailable"}});return;}
   created=true;await route.fulfill({json:{result:record}});return;
  }
  if(request.m==="RelayRevokeCloudAdmission"){expect(request.args).toEqual([record.id]);record.revoked=true;await route.fulfill({json:{result:null}});return;}
  if(request.m==="RelayCheckCloudAdmission"){expect(request.args).toEqual([record.id]);await route.fulfill({json:{result:{status:"claimed",provider:record.provider,session:record.session,leaseExpires:record.expires+3600,public:{id:"c".repeat(64),endpoint:"cloud/codex-current/"+"d".repeat(32),signing:"public-signing-key",recipient:"public-age-recipient"}}}});return;}
  await route.continue();
 });
 await page.getByRole("button",{name:"Refresh status"}).click();
 await page.getByRole("button",{name:"Invite cloud session…"}).click();
 const dialog=page.getByRole("dialog");
 await dialog.getByLabel("Cloud invitation provider").selectOption("codex-current");
 await dialog.getByLabel("Actual cloud session ID").fill(record.session);
 await dialog.getByRole("button",{name:"Create private invitation"}).click();
 await expect(dialog.getByRole("alert")).toHaveText("relay temporarily unavailable");
 await expect(dialog.getByLabel("Actual cloud session ID")).toHaveValue(record.session);
 await dialog.getByRole("button",{name:"Create private invitation"}).click();
 const invitationResult=dialog.getByRole("status").filter({hasText:"does not approve a cloud endpoint or enable sharing"});
 await expect(invitationResult).toBeVisible();
 await expect(invitationResult).toContainText(record.ownerFingerprint);
 await expect(invitationResult).toContainText("Keep it out of setup scripts");
 await expect(dialog.getByRole("button",{name:"Create private invitation"})).toBeDisabled();
 await dialog.getByRole("button",{name:"Close",exact:true}).click();
 await expect(page.getByText("Claim window open; claim status unconfirmed")).toBeVisible();
 await expect(page.getByText("No endpoints have been approved.")).toBeVisible();
 await page.getByRole("button",{name:"Check claim",exact:true}).click();
 await expect(page.getByRole("dialog").getByRole("status").filter({hasText:"delivery is provisional"})).toBeVisible();
 await page.getByRole("dialog").getByRole("button",{name:"Review cloud approval…"}).click();
 await expect(page.getByRole("dialog").getByLabel("Public pairing identity")).toHaveValue(/public-signing-key/);
 await expect(page.getByRole("dialog").getByLabel("Verified fingerprint")).toHaveValue("");
 await expect(page.getByRole("dialog").getByLabel("Allow conversation export")).not.toBeChecked();
 await page.getByRole("dialog").getByRole("button",{name:"Cancel",exact:true}).click();
 await page.getByRole("button",{name:"Revoke invitation",exact:true}).click();
 await page.getByRole("dialog").getByRole("button",{name:"Revoke",exact:true}).click();
 await expect(page.getByText("Delivery revoked",{exact:true})).toBeVisible();
 await expect(page.getByRole("button",{name:"Revoke invitation",exact:true})).toBeDisabled();
});

test("cloud startup reviews file changes and invalidates review when its version changes",async({page})=>{
 let previews=0,applied=0;
 await page.route("**/call",async route=>{
  const request=route.request().postDataJSON();
  if(request.m==="ChooseFolder"){await route.fulfill({json:{result:"/fixture/repository"}});return;}
  if(request.m==="CloudStartupPreview"){
   expect(request.args).toEqual(["codex-current",previews===0?"0.5.0":"0.5.1","https://downloads.hopsesh.codonic.dev","/fixture/repository"]);
   previews++;await route.fulfill({json:{result:{id:`review-${previews}`,setup:{provider:"codex-current",version:request.args[1],reason:"Start skill is an instruction, not a guaranteed lifecycle callback.",changes:[{path:".hopsesh/cloud-install.sh",action:"create"},{path:".hopsesh/codex-start.md",action:"create"}],installScript:"verified-install-script",startSkill:"fresh-task-start-instructions"}}}});return;
  }
  if(request.m==="CloudStartupApply"){expect(request.args).toEqual(["review-2"]);applied++;await route.fulfill({json:{result:null}});return;}
  await route.continue();
 });
 await page.getByRole("button",{name:"Prepare cloud startup…"}).click();
 const dialog=page.getByRole("dialog");
 await expect(dialog.getByRole("button",{name:"Install reviewed startup files"})).toBeDisabled();
 await dialog.getByRole("button",{name:"Preview startup files"}).click();
 await expect(dialog.getByRole("alert")).toHaveText("Choose a repository first.");
 await dialog.getByLabel("Startup provider").selectOption("codex-current");
 await dialog.getByLabel("Immutable cloud helper version").fill("0.5.0");
 await dialog.getByRole("button",{name:"Choose repository…"}).click();
 await dialog.getByRole("button",{name:"Preview startup files"}).click();
 await expect(dialog.getByRole("heading",{name:"Review startup changes"})).toBeVisible();
 await expect(dialog.getByText("create · .hopsesh/codex-start.md")).toBeVisible();
 await expect(dialog.getByRole("button",{name:"Copy Start skill instructions"})).toBeVisible();
 await expect(dialog.getByRole("button",{name:"Install reviewed startup files"})).toBeEnabled();
 await dialog.getByLabel("Immutable cloud helper version").fill("0.5.1");
 await expect(dialog.getByRole("button",{name:"Install reviewed startup files"})).toBeDisabled();
 await expect(dialog.getByRole("heading",{name:"Review startup changes"})).toHaveCount(0);
 await dialog.getByRole("button",{name:"Preview startup files"}).click();
 await dialog.getByRole("button",{name:"Install reviewed startup files"}).click();
 await expect(dialog.getByText("Startup files installed. No cloud identity was created or enrolled.")).toBeVisible();
 await expect.poll(()=>applied).toBe(1);
 await expect(dialog.getByRole("button",{name:"Install reviewed startup files"})).toBeDisabled();
 await dialog.getByRole("button",{name:"Close",exact:true}).click();
 await expect(page.getByRole("status").filter({hasText:"Not initialized"})).toBeVisible();
});
