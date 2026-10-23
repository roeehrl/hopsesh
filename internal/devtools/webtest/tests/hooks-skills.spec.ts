import { test, expect } from "@playwright/test";
import { fresh } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// Each machine's hooks and skill (Settings › Machines) are in machine-status.spec.ts.

test("a machine with sessions but no hopsesh is pointed out once, until its findings change", async ({ page }) => {
  let dismissed: string[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m === "DismissMachineNotice") { dismissed.push((req.a || req.args)[0]); return route.fulfill({ json: { result: null } }); }
    if (req.m !== "MachineNotices") return route.continue();
    const all = [{ machine: "lurus", kind: "no-hopsesh", title: "lurus has sessions but no hopsesh.", text: "Originals you move from lurus won't be protected there.", action: "Set up hopsesh on lurus", fingerprint: "ssh:lurus" }];
    await route.fulfill({ json: { result: all.filter((n) => !dismissed.includes(n.fingerprint)) } });
  });
  await page.reload();
  const notice = page.getByRole("status", { name: "lurus has sessions but no hopsesh." });
  await expect(notice).toBeVisible();
  await notice.getByRole("button", { name: "Not now" }).click();
  await expect(notice).toBeHidden();
  expect(dismissed).toEqual(["ssh:lurus"]);
});
