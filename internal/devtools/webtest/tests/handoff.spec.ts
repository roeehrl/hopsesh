import { test, expect, type Page } from "@playwright/test";
import { fresh, row, turnOnCloud } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const details = (page: Page) => page.getByRole("complementary", { name: "Session details" });

// openMenu selects the demo session and opens its Move menu, whose Cloud group hands off.
async function openMenu(page: Page) {
  await page.getByRole("navigation", { name: "Places" }).getByRole("button", { name: /All sessions/ }).click();
  await row(page, "Find the codeword").click();
  await details(page).getByRole("button", { name: "Move", exact: true }).click();
  return page.getByRole("menu", { name: "Move" });
}

async function turnOn(page: Page) {
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await turnOnCloud(page, "Claude Code cloud");
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
}

test("the Hand off menu and palette only list enabled clouds", async ({ page }) => {
  let menu = await openMenu(page);
  await expect(menu.getByRole("menuitem", { name: /Hand off to / })).toHaveCount(0);
  await expect(menu.getByText("A cloud gets a briefing, not this conversation.")).toHaveCount(0);
  await page.keyboard.press("Escape");

  await turnOn(page);
  menu = await openMenu(page);
  await expect(menu.getByRole("menuitem", { name: /Claude Code cloud/ })).toBeEnabled();
  await expect(menu.getByRole("menuitem", { name: /Claude Code cloud/ })).toContainText("Gets a briefing and the code on a branch");
  await expect(menu.getByRole("menuitem", { name: /Codex cloud/ })).toHaveCount(0);

  // The palette offers it too, for the selected session.
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  await page.getByRole("combobox", { name: "Search sessions or run a command" }).fill("hand off");
  await page.locator(".pal-item", { hasText: "Hand off to…" }).click();
  const picker = page.locator("#dlg").getByRole("menu", { name: "Hand off to" });
  await expect(picker.getByRole("menuitem", { name: /Claude Code cloud/ })).toBeEnabled();
  await expect(picker.getByRole("menuitem", { name: /Codex cloud/ })).toHaveCount(0);
});

test("the hand-off sheet: briefing, branch, what stays, options; done, then undo", async ({ page }) => {
  expect((await page.request.post("/dirty")).ok()).toBeTruthy();
  await turnOn(page);
  const menu = await openMenu(page);
  await menu.getByRole("menuitem", { name: /Claude Code cloud/ }).click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 Claude Code cloud session");
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 branch on github.com");
  await expect(sheet.getByLabel("What changes")).toContainText("~ this session marked");
  await expect(sheet).toContainText("The cloud agent receives a briefing, not this conversation. Tool calls and hidden reasoning stay here.");
  const brief = sheet.getByLabel("Briefing");
  await expect(brief).toHaveValue(/^\[hopsesh\] This task continues a Claude Code session/);
  await expect(sheet.locator(".brief-head")).toContainText(/\d+ tokens/);
  await expect(sheet.getByRole("button", { name: "masked: 0" })).toBeVisible();
  await expect(sheet).toContainText(/Pushes branch hopsesh\/handoff\/\d{8}-0b6c6a8e from [0-9a-f]{7} \(main\)\. Your checkout here is not touched\./);
  await expect(sheet).toContainText("Carries 1 unpushed commit");
  await expect(sheet.getByRole("checkbox", { name: /docs\/notes\.md/ })).not.toBeChecked();
  const stays = sheet.getByRole("group", { name: /Stays on this/ });
  await expect(stays).toContainText(".env");
  await expect(stays).toContainText("certs/dev.pem");
  const history = sheet.getByRole("checkbox", { name: /Also commit the conversation as \.hopsesh\/handoff\.md/ });
  await expect(history).not.toBeChecked();
  await expect(sheet).toContainText("hopsesh can't tell whether github.com/example/demo is public. Anyone who can see the branch could read this file.");
  await expect(sheet.getByRole("checkbox", { name: "Mark this session “continued in Claude Code cloud”" })).toBeChecked();
  await expect(sheet).toContainText("Cloud sessions use your plan's allowance.");
  await expect(sheet.locator("#ho-terminal")).toContainText("Claude Code starts the session in a terminal: it runs claude in hopsesh's hand-off folder for this repository");
  await expect(sheet.locator("#ho-terminal")).toContainText("handoff/github.com/example/demo");

  // Ticking the untracked note plans it in.
  await sheet.getByRole("checkbox", { name: /docs\/notes\.md/ }).check();
  await expect(sheet).toContainText("Carries 1 unpushed commit and 1 changed file", { timeout: 30_000 });

  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  await expect(page.getByRole("heading", { name: "Handed off to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "Open in browser" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();
  await expect(page.locator(".term")).toContainText("https://claude.ai/code/session_01");
  await expect(page.getByRole("button", { name: /^hopsesh\/handoff\// })).toBeVisible();
  await expect(page.locator(".page")).toContainText("Stayed on this");
  await expect(page.locator(".page")).toContainText(".env, certs/dev.pem");
  await expect(page.locator(".page")).toContainText("The session here is marked “continued in Claude Code cloud”");
  await expect(page.getByRole("note")).toHaveText("When it finishes: Clouds → Claude Code cloud → Bring here");
  // No cloud takes a follow-up from hopsesh: there is no button, and Claude Code's cloud
  // says where to write to the session instead.
  await expect(page.getByRole("button", { name: "Send a follow-up…" })).toHaveCount(0);
  await expect(page.locator("#ho-nofollow")).toContainText("hopsesh has not yet integrated and qualified Claude Code's cloud follow-up command");

  await page.getByRole("button", { name: "Undo", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "Undo the hand-off?" })).toBeVisible();
  await expect(dlg).toContainText(/This deletes the branch hopsesh\/handoff\/\d{8}-0b6c6a8e and the mark on the session here\./);
  await expect(dlg).toContainText("The session stays in Claude Code on the web; archive it there if you want it gone.");
  await dlg.getByRole("button", { name: "Undo hand-off" }).click();
  await expect(page.locator("#toast")).toContainText("Undone", { timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
});

// Jules's CLI can't name the branch a session starts from: the menu and the sheet say so,
// and the briefing asks Jules to check the handoff branch out first.
test("a hand-off to Jules: the briefing asks for the handoff branch", async ({ page }) => {
  expect((await page.request.post("/dirty")).ok()).toBeTruthy();
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await turnOnCloud(page, "Jules");
  await expect(sidebar.getByRole("button", { name: /Jules/ })).toBeVisible({ timeout: 30_000 });
  const menu = await openMenu(page);
  const jules = menu.getByRole("menuitem", { name: /Jules/ });
  await expect(jules).toBeEnabled();
  await expect(jules).toContainText("The jules CLI can't choose the branch a session starts from");
  await jules.click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Jules" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("Briefing")).toHaveValue(/git fetch origin hopsesh\/handoff\/\d{8}-0b6c6a8e && git checkout hopsesh\/handoff\/\d{8}-0b6c6a8e/);
  await expect(sheet).toContainText("Jules can't be told which branch to start from, so the briefing asks it to check out");
  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  await expect(page.getByRole("heading", { name: "Handed off to Jules" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".term")).toContainText("https://jules.google.com/session/");
  await expect(page.getByRole("note")).toHaveText("When it finishes: Clouds → Jules → Bring here");
});

// Claude Code starts the session in a terminal hopsesh opens: the sheet says what happens
// there while it waits, and takes the session's link by hand (one of another cloud is
// refused); a step that ends with no session can be left, and the hand-off says so.
test("the hand-off waits for the terminal and takes a pasted link", async ({ page }) => {
  await turnOn(page);
  const seeded = await page.request.post("/cloud?fail=hold&work=0&title=Started%20by%20hand");
  const { id } = await seeded.json();
  const menu = await openMenu(page);
  await menu.getByRole("menuitem", { name: /Claude Code cloud/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  const box = sheet.getByRole("group", { name: "In your terminal" });
  await expect(box).toContainText("Claude Code cloud is starting the session in your terminal", { timeout: 30_000 });
  await expect(box).toContainText("claude --cloud");
  await expect(box).toContainText("If it asks whether you trust this folder, answer it there");
  await expect(box).toContainText("handoff/github.com/example/demo");
  const link = box.getByLabel("Paste the link");
  await link.fill("https://chatgpt.com/codex/tasks/task_e_1");
  await box.getByRole("button", { name: "Use this link" }).click();
  await expect(box.getByRole("alert")).toContainText("is not a link to a Claude Code cloud session");
  await link.fill(`https://claude.ai/code/${id}?from=cli&m=0`);
  await link.press("Enter");
  await expect(page.getByRole("heading", { name: "Handed off to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".term")).toHaveText(`https://claude.ai/code/${id}`);
  await expect(page.locator(".page")).toContainText("The session's link is the one you pasted");
});

test("a terminal step with no session: the sheet says so, and stopping ends the hand-off", async ({ page }) => {
  await turnOn(page);
  expect((await page.request.post("/cloud?fail=trust-no&work=0")).ok()).toBeTruthy();
  const menu = await openMenu(page);
  await menu.getByRole("menuitem", { name: /Claude Code cloud/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  const box = sheet.getByRole("group", { name: "In your terminal" });
  await expect(box).toContainText("hopsesh saw no session link", { timeout: 30_000 });
  await expect(box).toContainText("Claude Code asked whether you trust hopsesh's hand-off folder");
  await box.getByRole("button", { name: "Stop waiting" }).click();
  await expect(sheet.getByRole("heading", { name: "The hand-off stopped at “Start cloud session”" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.locator("#ho-msg")).toContainText("No session started: Claude Code asked whether you trust hopsesh's hand-off folder");
  await expect(sheet.getByRole("button", { name: "Undo" })).toBeVisible();
});
