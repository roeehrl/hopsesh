import { test, expect } from "@playwright/test";
import { fresh, row } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("lists sessions by repository with their agents and states", async ({ page }) => {
  await expect(page.locator(".card-h .name", { hasText: "demo" })).toBeVisible();
  await expect(row(page, "Find the codeword")).toBeVisible();
  await expect(row(page, "What is the codeword in notes.txt?")).toBeVisible();
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await expect(sidebar.getByRole("button", { name: /All sessions/ })).toContainText("4");
  await expect(sidebar.getByRole("button", { name: /Claude Code/ })).toBeVisible();
  await expect(sidebar.getByRole("button", { name: /Codex/ })).toBeVisible();
  await expect(row(page, "Find the codeword").locator(".chip.st-ended")).toHaveText("Ended");
});

test("the details pane offers what can be done with a session", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.getByRole("heading", { name: "Find the codeword" })).toBeVisible();
  await expect(details.getByRole("button", { name: /^Resume/ })).toBeVisible();
  await expect(details.getByRole("button", { name: "Continue in Codex" })).toBeVisible();
  await expect(details).toContainText("https://github.com/example/demo.git");
});

test("the keyboard moves through the list", async ({ page }) => {
  await row(page, "What is the codeword in notes.txt?").click();
  await page.keyboard.press("ArrowDown");
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowUp");
  await expect(row(page, "What is the codeword in notes.txt?")).toHaveAttribute("aria-selected", "true");
});

test("scopes and filters narrow the list", async ({ page }) => {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.getByRole("button", { name: /Codex/ }).click();
  await expect(page.getByRole("heading", { name: "Codex" })).toBeVisible();
  await expect(row(page, "Find the codeword")).toHaveCount(0);
  await sidebar.getByRole("button", { name: /Needs you/ }).click();
  await expect(page.getByText("Nothing needs you right now.")).toBeVisible();
  await sidebar.getByRole("button", { name: /All sessions/ }).click();
  await page.getByRole("button", { name: "Live only" }).click();
  await expect(page.getByText("No sessions here.")).toBeVisible();
});
