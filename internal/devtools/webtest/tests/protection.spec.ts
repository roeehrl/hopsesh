import { test, expect, type Page } from "@playwright/test";
import { row, oldRow, details, action, roleRow } from "./helpers";

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
  await expect(card).toContainText("Codex hook · installed · waiting for your approval in Codex");
  await expect(card).toContainText("never approves hooks for you");
  await expect(card.getByRole("button", { name: "Set up protection" })).toHaveCount(0);
  // "Open ChatGPT desktop" joins these on a machine where the Codex app is installed.
  const buttons = await card.getByRole("button").allTextContents();
  expect(buttons.filter((b) => !/^Open .* desktop$/.test(b))).toEqual(["Approve in Codex…", "Not now", "Check again"]);
  await card.getByRole("button", { name: "Approve in Codex…" }).click();
  const dialog = page.getByRole("dialog").last();
  await expect(dialog).toContainText("/hooks");
  await expect(dialog).toContainText("never approves hooks for you");
});

test("blocked: a moved original is an old copy, blocked, and says so", async ({ page }) => {
  test.setTimeout(120_000);
  await world(page, "blocked");
  await expect(page.getByRole("status", { name: "Movement protection" })).toHaveCount(0, { timeout: 30_000 });
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex/);
  await page.locator("#sheet").getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: /is prepared for Codex/ })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Back to sessions", exact: true }).click();
  await roleRow(page, "Find the codeword", "Moved from").click();
  // The original is an old copy: a row of its own at the same level, drawn right after
  // its active copy, saying where it went and that it is blocked.
  const original = oldRow(page, "Find the codeword");
  await expect(original.locator(".chip.st-away")).toContainText("Moved to Codex");
  await expect(original.locator(".chip.mark")).toContainText("Blocked", { timeout: 30_000 });
  await expect(original).toHaveAttribute("aria-level", await roleRow(page, "Find the codeword", "Moved from").getAttribute("aria-level") || "");
  await expect(original).toHaveAttribute("aria-label", /^Old copy of “Find the codeword” on .+, moved to Codex · Blocked$/);
  await expect(original).not.toContainText("↳");
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
  const banner = details(page).getByRole("status", { name: "Old copy", exact: true });
  await expect(banner).toContainText("Old copy · moved to Codex", { timeout: 30_000 });
  await expect(banner).toContainText(/Blocked on this (Mac|PC|computer): new prompts in this copy are refused\./);
  // The real backend's answer, not one the window derived: protected by this Mac's hook,
  // read at a known time, nothing left to fix and no check still running.
  const guard = await page.evaluate(async () => { const { selected } = await import("/core.js"); const e = selected(); return { machine: e.machine, departure: e.departure, ...e.guard }; });
  expect(guard).toMatchObject({ state: "protected", effective: true, mode: "block", machine: guard.machine });
  expect(guard.checking).toBeFalsy();
  expect(guard.action ?? "").toBe("");
  expect(Date.parse(guard.checkedAt)).toBeGreaterThan(0);
  expect(Date.parse(guard.departure.at)).toBeGreaterThan(0);
  expect(guard.departure.sourceChanged).toBeFalsy();
  await expect(details(page).locator(".ins-lineage")).toHaveCount(0);
  await expect(details(page).getByLabel("Where it went", { exact: true })).toHaveCount(0);
  await expect(details(page).locator(".ins-status")).toContainText(/^Moved away · last used on this (Mac|PC|computer)/);
  await expect(details(page).locator("#act-primary")).toHaveText("Go to the active copy here");
  // The active copy names its old copy's state only when it needs attention: here it is blocked.
  await expect(roleRow(page, "Find the codeword", "Moved from").locator(".oldcopy-chip")).toHaveCount(0);
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
  // Every detected agent is listed with what its hook covers.
  await expect(card).toContainText("Claude Code hook · not set up");
  await expect(card).toContainText("Codex hook · not set up");
  await expect(card).toContainText("for Terminal");
  await card.getByRole("button", { name: "Not now" }).click();
  await expect(card).toHaveCount(0);
  // "Not now" is remembered (until the problems change), not only for this page.
  await page.reload();
  await expect(page.locator("#fresh")).toContainText("updated", { timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  await expect.poll(async () => page.evaluate(async () => (await import("/guard.js")).hookHealth()?.dismissed), { timeout: 30_000 }).toBe(true);
  await expect(card).toHaveCount(0);
});

// The card's states, from a scripted backend: a failed step says why and offers Try
// again, an agent that cannot take the hook says why, results stay until Done, Check
// again never hides the card, and a Codex hook waiting for approval says where to approve.
test("setup results stay on the card until Done", async ({ page }) => {
  const claude = { agent: "claude", profile: "", supported: true, enabled: true, installed: false, cli: true, app: "Claude", version: "2.1.284" };
  const oldCodex = { agent: "codex", profile: "", supported: false, enabled: true, installed: false, cli: true, version: "0.120.1", reason: "needs Codex 0.129.0 or newer; found 0.120.1" };
  const appCodex = { agent: "codex", profile: "", supported: true, enabled: true, installed: true, runtime: "app", app: "ChatGPT", version: "0.162.0-alpha.17.2", trust: { state: "needs-review" } };
  let health: any = { mode: "block", cliReady: true, cli: { state: "ours" }, hooks: [claude, oldCodex], problems: [{ agent: "claude", state: "not-installed" }, { agent: "codex", state: "unsupported" }], fingerprint: "fp1", dismissed: false };
  let install: any[] = [{ ...claude, result: "failed", reason: "hook settings changed during update; retry" }, { ...oldCodex, result: "unsupported" }];
  const calls: string[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m === "HookHealth") return route.fulfill({ json: { result: health } });
    if (req.m === "InstallNoticeHooks") { calls.push("install"); return route.fulfill({ json: { result: install } }); }
    if (req.m === "OpenAgentApp") { calls.push("open:" + req.args[0]); return route.fulfill({ json: { result: "ChatGPT" } }); }
    if (req.m === "DismissProtection") { calls.push("dismiss"); return route.fulfill({ json: { result: null } }); }
    return route.continue();
  });
  const r = await page.request.post("/reset?movement=1");
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("Protect moved sessions", { timeout: 30_000 });
  await expect(card).toContainText("Claude Code hook · not set up");
  await expect(card).toContainText("for Terminal and Claude desktop");
  await expect(card).toContainText("Codex hook · needs Codex 0.129.0 or newer; found 0.120.1");

  await card.getByRole("button", { name: "Set up protection" }).click();
  await expect(card).toContainText("Protection isn't fully set up");
  await expect(card).toContainText("Claude Code hook · couldn't set up: hook settings changed during update; retry");
  await expect(card).toContainText("Codex hook · needs Codex 0.129.0 or newer; found 0.120.1");
  await expect(card.getByRole("button", { name: "Not now" })).toHaveCount(0);
  await expect(card.getByRole("button", { name: "Done" })).toBeVisible();

  install = [{ ...claude, installed: true, result: "installed" }, { ...appCodex, result: "needs-approval" }];
  health = { ...health, hooks: [{ ...claude, installed: true }, appCodex], problems: [{ agent: "codex", state: "needs-review" }], fingerprint: "fp2" };
  await card.getByRole("button", { name: "Try again" }).click();
  await expect(card).toContainText("Approve hopsesh in Codex to finish");
  await expect(card).toContainText("Claude Code hook · installed");
  await expect(card).toContainText("Codex hook · installed · waiting for your approval in Codex");
  await expect(card).toContainText("for ChatGPT desktop (built-in Codex 0.162.0-alpha.17.2)");
  await expect(card).toContainText("In ChatGPT desktop, open Codex and choose Review hooks (or run /hooks in the terminal), then trust the two hopsesh hooks. hopsesh never approves hooks for you.");
  await card.getByRole("button", { name: "Open ChatGPT desktop" }).click();
  await expect.poll(() => calls).toEqual(["install", "install", "open:codex"]);

  health = { ...health, hooks: [{ ...claude, installed: true }, { ...appCodex, trust: { state: "trusted" } }], problems: [], fingerprint: "" };
  await card.getByRole("button", { name: "Check again" }).click();
  await expect(card).toContainText("Protection is set up");
  await expect(card).toContainText("Codex hook · installed");
  await card.getByRole("button", { name: "Done" }).click();
  await expect(card).toHaveCount(0);
  expect(calls).not.toContain("dismiss");
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
  expect(result.primary).toBe('Go to the active copy here');
  expect(result.count).toBe(4);expect(result.unique).toBe(4);
  expect(result.sameMain).toBe(true);expect(result.sameChild).toBe(true);
  expect(result.heightAfter).toBe(result.heightBefore);
  expect(result.accepted).toBe(false);
});
