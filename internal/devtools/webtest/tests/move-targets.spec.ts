import { test, expect } from "@playwright/test";
import { fresh, row, details } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// Where a session can continue: each place with who it runs as, what carries over and what
// becomes of the copy left behind; the chosen one is planned there.
test("choose where a session continues, with who it runs as there", async ({ page }, testInfo) => {
  const planned: any[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m === "PushPlan") { planned.push(req.a || req.args); return route.fulfill({ json: { error: "stop here" } }); }
    if (req.m !== "MoveTargets") return route.continue();
    await route.fulfill({ json: { result: [
      { machine: "atelier", local: false, profile: "p-default", folder: "default", path: "/Users/me/.claude", tool: "terminal", opensIn: "Terminal on atelier", opensNote: "you work in it here", runsAs: "me@gmail.com", carries: "everything", carriesWhy: "same account that ran it", left: "blocked", leftWhy: "by hopsesh on this machine" },
      { machine: "atelier", local: false, profile: "p-work", folder: "work", path: "/Users/me/.hopsesh/accounts/claude/work", tool: "terminal", opensIn: "Terminal on atelier", runsAs: "roy@conduct.security", carries: "conversation", carriesWhy: "reasoning signed for me@gmail.com is left out", left: "blocked" },
      { machine: "lurus", local: false, folder: "", tool: "terminal", opensIn: "—", carries: "unknown", left: "blocked", disabled: true, why: "Set up hopsesh on lurus to send sessions there." },
    ] } });
  });
  await row(page, "Find the codeword").click();
  await details(page).getByRole("button", { name: "Move", exact: true }).click();
  await page.getByRole("menuitem", { name: /Choose where it continues/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Move “Find the codeword”" })).toBeVisible();
  await expect(sheet).toContainText("reasoning signed for me@gmail.com is left out");
  await expect(sheet).toContainText("Set up hopsesh on lurus to send sessions there.");
  await expect(sheet.getByRole("radio", { name: "lurus" })).toBeDisabled();
  await expect(sheet.locator("#go-target")).toHaveText("Move to atelier");
  await page.screenshot({ path: testInfo.outputPath("move-targets.png"), fullPage: true });
  await sheet.getByRole("radio", { name: "atelier work" }).check();
  await expect(sheet.locator("#go-target")).toHaveText("Move to atelier · work");
  await sheet.locator("#go-target").click();
  await expect.poll(() => planned.length).toBeGreaterThan(0);
  expect(planned[0][1]).toBe("atelier");
  expect(planned[0][3].targetProfile).toBe("p-work");
});
