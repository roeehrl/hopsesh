import { test, expect, type Page } from "@playwright/test";
import { fresh, menu, row, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// What hopsesh can do on each machine (MachineStatuses): this Mac on 0.6.1 receiving,
// roee-macbook on 0.5.0 not in a login session, nas without hopsesh.
const ok = (text: string) => ({ state: "ok", text });
const off = (text: string, fix = "", reason = "") => ({ state: "off", text, fix, reason });
const setup = (agent: string, name: string, hook: any, skill: any, profileLabel = "") => ({ agent, name, profileLabel, hook, skill });
const statuses = () => [
  { machine: "here", local: true, version: "0.6.1", newest: "0.6.1", newestOn: "here", reach: ok("this machine"), hopsesh: ok("0.6.1"), login: ok("in your login session"),
    receiving: ok("on, listening"), cli: ok("on PATH · ~/.local/bin/hopsesh"), todo: 1, total: 7, checkedAt: new Date().toISOString(), detailsAt: new Date().toISOString(),
    agents: [setup("claude", "Claude Code", ok("installed"), ok("installed")), setup("claude", "Claude Code", ok("installed"), { state: "warn", text: "outdated", fix: "skill", reason: "written by hopsesh 0.6.0" }, "work"),
      setup("codex", "Codex", ok("installed · trusted"), ok("installed"))] },
  { machine: "roee-macbook", local: false, version: "0.5.0", newest: "0.6.1", newestOn: "here", reach: ok("reached over SSH"),
    hopsesh: { state: "warn", text: "0.5.0", reason: "0.6.1 is the newest, on here", fix: "update" }, login: off("not running", "", "nobody is logged in there"),
    receiving: off("not listening", "", "until hopsesh runs in a login session there"), cli: ok("on PATH · ~/.local/bin/hopsesh"), todo: 2, total: 7,
    checkedAt: new Date().toISOString(), detailsAt: new Date().toISOString(),
    agents: [setup("claude", "Claude Code", ok("installed"), off("not installed", "skill")), setup("codex", "Codex", ok("installed · trust checked at login"), ok("installed"))] },
  { machine: "nas", local: false, version: "", newest: "0.6.1", reach: ok("reached over SSH"), hopsesh: off("not installed", "setup", "sessions can still be brought from it over SSH"),
    login: off("–"), receiving: off("can't receive"), cli: off("–"), agents: [], todo: 1, total: 3, checkedAt: new Date().toISOString() },
];
const machines = (here: string) => ({ here: { name: here, agents: ["Claude Code", "Codex"], hopsesh: "0.6.1", receive: true, storage: [] },
  machines: [
    { name: "roee-macbook", destination: "roee-macbook", os: "darwin", auth: "key", scanned: true, scan: {}, status: "ok", hint: "", error: "", agents: ["Claude Code"], hopsesh: "0.5.0", sessions: 4, storage: [], presence: "installed", can: [] },
    { name: "nas", destination: "alice@192.168.1.20", os: "linux", auth: "password", keychain: true, scanned: true, scan: {}, status: "ok", hint: "", error: "", agents: [], hopsesh: "", sessions: 0, storage: [], presence: "ssh", can: [] },
  ], found: [{ name: "tnt-pc", destination: "tnt-pc", via: ["ssh-config"], os: "", online: null, owner: "" }], clouds: [] });

async function fixtures(page: Page) {
  const calls: { m: string; a: any[] }[] = [];
  let here = "here";
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON(), a = req.a || req.args || [];
    if (req.m === "MachineStatuses") { calls.push({ m: req.m, a }); const ss = statuses(); ss[0].machine = here; return route.fulfill({ json: { result: ss } }); }
    if (req.m === "Machines") { const res = await route.fetch(), body = await res.json(); here = body.result.here.name; return route.fulfill({ json: { result: machines(here) } }); }
    if (req.m === "SetUpProtection") { calls.push({ m: req.m, a }); return route.fulfill({ json: { result: { machine: a[0], hooks: [], skill: { state: "current" }, command: true } } }); }
    return route.continue();
  });
  return calls;
}

// The sidebar shows hopsesh's mark beside each machine: green and filled when it receives
// sessions, gray and hollow when it is installed but not, none without hopsesh, and an ↑
// when a newer hopsesh is on another machine. The words are the mark's name.
test("the sidebar marks the machines hopsesh runs on", async ({ page }) => {
  await page.route("**/call", async (route) => {
    const m = route.request().postDataJSON()?.m;
    if (!["InitialScan", "Scan", "RefreshHere", "CachedScan", "ScanSnapshot"].includes(m)) return route.continue();
    try {
      const res = await route.fetch(), body = await res.json(), scan = body.result?.scan || body.result;
      if (scan?.machines) {
        for (const x of scan.machines) if (x.local) Object.assign(x, { hopsesh: "0.6.1", receiving: true });
        scan.machines.push({ name: "roee-macbook", status: "ok", sessions: 0, local: false, agents: [], agentNames: ["Claude Code"], hopsesh: "0.5.0", receiving: false, newest: "0.6.1", presence: "installed", os: "darwin" },
          { name: "nas", status: "ok", sessions: 0, local: false, agents: [], agentNames: [], hopsesh: "", receiving: false, presence: "ssh", os: "linux" });
      }
      await route.fulfill({ json: body });
    } catch { /* cancelled */ }
  });
  await page.reload();
  const side = page.getByRole("navigation", { name: "Places" });
  await expect(side.getByRole("img", { name: "hopsesh 0.6.1, receiving sessions" })).toBeVisible({ timeout: 30_000 });
  const mb = side.getByRole("button", { name: /roee-macbook/ });
  await expect(mb.getByRole("img", { name: "hopsesh 0.5.0, not receiving sessions · 0.6.1 is the newest" })).toBeVisible();
  await expect(mb.locator(".hop-mark.off")).toBeVisible();
  await expect(mb.locator(".hop-behind")).toHaveText("↑");
  await expect(side.getByRole("button", { name: /^nas/ }).locator(".hop-mark")).toHaveCount(0);
});

// Settings › Machines: the list beside the selected machine; Up next lists only what is
// left, each with its fix; a machine behind the newest is offered the update.
test("Settings › Machines shows one machine, everything about it", async ({ page }, testInfo) => {
  await fixtures(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  const list = page.getByRole("listbox", { name: "Machines" });
  // This machine is first, named for its system (This Mac, This PC, This computer).
  await expect(list.getByRole("option").first()).toContainText("1 to do");
  await expect(list.getByRole("option", { name: /roee-macbook/ })).toContainText("2 to do");
  await expect(list.getByRole("option", { name: /nas/ })).toContainText("SSH only");
  await expect(page.getByRole("status").filter({ hasText: "older hopsesh" })).toContainText("roee-macbook (0.5.0)");
  await list.getByRole("option", { name: /roee-macbook/ }).click();
  await expect(list.getByRole("option", { name: /roee-macbook/ })).toHaveAttribute("aria-selected", "true");
  const detail = page.locator(".mdetail");
  await expect(detail.getByRole("heading", { name: "roee-macbook" })).toBeVisible();
  await expect(detail.locator(".mnext")).toContainText("Up next · 2");
  // Every agent folder's hook and skill are one line, set up in one step.
  await expect(detail.locator(".mnext")).toContainText("Hooks and skill");
  await expect(detail.locator(".mnext")).toContainText("1 of 2 agent folders need the skill");
  await expect(detail).toContainText("nobody is logged in there");
  await page.screenshot({ path: testInfo.outputPath("settings-machines.png"), fullPage: true });
  await detail.locator(".mnext").getByRole("button", { name: "Update to 0.6.1" }).click();
  await expect(page.locator("#dlg").getByRole("heading", { name: "Update hopsesh on roee-macbook" })).toBeVisible();
  await page.locator("#dlg").getByRole("button", { name: "Not now" }).click();
  await list.getByRole("option", { name: /nas/ }).click();
  await expect(detail.locator(".mnext")).toContainText("not installed");
  await expect(detail.getByRole("button", { name: "Set up hopsesh…" })).toBeVisible();
  // The old Machines screen opens this tab.
  await menu(page, "machines");
  await expect(page.getByRole("tab", { name: "Machines", exact: true })).toHaveAttribute("aria-selected", "true");
});

// This machine's page holds its command, skill and protection hooks (there is no separate
// Hooks & skills, Skill or Command line tab), and its agent folders' hooks and skill are
// set up from Up next in one step.
test("This Mac's page sets up its hooks and skill, and holds its command and skill", async ({ page }, testInfo) => {
  const calls = await fixtures(page);
  await menu(page, "settings");
  const tabs = page.getByRole("tablist", { name: "Settings" });
  for (const gone of ["Hooks & skills", "Skill", "Command line"]) await expect(tabs.getByRole("tab", { name: gone, exact: true })).toHaveCount(0);
  await tabs.getByRole("tab", { name: "Machines", exact: true }).click();
  const detail = page.locator(".mdetail");
  await expect(detail.locator(".mnext")).toContainText("1 of 3 agent folders need the skill");
  await expect(detail.getByRole("heading", { name: "Command, skill and hooks" })).toBeVisible();
  await expect(detail).toContainText("The hopsesh command");
  await expect(detail).toContainText("The hopsesh skill");
  await expect(detail).toContainText("Protection hooks");
  await page.screenshot({ path: testInfo.outputPath("settings-this-mac.png"), fullPage: true });
  await detail.locator(".mnext").getByRole("button", { name: "Set up" }).click();
  await expect.poll(() => calls.filter((c) => c.m === "SetUpProtection").length).toBe(1);
  expect(calls.find((c) => c.m === "SetUpProtection")!.a[0]).toBe("");
});

// The plan sheet keeps "Block the original" in view but off when the machine the session
// leaves can't block it: focusable, with the reasons and the fixes beneath, and without
// repeating the planner's warning.
test("the plan keeps Block the original off, with why, when the source can't block", async ({ page }, testInfo) => {
  await page.route("**/call", async (route) => {
    const r = route.request().postDataJSON();
    if (r.m !== "Plan") return route.continue();
    const res = await route.fetch(), body = await res.json();
    Object.assign(body.result, { sourceHost: "roee-macbook", cannotProtect: ["hopsesh there is 0.5.0; blocking needs 0.6.0 or newer"],
      warnings: [...(body.result.warnings || []), "the original on roee-macbook can't be blocked or warned: hopsesh there is 0.5.0; blocking needs 0.6.0 or newer; nothing stops it from being continued there too"] });
    await route.fulfill({ json: body });
  });
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  const sheet = page.locator("#sheet");
  const box = sheet.getByRole("checkbox", { name: /Block the original until you move back|Warn in the original/ });
  await expect(box).toHaveAttribute("aria-disabled", "true");
  await expect(box).not.toBeChecked();
  await box.focus();
  await expect(box).toBeFocused();
  await box.press("Space");
  await expect(box).not.toBeChecked();
  await expect(sheet.locator(".guard-why")).toContainText("blocking needs 0.6.0 or newer");
  await expect(sheet.locator(".guard-why").getByRole("button", { name: "Update roee-macbook…" })).toBeVisible();
  await expect(sheet).not.toContainText("can't be blocked or warned: hopsesh there");
  await sheet.locator(".guard-why").scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath("plan-guard.png") });
});
