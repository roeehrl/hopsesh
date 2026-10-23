import { test, expect, type Page } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// Settings › Machines: the list beside the selected machine (machine-status.spec.ts has
// its checks). Opening the old Machines screen opens this tab.
async function machinesTab(page: Page) {
  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();
  return page.getByRole("listbox", { name: "Machines" });
}
const detail = (page: Page) => page.locator(".mdetail");

async function add(page: Page, name: string) {
  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill(name);
  await page.getByLabel("SSH destination").fill(`me@${name}.invalid`);
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
}

test("add a machine, receive sessions, and remove the machine", async ({ page }) => {
  const list = await machinesTab(page);
  // This machine is selected first: receiving is switched on its card.
  const receive = detail(page).getByRole("switch", { name: "Receive sessions from my other machines" });
  await expect(receive).toHaveAttribute("aria-checked", "false");
  await receive.click();
  await expect(receive).toHaveAttribute("aria-checked", "true");

  await add(page, "build-box");
  const added = list.getByRole("option", { name: /build-box/ });
  await expect(added).toBeVisible();
  await added.click();
  await expect(detail(page).getByRole("heading", { name: "build-box" })).toBeVisible();
  await expect(detail(page)).toContainText("me@build-box.invalid");

  await detail(page).getByRole("button", { name: "Remove" }).click();
  await page.locator("#dlg").getByRole("button", { name: "Remove" }).click();
  await expect(list.getByRole("option", { name: /build-box/ })).toHaveCount(0);
});

test("a completed machine scan cannot replace a pressed Remove button", async ({ page }) => {
  let release!: () => void, completed = false, reloads = 0;
  const pending = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/call", async route => {
    const method = route.request().postDataJSON().m;
    if (method === "ScanMachine") {
      await pending;
      completed = true;
      return route.fulfill({ json: { error: "Fixture connection unavailable" } });
    }
    if (method === "Machines" && completed) {
      const response = await route.fetch();
      await route.fulfill({ response });
      reloads++;
      return;
    }
    return route.continue();
  });
  const list = await machinesTab(page);
  await add(page, "press-box");
  await list.getByRole("option", { name: /press-box/ }).click();
  await expect(detail(page).getByRole("button", { name: "Checking…" })).toBeDisabled();
  const remove = detail(page).getByRole("button", { name: "Remove", exact: true });
  await remove.hover();
  await page.mouse.down();
  await remove.evaluate(button => { (window as any).__pressedRemove = button; });
  release();
  await expect.poll(() => reloads).toBeGreaterThan(0);
  await page.waitForTimeout(250); // allow the scheduled render to try while held
  expect(await remove.evaluate(button => button === (window as any).__pressedRemove)).toBeTruthy();
  await page.mouse.up();
  await expect(page.locator("#dlg").getByRole("heading", { name: "Remove press-box?" })).toBeVisible();
  await page.locator("#dlg").getByRole("button", { name: "Cancel" }).click();
});

test("adding a machine starts one scan, shows it checking, and explains failures with Retry", async ({ page }) => {
  let scans = 0, release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/call", async (route) => {
    if (route.request().postDataJSON().m !== "ScanMachine") return route.continue();
    scans++;
    await pending;
    return route.fulfill({ json: { error: "Connection timed out: check the machine's address" } });
  });
  const list = await machinesTab(page);
  await add(page, "scan-box");
  const option = list.getByRole("option", { name: /scan-box/ });
  await expect(option).toContainText("checking…");
  await option.click();
  await expect(detail(page).getByRole("button", { name: "Checking…" })).toBeDisabled();
  // "Checking…" also shows while statuses load: wait for the one scan itself.
  await expect.poll(() => scans).toBe(1);
  release();
  await expect(detail(page)).toContainText("Connection timed out");
  await detail(page).getByRole("button", { name: "Retry" }).click();
  await expect.poll(() => scans).toBe(2);
  await expect(detail(page).getByRole("button", { name: "Retry" })).toBeEnabled();
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
});

// A machine reached over SSH only is set up from its Up next, after the dialog says what
// setting it up does; nothing happens before that.
test("an SSH-only machine says what hopsesh would do there, and is set up on request", async ({ page }) => {
  const ok = (text: string) => ({ state: "ok", text });
  let setups = 0;
  await page.route("**/call", async (route) => {
    const m = route.request().postDataJSON().m;
    if (m === "SetUpMachine") { setups++; return route.fulfill({ json: { result: { machine: "lurus", version: "0.6.1", login: true, steps: [] } } }); }
    if (m === "ScanMachine" || m === "MachineScans") return route.fulfill({ json: { result: m === "ScanMachine" ? null : {} } });
    if (m === "MachineStatuses") return route.fulfill({ json: { result: [
      { machine: "lurus", version: "", reach: ok("reached over SSH"), hopsesh: { state: "off", text: "not installed", fix: "setup", reason: "sessions can still be brought from it over SSH" },
        login: { state: "off", text: "–" }, receiving: { state: "off", text: "can't receive", reason: "hopsesh isn't installed there" }, cli: { state: "off", text: "–" }, agents: [], todo: 1, total: 3, checkedAt: new Date().toISOString() }] } });
    if (m !== "Machines") return route.continue();
    const response = await route.fetch(), body = await response.json();
    body.result.machines = [{ name: "lurus", destination: "me@lurus", os: "darwin", auth: "key", keychain: false, canRemember: true, scanned: true,
      scan: { phase: "done", finished: new Date().toISOString() }, status: "ok", hint: "", error: "", agents: ["Claude Code 2.1.299"], hopsesh: "", sessions: 4, presence: "ssh",
      can: [{ text: "Lists sessions (no live status)", ok: true }, { text: "Sign-ins: the Keychain is locked to SSH", ok: false }, { text: "Originals left there aren't protected", ok: false }] }];
    await route.fulfill({ json: body });
  });
  const list = await machinesTab(page);
  await expect(list.getByRole("option", { name: /lurus/ })).toContainText("SSH only");
  await list.getByRole("option", { name: /lurus/ }).click();
  await expect(detail(page).locator(".mnext")).toContainText("not installed");
  await expect(detail(page)).toContainText("which is locked to SSH");
  await detail(page).locator(".mnext").getByRole("button", { name: "Set up hopsesh…" }).click();
  const d = page.locator("#dlg");
  await expect(d).toContainText("~/.hopsesh/bin");
  await expect(d).toContainText("A hopsesh you installed there yourself is left as it is.");
  await expect(d).toContainText("hopsesh serve uninstall");
  await expect(d).toContainText("Asks before installing hooks or the skill there");
  expect(setups).toBe(0);
  await d.getByRole("button", { name: "Set up hopsesh on lurus", exact: true }).click();
  await expect(d).toContainText("hopsesh 0.6.1 runs in the login session on lurus.");
  expect(setups).toBe(1);
  await d.getByRole("button", { name: "Done" }).click();
  await expect(d).toBeHidden();
});
