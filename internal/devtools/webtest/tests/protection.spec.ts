import { test, expect, type Page } from "@playwright/test";
import { row, details, action, roleRow } from "./helpers";

// The capture worlds for movement protection: /reset?protection=review (Codex has not
// trusted hopsesh's hooks) and /reset?protection=blocked (hooks installed and trusted).
async function world(page: Page, mode: string) {
  const r = await page.request.post(`/reset?movement=1&protection=${mode}`);
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
}

test("review: the Sessions warning asks to approve the hopsesh hooks in Codex", async ({ page }) => {
  await world(page, "review");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("Codex", { timeout: 30_000 });
  await expect(card).toContainText("approve the hopsesh hooks");
  await expect(card).not.toContainText("command is not installed");
  await card.getByRole("button", { name: "How to approve" }).click();
  const dialog = page.getByRole("dialog").last();
  await expect(dialog).toContainText("/hooks");
  await expect(dialog).toContainText("never approves hooks for you");
});

test("blocked: a moved original is blocked and says so", async ({ page }) => {
  test.setTimeout(120_000);
  await world(page, "blocked");
  await expect(page.getByRole("status", { name: "Movement protection" })).toHaveCount(0, { timeout: 30_000 });
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex/);
  await page.locator("#sheet").getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: /is prepared for Codex/ })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Back to sessions", exact: true }).click();
  await roleRow(page, "Find the codeword", "Moved copy").click();
  await details(page).getByRole("button", { name: /Copies & history/ }).click();
  await details(page).getByRole("button", { name: "Show copy", exact: true }).first().click();
  await expect(details(page).locator(".ins-lineage")).toContainText("◆", { timeout: 30_000 });
  await expect(details(page).locator(".ins-lineage")).toContainText("blocked until you move back");
  await expect(details(page).getByLabel("Moved out", { exact: true })).toContainText("Blocked until you move the session back");
});
