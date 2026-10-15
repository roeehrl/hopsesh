import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const hook = (agent: string, patch: any = {}) => ({ agent, profile: agent + "-default", installed: true, enabled: true, supported: true, ...patch });

// Settings › Hooks & skills: every machine's command, hooks and skill, and setting them up
// from here on the machines hopsesh can reach.
test("hooks and skills on every machine, set up from here", async ({ page }, testInfo) => {
  const setUps: any[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m === "SetUpProtection") { setUps.push(req.a || req.args); return route.fulfill({ json: { result: { machine: "atelier", hooks: [], skill: { state: "current" }, command: true } } }); }
    if (req.m !== "ProtectionAll") return route.continue();
    await route.fulfill({ json: { result: [
      { machine: "here", local: true, protection: { machine: "here", local: true, version: "0.5.0", command: true, hooks: [hook("claude"), hook("codex", { trust: { state: "needs-review", fix: "In ChatGPT desktop choose Review hooks" } })], skill: { state: "stale", copies: [{ dir: "/h/.claude/skills/hopsesh", agents: ["Claude Code"], status: { state: "stale" } }] } } },
      { machine: "atelier", presence: "login", hopsesh: "0.5.0", protection: { machine: "atelier", version: "0.5.0", command: true, hooks: [hook("claude"), hook("claude", { profile: "w", profileLabel: "work", installed: false })], skill: { state: "absent" } } },
      { machine: "lurus", presence: "ssh" },
    ] } });
  });
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Hooks & skills", exact: true }).click();
  const grid = page.getByRole("table", { name: "Hooks and skills on every machine" });
  await expect(grid).toContainText("! Approve in Codex");
  await expect(grid).toContainText("○ work");
  await expect(grid).toContainText("Nothing can be set up on lurus without hopsesh there.");
  await expect(page.getByRole("status").filter({ hasText: "need" })).toContainText("on 3 machines");
  await page.screenshot({ path: testInfo.outputPath("hooks-skills.png"), fullPage: true });
  await page.getByRole("button", { name: "Set up everything I can" }).click();
  await expect.poll(() => setUps.length).toBe(2);
  expect(setUps.map((a) => a[0]).sort()).toEqual(["", "atelier"]);
});

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
