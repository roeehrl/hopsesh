import { expect, type Page } from "@playwright/test";

// fresh starts every test from a new demo home and waits for the first scan. world picks
// a fixture world ("?protection=blocked": hopsesh's hooks installed and trusted here, so
// a moved original can be blocked).
export async function fresh(page: Page, world = "") {
  const r = await page.request.post("/reset" + world);
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator("#fresh")).toContainText("updated",{timeout:30_000});
}

// menu sends a Session-menu command, as the app's menu bar does.
export async function menu(page: Page, cmd: string) {
  await page.evaluate((c) => (window as any).__emit("hopsesh:menu", c), cmd);
}

// row is the session row with exactly this title (not an old copy a move left behind,
// which shares its title: oldRow).
export const row = (page: Page, title: string) =>
  page.locator(".row:not(.moved-away)").filter({ has: page.locator(".t").getByText(title, { exact: true }) });

// oldRow is the row of an old copy with this title: the copy a move left behind, a row
// of its own, shown only where its machine is.
export const oldRow = (page: Page, title: string) =>
  page.locator(".row.moved-away").filter({ has: page.locator(".t").getByText(title, { exact: true }) });

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
  await page.getByRole("button", { name: "Back to sessions" }).click();
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

// agentRow is the session row with exactly this title in one agent. Titles are never
// changed by a move, so the original and its moved copy share a title; the agent's icon
// tells them apart.
export const agentRow = (page: Page, title: string, agent: string) =>
  row(page, title).filter({ has: page.locator(`.r-ic img[alt="${agent}"], .r-ic [title="${agent}"]`) });

// roleRow is the session row with exactly this title whose role chip names this role
// ("Moved from Claude Code", "Moved back from …", …).
export const roleRow = (page: Page, title: string, role: string) =>
  row(page, title).filter({ has: page.locator(".role-chip", { hasText: role }) });
