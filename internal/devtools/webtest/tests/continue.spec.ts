import { test, expect } from "@playwright/test";
import { fresh, row, menu, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("continue a Claude Code session in Codex, see where it has been, and undo", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Continue “Find the codeword” in Codex" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("1 new Codex session");
  await expect(sheet.locator(".box.kept")).toContainText("Carried over");
  await expect(sheet.locator(".box.lost")).toContainText("1 reasoning block");

  // A briefing only, then the whole conversation again.
  await sheet.getByRole("radio", { name: "Briefing only" }).click();
  await expect(sheet.locator(".box.kept")).toContainText("A briefing only");
  await sheet.getByRole("radio", { name: "Whole conversation" }).click();
  await expect(sheet.locator(".box.kept")).toContainText("2 messages");

  await sheet.getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: "“Find the codeword” continues in Codex" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Codex session written")).toBeVisible();

  await page.getByRole("button", { name: /Back to sessions/ }).click();
  const moved = row(page, "Find the codeword (from Claude Code)");
  await expect(moved).toBeVisible({ timeout: 30_000 });
  await expect(moved).not.toContainText("[hopsesh]");
  await moved.click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details).toContainText("Continued in Codex on studio");
  await details.getByRole("button", { name: /Copies & history/ }).click();
  await expect(details).toContainText("1 transfers");
  await expect(details).toContainText("0 round trips to origin");
  await expect(details).toContainText("Origin: studio/claude");
  await expect(details).toContainText("reasoning omitted");

  await menu(page, "activity");
  await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible();
  await expect(page.locator(".line-item", { hasText: "Find the codeword → Codex" })).toBeVisible();
  await page.locator(".line-item", { hasText: "Find the codeword → Codex" }).getByRole("button", { name: "Undo" }).click();
  await expect(page.locator(".line-item", { hasText: "Find the codeword → Codex" }).getByText("Undone")).toBeVisible();

  await menu(page, "sessions");
  await expect(row(page, "Find the codeword (from Claude Code)")).toHaveCount(0, { timeout: 30_000 });
});
