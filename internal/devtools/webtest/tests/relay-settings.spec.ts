import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => {
  await fresh(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Internet delivery" }).click();
});

test("opening internet settings does not enroll or create an identity", async ({ page }) => {
  await expect(page.getByRole("status").filter({ hasText: "Not initialized" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Enroll endpoint…" })).toBeDisabled();
  await expect(page.getByRole("button", { name: "Pair machine…" })).toBeDisabled();
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Not enrolled" })).toBeVisible();
  await page.getByRole("button", { name: "Show public identity" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.locator("pre")).toContainText('"signing"');
  await expect(dialog.locator("pre")).not.toContainText("AGE-SECRET-KEY");
  await expect(dialog.locator("pre")).not.toContainText('"token"');
});

test("pairing keeps invalid input visible and separates transfer permissions", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Pair machine…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Allow sending sessions to this machine")).not.toBeChecked();
  await expect(dialog.getByLabel("Allow bringing conversations from this machine")).not.toBeChecked();
  await dialog.getByLabel("Public pairing identity").fill("{invalid}");
  await dialog.getByRole("button", { name: "Approve endpoint" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("invalid public identity JSON");
  await expect(dialog.getByLabel("Public pairing identity")).toHaveValue("{invalid}");
  await expect(dialog.getByRole("button", { name: "Approve endpoint" })).toBeEnabled();
});

test("cloud approval has bounded lifetime and no machine receive controls", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Approve cloud session…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Approval lifetime")).toHaveValue("3600");
  await expect(dialog.getByLabel("Approval lifetime").locator("option")).toHaveCount(4);
  await expect(dialog.getByLabel("Machine name")).toHaveCount(0);
  await expect(dialog.getByRole("button", { name: "Choose repository…" })).toHaveCount(0);
  await expect(dialog.getByLabel("Allow conversation export")).not.toBeChecked();
});

test("enrollment hides credentials and preserves the dialog on validation failure", async ({ page }) => {
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Enroll endpoint…" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByLabel("Scoped enrollment credential JSON")).toHaveAttribute("type", "password");
  await dialog.getByLabel("Relay HTTPS origin").fill("https://relay.example.com");
  await dialog.getByLabel("Scoped enrollment credential JSON").fill("invalid");
  await dialog.getByRole("button", { name: "Enroll this endpoint" }).click();
  await expect(dialog.getByRole("alert")).toHaveText("invalid scoped enrollment credential JSON");
  await expect(dialog.getByRole("button", { name: "Enroll this endpoint" })).toBeEnabled();
});

test("public pairing identity wraps within a compact window", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 650 });
  await page.getByRole("button", { name: "Create endpoint identity" }).click();
  await page.getByRole("button", { name: "Show public identity" }).click();
  const dialog = page.getByRole("dialog");
  await expect(dialog.getByRole("button", { name: "Close", exact: true })).toBeInViewport();
  const dimensions = await dialog.evaluate(element => ({ width: element.scrollWidth, client: element.clientWidth }));
  expect(dimensions.width).toBeLessThanOrEqual(dimensions.client + 1);
});
