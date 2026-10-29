import { test, expect } from "@playwright/test";
import { row, oldRow, details } from "./helpers";

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
  const codex = oldRow(page, pair.codex.session.title);
  await codex.click();
  await expect(details(page).locator("#act-primary")).toHaveText("Go to the active copy here");
  // Saved information is being checked: a spinner, never a claim either way.
  const banner = details(page).getByLabel("Old copy", { exact: true });
  await expect(banner).toContainText("Checking current protection");
  await expect(banner.locator(".mini-spin")).toHaveCount(1);
  await expect(codex.locator(".chip.mark", { hasText: "Protection unknown" }).locator(".mini-spin")).toHaveCount(1);
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
    const { oldCopyBanner } = await import("/returns.js");
    const e = selected();
    return oldCopyBanner({ ...e, departure: { ...e.departure, status: "prepared", checkedAt: "0001-01-01T00:00:00Z" } }, () => {}).textContent;
  });
  expect(legacyText).toContain("Checking for work in");
  expect(legacyText).not.toContain("no new work yet");
  expect(legacyText).not.toContain("Not blocked:");
  release();
  await expect(page.locator("#fresh")).toContainText("updated", { timeout: 30_000 });
  await codex.click();
  await expect(details(page).getByRole("alert", { name: "Old copy", exact: true })).toContainText("Both copies changed after the move: they have diverged.");
  await expect(codex.locator(".chip.mark.st-error")).toHaveText("Diverged");
  await expect(details(page)).not.toContainText("Checking current protection");
  await expect(details(page).locator("#act-primary")).toHaveText("Go to the active copy here");
});
