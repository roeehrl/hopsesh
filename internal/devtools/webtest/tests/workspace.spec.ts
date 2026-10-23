import { test, expect } from "@playwright/test";
import { fresh, row, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// A session started in a folder of repositories (~/git/course holding course-core and
// course-sdk) on another machine: the plan names the folder here and each repository in it,
// and offers to clone the one that is missing.
test("a folder of repositories shows each one and clones the missing one", async ({ page }) => {
  const asks: any[] = [];
  const ws = "/home/u/git/course";
  const member = (name: string, action: string, extra = {}) => ({
    action, localPath: `${ws}/${name}`, sourceTop: `/Users/sam/git/course/${name}`, sourceMain: `/Users/sam/git/course/${name}`,
    identity: `github.com/acme/${name}`, remote: `git@github.com:acme/${name}.git`, ...extra,
  });
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m !== "Plan") return route.continue();
    const opts = (req.a || req.args || [])[3] || {};
    asks.push(opts);
    const body = await (await route.fetch()).json();
    const p = body.result;
    p.sourceHost = "laptop";
    p.sourceCwd = "/Users/sam/git/course";
    p.targetCwd = ws;
    p.repo = { action: "workspace", localPath: ws, members: [
      member("course-core", "use", { localBranch: "main", unpushed: 2, dirty: 1 }),
      member("course-sdk", opts.clone ? "clone" : "needs-clone"),
    ] };
    p.blockers = opts.clone ? [] : [`course-sdk is not cloned on this machine; clone it into ${ws} (pass --clone, or clone it yourself)`];
    p.warnings = [...(p.warnings || []), "/Users/sam/git/course holds 2 repositories on laptop; hopsesh brings each one, not other folders in it (such as worktrees)",
      "on laptop course-core has 2 unpushed commit(s) and 1 uncommitted file(s) that a checkout here will not contain"];
    await route.fulfill({ json: body });
  });

  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  const sheet = page.locator("#sheet");
  await expect(sheet.getByText(`Continues in ${ws}`)).toBeVisible({ timeout: 30_000 });
  await expect(sheet).toContainText("On laptop this folder holds 2 repositories");
  await expect(sheet).toContainText(`course-core: found here at ${ws}/course-core (on main)`);
  await expect(sheet).toContainText("2 unpushed commits and 1 uncommitted file stay on laptop.");
  await expect(sheet).toContainText("course-sdk: not found here");
  // Said once, by the repository list: not again as a blocker or a warning.
  await expect(sheet).not.toContainText("is not cloned on this machine");
  await expect(sheet).not.toContainText("holds 2 repositories on laptop");

  await sheet.getByRole("button", { name: "Clone course-sdk for me" }).click();
  await expect(sheet).toContainText(`course-sdk: will clone into ${ws}/course-sdk`);
  await expect(sheet.getByLabel("What changes")).toContainText("1 clone");
  expect(asks.at(-1).clone).toBe(true);
});
