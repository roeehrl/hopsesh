import { expect, type Page } from "@playwright/test";

// fresh starts every test from a new demo home and waits for the first scan.
export async function fresh(page: Page) {
  const r = await page.request.post("/reset");
  expect(r.ok()).toBeTruthy();
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
