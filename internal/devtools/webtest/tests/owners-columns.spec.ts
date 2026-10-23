import { test, expect, type Page } from "@playwright/test";
import { row, details, action } from "./helpers";

// The owners world: Claude Code is signed in to alice@example.com now; "Find the codeword"
// recorded that another account ran it, "Feature flag" that alice did.
async function owners(page: Page) {
  const r = await page.request.post("/reset?owners=1");
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.locator("#fresh")).toContainText("updated", { timeout: 30_000 });
  await expect(row(page, "Feature flag").locator(".account-chip")).toBeVisible({ timeout: 30_000 });
}

const stateWidth = (page: Page) => page.evaluate(() => parseInt(getComputedStyle(document.documentElement).getPropertyValue("--state-w")));

// Every chip in the status column ends inside it, and a chip narrower than its text has
// an ellipsis span rather than text cut mid-word.
async function noClippedChips(page: Page) {
  const bad = await page.evaluate(() => [...document.querySelectorAll(".row .r-indicators > .chip")].filter((c) => {
    const box = c.getBoundingClientRect(), col = c.closest(".r-state")!.getBoundingClientRect();
    const t = c.querySelector(".chip-t");
    return box.right > col.right + 0.5 || (!t && c.scrollWidth > c.clientWidth + 1);
  }).map((c) => c.textContent));
  expect(bad).toEqual([]);
}

test("a session shows the account that ran it, not its root's sign-in now", async ({ page }) => {
  await owners(page);
  const other = row(page, "Find the codeword").locator(".account-chip");
  await expect(other).toHaveText("another account");
  await expect(other).toHaveAttribute("title", /Run by an account that no profile here is signed in to now.*signed in as alice@example\.com now/);
  await expect(row(page, "Feature flag").locator(".account-chip")).toHaveText("alice@example.com");
  // Not recorded: no email is claimed.
  await expect(row(page, "README cleanup").locator(".account-chip")).toHaveCount(0);
  await row(page, "Find the codeword").click();
  await expect(details(page)).toContainText("another account");

  await page.locator("#btn-display").click();
  await page.locator("#dp-group").selectOption("account");
  await page.keyboard.press("Escape");
  await expect(page.locator(".gname").filter({ hasText: /^alice@example\.com · Claude Code/ })).toHaveCount(1);
  await expect(page.locator(".gname").filter({ hasText: /^another account · Claude Code .* · in Claude Code default$/ })).toHaveCount(1);
  await expect(page.locator(".gname").filter({ hasText: /^alice@example\.com/ })).not.toContainText("another");
});

test("the status column resizes by pointer and keyboard, fits, and is remembered", async ({ page }) => {
  await owners(page);
  const handle = page.getByRole("separator", { name: "Resize the status column" });
  await expect(handle).toHaveAttribute("aria-valuenow", "220");
  await noClippedChips(page);

  const box = (await handle.boundingBox())!;
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(box.x - 80, box.y + box.height / 2, { steps: 5 });
  await page.mouse.up();
  expect(await stateWidth(page)).toBeGreaterThan(280);
  await noClippedChips(page);

  // Escape during a drag puts it back.
  const before = await stateWidth(page);
  const b2 = (await handle.boundingBox())!;
  await page.mouse.move(b2.x + b2.width / 2, b2.y + b2.height / 2);
  await page.mouse.down();
  await page.mouse.move(b2.x + 60, b2.y + b2.height / 2, { steps: 3 });
  await page.keyboard.press("Escape");
  await page.mouse.up();
  expect(await stateWidth(page)).toBe(before);

  await handle.focus();
  await page.keyboard.press("Home");
  await expect(handle).toHaveAttribute("aria-valuenow", "140");
  await noClippedChips(page); // the narrowest column still ends every chip with an ellipsis
  await page.keyboard.press("ArrowLeft");
  await expect(handle).toHaveAttribute("aria-valuenow", "156");
  await page.keyboard.press("Enter");
  await expect(handle).toHaveAttribute("aria-valuenow", "220");

  await handle.dblclick();
  const fit = await stateWidth(page);
  const need = await page.evaluate(() => Math.max(...[...document.querySelectorAll(".row .r-indicators")].map((r) => [...r.children].reduce((w, c) => w + (c as HTMLElement).scrollWidth, 0))));
  expect(fit).toBeGreaterThanOrEqual(need);
  await noClippedChips(page);

  await page.keyboard.press("Home");
  await page.waitForTimeout(500); // saved after a short pause
  await page.reload();
  await expect(page.getByRole("separator", { name: "Resize the status column" })).toHaveAttribute("aria-valuenow", "140", { timeout: 30_000 });
});

test("a narrow list keeps titles readable and hides the column handle", async ({ page }) => {
  await owners(page);
  await page.setViewportSize({ width: 760, height: 820 });
  await expect(page.getByRole("separator", { name: "Resize the status column" })).toBeHidden();
  const title = row(page, "Find the codeword").locator(".t");
  await expect.poll(async () => (await title.boundingBox())!.width).toBeGreaterThan(90);
});

test("a move names the account that ran the session, not its folder's sign-in now", async ({ page }) => {
  await owners(page);
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  const source = page.locator(".transfer-accounts>div").first();
  await expect(source.locator("b")).toHaveText("another account");
  await expect(source).toContainText("signed in as alice@example.com now");
  await expect(page.locator(".transfer-accounts>div").last()).not.toContainText(/^alice@example\.com/);
});

test("a session already in its place continues there under the account signed in now", async ({ page }) => {
  await owners(page);
  // The planner says so for a session already in its destination (TestResumeInOwnRoot…).
  await page.route("**/call", async (route) => {
    const r = route.request().postDataJSON();
    if (r.m !== "Plan") return route.continue();
    const res = await route.fetch(), body = await res.json();
    Object.assign(body.result, { resumeHere: true, noWork: true, blockers: [], warnings: ["Hopsesh has no record of the Claude Code account it ran under; continuing it here uses the account signed in now."] });
    await route.fulfill({ json: body });
  });
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  const sheet = page.locator("#sheet");
  await expect(sheet).toContainText("Nothing is copied. The session continues where it is");
  await expect(sheet.locator("#go")).toContainText("Continue here");
  await expect(sheet.locator("#go")).toBeEnabled();
});
