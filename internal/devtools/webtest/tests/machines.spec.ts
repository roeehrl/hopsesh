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

test("adding a machine starts one scan, disables Scan, and explains failures with Retry", async ({ page }) => {
  let scans = 0, release!: () => void;
  const pending = new Promise<void>(resolve => { release = resolve; });
  await page.route("**/call", async route => {
    if (route.request().postDataJSON().m !== "ScanMachine") return route.continue();
    scans++;
    await pending;
    return route.fulfill({ json: { error: "Connection timed out: check the machine's address" } });
  });
  await menu(page, "machines");
  await page.getByRole("button", { name: "Add by address…" }).click();
  await page.getByLabel("Name").fill("scan-box");
  await page.getByLabel("SSH destination").fill("me@scan-box.invalid");
  await page.locator("#dlg").getByRole("button", { name: "Add", exact: true }).click();
  const machine = page.locator('[data-machine="scan-box"]');
  await expect(machine.getByRole("button", { name: "Scanning scan-box" })).toBeDisabled();
  await expect(machine).toHaveAttribute("aria-busy", "true");
  expect(scans).toBe(1);
  release();
  await expect(machine).toContainText("Connection timed out");
  await expect(machine).toHaveAttribute("aria-busy", "false");
  await machine.getByRole("button", { name: "Retry scan scan-box" }).click();
  await expect.poll(() => scans).toBe(2);
  await expect(machine.getByRole("button", { name: "Retry scan scan-box" })).toBeEnabled();
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
});
