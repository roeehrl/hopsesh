import { test, expect } from "@playwright/test";
import { fresh, row, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const plan = (patch: any = {}) => ({ machine: "here", local: true, change: { entry: {}, open: false, plan: {
  method: "files", from: "/home/u/git/demo", to: "/home/u/git/demo-2", main: "/x", moves: [{ from: "/a", to: "/b" }], records: ["a relocated record (as /cd writes)"],
  notes: ["Claude Code loads /home/u/git/demo-2's CLAUDE.md when the session resumes"], warnings: [], blockers: [] }, ...patch } });

// Change folder…: plan, then the same session continues in the new folder, with Undo.
test("Change folder… moves a session to another checkout, the same session", async ({ page }, testInfo) => {
  const calls: { m: string; a: any[] }[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON(), a = req.a || req.args || [];
    if (req.m === "DirSuggestions") return route.fulfill({ json: { result: ["/home/u/git/demo-2"] } });
    if (req.m === "PlanChangeDir") { calls.push({ m: req.m, a }); return route.fulfill({ json: { result: plan() } }); }
    if (req.m === "ChangeDir") { calls.push({ m: req.m, a }); return route.fulfill({ json: { result: "20261022T120000Z" } }); }
    if (req.m === "CancelChangeDir") return route.fulfill({ json: { result: null } });
    return route.continue();
  });
  await row(page, "Find the codeword").click();
  await action(page, "more", "Change folder…");
  const d = page.locator("#dlg");
  await expect(d.getByRole("heading", { name: "Change the folder of “Find the codeword”" })).toBeVisible();
  await d.getByRole("button", { name: "/home/u/git/demo-2" }).click();
  await expect(d.locator(".cd-plan")).toContainText("Moves 1 file to the new folder's project folder");
  await expect(d.locator(".cd-plan")).toContainText("The same session continues there");
  expect(calls[0].a[2]).toBe("/home/u/git/demo-2");
  await page.screenshot({ path: testInfo.outputPath("change-folder.png") });
  await d.getByRole("button", { name: "Change folder" }).click();
  await expect.poll(() => calls.some((c) => c.m === "ChangeDir")).toBeTruthy();
  await expect(page.locator("#toast")).toContainText("continues in /home/u/git/demo-2 now, the same session");
  await expect(page.locator("#toast").getByRole("button", { name: "Undo" })).toBeVisible();
});

// An open Claude Code session moves itself: the dialog says what to type, then waits.
test("Change folder… of an open session says to type /cd, then waits for it", async ({ page }) => {
  let waited = 0;
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (req.m === "DirSuggestions") return route.fulfill({ json: { result: [] } });
    if (req.m === "PlanChangeDir") return route.fulfill({ json: { result: plan({ open: true, inSession: "/cd /home/u/git/demo-2" }) } });
    if (req.m === "WaitChangedDir") { waited++; return route.fulfill({ json: { result: null } }); }
    if (req.m === "ChangeDir") throw new Error("an open session's files are never moved by hopsesh");
    if (req.m === "CancelChangeDir") return route.fulfill({ json: { result: null } });
    return route.continue();
  });
  await row(page, "Find the codeword").click();
  await action(page, "more", "Change folder…");
  const d = page.locator("#dlg");
  await d.getByLabel("Folder", { exact: true }).fill("/home/u/git/demo-2");
  await expect(d.locator(".cd-insession")).toContainText("In it, type:");
  await expect(d.locator(".cd-cmd code")).toHaveText("/cd /home/u/git/demo-2");
  await d.getByRole("button", { name: "I typed it, wait for it" }).click();
  await expect.poll(() => waited).toBe(1);
  await expect(page.locator("#toast")).toContainText("It is the same session.");
});

// A plan that is blocked (another repository) shows why and offers to allow it.
test("Change folder… to another repository asks before allowing it", async ({ page }) => {
  const asked: boolean[] = [];
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON(), a = req.a || req.args || [];
    if (req.m === "DirSuggestions") return route.fulfill({ json: { result: [] } });
    if (req.m === "PlanChangeDir") {
      asked.push(a[3]);
      const blocked = !a[3];
      return route.fulfill({ json: { result: plan({ plan: { ...plan().change.plan, blockers: blocked ? ["/tmp/x is not in a git repository, and the conversation is about the files in /home/u/git/demo; pass --other-repo to change it anyway"] : [] } }) } });
    }
    if (req.m === "CancelChangeDir") return route.fulfill({ json: { result: null } });
    return route.continue();
  });
  await row(page, "Find the codeword").click();
  await action(page, "more", "Change folder…");
  const d = page.locator("#dlg");
  await d.getByLabel("Folder", { exact: true }).fill("/tmp/x");
  await expect(d.locator(".cd-plan .err")).toContainText("is not in a git repository");
  await expect(d.getByRole("button", { name: "Change folder" })).toBeDisabled();
  await d.getByRole("checkbox", { name: /Allow a folder of another repository/ }).check();
  await expect(d.getByRole("button", { name: "Change folder" })).toBeEnabled();
  expect(asked.at(-1)).toBe(true);
});
