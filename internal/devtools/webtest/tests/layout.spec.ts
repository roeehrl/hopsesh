import { test, expect, type Page } from "@playwright/test";
import { fresh, menu } from "./helpers";

// The Sessions screen's panes: dividers that resize them (pointer and keyboard), hiding
// and showing (button, View menu, Enter, a drag past half the minimum), the default width
// on double-click, the layout kept across a reload, and the sidebar out of the way in a
// narrow window.

test.beforeEach(async ({ page }) => fresh(page));

const width = (page: Page, sel: string) => page.locator(sel).evaluate((el) => Math.round(el.getBoundingClientRect().width));
const divider = (page: Page, name: string) => page.getByRole("separator", { name });

async function dragBy(page: Page, name: string, dx: number) {
  const box = (await divider(page, name).boundingBox())!;
  const x = box.x + box.width / 2, y = box.y + box.height / 2;
  await page.mouse.move(x, y);
  await page.mouse.down();
  await page.mouse.move(x + dx / 2, y, { steps: 3 });
  await page.mouse.move(x + dx, y, { steps: 3 });
  await page.mouse.up();
}

test("dragging a divider resizes its pane within its limits; past half the minimum it hides", async ({ page }) => {
  expect(await width(page, "#sidebar")).toBe(220);
  expect(await width(page, "#inspector")).toBe(360);
  await dragBy(page, "Resize sidebar", 60);
  await expect.poll(() => width(page, "#sidebar")).toBe(280);
  await dragBy(page, "Resize sidebar", 200); // clamped at 320
  await expect.poll(() => width(page, "#sidebar")).toBe(320);
  await expect(divider(page, "Resize sidebar")).toHaveAttribute("aria-valuenow", "320");
  await dragBy(page, "Resize inspector", -400); // wider, until the list keeps 440 (and at most 60% of the room)
  const list = await width(page, ".content");
  expect(list).toBeGreaterThanOrEqual(439);
  expect(await width(page, "#inspector")).toBeLessThanOrEqual(Math.round((1280 - 320) * 0.6));
  await dragBy(page, "Resize inspector", 600); // past half its minimum: hidden
  await expect.poll(() => width(page, "#inspector")).toBe(0);
  await expect(page.locator("#inspector")).toHaveAttribute("inert", "");
  await expect(page.getByRole("button", { name: "Show inspector" })).toHaveAttribute("aria-expanded", "false");
  // A double-click on its divider brings it back at its default width.
  await divider(page, "Resize inspector").dblclick();
  await expect.poll(() => width(page, "#inspector")).toBe(360);
});

test("the buttons, the View menu and Enter hide and show the panes, and the keyboard resizes them", async ({ page }) => {
  await page.getByRole("button", { name: "Hide sidebar" }).click();
  await expect.poll(() => width(page, "#sidebar")).toBe(0);
  await expect(page.getByRole("button", { name: "Show sidebar" })).toHaveAttribute("aria-expanded", "false");
  await menu(page, "toggle-sidebar");
  await expect.poll(() => width(page, "#sidebar")).toBe(220);
  await menu(page, "toggle-inspector");
  await expect.poll(() => width(page, "#inspector")).toBe(0);
  await menu(page, "toggle-inspector");
  await expect.poll(() => width(page, "#inspector")).toBe(360);

  const sep = divider(page, "Resize sidebar");
  await sep.focus();
  await page.keyboard.press("ArrowRight");
  await expect.poll(() => width(page, "#sidebar")).toBe(236);
  await page.keyboard.press("Shift+ArrowRight");
  await expect.poll(() => width(page, "#sidebar")).toBe(300);
  await page.keyboard.press("Home");
  await expect.poll(() => width(page, "#sidebar")).toBe(180);
  await page.keyboard.press("End");
  await expect.poll(() => width(page, "#sidebar")).toBe(320);
  await page.keyboard.press("Enter");
  await expect.poll(() => width(page, "#sidebar")).toBe(0);
  await expect(sep).toHaveAttribute("aria-valuenow", "0");
  await page.keyboard.press("Enter");
  await expect.poll(() => width(page, "#sidebar")).toBe(320);
  // On the inspector's divider, Left makes the inspector wider.
  await divider(page, "Resize inspector").focus();
  await page.keyboard.press("ArrowLeft");
  await expect.poll(() => width(page, "#inspector")).toBe(376);
});

test("the layout is kept across a reload", async ({ page }) => {
  await dragBy(page, "Resize sidebar", 40);
  await expect.poll(() => width(page, "#sidebar")).toBe(260);
  await page.getByRole("button", { name: "Hide inspector" }).click();
  await page.waitForTimeout(600); // saved after 300 ms
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  expect(await width(page, "#sidebar")).toBe(260);
  expect(await width(page, "#inspector")).toBe(0);
});

test("a narrow window hides the sidebar for now, and brings it back when wide again", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 600 });
  await expect.poll(() => width(page, "#sidebar")).toBe(0);
  expect(await width(page, ".content")).toBeGreaterThanOrEqual(440);
  expect(await width(page, "#inspector")).toBeGreaterThanOrEqual(300);
  // Shown by the user in the narrow window, for now.
  await page.getByRole("button", { name: "Show sidebar" }).click();
  await expect.poll(() => width(page, "#sidebar")).toBe(220);
  await page.setViewportSize({ width: 1280, height: 820 });
  await expect.poll(() => page.evaluate(() => window.innerWidth)).toBe(1280);
  await expect.poll(() => width(page, "#sidebar")).toBe(220);
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => setTimeout(r, 50)))); // the resize is handled
  await page.setViewportSize({ width: 900, height: 600 });
  await expect.poll(() => width(page, "#sidebar")).toBe(0);
  await page.setViewportSize({ width: 1280, height: 820 });
  await expect.poll(() => width(page, "#sidebar")).toBe(220);
  // Nothing of that was saved.
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  expect(await width(page, "#sidebar")).toBe(220);
});

test("the inspector's width follows the window until set: 30% of the room, at most 60% and what leaves the list 440px", async ({ page }) => {
  await page.setViewportSize({ width: 1600, height: 900 });
  await expect.poll(() => width(page, "#inspector")).toBe(Math.round((1600 - 220) * 0.3)); // 414
  await divider(page, "Resize inspector").focus();
  await page.keyboard.press("End");
  await expect.poll(() => width(page, "#inspector")).toBe(Math.min(960, 1600 - 220 - 440, Math.round((1600 - 220) * 0.6)));
  await page.setViewportSize({ width: 2560, height: 1200 });
  await divider(page, "Resize inspector").focus();
  await page.keyboard.press("End");
  await expect.poll(() => width(page, "#inspector")).toBe(960);
  await divider(page, "Resize inspector").dblclick(); // back to its default for this window
  await expect.poll(() => width(page, "#inspector")).toBe(480);
});

test("the list's own width decides compact rows: below 600px they are one line", async ({ page }) => {
  const rowHeight = () => page.locator(".row").first().evaluate((el) => Math.round(el.getBoundingClientRect().height));
  expect(await rowHeight()).toBeGreaterThan(40); // comfortable
  await divider(page, "Resize inspector").focus();
  await page.keyboard.press("End"); // a wide inspector leaves the list under 600px
  await expect.poll(() => width(page, ".content")).toBeLessThan(600);
  await expect.poll(rowHeight).toBe(32);
  await expect(page.locator(".row .act").first()).toBeHidden();
});
