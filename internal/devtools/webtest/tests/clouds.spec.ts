import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// cloudSession adds a Claude Code cloud session on the demo repository (fail: what the next
// teleport plays) and returns its id.
async function cloudSession(page: Page, fail = ""): Promise<string> {
  const r = await page.request.post("/cloud?fail=" + fail);
  expect(r.ok()).toBeTruthy();
  return (await r.json()).id;
}

// turnOnAndPaste allows Claude Code cloud from the sidebar and pastes the session's link.
async function turnOnAndPaste(page: Page, id: string) {
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.locator(".side-off", { hasText: "Claude Code cloud" }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
  await page.getByRole("button", { name: "Paste a link…" }).click();
  await page.getByLabel("The session's link or id").fill(`https://claude.ai/code/${id}`);
  await page.locator("#dlg").getByRole("button", { name: "Add" }).click();
  await expect(row(page, `Session ${id}`)).toBeVisible({ timeout: 30_000 });
}

test("the Clouds group: turn Claude Code cloud on, paste a link, and see the session in its scope", async ({ page }) => {
  const id = await cloudSession(page);
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await expect(sidebar.getByText("Clouds", { exact: true })).toBeVisible();
  await expect(sidebar.locator(".side-off", { hasText: "Claude Code cloud" }).getByRole("button", { name: "Turn on" })).toBeVisible();
  await turnOnAndPaste(page, id);

  await expect(page.getByRole("heading", { name: "Claude Code cloud" })).toBeVisible();
  await expect(page.getByText("Showing the cloud sessions hopsesh started or brought here, and Remote Control mirrors. Claude Code can show you the rest.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Find in Claude Code…" })).toBeVisible();
  const r = row(page, `Session ${id}`);
  await expect(r.locator(".chip.cloud")).toContainText("claude-cloud");
  await expect(r.locator(".chip.st-unknown")).toHaveText("State unknown");
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("1");
  await expect(sidebar.getByRole("button", { name: /In the cloud/ })).toContainText("1");

  const details = page.getByRole("complementary", { name: "Cloud session details" });
  await expect(details.getByRole("heading", { name: `Session ${id}` })).toBeVisible();
  await expect(details.getByRole("button", { name: /Bring here \(Claude Code\)/ })).toBeEnabled();
  await expect(details.getByRole("button", { name: /Bring here and continue in/ })).toContainText("Codex");
  await expect(details.getByRole("button", { name: "Archive" })).toBeDisabled();
  await expect(details).toContainText("Claude Code archives only on claude.ai");

  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  await page.getByRole("combobox", { name: "Search sessions or run a command" }).fill("cloud");
  await expect(page.locator(".pal-item", { hasText: "Bring from cloud…" })).toBeVisible();
  await expect(page.locator(".pal-item", { hasText: "Paste a cloud link…" })).toBeVisible();
});

test("the bring-back sheet says what comes back and where, and switches to Codex", async ({ page }) => {
  const id = await cloudSession(page);
  await turnOnAndPaste(page, id);
  await page.getByRole("complementary", { name: "Cloud session details" }).getByRole("button", { name: /Bring here \(Claude Code\)/ }).click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: `Bring “Session ${id}” here from Claude Code cloud` })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 Claude Code session here");
  await expect(sheet.getByLabel("What changes")).toContainText("The cloud session is not changed");
  await expect(sheet.locator(".fid")).toContainText("Full");
  await expect(sheet.locator(".fid")).toContainText("Claude Code copies the whole conversation; hopsesh checks the message count.");
  await expect(sheet).toContainText("New worktree");
  await expect(sheet).toContainText(`claude --teleport ${id}`);
  await expect(sheet).toContainText("Clean worktree (hopsesh uses a new one)");
  await expect(sheet.getByRole("button", { name: /Bring here in/ })).toBeEnabled();

  await sheet.getByRole("radio", { name: "Codex" }).click();
  await expect(sheet.locator(".fid")).toContainText("Lossy");
  await expect(sheet.locator(".fid")).toContainText("Copied by Claude Code, then converted (tool calls become text).");
  await sheet.getByRole("button", { name: "Cancel" }).click();
});

test("a partial copy comes back amber, with the known problem and what to do", async ({ page }) => {
  const id = await cloudSession(page, "partial");
  await turnOnAndPaste(page, id);
  await page.getByRole("complementary", { name: "Cloud session details" }).getByRole("button", { name: /Bring here \(Claude Code\)/ }).click();
  const sheet = page.locator("#sheet");
  await sheet.getByRole("button", { name: /Bring here in/ }).click();

  const outcome = page.locator(".outcome.partial");
  await expect(outcome).toBeVisible({ timeout: 30_000 });
  await expect(outcome.getByRole("heading", { name: "Only part of the conversation came back" })).toBeVisible();
  await expect(outcome).toContainText("Claude Code restored 1 of 3 messages. This is a known Claude Code problem (#94836).");
  await expect(outcome).toContainText("The code is here in full");
  await expect(outcome.getByRole("button", { name: "Open the session in the browser" })).toBeVisible();
  await expect(outcome.getByRole("button", { name: "Undo" })).toBeVisible();
  await outcome.getByRole("button", { name: "Keep the partial copy" }).click();
  await expect(outcome).toContainText("You kept the partial copy.");

  await menu(page, "activity");
  const act = page.locator(".line-item", { hasText: "from Claude Code cloud: partial" });
  await expect(act).toBeVisible();
  await expect(act).toContainText("1 of 3 messages restored, partial copy kept");
});

test("a cloud-only module: Copilot's tasks are listed, and only their code comes home", async ({ page }) => {
  const r = await page.request.post("/cloud?cloud=copilot-cloud&title=" + encodeURIComponent("Fix the search index"));
  expect(r.ok()).toBeTruthy();
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  for (const title of ["Copilot cloud agent", "Jules", "Devin", "Amp"]) {
    await expect(sidebar.locator(".side-off", { hasText: title }).getByRole("button", { name: "Turn on" })).toBeVisible();
  }
  await sidebar.locator(".side-off", { hasText: "Copilot cloud agent" }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: /Copilot cloud agent/ })).toContainText("ready", { timeout: 30_000 });
  await expect(sidebar.getByRole("button", { name: /Copilot cloud agent/ })).toContainText("1");

  const task = row(page, "Fix the search index");
  await expect(task).toBeVisible({ timeout: 30_000 });
  await expect(task.locator(".chip.cloud")).toContainText("copilot-cloud");
  await expect(task).toContainText("copilot/fix-the-search-index");
  await expect(task.locator(".chip.st-done")).toHaveText("Done");
  await task.click();
  const details = page.getByRole("complementary", { name: "Cloud session details" });
  await expect(details.getByRole("button", { name: /Get the code/ })).toBeEnabled();
  await expect(details.getByRole("button", { name: /Bring here/ })).toHaveCount(0);
  await expect(details).toContainText("the conversation stays in Copilot cloud agent for now");

  await details.getByRole("button", { name: /Get the code/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Bring the code of “Fix the search index” here from Copilot cloud agent" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet).toContainText("hopsesh/from/copilot-cloud/fix-the-search-index");
  await expect(sheet).toContainText("Only the code comes from Copilot cloud agent");
  await sheet.getByRole("button", { name: /Get the code/ }).click();
  await expect(page.locator(".outcome.ok")).toContainText("The code of “Fix the search index” is here", { timeout: 30_000 });

  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines" })).toBeVisible();
  const card = page.locator(".cloud-card", { hasText: "Copilot cloud agent" });
  await expect(card).toContainText("The code (its branch); the conversation stays in the cloud for now", { timeout: 30_000 });
  // By its heading: "Amp" alone would also match "example" on the Codex cloud card's table.
  await expect(page.getByRole("region", { name: "Amp", exact: true })).toContainText("Nothing yet: the conversation stays in the cloud", { timeout: 30_000 });
});
