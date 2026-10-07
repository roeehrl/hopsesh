import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu, filter } from "./helpers";

// The session list's display: the Filter menu and its chips, the text filter, Display
// (group by, sort, rows, collapse inactive), collapsing groups (⌥-click: all) and what
// is kept across a reload.

test.beforeEach(async ({ page }) => fresh(page));

const count = (page: Page) => page.locator("#list-count");
const names = (page: Page) => page.locator(".gname").allTextContents();
const display = async (page: Page) => { await page.locator("#btn-display").click(); return page.getByRole("dialog", { name: "Display options" }); };

test("filters: status first, faceted counts, chips with is / is not, Clear, and kept across a reload", async ({ page }) => {
  await page.locator("#btn-filter").click();
  const menuEl = page.getByRole("menu", { name: "Filter" });
  await expect(menuEl.getByRole("menuitemcheckbox", { name: /^Moved/ })).toContainText("1");
  await expect(menuEl.getByRole("menuitemcheckbox", { name: /^Ended/ })).toContainText("3");
  await menuEl.getByRole("menuitemcheckbox", { name: /^Ended/ }).click();
  await expect(menuEl.getByRole("menuitemcheckbox", { name: /^Ended/ })).toHaveAttribute("aria-checked", "true");
  await expect(count(page)).toHaveText("3 of 4");
  // The other facets count with Status applied: the agents' sessions that ended.
  await menuEl.getByRole("menuitem", { name: /^Agent/ }).click();
  const agents = page.getByRole("menu", { name: "Agent" });
  await expect(agents.getByRole("menuitemcheckbox", { name: /^Codex/ })).toContainText("2");
  await expect(agents.getByRole("menuitemcheckbox", { name: /^Claude Code/ })).toContainText("1");
  await agents.getByRole("menuitemcheckbox", { name: /^Codex/ }).click();
  await expect(count(page)).toHaveText("2 of 4");
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect(menuEl).toBeHidden();
  await expect(page.locator("#filter-n")).toHaveText("2");

  // Chips: the facet word switches is / is not, the values reopen the facet, ✕ removes it.
  const chips = page.locator("#list-chips");
  await expect(chips.getByRole("button", { name: "Status is Ended, edit" })).toBeVisible();
  // Editing a chip opens its facet directly, without the parent Filter menu.
  // Toggling a value must update that menu's checks as well as the list.
  const errors: string[] = [];
  page.on("pageerror", (err) => errors.push(err.message));
  await chips.getByRole("button", { name: "Status is Ended, edit" }).click();
  const status = page.getByRole("menu", { name: "Status", exact: true });
  await status.getByRole("menuitemcheckbox", { name: /^Moved/ }).click();
  await expect(status.getByRole("menuitemcheckbox", { name: /^Moved/ })).toHaveAttribute("aria-checked", "true");
  await status.getByRole("menuitemcheckbox", { name: /^Moved/ }).click();
  await expect(status.getByRole("menuitemcheckbox", { name: /^Moved/ })).toHaveAttribute("aria-checked", "false");
  await page.keyboard.press("Escape");
  expect(errors).toEqual([]);
  await chips.getByRole("button", { name: /^Status: is, switch/ }).click();
  await expect(chips.getByRole("button", { name: "Status is not Ended, edit" })).toBeVisible();
  await expect(count(page)).toHaveText("0 of 4");
  await expect(page.getByText(/No sessions match · 4 sessions hidden by filters/)).toBeVisible();
  await chips.getByRole("button", { name: "Remove Agent filter" }).click();
  await expect(count(page)).toHaveText("1 of 4");
  await expect(row(page, "Feature flag")).toBeVisible();

  // Kept across a reload; the text filter is not.
  await page.locator("#list-filter").fill("feature");
  await expect(count(page)).toHaveText("1 of 4");
  await page.waitForTimeout(600);
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  await expect(chips.getByRole("button", { name: "Status is not Ended, edit" })).toBeVisible();
  await expect(page.locator("#list-filter")).toHaveValue("");
  await chips.getByRole("button", { name: "Clear" }).click();
  await expect(count(page)).toHaveText("4 sessions");
  await expect(chips).toBeHidden();
});

test("the text filter narrows as you type; Escape clears it, then goes to the list", async ({ page }) => {
  const mac = await page.evaluate(() => document.documentElement.dataset.os === "darwin");
  await page.keyboard.press(mac ? "Meta+f" : "Control+f");
  const f = page.locator("#list-filter");
  await expect(f).toBeFocused();
  await page.keyboard.type("codeword");
  await expect(count(page)).toHaveText("2 of 4");
  await expect(row(page, "README cleanup")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(f).toHaveValue("");
  await expect(count(page)).toHaveText("4 sessions");
  await page.keyboard.press("Escape");
  await expect(page.locator(".tree [role=treeitem]").first()).toBeFocused();
  // ⇧⌘F opens the Filter menu.
  await page.keyboard.press(mac ? "Shift+Meta+f" : "Control+Shift+f");
  await expect(page.getByRole("menu", { name: "Filter" })).toBeVisible();
  await page.keyboard.press("Escape");
});

test("a location with nothing in it, and the empty state's Clear filters", async ({ page }) => {
  await filter(page, /^Clouds/, "Location");
  await expect(count(page)).toHaveText("0 of 4");
  const empty = page.locator(".list .empty");
  await expect(empty).toContainText("No sessions match · 4 sessions hidden by filters");
  await empty.getByRole("button", { name: "Clear filters" }).click();
  await expect(count(page)).toHaveText("4 sessions");
});

test("Display: group by each property, sort, rows, and reset", async ({ page }) => {
  const d = await display(page);
  await expect(d.getByLabel("Group by")).toHaveValue("repository");
  expect(await names(page)).toEqual(["demo", "Outside a git checkout"]);
  await d.getByLabel("Group by").selectOption("location");
  await expect.poll(() => names(page)).toEqual([expect.stringMatching(/^This (Mac|PC|computer)$/)]);
  await d.getByLabel("Group by").selectOption("agent");
  await expect.poll(async () => (await names(page)).sort()).toEqual(["Claude Code", "Codex"]);
  await d.getByLabel("Group by").selectOption("status");
  await expect.poll(() => names(page)).toEqual(["Moved", "Ended"]);
  await d.getByLabel("Group by").selectOption("last-active");
  await expect.poll(async () => (await names(page)).every((n) => ["Today", "Yesterday", "Previous 7 days", "Previous 30 days", "Older"].includes(n))).toBeTruthy();
  await d.getByLabel("Group by").selectOption("none");
  await expect(page.locator(".grp[role=treeitem]")).toHaveCount(0);
  await expect(page.locator(".row").first()).toHaveAttribute("aria-level", "1");
  // Sort by title, A–Z then Z–A.
  await d.getByLabel("Sort by").selectOption("title");
  await expect.poll(async () => (await page.locator(".row .t").allTextContents())[0]).toBe("Feature flag");
  await d.getByRole("button", { name: /^A–Z, switch to Z–A/ }).click();
  await expect.poll(async () => (await page.locator(".row .t").allTextContents())[0]).toBe("What is the codeword in notes.txt?");
  // Rows.
  await d.getByRole("radio", { name: "Compact" }).click();
  await expect(page.locator(".tree.compact")).toBeVisible();
  expect(await page.locator(".row").first().evaluate((el) => Math.round(el.getBoundingClientRect().height))).toBe(32);
  // Kept across a reload, then reset.
  await page.waitForTimeout(600);
  await page.reload();
  await expect(page.locator(".tree.compact")).toBeVisible({ timeout: 30_000 });
  const d2 = await display(page);
  await expect(d2.getByLabel("Group by")).toHaveValue("none");
  await d2.getByRole("button", { name: "Reset to defaults" }).click();
  await expect(d2.getByLabel("Group by")).toHaveValue("repository");
  await expect(page.locator(".tree.compact")).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(d2).toBeHidden();
});

test("groups collapse and stay so; ⌥-click and Display do all of them", async ({ page }) => {
  const demo = page.locator('.grp[data-gkey^="repository:"]').filter({ has: page.locator(".gname", { hasText: "demo" }) });
  await demo.locator(".gh").click();
  await expect(demo).toHaveAttribute("aria-expanded", "false");
  await expect(row(page, "Find the codeword")).toHaveCount(0);
  await expect(demo).toHaveAttribute("aria-label", /^demo, 2 sessions/); // the count stays
  await page.waitForTimeout(600);
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  await expect(demo).toHaveAttribute("aria-expanded", "false");
  // Saved per grouping: by agent, every group is open.
  const d = await display(page);
  await d.getByLabel("Group by").selectOption("agent");
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
  await d.getByLabel("Group by").selectOption("repository");
  await expect(demo).toHaveAttribute("aria-expanded", "false");
  await page.keyboard.press("Escape");
  // ⌥-click (Alt-click) opens or closes all.
  await page.locator(".grp .gh").last().click({ modifiers: ["Alt"] });
  await expect(page.locator('.grp[aria-expanded="true"]')).toHaveCount(0);
  await page.locator(".grp .gh").first().click({ modifiers: ["Alt"] });
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
  const d2 = await display(page);
  await d2.getByRole("button", { name: "Collapse all" }).click();
  await expect(page.locator('.grp[aria-expanded="true"]')).toHaveCount(0);
  await d2.getByRole("button", { name: "Expand all" }).click();
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
});

test("a long list: compact rows and older groups collapsed by default, and a toast that says so", async ({ page }) => {
  // Stop the previous page's delayed SaveList before replacing its service/home.
  // Otherwise it can save the four-session defaults into the freshly seeded home.
  await page.goto("about:blank");
  expect((await page.request.post("/reset")).ok()).toBeTruthy();
  expect((await page.request.post("/seed?n=180")).ok()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 60_000 });
  await expect(page.locator("#toast")).toContainText("184 sessions: compact rows, older repositories collapsed.", { timeout: 30_000 });
  await expect(page.locator(".tree.compact")).toBeVisible();
  await expect(count(page)).toHaveText("184 sessions");
  // Collapsed groups build no rows.
  const closed = await page.locator('.grp[aria-expanded="false"]').count();
  const open = await page.locator('.grp[aria-expanded="true"]').count();
  expect(closed + open).toBeGreaterThan(5);
  expect(await page.locator(".row").count()).toBeLessThan(184 + 1);
  // The header sticks under the toolbar as the list scrolls.
  await page.locator(".content").evaluate((el) => (el.scrollTop = 600));
  const tb = await page.locator("#list-toolbar").boundingBox();
  const heads = await page.locator(".gh").evaluateAll((els) => els.map((e) => e.getBoundingClientRect().top));
  expect(heads.some((t) => Math.abs(t - (tb!.y + tb!.height)) < 2)).toBeTruthy();
});

test("the View menu's commands change the display", async ({ page }) => {
  await menu(page, "group:status");
  await expect.poll(() => names(page)).toEqual(["Moved", "Ended"]);
  await menu(page, "compact");
  await expect(page.locator(".tree.compact")).toBeVisible();
  await menu(page, "collapse-all");
  await expect(page.locator('.grp[aria-expanded="true"]')).toHaveCount(0);
  await menu(page, "expand-all");
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
  await menu(page, "display");
  await expect(page.getByRole("dialog", { name: "Display options" })).toBeVisible();
});

test('conversation families keep separate identities, rename and collapse across refresh', async({page})=>{
 const d=await display(page);await d.getByLabel('Group by').selectOption('family');await page.keyboard.press('Escape');
 await expect(page.locator('.grp[role=treeitem]')).toHaveCount(4);
 const first=page.locator('.grp[role=treeitem]').first();await first.getByRole('button',{name:'Rename conversation family'}).click();
 const rename=page.getByRole('dialog');await rename.locator('input').fill('My conversation family');await rename.getByRole('button',{name:'Save name'}).click();
 await expect(page.locator('.gname').filter({hasText:'My conversation family'})).toHaveCount(1);
 const group=page.locator('.grp[role=treeitem]').filter({hasText:'My conversation family'});
 await group.locator(".gh").click();await expect(group).toHaveAttribute('aria-expanded','false');
 await page.waitForTimeout(600);await page.reload();
 await expect(page.locator('.grp[role=treeitem]').filter({hasText:'My conversation family'})).toHaveAttribute('aria-expanded','false');
 await expect(page.locator('#btn-terminal')).not.toContainText('1');
});
