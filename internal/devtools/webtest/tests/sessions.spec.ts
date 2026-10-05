import { test, expect } from "@playwright/test";
import { fresh, row, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("lists sessions by repository with their agents and states", async ({ page }) => {
  await expect(page.locator(".card-h .name", { hasText: "demo" })).toBeVisible();
  await expect(row(page, "Find the codeword")).toBeVisible();
  await expect(row(page, "What is the codeword in notes.txt?")).toBeVisible();
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await expect(sidebar.getByRole("button", { name: /All sessions/ })).toContainText("4");
  await expect(sidebar.getByRole("button", { name: /Claude Code/ })).toBeVisible();
  await expect(sidebar.getByRole("button", { name: /Codex/ })).toBeVisible();
  await expect(row(page, "Find the codeword").locator(".chip.st-ended")).toHaveText("Ended");
});

test("the details pane offers what can be done with a session", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.getByRole("heading", { name: "Find the codeword" })).toBeVisible();
  await expect(details.getByRole("button", { name: /^Resume/ })).toBeVisible();
  await expect(details.getByRole("button", { name: "Continue in Codex" })).toBeVisible();
  await expect(details).toContainText("https://github.com/example/demo.git");
});

test("the keyboard moves through the list", async ({ page }) => {
  await row(page, "What is the codeword in notes.txt?").click();
  await page.keyboard.press("ArrowDown");
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowUp");
  await expect(row(page, "What is the codeword in notes.txt?")).toHaveAttribute("aria-selected", "true");
});

test("scopes and filters narrow the list", async ({ page }) => {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.getByRole("button", { name: /Codex/ }).click();
  await expect(page.getByRole("heading", { name: "Codex" })).toBeVisible();
  await expect(row(page, "Find the codeword")).toHaveCount(0);
  await sidebar.getByRole("button", { name: /Needs you/ }).click();
  await expect(page.getByText("Nothing needs you right now.")).toBeVisible();
  await sidebar.getByRole("button", { name: /All sessions/ }).click();
  await page.getByRole("button", { name: "Live only" }).click();
  await expect(page.getByText("No sessions here.")).toBeVisible();
});

test("another list drops a selection it doesn't show", async ({ page }) => {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.getByRole("heading", { name: "Find the codeword" })).toBeVisible();
  await sidebar.getByRole("button", { name: /Codex/ }).click();
  await expect(details).toContainText("Select a session to see what you can do with it.");
  await sidebar.getByRole("button", { name: /All sessions/ }).click();
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-selected", "false");
});

test("the Hand off menu closes on Escape, a click elsewhere and another list", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  const menu = page.getByRole("menu", { name: "Hand off to" });
  const open = async () => { await details.getByRole("button", { name: "Hand off ▸" }).click(); await expect(menu).toBeVisible(); };
  await open();
  await expect(menu.locator(".chip.cloud")).toHaveCount(0); // the clouds by name, ids only in tooltips
  expect((await menu.boundingBox())!.width).toBeGreaterThanOrEqual(320);
  await page.keyboard.press("Escape");
  await expect(menu).toBeHidden();
  await expect(details.getByRole("button", { name: "Hand off ▸" })).toBeFocused();
  await open();
  await page.getByRole("heading", { name: "All sessions" }).click();
  await expect(menu).toBeHidden();
  await open();
  await page.getByRole("navigation", { name: "Scopes" }).getByRole("button", { name: /Claude Code/ }).click();
  await expect(menu).toBeHidden();
});

test("a session open in the Claude app says so, and offers the app, not a terminal tab", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=claude-desktop`)).ok()).toBeTruthy();
  await menu(page, "refresh");
  const r = row(page, "Find the codeword");
  await expect(r.getByRole("button", { name: "Show Claude" })).toBeVisible({ timeout: 30_000 });
  await r.click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details).toContainText("It is running in the Claude app.");
  await expect(details.getByRole("button", { name: /Show its terminal tab/ })).toHaveCount(0);
  await details.getByRole("button", { name: /^Show the Claude app/ }).click();
  await expect.poll(async () => (await (await page.request.get("/terminal-test/links")).json()) as string[]).toContain("app:Claude");
});

test("a session open in a terminal offers its tab, and says so when there is none to show", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=cli`)).ok()).toBeTruthy();
  await menu(page, "refresh");
  const r = row(page, "Find the codeword");
  await r.getByRole("button", { name: "Show its tab" }).click({ timeout: 30_000 });
  await expect(page.locator("#toast")).toContainText("Couldn't show “Find the codeword”");
});

test("the list refreshes this machine by itself while the window is in front", async ({ page }) => {
  await page.clock.install();
  await page.goto("/");
  await expect(row(page, "Find the codeword")).toBeVisible({ timeout: 30_000 });
  await expect(row(page, "Find the codeword").locator(".chip.st-ended")).toBeVisible();
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=cli`)).ok()).toBeTruthy();
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await page.clock.fastForward("01:30");
  await expect(row(page, "Find the codeword").locator(".chip.st-idle")).toBeVisible({ timeout: 30_000 });
});

test("one line of setup left, on All sessions only, closed for good with ✕", async ({ page }) => {
  const setup = page.getByRole("note", { name: "Finish setting up" });
  await expect(setup).toContainText(/Finish setting up \(\d of 3 done\):/);
  await expect(setup.getByRole("button", { name: "Add machines" })).toBeVisible();
  await expect(page.locator(".notice")).toHaveCount(0); // no setup cards
  await page.getByRole("navigation", { name: "Scopes" }).getByRole("button", { name: /Codex/ }).click();
  await expect(setup).toHaveCount(0);
  await page.getByRole("navigation", { name: "Scopes" }).getByRole("button", { name: /All sessions/ }).click();
  await setup.getByRole("button", { name: "Install the skill" }).click();
  await expect(page.getByRole("heading", { name: "Skill", exact: true })).toBeVisible();
  await menu(page, "sessions");
  await setup.getByRole("button", { name: "Close: don't show this again" }).click();
  await expect(setup).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
  await expect(row(page, "Find the codeword")).toBeVisible();
  await expect(setup).toHaveCount(0);
});

test("the sidebar lists the agents with sessions, and only the clouds that are on", async ({ page }) => {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await expect(sidebar.getByRole("button", { name: /^Claude Code/ })).toBeVisible();
  await expect(sidebar.getByRole("button", { name: /Jules|Devin|Amp|Copilot/ })).toHaveCount(0);
  await sidebar.getByRole("button", { name: "Turn on a cloud…" }).click();
  await expect(page.getByRole("heading", { name: "Clouds", exact: true })).toBeInViewport();
});

test("the palette finds a session and shows it; its action is on mod+Enter", async ({ page }) => {
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("codeword notes");
  const item = page.locator(".pal-item").first();
  await expect(item).toContainText("demo"); // its repository
  await input.press("Enter");
  await expect(page.locator("#palette")).toBeHidden();
  const r = row(page, "What is the codeword in notes.txt?");
  await expect(r).toHaveAttribute("aria-selected", "true");
  await expect(r).toBeInViewport();
  await expect(page.getByRole("complementary", { name: "Session details" }).getByRole("heading", { name: "What is the codeword in notes.txt?" })).toBeVisible();
});
