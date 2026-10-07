import { test, expect, type Page } from "@playwright/test";
import { fresh, menu } from "./helpers";

async function theme(page: Page, mode: "light" | "dark") {
  await expect(page.locator("html")).toHaveAttribute("data-theme", mode);
  await expect(page.locator("html")).toHaveCSS("color-scheme", mode);
  const panel = mode === "dark" ? "#1f1f1c" : "#ffffff";
  await expect.poll(() => page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--panel").trim())).toBe(panel);
}

async function choose(page: Page, mode: string) {
  const select = page.getByRole("combobox", { name: "Color scheme" });
  await expect(select).toBeEnabled();
  await select.selectOption(mode);
  await expect(select).toBeEnabled();
  await expect(select).toHaveValue(mode);
}

test.beforeEach(async ({ page }) => fresh(page));

test("appearance overrides the OS, updates open windows and terminals, and survives reload", async ({ page, context }) => {
  await page.emulateMedia({ colorScheme: "dark" });
  await theme(page, "dark");
  const quick = await context.newPage();
  await quick.emulateMedia({ colorScheme: "dark" });
  await quick.goto("/quick.html");
  await theme(quick, "dark");
  const opened = await page.request.post("/terminal-test/open?title=Appearance");
  expect(opened.ok()).toBeTruthy();
  const terminal = await context.newPage();
  await terminal.emulateMedia({ colorScheme: "dark" });
  await terminal.goto("/terminal/?renderer=dom");
  await expect(terminal.locator(".xterm-screen")).toBeVisible();
  await theme(terminal, "dark");
  await menu(page, "settings");
  await expect(page.getByRole("combobox", { name: "Color scheme" })).toHaveValue("system");

  for (const mode of ["light", "dark"] as const) {
    // Opposite OS preference in each window: explicit choice must win in CSS and xterm.
    for (const p of [page, quick, terminal]) await p.emulateMedia({ colorScheme: mode === "light" ? "dark" : "light" });
    await choose(page, mode);
    for (const p of [page, quick, terminal]) await theme(p, mode);
    await expect(quick.locator("#view")).toHaveCSS("background-color", mode === "dark" ? "rgb(31, 31, 28)" : "rgb(255, 255, 255)");
    await expect(terminal.locator(".xterm-scrollable-element")).toHaveCSS("background-color", mode === "dark" ? "rgb(31, 31, 28)" : "rgb(255, 255, 255)");
    // Live OS changes cannot dislodge the explicit choice.
    for (const p of [page, quick, terminal]) {
      await p.emulateMedia({ colorScheme: mode });
      await p.emulateMedia({ colorScheme: mode === "light" ? "dark" : "light" });
      await theme(p, mode);
    }
  }
  await page.reload();
  await theme(page, "dark");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  await menu(page, "settings");
  await expect(page.getByRole("combobox", { name: "Color scheme" })).toHaveValue("dark");
  // A newly opened window also reads the saved choice.
  await quick.reload();
  await theme(quick, "dark");
  await terminal.reload();
  await theme(terminal, "dark");

  await choose(page, "system");
  for (const mode of ["light", "dark", "light"] as const) {
    for (const p of [page, quick, terminal]) {
      await p.emulateMedia({ colorScheme: mode });
      await theme(p, mode);
      await expect(p.locator("html")).toHaveAttribute("data-appearance", "system");
    }
    await expect(terminal.locator(".xterm-scrollable-element")).toHaveCSS("background-color", mode === "dark" ? "rgb(31, 31, 28)" : "rgb(255, 255, 255)");
  }
});
