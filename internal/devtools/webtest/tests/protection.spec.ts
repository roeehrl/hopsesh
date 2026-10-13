import { test, expect, type Page } from "@playwright/test";
import { row, details, action, roleRow } from "./helpers";

// The capture worlds for movement protection: /reset?protection=review (Codex has not
// trusted hopsesh's hooks) and /reset?protection=blocked (hooks installed and trusted).
async function world(page: Page, mode: string) {
  const r = await page.request.post(`/reset?movement=1&protection=${mode}`);
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#fresh")).toContainText("updated", { timeout: 30_000 });
}

test("review: the Sessions warning asks to approve the hopsesh hooks in Codex", async ({ page }) => {
  await world(page, "review");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("Approve hopsesh in Codex to finish", { timeout: 30_000 });
  await expect(card).toContainText("waiting for your approval in Codex");
  await expect(card.getByRole("button", { name: "Set up protection" })).toHaveCount(0);
  await expect(card.getByRole("button")).toHaveText(["Approve in Codex…", "Not now", "Check again"]);
  await card.getByRole("button", { name: "Approve in Codex…" }).click();
  const dialog = page.getByRole("dialog").last();
  await expect(dialog).toContainText("/hooks");
  await expect(dialog).toContainText("never approves hooks for you");
});

test("blocked: a moved original is blocked and says so", async ({ page }) => {
  test.setTimeout(120_000);
  await world(page, "blocked");
  await expect(page.getByRole("status", { name: "Movement protection" })).toHaveCount(0, { timeout: 30_000 });
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex/);
  await page.locator("#sheet").getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: /is prepared for Codex/ })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Back to sessions", exact: true }).click();
  await roleRow(page, "Find the codeword", "Moved from").click();
  const original = page.locator(".subrow").filter({hasText:"Claude Code original"});
  await expect(original).toContainText("Moved to Codex");
  await page.evaluate(async () => {
    const {list} = await import('/listview.js');
    const {render} = await import('/sessions.js');
    list.density = "compact";
    render();
  });
  const movedRow = roleRow(page, "Find the codeword", "Moved from");
  await expect(movedRow.locator(".r-main .role-chip, .r-main .pchip")).toHaveCount(0);
  const titleColumn = await movedRow.locator(".r-main").boundingBox();
  const metadataColumn = await movedRow.locator(".r-status").boundingBox();
  expect(metadataColumn!.x).toBeGreaterThanOrEqual(titleColumn!.x + titleColumn!.width);
  const movedBox = await movedRow.boundingBox();
  const originalBox = await original.boundingBox();
  expect(originalBox!.y).toBeGreaterThanOrEqual(movedBox!.y + movedBox!.height);
  await roleRow(page, "Find the codeword", "Moved from").press("ArrowDown");
  await expect(original).toBeFocused();
  await expect(details(page).locator(".ins-lineage")).toContainText("◆", { timeout: 30_000 });
  await expect(details(page).locator(".ins-lineage")).toContainText("Blocked until you move it back");
  await expect(details(page).getByLabel("Where it went", { exact: true })).toContainText("Blocked until you move the session back");
});

test("setup: one card, one action, agents named without account labels", async ({ page }) => {
  const r = await page.request.post("/reset?movement=1");
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("Protect moved sessions", { timeout: 30_000 });
  await expect(card.getByRole("button", { name: "Set up protection" })).toHaveCount(1);
  await expect(card.getByRole("button", { name: /Install/ })).toHaveCount(0);
  await expect(card).not.toContainText("default ·");
  await card.getByRole("button", { name: "Not now" }).click();
  await expect(card).toHaveCount(0);
});

test("original selection preserves conversation parents, row identity and height through refresh", async ({ page }) => {
  await world(page, "blocked");
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex/);
  await page.locator("#sheet").getByRole("button", {name:/Continue in Codex/}).click();
  await page.getByRole("button", {name:"Back to sessions",exact:true}).click();
  await expect(roleRow(page,"Find the codeword","Moved from")).toBeVisible();
  const result = await page.evaluate(async () => {
    const {state, selected} = await import('/core.js');
    const {render, showEntry, acceptScan} = await import('/sessions.js');
    const {list} = await import('/listview.js');
    const source = state.scan.groups.flatMap(g=>g.entries).find(e=>e.agent==='codex' && e.title==='Find the codeword');
    const original = state.scan.profileCopies.flatMap(g=>g.entries).find(e=>e.agent==='claude' && e.title==='Find the codeword');
    const make = (n:string) => {
      const main = {...source,key:`codex/${n}`,title:`Conversation ${n}`,copies:[]};
      const child = {...original,key:`claude/${n}`,title:`Conversation ${n}`,copies:[]};
      main.copies = [{machine:main.machine,key:main.key,agent:main.agent}, {machine:child.machine,key:child.key,agent:child.agent,leftBehind:true}];
      child.copies = main.copies;
      return {main,child};
    };
    const a=make('A'),b=make('B');
    const group={...state.scan.groups[0],entries:[a.main,b.main]};
    const snapshot={...state.scan,groups:[group],profileCopies:[{...group,entries:[a.child,b.child]}],discovering:false,cached:false,revision:(state.scan.revision||0)+1};
    acceptScan(snapshot);list.density='compact';list.text='';state.sel=null;
    render();showEntry(a.child);
    const rows=[...document.querySelectorAll('.tree .row')];
    const main=rows.find(el=>el.getAttribute('data-key')===a.main.machine+'\0'+a.main.key);
    const child=rows.find(el=>el.getAttribute('data-key')===a.child.machine+'\0'+a.child.key);
    const height=main.getBoundingClientRect().height;
    a.main.arrival=null;a.main.app='';a.main.live=false;a.main.places=[];
    render();
    const now=[...document.querySelectorAll('.tree .row')];
    const partial={...snapshot,revision:snapshot.revision+1,discovering:true,groups:[]};
    const accepted=acceptScan(partial);
    render();
    return {parents:group.entries.map(e=>e.title),selected:selected()?.key,count:now.length,unique:new Set(now.map(e=>e.getAttribute('data-key'))).size,
      sameMain:main.isConnected,sameChild:child.isConnected,heightBefore:height,heightAfter:main.getBoundingClientRect().height,accepted,
      heading:document.querySelector('#inspector h2')?.textContent,primary:document.querySelector('#act-primary')?.textContent};
  });
  expect(result.parents).toEqual(['Conversation A','Conversation B']);
  expect(result.selected).toBe('claude/A');
  expect(result.heading).toBe('Conversation A');
  expect(result.primary).toBe('Open in Codex');
  expect(result.count).toBe(4);expect(result.unique).toBe(4);
  expect(result.sameMain).toBe(true);expect(result.sameChild).toBe(true);
  expect(result.heightAfter).toBe(result.heightBefore);
  expect(result.accepted).toBe(false);
});
