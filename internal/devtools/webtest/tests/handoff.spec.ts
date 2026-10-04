import { test, expect, type Page } from "@playwright/test";
import { fresh, row } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const details = (page: Page) => page.getByRole("complementary", { name: "Session details" });

// openMenu selects the demo session and opens its Hand off ▸ menu.
async function openMenu(page: Page) {
  await page.getByRole("navigation", { name: "Scopes" }).getByRole("button", { name: /All sessions/ }).click();
  await row(page, "Find the codeword").click();
  await details(page).getByRole("button", { name: "Hand off ▸" }).click();
  return details(page).getByRole("menu", { name: "Hand off to" });
}

async function turnOn(page: Page) {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.locator(".side-off", { hasText: "Claude Code cloud" }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
}

test("the Hand off menu lists every cloud, and a disabled one says why", async ({ page }) => {
  let menu = await openMenu(page);
  const claude = menu.getByRole("menuitem", { name: /Claude Code cloud/ });
  await expect(claude).toBeDisabled();
  await expect(claude).toContainText("Claude Code cloud: turned off. Turn it on in Machines.");
  const codex = menu.getByRole("menuitem", { name: /Codex cloud/ });
  await expect(codex).toBeDisabled();
  await expect(codex).toContainText("Codex cloud: hopsesh does not reach Codex cloud yet");
  await expect(menu).toContainText("A cloud gets a briefing, not this conversation.");

  await turnOn(page);
  menu = await openMenu(page);
  await expect(menu.getByRole("menuitem", { name: /Claude Code cloud/ })).toBeEnabled();
  await expect(menu.getByRole("menuitem", { name: /Claude Code cloud/ })).toContainText("Gets a briefing and the code on a branch");
  await expect(menu.getByRole("menuitem", { name: /Codex cloud/ })).toBeDisabled();

  // The palette offers it too, for the selected session.
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  await page.getByRole("combobox", { name: "Search sessions or run a command" }).fill("hand off");
  await page.locator(".pal-item", { hasText: "Hand off to…" }).click();
  const picker = page.locator("#dlg").getByRole("menu", { name: "Hand off to" });
  await expect(picker.getByRole("menuitem", { name: /Claude Code cloud/ })).toBeEnabled();
  await expect(picker.getByRole("menuitem", { name: /Codex cloud/ })).toContainText("hopsesh does not reach Codex cloud yet");
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
  await expect(sheet.getByRole("checkbox", { name: "Mark this session “continued in Claude Code on claude-cloud”" })).toBeChecked();
  await expect(sheet).toContainText("Cloud sessions use your plan's allowance.");

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
  await expect(page.locator(".page")).toContainText("The session here is marked “continued in Claude Code on claude-cloud”");
  await expect(page.getByRole("note")).toHaveText("When it finishes: Clouds → Claude Code cloud → Bring here");
  await expect(page.getByRole("button", { name: "Send a follow-up…" })).toBeVisible();

  await page.getByRole("button", { name: "Undo", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "Undo the hand-off?" })).toBeVisible();
  await expect(dlg).toContainText(/This deletes the branch hopsesh\/handoff\/\d{8}-0b6c6a8e and the mark on the session here\./);
  await expect(dlg).toContainText("The session stays in Claude Code on the web; archive it there if you want it gone.");
  await dlg.getByRole("button", { name: "Undo hand-off" }).click();
  await expect(page.locator("#toast")).toContainText("Undone", { timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
});
