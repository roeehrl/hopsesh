import { expect, type Page } from "@playwright/test";

// fresh starts every test from a new demo home and waits for the first scan.
export async function fresh(page: Page) {
  const r = await page.request.post("/reset");
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
}

// menu sends a Session-menu command, as the app's menu bar does.
export async function menu(page: Page, cmd: string) {
  await page.evaluate((c) => (window as any).__emit("hopsesh:menu", c), cmd);
}

// row is the session row with exactly this title.
export const row = (page: Page, title: string) =>
  page.locator(".row").filter({ has: page.locator(".t").getByText(title, { exact: true }) });

// turnOnCloud turns a cloud on the way the window offers it: the sidebar's "Turn on a
// cloud…" opens Machines at its clouds, the cloud's switch, then back to the sessions
// (scanned again), showing that cloud's own list.
export async function turnOnCloud(page: Page, title: string) {
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await sidebar.getByRole("button", { name: "Turn on a cloud…" }).click();
  await expect(page.getByRole("heading", { name: "Clouds", exact: true })).toBeInViewport({ timeout: 30_000 });
  const sw = page.getByRole("switch", { name: `Turn on ${title}` });
  await sw.click();
  await expect(sw).toHaveAttribute("aria-checked", "true");
  await page.getByRole("button", { name: "Scan them now" }).click();
  await sidebar.getByRole("button", { name: new RegExp(title) }).click({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: title, exact: true })).toBeVisible();
}

// details is the inspector (a cloud session's is named for its cloud).
export const details = (page: Page, cloud = false) => page.getByRole("complementary", { name: cloud ? "Cloud session details" : "Session details" });

// action opens one of the inspector's menus (the primary's other places, Move or ⋯) and
// picks an item by name.
export async function action(page: Page, which: "places" | "move" | "more", item: string | RegExp, cloud = false) {
  const d = details(page, cloud);
  const btn = which === "move" ? d.getByRole("button", { name: "Move", exact: true }) : which === "more" ? d.getByRole("button", { name: "More actions" }) : d.locator("#act-chevron");
  await btn.click();
  await page.getByRole("menuitem", { name: item }).first().click();
}

// filter ticks a value in the Filter menu (a facet's submenu when facet is given), then
// closes it.
export async function filter(page: Page, value: string | RegExp, facet = "") {
  await page.locator("#btn-filter").click();
  if (facet) await page.getByRole("menuitem", { name: new RegExp("^" + facet) }).click();
  await page.getByRole("menuitemcheckbox", { name: value }).or(page.getByRole("menuitemradio", { name: value })).first().click();
  await page.keyboard.press("Escape");
  if (facet) await page.keyboard.press("Escape");
}
