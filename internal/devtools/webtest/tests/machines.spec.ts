import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("add a machine, receive sessions, and remove the machine", async ({ page }) => {
  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();

  const receive = page.getByRole("switch", { name: "Receive sessions from my other machines" });
  await expect(receive).toHaveAttribute("aria-checked", "false");
  await receive.click();
  await expect(receive).toHaveAttribute("aria-checked", "true");

  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill("build-box");
  await page.getByLabel("SSH destination").fill("me@build-box.invalid");
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
  const added = page.locator(".mgrid", { hasText: "build-box" });
  await expect(added).toBeVisible();
  await expect(added.getByRole("button", { name: "SSH key" })).toBeVisible();

  await added.getByRole("button", { name: "Remove" }).click();
  await page.locator("#dlg").getByRole("button", { name: "Remove" }).click();
  await expect(page.locator(".mgrid", { hasText: "build-box" })).toHaveCount(0);
});
