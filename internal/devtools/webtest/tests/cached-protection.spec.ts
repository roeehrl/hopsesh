import { test, expect } from "@playwright/test";
import { row, details } from "./helpers";

test("warm saved movement checks protection and work without claiming absent hooks or no work", async ({ page }) => {
  test.setTimeout(90_000);
  expect((await page.request.post("/reset?movement=1&protection=blocked")).ok()).toBeTruthy();
  const seed = await page.request.post("/cached-movement-world?legacy=1");
  expect(seed.ok(), await seed.text()).toBeTruthy();
  const pair = await seed.json();
  let release!: () => void;
  const gate = new Promise<void>(resolve => release = resolve);
  await page.route("**/call", async route => {
    if (["InitialScan", "HookHealth"].includes(route.request().postDataJSON().m)) await gate;
    await route.continue();
  });
  await page.goto("/");
  await expect(row(page, pair.claude.session.title)).toBeVisible();
  const codex = page.locator(".subrow").filter({ has: page.locator(".t").getByText(pair.codex.session.title, { exact: true }) });
  await codex.click();
  await expect(details(page).locator("#act-primary")).toHaveText("Open in Claude Code");
  await expect(details(page).getByLabel("Where it went")).toContainText("Checking current protection");
  await expect(details(page)).not.toContainText("protection isn't set up");
  await expect(details(page)).not.toContainText("Not blocked:");
  const saved = await page.evaluate(async () => {
    const { state, selected } = await import("/core.js");
    const e = selected();
    return { cached: state.scan.cached, checking: e.guard.checking, mode: e.guard.mode, effective: e.guard.effective };
  });
  expect(saved).toEqual({ cached: true, checking: true, mode: "block", effective: false });
  // A legacy receipt fallback has the same native destination identity but no
  // verification time. Render the real component with that valid notice shape;
  // this does not replace the catalog or invent native divergence evidence.
  const legacyText = await page.evaluate(async () => {
    const { selected } = await import("/core.js");
    const { movementNotice } = await import("/returns.js");
    const e = selected();
    return movementNotice({ ...e, departure: { ...e.departure, status: "prepared", checkedAt: "0001-01-01T00:00:00Z" } }, () => {}, () => {}).textContent;
  });
  expect(legacyText).toContain("Checking for work in");
  expect(legacyText).not.toContain("no new work there yet");
  expect(legacyText).not.toContain("Not blocked:");
  release();
  await expect(page.locator("#fresh")).toContainText("updated", { timeout: 30_000 });
  await codex.click();
  await expect(details(page)).toContainText("Both copies have new work");
  await expect(details(page).locator("#act-primary")).toHaveText("Open in Claude Code");
});
