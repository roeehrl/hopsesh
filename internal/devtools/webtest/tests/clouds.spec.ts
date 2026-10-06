import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu, turnOnCloud, details as pane, action } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// cloudSession adds a Claude Code cloud session on the demo repository (fail: what the next
// teleport plays) and returns its id.
async function cloudSession(page: Page, fail = "", handed = false): Promise<string> {
  const r = await page.request.post("/cloud?fail=" + fail + (handed ? "&handed=1" : ""));
  expect(r.ok()).toBeTruthy();
  return (await r.json()).id;
}

// turnOnAndPaste allows Claude Code cloud from the sidebar and pastes the session's link.
async function turnOnAndPaste(page: Page, id: string) {
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await turnOnCloud(page, "Claude Code cloud");
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
  await page.getByRole("button", { name: "Paste a link…" }).click();
  await page.getByLabel("The session's link or id").fill(`https://claude.ai/code/${id}`);
  await page.locator("#dlg").getByRole("button", { name: "Add" }).click();
  await expect(row(page, `Session ${id}`)).toBeVisible({ timeout: 30_000 });
}

test("the Clouds group: turn Claude Code cloud on, paste a link, and see the session in its scope", async ({ page }) => {
  const id = await cloudSession(page);
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await expect(sidebar.getByText("Clouds", { exact: true })).toBeVisible();
  // A cloud that is off is not listed: one row turns one on (in Machines).
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toHaveCount(0);
  await expect(sidebar.getByRole("button", { name: "Turn on a cloud…" })).toBeVisible();
  await turnOnAndPaste(page, id);

  await expect(page.getByRole("heading", { name: "Claude Code cloud" })).toBeVisible();
  await expect(page.getByText(/^Showing the cloud sessions hopsesh started or brought here\. Claude Code can show you the rest/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Find in Claude Code…" })).toBeVisible();
  const r = row(page, `Session ${id}`);
  await expect(r.locator(".chip.cloud")).toContainText("Claude Code cloud");
  await expect(r.locator(".chip.st-unknown")).toHaveText("Status on claude.ai");
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("1");
  await expect(sidebar.getByRole("button", { name: /In the cloud/ })).toHaveCount(0); // a Location filter now

  const details = pane(page, true);
  await expect(details.getByRole("heading", { name: `Session ${id}` })).toBeVisible();
  await expect(details.getByRole("button", { name: /^Bring to this/ })).toBeEnabled();
  await details.getByRole("button", { name: "Other ways to bring it" }).click();
  await expect(page.getByRole("menuitem", { name: /^Bring to this .* into Codex…/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await details.getByRole("button", { name: "More actions" }).click();
  const archive = page.getByRole("menuitem", { name: /^Archive/ });
  await expect(archive).toHaveAttribute("aria-disabled", "true");
  await expect(archive).toContainText("Claude Code archives only on claude.ai");
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  await page.getByRole("combobox", { name: "Search sessions or run a command" }).fill("cloud");
  await expect(page.locator(".pal-item", { hasText: "Bring from cloud…" })).toBeVisible();
  await expect(page.locator(".pal-item", { hasText: "Paste a cloud link…" })).toBeVisible();
});

test("the bring-back sheet says what comes back and where, and switches to Codex", async ({ page }) => {
  const id = await cloudSession(page);
  await turnOnAndPaste(page, id);
  await pane(page, true).getByRole("button", { name: /^Bring to this/ }).click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: `Bring “Session ${id}” here from Claude Code cloud` })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 Claude Code session here");
  await expect(sheet.getByLabel("What changes")).toContainText("The cloud session is not changed");
  await expect(sheet.locator(".fid")).toContainText("Full");
  await expect(sheet.locator(".fid")).toContainText("Claude Code copies the whole conversation, once you send a message in it; hopsesh checks the copy.");
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
  const id = await cloudSession(page, "partial", true);
  await turnOnAndPaste(page, id);
  await pane(page, true).getByRole("button", { name: /^Bring to this/ }).click();
  const sheet = page.locator("#sheet");
  await sheet.getByRole("button", { name: /Bring here in/ }).click();

  const outcome = page.locator(".outcome.partial");
  await expect(outcome).toBeVisible({ timeout: 30_000 });
  await expect(outcome.getByRole("heading", { name: "Only part of the conversation came back" })).toBeVisible();
  await expect(outcome).toContainText("Claude Code restored 1 message, but it holds only the briefing hopsesh sent, none of the cloud's replies. This is a known Claude Code problem (#94836).");
  await expect(outcome).toContainText("The code is here in full");
  await expect(outcome.getByRole("button", { name: "Open the session in the browser" })).toBeVisible();
  await expect(outcome.getByRole("button", { name: "Undo" })).toBeVisible();
  await outcome.getByRole("button", { name: "Keep the partial copy" }).click();
  await expect(outcome).toContainText("You kept the partial copy.");

  await menu(page, "activity");
  const act = page.locator(".line-item", { hasText: "from Claude Code cloud: partial" });
  await expect(act).toBeVisible();
  await expect(act).toContainText("1 message restored, partial copy kept");
});

test("a cloud-only module: Copilot's tasks are listed, their code comes home, and their log is written into Claude Code", async ({ page }) => {
  const r = await page.request.post("/cloud?cloud=copilot-cloud&title=" + encodeURIComponent("Fix the search index"));
  expect(r.ok()).toBeTruthy();
  const sidebar = page.getByRole("navigation", { name: "Places" });
  for (const title of ["Copilot cloud agent", "Jules", "Devin", "Amp"]) {
    await expect(sidebar.getByRole("button", { name: new RegExp(title) })).toHaveCount(0);
  }
  await turnOnCloud(page, "Copilot cloud agent");
  await expect(sidebar.getByRole("button", { name: /Copilot cloud agent/ })).toContainText("ready", { timeout: 30_000 });
  await expect(sidebar.getByRole("button", { name: /Copilot cloud agent/ })).toContainText("1");

  const task = row(page, "Fix the search index");
  await expect(task).toBeVisible({ timeout: 30_000 });
  await expect(task.locator(".chip.cloud")).toContainText("Copilot cloud agent");
  await expect(task).toContainText("copilot/fix-the-search-index");
  await expect(task.locator(".chip.st-done")).toHaveText("Done");
  await task.click();
  const details = pane(page, true);
  await expect(details.getByRole("button", { name: /^Bring to this/ })).toBeEnabled();
  await expect(details).toContainText("hopsesh writes its messages as a new session of the agent you pick");

  // The code alone, as before.
  await action(page, "places", "Get the code only…", true);
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Bring the code of “Fix the search index” here from Copilot cloud agent" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet).toContainText("hopsesh/from/copilot-cloud/fix-the-search-index");
  await sheet.getByRole("button", { name: /Get the code/ }).click();
  await expect(page.locator(".outcome.ok")).toContainText("The code of “Fix the search index” is here", { timeout: 30_000 });

  // The session log, written into Claude Code beside the code.
  await menu(page, "sessions");
  await row(page, "Fix the search index").click();
  await details.getByRole("button", { name: /^Bring to this/ }).click();
  await expect(sheet.getByRole("heading", { name: "Bring “Fix the search index” here from Copilot cloud agent" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByRole("radiogroup", { name: "Write it into" })).toBeVisible();
  await expect(sheet).toContainText("The messages come back as text; tool calls stay in the cloud.");
  await expect(sheet).toContainText("hopsesh writes it as a new Claude Code session (2 messages)");
  await sheet.getByRole("button", { name: /^Bring here/ }).click();
  const done = page.locator(".outcome.ok");
  await expect(done).toContainText("“Fix the search index” is here in Claude Code", { timeout: 30_000 });
  await expect(done).toContainText("2 messages, as text");
  await expect(done).toContainText("claude --resume");
  await expect(done).toContainText("Tool calls and their output come only as the session log's text");

  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();
  const card = page.locator(".cloud-card", { hasText: "Copilot cloud agent" });
  await expect(card).toContainText("The messages, as text, and the code", { timeout: 30_000 });
  await expect(page.locator(".cloud-card", { hasText: "Jules" })).toContainText("The code (its patch, committed on a new branch)");
  // By its heading: "Amp" alone would also match "example" on the Codex cloud card's table.
  await expect(page.getByRole("region", { name: "Amp", exact: true })).toContainText("The messages, as text (no code)", { timeout: 30_000 });
});
