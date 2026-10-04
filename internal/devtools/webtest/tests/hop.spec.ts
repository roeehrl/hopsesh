import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const cloudDetails = (page: Page) => page.getByRole("complementary", { name: "Cloud session details" });

// post asks the stand-in world for a cloud session (or task) and returns its id.
async function post(page: Page, query: string): Promise<string> {
  const r = await page.request.post("/cloud?" + query);
  expect(r.ok(), await r.text()).toBeTruthy();
  return (await r.json()).id;
}

async function turnOn(page: Page, title: string) {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.locator(".side-off", { hasText: title }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: new RegExp(title) })).toContainText("ready", { timeout: 30_000 });
}

async function paste(page: Page, id: string) {
  await page.getByRole("button", { name: "Paste a link…" }).click();
  await page.getByLabel("The session's link or id").fill(`https://claude.ai/code/${id}`);
  await page.locator("#dlg").getByRole("button", { name: "Add" }).click();
  await expect(row(page, `Session ${id}`)).toBeVisible({ timeout: 30_000 });
}

test("a Claude Code cloud session goes on to Codex cloud through this machine, and one undo takes both legs back", async ({ page }) => {
  await post(page, "cloud=codex-cloud&env=env_api&title=" + encodeURIComponent("An earlier task"));
  const id = await post(page, "handed=1");
  await turnOn(page, "Claude Code cloud");
  await paste(page, id);
  await turnOn(page, "Codex cloud");
  await page.getByRole("navigation", { name: "Scopes" }).getByRole("button", { name: /Claude Code cloud/ }).click();
  await row(page, `Session ${id}`).click();

  await cloudDetails(page).getByRole("button", { name: "Hand off ▸" }).click();
  const list = cloudDetails(page).getByRole("menu", { name: "Hand off to" });
  await expect(list.locator(".chip.cloud", { hasText: /^claude-cloud$/ })).toHaveCount(0); // not to its own cloud
  const codex = list.getByRole("menuitem", { name: /Codex cloud/ });
  await expect(codex).toBeEnabled();
  await expect(codex).toContainText("Comes here from Claude Code cloud first, then gets a briefing and the code on a branch");
  await expect(list.getByRole("menuitem", { name: /Jules/ })).toContainText("Jules: turned off. Turn it on in Machines.");
  await codex.click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: `Hand “Session ${id}” on to Codex cloud` })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.locator(".hop-leg[data-leg='1']")).toContainText("Bring here: claude-cloud →");
  await expect(sheet.locator(".hop-leg[data-leg='1']")).toContainText("native");
  await expect(sheet.locator(".hop-leg[data-leg='2']")).toContainText("→ codex-cloud");
  await expect(sheet.locator(".hop-leg[data-leg='2']")).toContainText("brief");
  await expect(sheet).toContainText("Codex cloud starts from the claude/… branch Claude Code cloud pushed");
  await expect(sheet.locator("#hop-terminal")).toContainText("Claude Code saves its copy only after you send a message in it");
  await expect(sheet.getByLabel("What changes")).toContainText("Undo from Activity takes both legs back");
  const env = sheet.getByLabel("Environment", { exact: true });
  await expect(sheet.getByRole("button", { name: /^Hand it on/ })).toBeDisabled();
  await env.selectOption({ label: "acme-api (used by 1 of your recent tasks)" });
  await expect(sheet.getByRole("button", { name: /^Hand it on/ })).toBeEnabled({ timeout: 30_000 });
  await sheet.getByRole("button", { name: /^Hand it on/ }).click();

  // The teleport runs in the terminal (the stand-in's user sends a message); then it goes on.
  await expect(page.getByRole("heading", { name: "Handed off to Codex cloud" })).toBeVisible({ timeout: 60_000 });
  await expect(page.locator(".page")).toContainText(/Task task_e_\w+ is running/);
  await expect(page.locator("#ho-via")).toContainText("Brought here from Claude Code cloud first");
  await expect(page.locator(".page")).toContainText("Environment acme-api");
  await expect(page.locator(".page")).toContainText("The session here is marked “continued in Codex on codex-cloud”");

  await page.getByRole("button", { name: "Undo", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg).toContainText("This undoes both legs");
  await expect(dlg).toContainText("then the copy brought here from Claude Code cloud and its worktree");
  await dlg.getByRole("button", { name: "Undo hand-off" }).click();
  await expect(page.locator("#toast")).toContainText("Undone", { timeout: 30_000 });

  await menu(page, "activity");
  const hop = page.locator(".line-item", { hasText: "Handed on" });
  await expect(hop).toBeVisible();
  await expect(hop).toContainText("Undone");
  await expect(page.locator(".line-item", { hasText: /^Brought/ })).toHaveCount(0); // its legs go with it
});

test("Activity looks for merged branches only when asked, and offers none that is not merged", async ({ page }) => {
  const id = await post(page, "handed=1");
  await turnOn(page, "Claude Code cloud");
  await paste(page, id);
  await cloudDetails(page).getByRole("button", { name: /Bring here \(Claude Code\)/ }).click();
  const sheet = page.locator("#sheet");
  await sheet.getByRole("button", { name: /Bring here in/ }).click();
  await expect(page.locator(".outcome.ok")).toBeVisible({ timeout: 60_000 });
  await expect(page.locator(".outcome.ok")).toContainText("it begins with the briefing hopsesh sent");
  await expect(page.locator("#cleanup-hint")).toContainText("Once its work is merged, hopsesh can delete the cloud's branch for you");
  await page.locator("#cleanup-hint").getByRole("button", { name: "Activity → Look for merged branches" }).click();

  const card = page.getByRole("region", { name: "Branches on your remotes" });
  await expect(card.locator(".branch-item")).toHaveCount(0);
  await card.getByRole("button", { name: "Look for merged branches" }).click();
  const branch = card.locator(".branch-item");
  await expect(branch).toHaveCount(1, { timeout: 30_000 });
  await expect(branch).toContainText(/claude\/web-session-\w+/);
  await expect(branch).toContainText("the cloud's own branch");
  await expect(branch).toContainText("not merged into main yet");
  await expect(branch.getByRole("button", { name: "Delete" })).toHaveCount(0);
});
