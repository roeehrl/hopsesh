import { test, expect } from "@playwright/test";
import { fresh, menu, row } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("settings tabs, agents and their capabilities", async ({ page }) => {
  await menu(page, "settings");
  const tabs = page.getByRole("tablist", { name: "Settings" });
  await tabs.getByRole("tab", { name: "Agents" }).click();
  await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
  await expect(page.getByText("Claude Code", { exact: true }).first()).toBeVisible();
  await expect(page.locator(".cap", { hasText: "continues in other agents" }).first()).toBeVisible();
  await page.keyboard.press("ArrowDown");
  await expect(tabs.getByRole("tab", { name: "History" })).toHaveAttribute("aria-selected", "true");
});

test("turning an agent off hides its sessions", async ({ page }) => {
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Agents" }).click();
  await page.getByRole("switch", { name: "Codex on" }).click();
  await expect(page.getByRole("switch", { name: "Codex on" })).toHaveAttribute("aria-checked", "false");
  await menu(page, "refresh");
  await expect(row(page, "Find the codeword")).toBeVisible({ timeout: 30_000 });
  await expect(row(page, "What is the codeword in notes.txt?")).toHaveCount(0); // Codex's
});
