import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu, turnOnCloud } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

const details = (page: Page) => page.getByRole("complementary", { name: "Session details" });

// codexTask adds a Codex cloud task on the demo repository's main, done (with a diff), in
// an environment, and returns its id.
async function codexTask(page: Page, title = "Add a changelog entry", env = "env_api"): Promise<string> {
  const r = await page.request.post(`/cloud?cloud=codex-cloud&env=${env}&title=${encodeURIComponent(title)}`);
  expect(r.ok(), await r.text()).toBeTruthy();
  return (await r.json()).id;
}

async function turnOnCodex(page: Page) {
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await turnOnCloud(page, "Codex cloud");
  await expect(sidebar.getByRole("button", { name: /Codex cloud/ })).toContainText("ready", { timeout: 30_000 });
}

test("the hand-off sheet asks for a Codex cloud environment; the done screen names the task", async ({ page }) => {
  await codexTask(page, "An earlier task");
  expect((await page.request.post("/dirty")).ok()).toBeTruthy();
  await turnOnCodex(page);
  await page.getByRole("navigation", { name: "Places" }).getByRole("button", { name: /All sessions/ }).click();
  await row(page, "Find the codeword").click();
  await details(page).getByRole("button", { name: "Move", exact: true }).click();
  const menu = page.getByRole("menu", { name: "Move" });
  const codex = menu.getByRole("menuitem", { name: /Codex cloud/ });
  await expect(codex).toBeEnabled();
  await expect(codex).toContainText("Gets a briefing and the code on a branch");
  await expect(codex).toContainText("Codex can't see any cloud environments from its command line");
  await codex.click();

  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Codex cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 Codex cloud task");
  await expect(sheet.getByLabel("What changes")).toContainText("+ 1 branch on github.com");
  await expect(sheet).toContainText("The cloud agent receives a briefing, not this conversation. Tool calls and hidden reasoning stay here.");
  const env = sheet.getByLabel("Environment", { exact: true });
  await expect(env).toHaveValue("");
  await expect(env.locator("option")).toHaveText(["Pick an environment…", "acme-api (used by 1 of your recent tasks)", "Other…"]);
  await expect(sheet.locator("#ho-env-note")).toHaveText("Pick a Codex cloud environment for github.com/example/demo. If you have none, open codex cloud once to create one.");
  await expect(sheet.getByRole("button", { name: /^Hand off/ })).toBeDisabled();
  await expect(sheet).toContainText("Cloud tasks use your plan's allowance.");
  await expect(sheet.getByRole("checkbox", { name: "Mark this session “continued in Codex cloud”" })).toBeChecked();

  await env.selectOption({ label: "acme-api (used by 1 of your recent tasks)" });
  await expect(sheet.locator("#ho-env-note")).toHaveCount(0, { timeout: 30_000 });
  await expect(env).toHaveValue("env_api");
  await expect(sheet.getByRole("button", { name: /^Hand off/ })).toBeEnabled();

  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  await expect(page.getByRole("heading", { name: "Handed off to Codex cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(page.locator(".page")).toContainText(/Task task_e_\w+ is running · “Find the codeword”/);
  await expect(page.locator(".term")).toContainText("https://chatgpt.com/codex/tasks/task_e_");
  await expect(page.getByRole("button", { name: "Open in browser" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy link" })).toBeVisible();
  await expect(page.locator(".page")).toContainText("Environment acme-api");
  await expect(page.getByRole("button", { name: /^hopsesh\/handoff\// })).toBeVisible();
  await expect(page.locator(".page")).toContainText("The session here is marked “continued in Codex cloud”");
  await expect(page.getByRole("note")).toHaveText("When it finishes: Clouds → Codex cloud → Bring here");
  await expect(page.getByRole("button", { name: "Send a follow-up…" })).toHaveCount(0);

  await page.getByRole("button", { name: "Undo", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg).toContainText(/This deletes the branch hopsesh\/handoff\/\d{8}-0b6c6a8e and the mark on the session here\./);
  await expect(dlg).toContainText("The task stays in your Codex cloud list; archive it there if you want it gone.");
  await dlg.getByRole("button", { name: "Undo hand-off" }).click();
  await expect(page.locator("#toast")).toContainText("Undone", { timeout: 30_000 });
});

test("a Codex cloud task comes back: its code committed on a branch, a Codex session written", async ({ page }) => {
  const id = await codexTask(page);
  await turnOnCodex(page);
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await sidebar.getByRole("button", { name: /Codex cloud/ }).click();
  await expect(page.getByRole("heading", { name: "Codex cloud" })).toBeVisible();
  const r = row(page, "Add a changelog entry");
  await expect(r.locator(".chip.cloud")).toContainText("Codex cloud", { timeout: 30_000 });
  await expect(r.locator(".chip.st-done")).toHaveText("Done");
  await expect(r).toContainText("+1 −0 · 1 file");
  await r.click();

  const inspector = page.getByRole("complementary", { name: "Cloud task details" });
  await expect(inspector.getByRole("heading", { name: "Add a changelog entry" })).toBeVisible();
  await expect(inspector).toContainText(id);
  await expect(inspector.getByRole("button", { name: /^Bring to this/ })).toBeEnabled();
  await inspector.getByRole("button", { name: "Other ways to bring it" }).click();
  await expect(page.getByRole("menuitem", { name: /into Claude Code…/ })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: /^Get the code only…/ })).toBeEnabled();
  await page.keyboard.press("Escape");
  await inspector.getByRole("button", { name: "More actions" }).click();
  await expect(page.getByRole("menuitem", { name: /^Archive/ })).toBeDisabled();
  await expect(page.getByRole("menuitem", { name: /^Archive/ })).toContainText("Codex archives only on chatgpt.com");
  await page.keyboard.press("Escape");
  await expect(inspector).toContainText("acme-api");
  await expect(inspector).toContainText("hopsesh writes the task's title and what came of it as a new session");

  await inspector.getByRole("button", { name: /^Bring to this/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Bring “Add a changelog entry” here from Codex cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet).toContainText("Only the task title, summary and code come back. The steps stay in the cloud.");
  // Codex cloud lists a task's environment, not its repository: choose the checkout here.
  await expect(sheet).toContainText(/doesn't know which repository this cloud task works on; choose its checkout here/);
  await expect(sheet.getByRole("button", { name: /^Bring here/ })).toBeDisabled();
  await sheet.getByRole("button", { name: "Choose its checkout…" }).first().click();
  await sheet.getByRole("button", { name: "Use it" }).click();
  await expect(sheet).toContainText("hopsesh writes it as a new Codex session (2 messages), in the worktree with the task's code.", { timeout: 30_000 });
  await expect(sheet).toContainText(`hopsesh/from/codex-cloud/${id}`);
  await expect(sheet).toContainText("the task's patch (+1 −0 · 1 file), committed");
  await expect(sheet.getByLabel("What changes")).toContainText("The cloud task is not changed");
  const go = sheet.getByRole("button", { name: /^Bring here/ });
  await expect(go).toHaveText(/^Bring here/);
  await expect(go).not.toContainText("Terminal");
  await go.click();

  await expect(page.getByRole("heading", { name: "Brought from Codex cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "“Add a changelog entry” is here in Codex" })).toBeVisible();
  await expect(page.locator(".page")).toContainText("The task's title and what came of it, written by hopsesh");
  await expect(page.locator(".page")).toContainText(`hopsesh/from/codex-cloud/${id}`);
  await expect(page.locator(".page")).toContainText("The cloud task is untouched.");
  await expect(page.locator(".term")).toContainText("codex resume");
  await expect(page.getByRole("button", { name: /Continue in/ })).toHaveCount(0);
  await page.locator(".page").getByText("What stays in the cloud").click();
  await expect(page.locator(".page")).toContainText("The task's messages, steps and tool calls stay in Codex cloud");

  await page.locator(".page").getByRole("button", { name: /^Undo/ }).click();
  await expect(page.locator("#toast")).toContainText("Undone", { timeout: 30_000 });
});

test("Machines: the Codex cloud card sets each repository's environment", async ({ page }) => {
  await codexTask(page);
  await turnOnCodex(page);
  await menu(page, "machines");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();
  const card = page.locator(".cloud-card", { has: page.getByRole("heading", { name: "Codex cloud" }) });
  await expect(card).toContainText("a hand-off branch");
  await expect(card).toContainText("GitHub only");
  await expect(card).toContainText("Codex can't see any cloud environments from its command line");
  const table = card.getByRole("table", { name: "Codex cloud environment per repository" });
  const select = table.getByLabel("Environment for github.com/example/demo");
  await expect(select).toHaveValue("");
  await select.selectOption({ label: "acme-api" });
  await expect(page.locator("#toast")).toContainText("github.com/example/demo runs in env_api");
  await expect(card.getByRole("table", { name: "Codex cloud environment per repository" }).getByLabel("Environment for github.com/example/demo")).toHaveValue("env_api", { timeout: 30_000 });
  await expect(card).toContainText("No environment yet? Open codex cloud once to create one.");
  await expect(card.locator("code", { hasText: "codex cloud" }).first()).toBeVisible(); // a command, shown as code
});
