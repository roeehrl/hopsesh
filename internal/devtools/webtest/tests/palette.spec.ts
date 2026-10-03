import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("the palette finds sessions and runs commands", async ({ page }) => {
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("codeword");
  await expect(page.locator(".pal-grp", { hasText: "Session" }).first()).toBeVisible();
  await expect(page.locator(".pal-item", { hasText: "Continue in" }).first()).toBeVisible();
  await input.fill("activity");
  await input.press("Enter");
  await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible();
});

test("the menu's commands open each screen", async ({ page }) => {
  for (const [cmd, heading] of [["machines", "Machines"], ["settings", "General"], ["activity", "Activity"], ["sessions", "All sessions"]]) {
    await menu(page, cmd);
    await expect(page.getByRole("heading", { name: heading, exact: true })).toBeVisible();
  }
});
