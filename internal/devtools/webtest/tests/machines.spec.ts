import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("add a machine, receive sessions, and remove the machine", async ({ page }) => {
  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();

  const receive = page.getByRole("switch", { name: "Receive sessions from my other machines" });
  await expect(receive).toHaveAttribute("aria-checked", "false");
  await receive.click();
  await expect(receive).toHaveAttribute("aria-checked", "true");

  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill("build-box");
  await page.getByLabel("SSH destination").fill("me@build-box.invalid");
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
  const added = page.locator(".mgrid", { hasText: "build-box" });
  await expect(added).toBeVisible();
  await expect(added.getByRole("button", { name: "SSH key" })).toBeVisible();

  await added.getByRole("button", { name: "Remove" }).click();
  await page.locator("#dlg").getByRole("button", { name: "Remove" }).click();
  await expect(page.locator(".mgrid", { hasText: "build-box" })).toHaveCount(0);
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
  await menu(page, "machines");
  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill("press-box");
  await page.getByLabel("SSH destination").fill("me@press-box.invalid");
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
  const added = page.locator('.mgrid[data-machine="press-box"]');
  await expect(added).toHaveAttribute("aria-busy", "true");
  const remove = added.getByRole("button", { name: "Remove", exact: true });
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
  await expect(added).toHaveAttribute("aria-busy", "false");
});

test("adding a machine starts one scan, disables Scan, and explains failures with Retry", async ({ page }) => {
  let scans = 0, release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/call", async route => {
    if (route.request().postDataJSON().m !== "ScanMachine") return route.continue();
    scans++;
    await pending;
    return route.fulfill({ json: { error: "Connection timed out: check the machine's address" } });
  });
  await menu(page, "machines");
  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill("scan-box");
  await page.getByLabel("SSH destination").fill("me@scan-box.invalid");
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
  const machine = page.locator('[data-machine="scan-box"]');
  await expect(machine.getByRole("button", { name: "Scanning scan-box" })).toBeDisabled();
  await expect(machine).toHaveAttribute("aria-busy", "true");
  expect(scans).toBe(1);
  release();
  await expect(machine).toContainText("Connection timed out");
  await expect(machine).toHaveAttribute("aria-busy", "false");
  await machine.getByRole("button", { name: "Retry scan scan-box" }).click();
  await expect.poll(() => scans).toBe(2);
  await expect(machine.getByRole("button", { name: "Retry scan scan-box" })).toBeEnabled();
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
});

// What hopsesh can do on each machine, and setting it up on one reached over SSH only.
test("each machine says what hopsesh can do there, and SSH-only ones can be set up", async ({ page }, testInfo) => {
  const row = (name: string, presence: string, hopsesh: string, can: [string, boolean][]) => ({
    name, destination: "me@" + name, os: "darwin", auth: "key", keychain: false, canRemember: true, scanned: true,
    scan: { phase: "done", finished: new Date().toISOString() }, status: "ok", hint: "", error: "", agents: ["Claude Code 2.1.299"], hopsesh, sessions: 4,
    presence, can: can.map(([text, ok]) => ({ text, ok })) });
  let setups = 0;
  await page.route("**/call", async (route) => {
    const m = route.request().postDataJSON().m;
    if (m === "SetUpMachine") { setups++; return route.fulfill({ json: { result: { machine: "lurus", version: "0.5.0", login: true, steps: [] } } }); }
    if (m === "ScanMachine" || m === "MachineScans") return route.fulfill({ json: { result: m === "ScanMachine" ? null : {} } });
    if (m !== "Machines") return route.continue();
    const response = await route.fetch(), body = await response.json();
    body.result.machines = [
      row("studio", "login", "0.5.0", [["Lists sessions, live and moved", true], ["Checks sign-ins there (Keychain included)", true], ["Runs sessions there; you work in them here", true]]),
      row("roys-mbp", "older", "0.4.3", [["Lists sessions", true], ["Runs sessions there", false]]),
      row("lurus", "ssh", "", [["Lists sessions (no live status)", true], ["Sign-ins: the Keychain is locked to SSH", false], ["Originals left there aren't protected", false]]),
    ];
    await route.fulfill({ json: body });
  });
  await menu(page, "machines");
  const studio = page.locator(".mgrid", { hasText: "me@studio" }), lurus = page.locator(".mgrid", { hasText: "me@lurus" }), roys = page.locator(".mgrid", { hasText: "me@roys-mbp" });
  await expect(studio).toContainText("hopsesh 0.5.0 running there");
  await expect(studio.getByRole("list")).toContainText("Checks sign-ins there (Keychain included)");
  await expect(studio.getByRole("button", { name: /Set up hopsesh/ })).toHaveCount(0);
  await expect(roys).toContainText("hopsesh 0.4.3 there · update needed");
  await expect(roys.getByRole("button", { name: "Update hopsesh on roys-mbp" })).toBeVisible();
  await expect(lurus).toContainText("SSH only: no hopsesh there");
  await expect(lurus).toContainText("which is locked to SSH");
  await page.screenshot({ path: testInfo.outputPath("machines-presence.png"), fullPage: true });
  await lurus.getByRole("button", { name: "Set up hopsesh on lurus…" }).click();
  const d = page.locator("#dlg");
  await expect(d).toContainText("~/.hopsesh/bin");
  await expect(d).toContainText("A hopsesh you installed there yourself is left as it is.");
  await expect(d).toContainText("hopsesh serve uninstall");
  expect(setups).toBe(0);
  await d.getByRole("button", { name: "Set up hopsesh on lurus", exact: true }).click();
  await expect(d).toContainText("hopsesh 0.5.0 runs in the login session on lurus.");
  expect(setups).toBe(1);
  await d.getByRole("button", { name: "Done" }).click();
  await expect(d).toBeHidden();
});
