import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("the palette finds sessions and runs commands", async ({ page }) => {
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("codeword");
  await expect(page.locator(".pal-grp", { hasText: "Session" }).first()).toBeVisible();
  await expect(page.locator(".pal-item", { hasText: /Continue with (Codex|Claude Code)…/ }).first()).toBeVisible();
  // Explanatory lines from Move are not runnable palette entries.
  expect(await page.locator(".pal-item > span:first-of-type").allTextContents()).not.toContain("");
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

test("the palette's list commands: group, sort, rows, collapse and the filters", async ({ page }) => {
  const run = async (q: string) => {
    await page.getByRole("button", { name: "Search sessions or run a command" }).click();
    const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
    await input.fill(q);
    await page.locator(".pal-item", { hasText: q }).first().click();
  };
  await run("Group by: Agent");
  await expect(page.locator(".gname", { hasText: "Codex" })).toBeVisible();
  await expect(page.locator(".gname", { hasText: "Claude Code" })).toBeVisible();
  await run("Rows: Compact");
  await expect(page.locator(".tree.compact")).toBeVisible();
  await run("Collapse all groups");
  await expect(page.locator('.grp[aria-expanded="true"]')).toHaveCount(0);
  await run("Expand all groups");
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
});
