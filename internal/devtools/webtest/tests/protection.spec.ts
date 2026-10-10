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
  await expect(card).toContainText("Approve hopsesh in Codex to finish", { timeout: 30_000 });
  await expect(card).toContainText("waiting for your approval in Codex");
  await expect(card.getByRole("button", { name: "Set up protection" })).toHaveCount(0);
  await expect(card.getByRole("button")).toHaveText(["Approve in Codex…", "Not now", "Check again"]);
  await card.getByRole("button", { name: "Approve in Codex…" }).click();
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
  await expect(details(page).locator(".ins-lineage")).toContainText("Blocked until you move it back");
  await expect(details(page).getByLabel("Moved out", { exact: true })).toContainText("Blocked until you move the session back");
});

test("setup: one card, one action, agents named without account labels", async ({ page }) => {
  const r = await page.request.post("/reset?movement=1");
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("Protect moved sessions", { timeout: 30_000 });
  await expect(card.getByRole("button", { name: "Set up protection" })).toHaveCount(1);
  await expect(card.getByRole("button", { name: /Install/ })).toHaveCount(0);
  await expect(card).not.toContainText("default ·");
  await card.getByRole("button", { name: "Not now" }).click();
  await expect(card).toHaveCount(0);
});
