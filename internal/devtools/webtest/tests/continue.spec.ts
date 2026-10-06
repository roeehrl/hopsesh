import { test, expect } from "@playwright/test";
import { fresh, row, menu, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("continue a Claude Code session in Codex, see where it has been, and undo", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Continue “Find the codeword” in Codex" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("1 new Codex session");
  await expect(sheet.locator(".box.kept")).toContainText("Carried over");
  await expect(sheet.locator(".box.lost")).toContainText("1 reasoning block");

  // A briefing only, then the whole conversation again.
  await sheet.getByRole("radio", { name: "Briefing only" }).click();
  await expect(sheet.locator(".box.kept")).toContainText("A briefing only");
  await sheet.getByRole("radio", { name: "History + bounded context" }).click();
  await expect(sheet.locator(".box.kept")).toContainText("2 messages");

  await sheet.getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: "“Find the codeword” is prepared for Codex" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Codex session written")).toBeVisible();

  await page.getByRole("button", { name: /Back to sessions/ }).click();
  const moved = row(page, "Find the codeword (from Claude Code)");
  await expect(moved).toBeVisible({ timeout: 30_000 });
  await expect(moved).not.toContainText("[hopsesh]");
  await moved.click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details).toContainText("Continued in Codex on studio");
  await details.getByRole("button", { name: /Copies & history/ }).click();
  await expect(details).toContainText("1 transfers");
  await expect(details).toContainText("0 round trips to origin");
  await expect(details).toContainText("Origin: studio/claude");
  await expect(details).toContainText("reasoning omitted");

  await menu(page, "activity");
  await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible();
  await expect(page.locator(".line-item", { hasText: "Find the codeword → Codex" })).toBeVisible();
  await page.locator(".line-item", { hasText: "Find the codeword → Codex" }).getByRole("button", { name: "Undo" }).click();
  await expect(page.locator(".line-item", { hasText: "Find the codeword → Codex" }).getByText("Undone")).toBeVisible();

  await menu(page, "sessions");
  await expect(row(page, "Find the codeword (from Claude Code)")).toHaveCount(0, { timeout: 30_000 });
});


test("an unverified account return preserves the original and creates a portable copy", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  await page.locator("#sheet").getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: /is prepared for Codex/ })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: /Back to sessions/ }).click();
  await row(page, "Find the codeword (from Claude Code)").click();
  await action(page, "move", /^Continue with Claude Code…/);
  const sheet = page.locator("#sheet");
  await expect(sheet.getByLabel("What changes")).toContainText("1 new Claude Code session");
  await expect(sheet).toContainText("Account identity continuity is unverified");
  await sheet.getByRole("button", { name: /Continue in Claude Code/ }).click();
  await expect(page.getByText("Claude Code session written", { exact: true })).toBeVisible({timeout:30_000});
});

// The Windows screenshot world selects a remote session. Its action explicitly
// names the destination PC; a local-only locator waits forever on that menu.
test("remote continuation names this PC and retains its source when planning", async ({ page }) => {
  await page.evaluate(async () => {
    const { state, setSystem } = await import(/* @vite-ignore */ '/core.js');
    const { render } = await import(/* @vite-ignore */ '/sessions.js');
    setSystem('windows', 'Windows Terminal');
    const entry = state.scan.groups.flatMap(g => g.entries).find(e => e.title === 'Find the codeword');
    entry.machine = 'remote-fixture';
    state.scan.machines.push({ name: 'remote-fixture', local: false, status: 'ok', agents: ['claude'] });
    render();
  });
  await page.route('**/call', async route => {
    const request = route.request().postDataJSON();
    if (request.m === 'Plan') return route.fulfill({ json: { error: 'Remote plan captured for regression test' } });
    return route.continue();
  });
  await row(page, 'Find the codeword').click();
  await page.getByRole('complementary', { name: 'Session details' }).getByRole('button', { name: 'Move', exact: true }).click();
  const target = page.getByRole('menuitem', { name: /^Continue with Codex on this PC…/ });
  await expect(target).toBeVisible();
  await expect(page.getByRole('menuitem', { name: /^Continue with Codex…/ })).toHaveCount(0);
  const request = page.waitForRequest(r => r.url().endsWith('/call') && r.postDataJSON()?.m === 'Plan');
  await target.click();
  const args = (await request).postDataJSON().args;
  expect(args[0]).toBe('remote-fixture');
  expect(args[2]).toBe('codex');
  await expect(page.locator('#sheet')).toContainText('Remote plan captured for regression test');
});

test("bounded recovery keeps the original and explains capacity and archive", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Create bounded continuation…/);
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", {name: /Continue.*Claude Code/})).toBeVisible();
  await expect(sheet).toContainText("Working context:");
  await expect(sheet).toContainText("Portable history preserved separately");
  await expect(sheet).toContainText("The original remains available");
  await expect(sheet.getByRole("checkbox", { name: /Replay shell commands|own importer/ })).toHaveCount(0);
  await sheet.getByRole("button", {name: /Continue in Claude Code/}).click();
  await expect(page.getByRole("heading", {name: /is prepared for Claude Code/})).toBeVisible();
  await expect(page.getByText("Claude Code session written", {exact: true})).toBeVisible();
  await page.getByRole("button", {name: /Back to sessions/}).click();
  await expect(row(page, "Find the codeword (from Claude Code)")).toBeVisible();
});

test("oversized note is bounded and disclosed before apply", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with Codex…/);
  const sheet = page.locator("#sheet");
  await sheet.locator('#note').fill('Pending task: '.repeat(20000));
  await sheet.locator('#note').blur();
  await expect(sheet).toContainText("Briefing shortened to fit");
  await expect(sheet).toContainText("Portable history preserved separately");
  await expect(sheet.getByRole("button", {name: /Continue in Codex/})).toBeEnabled();
});

test("a recorded context error offers bounded recovery in session details", async ({ page }) => {
  await page.evaluate(async () => {
    const { state } = await import(/* @vite-ignore */ '/core.js');
    const { render } = await import(/* @vite-ignore */ '/sessions.js');
    const entry = state.scan.groups.flatMap(g => g.entries).find(e => e.title === 'Find the codeword');
    entry.contextOverflow = true;
    render();
  });
  await row(page, 'Find the codeword').click();
  const details = page.getByRole('complementary', { name: 'Session details' });
  await expect(details).toContainText('The agent reported a context limit');
  await details.getByRole('button', { name: 'Create bounded continuation…', exact: true }).click();
  await expect(page.locator('#sheet')).toContainText('The original remains available');
});
