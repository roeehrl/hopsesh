import { test, expect, type Page } from "@playwright/test";
import { fresh, row, agentRow, action, details, menu } from "./helpers";

// OpenCode in the window, with the stand-in opencode (internal/testkit/fakeopencode) and
// the demo's three OpenCode sessions: sizes in messages, the preview, continuing into and
// out of OpenCode, its protection plugin, its account folders, the experimental badge
// (decision D10), no desktop app to open a session in (D11), and the question before a
// resume or a new session that would restart OpenCode's background service (D2).

const tabs = async (page: Page) => (await (await page.request.post("/call", { data: { m: "TerminalTabs", args: [] } })).json()).result as any[];

test("OpenCode sessions are listed with their sizes in messages and previewed", async ({ page }) => {
  await fresh(page);
  for (const [title, n] of [["renamed by api", 3], ["subagent", 6], ["part rows", 7]] as const) {
    const r = agentRow(page, title, "OpenCode");
    await expect(r).toBeVisible();
    await expect(r).toContainText(`${n} messages`);
    await expect(r).not.toContainText(/\d\s?KB/);
  }
  // The repository's size on disk leaves the message counts out.
  await expect(page.locator(".grp").filter({ has: page.locator(".gname", { hasText: "demo" }) }).locator(".gsize")).toHaveText(/KB$/);

  await row(page, "renamed by api").click();
  const d = details(page);
  await expect(d.getByRole("heading", { name: "renamed by api" })).toBeVisible();
  await expect(d.locator(".ins-status")).toContainText(/Ended · this (Mac|PC|computer) · /);
  const conv = d.locator(".conv");
  await expect(conv.locator(".msg.user .msg-x")).toHaveText("Reply with exactly: hi there", { timeout: 30_000 });
  await expect(conv.locator(".msg.agent .msg-x")).toHaveText("hi there");
  await expect(conv.locator(".msg.agent .msg-l")).toContainText("OpenCode");
  await expect(conv.locator(".msg.agent .msg-l")).toHaveClass(/agent-opencode/);
  // The agent's own colour, not the default.
  const color = await conv.locator(".msg.agent .msg-l").evaluate((el) => getComputedStyle(el).color);
  const ink2 = await page.evaluate(() => { const p = document.createElement("span"); p.style.color = "var(--ink2)"; document.body.append(p); const c = getComputedStyle(p).color; p.remove(); return c; });
  expect(color).not.toBe(ink2);
  await expect(d.locator('dt:text-is("Size") + dd')).toHaveText("3 messages");
  await conv.getByRole("button", { name: "Open transcript" }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByText("Transcript · read-only")).toBeVisible();
  await expect(sheet).toContainText("hi there", { timeout: 30_000 });
  await sheet.getByRole("button", { name: "Close" }).click();
});

test("no desktop app opens an OpenCode session (D11), and OpenCode is experimental (D10)", async ({ page }) => {
  await fresh(page);
  await row(page, "renamed by api").click();
  const d = details(page);
  await expect(d.locator("#act-primary")).toHaveText(/^Resume in /);
  await d.locator("#act-chevron").click();
  const places = page.getByRole("menu", { name: "Resume elsewhere" });
  await expect(places.getByRole("menuitem").first()).toBeVisible();
  await expect(places.getByRole("menuitem", { name: /app|desktop/i })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await d.getByRole("button", { name: "More actions" }).click();
  await expect(page.getByRole("menuitem", { name: /OpenCode (app|desktop)/ })).toHaveCount(0);
  await page.keyboard.press("Escape");

  // Continuing another agent's session in OpenCode says it is experimental.
  await row(page, "Find the codeword").click();
  await d.getByRole("button", { name: "Move", exact: true }).click();
  await expect(page.getByRole("menuitem", { name: /^Continue with OpenCode…/ })).toContainText("experimental");
  await page.keyboard.press("Escape");
  // So does its card in Settings › Agents.
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Agents" }).click();
  const card = page.locator(".card").filter({ has: page.locator(".set-row", { hasText: "OpenCode" }) }).first();
  await expect(card.locator(".chip.st-warn")).toHaveText("experimental");
  await expect(card.locator(".chip.st-warn")).toHaveAttribute("title", /OpenCode support is new/);
});

test("continue a Claude Code session in OpenCode, an OpenCode session in Claude Code, and undo", async ({ page }) => {
  await fresh(page);
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with OpenCode…/);
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Continue “Find the codeword” in OpenCode" })).toBeVisible({ timeout: 30_000 });
  await expect(sheet.getByLabel("What changes")).toContainText("1 new OpenCode session");
  await sheet.getByRole("button", { name: /Continue in OpenCode/ }).click();
  await expect(page.getByRole("heading", { name: "“Find the codeword” is prepared for OpenCode" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("OpenCode session written")).toBeVisible();
  // The stopped service is not restarted to open it: nothing to ask.
  await expect(page.getByText(/restarts OpenCode's background service/)).toHaveCount(0);
  await page.getByRole("button", { name: /Back to sessions/ }).click();
  const copy = agentRow(page, "Find the codeword", "OpenCode");
  await expect(copy).toBeVisible({ timeout: 30_000 });
  await expect(copy).toContainText(/\d+ messages/);
  await copy.click();
  await expect(details(page)).toContainText("Continued in OpenCode");

  // Out of OpenCode: one of its own sessions, into Claude Code.
  await row(page, "renamed by api").click();
  await action(page, "move", /^Continue with Claude Code…/);
  await expect(sheet.getByRole("heading", { name: "Continue “renamed by api” in Claude Code" })).toBeVisible({ timeout: 30_000 });
  await sheet.getByRole("button", { name: /Continue in Claude Code/ }).click();
  await expect(page.getByRole("heading", { name: "“renamed by api” is prepared for Claude Code" })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: /Back to sessions/ }).click();
  await expect(agentRow(page, "renamed by api", "Claude Code")).toBeVisible({ timeout: 30_000 });

  await menu(page, "activity");
  const line = page.locator(".line-item", { hasText: "Find the codeword → OpenCode" });
  await expect(line).toBeVisible();
  await line.getByRole("button", { name: "Undo" }).click();
  await expect(line.getByText("Undone")).toBeVisible({ timeout: 30_000 });
  await menu(page, "sessions");
  await expect(agentRow(page, "Find the codeword", "OpenCode")).toHaveCount(0, { timeout: 30_000 });
});

test("OpenCode's protection is a plugin: set up, its texts, and an outdated copy offered for update", async ({ page }) => {
  await fresh(page);
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("OpenCode plugin · not set up");
  await expect(card).toContainText("for Terminal");

  // An older hopsesh's plugin still protects; the window offers to replace it.
  await fresh(page, "?opencode=outdated");
  await expect(card).toContainText("Update the hopsesh plugin", { timeout: 30_000 });
  await expect(card).toContainText("its files are an older hopsesh's");
  await expect(card).toContainText("OpenCode plugin · installed · out of date");
  await expect(card.getByRole("button", { name: "Update the plugin" })).toBeVisible();
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  const plugin = page.locator(".set-row").filter({ hasText: "OpenCode plugin" });
  await expect(plugin).toContainText("Installed · not confirmed loaded yet · out of date"); // the service is stopped
  await expect(page.getByText(/In OpenCode it is a plugin, which OpenCode loads by itself: it refuses a blocked session's tool calls/)).toBeVisible();
  await plugin.getByRole("button", { name: "Update plugin" }).click();
  await expect(plugin).not.toContainText("out of date", { timeout: 30_000 });
  await expect(plugin.getByRole("button", { name: "Remove plugin" })).toBeVisible();
  await expect(plugin.getByRole("button", { name: "Update plugin" })).toHaveCount(0);

  // With OpenCode's service running, it says the service loaded it.
  await fresh(page, "?protection=blocked&opencode=running");
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  await expect(page.locator(".set-row").filter({ hasText: "OpenCode plugin" })).toContainText("Installed · loaded by OpenCode", { timeout: 30_000 });
});

test("a plugin OpenCode's own config removes: the card says so, and offers no setup that can't fix it", async ({ page }) => {
  await fresh(page, "?opencode=removed");
  const card = page.getByRole("status", { name: "Movement protection" });
  await expect(card).toContainText("OpenCode doesn't load the hopsesh plugin", { timeout: 30_000 });
  await expect(card).toContainText("OpenCode plugin · installed · not loaded by OpenCode");
  await expect(card).toContainText("hopsesh can't fix this from here");
  await expect(card).toContainText('Take the entry that removes hopsesh ("-hopsesh", "-*")');
  await expect(card.getByRole("button", { name: /Set up protection|Try again/ })).toHaveCount(0);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  const plugin = page.locator(".set-row").filter({ hasText: "OpenCode plugin" });
  await expect(plugin).toContainText("Installed · not loaded by OpenCode");
  await expect(plugin).toContainText('out of the "plugins" list in your OpenCode config');
});

test("an OpenCode account folder: made with its own service port, in the grid and the folders", async ({ page }) => {
  await fresh(page);
  await page.locator("#btn-settings").click();
  await page.getByRole("button", { name: "Accounts", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Accounts", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Add an account…", exact: true }).click();
  const dlg = page.locator("#dlg");
  await dlg.getByRole("radio", { name: "OpenCode", exact: true }).check();
  await dlg.getByLabel("Name", { exact: true }).fill("Work");
  await expect(dlg).toContainText("~/.hopsesh/accounts/opencode/work");
  await dlg.getByRole("button", { name: "Create and sign in", exact: true }).click();
  await expect(dlg).toBeHidden({ timeout: 30_000 });
  await expect(page.getByRole("button", { name: "Check all machines", exact: true })).toBeEnabled({ timeout: 30_000 });
  const work = page.locator(".folder-card").filter({ has: page.locator(".folder-name").getByText("Work", { exact: true }) });
  await expect(work).toBeVisible();
  await expect(work).toContainText("~/.hopsesh/accounts/opencode/work");
  await expect(work).toContainText(/port 4937[5-9]/);
  const def = page.locator(".folder-card").filter({ hasText: "~/.local/share/opencode" });
  await expect(def).toBeVisible();
  // The grid has OpenCode's tools for this machine.
  const grid = page.getByRole("table", { name: "Where you're signed in" });
  await expect(grid).toContainText("OpenCode");
  await expect(grid).toContainText("Work ~/.hopsesh/accounts/opencode/work");
});

test("opening an OpenCode session that would restart its background service asks first (D2)", async ({ page }) => {
  await fresh(page, "?opencode=restart&terminal=here");
  await row(page, "renamed by api").click();
  const d = details(page);
  await d.locator("#act-primary").click();
  const ask = page.locator("#dlg");
  await expect(ask.getByRole("heading", { name: "Restart OpenCode's background service?" })).toBeVisible({ timeout: 30_000 });
  await expect(ask).toContainText("runs 2.2.5");
  await expect(ask).toContainText("a turn it is running now stops");
  await ask.getByRole("button", { name: "Cancel" }).click();
  await expect(ask).toBeHidden();
  expect(await tabs(page)).toHaveLength(0);
  // Agreed: it opens once, in a tab running opencode.
  await d.locator("#act-primary").click();
  await ask.getByRole("button", { name: "Restart it and open" }).click();
  await expect.poll(async () => (await tabs(page)).map((t) => t.command)).toEqual([expect.stringMatching(/^opencode -s ses_e29aa1ec8ffes7x6RCUekHyBbf/)]);
});

test("a new OpenCode session that would restart its background service asks first (D2)", async ({ page }) => {
  await fresh(page, "?opencode=restart&terminal=here");
  await page.locator("#btn-settings").click();
  await page.getByRole("button", { name: "Accounts", exact: true }).click();
  const def = page.locator(".folder-card").filter({ hasText: "~/.local/share/opencode" });
  await def.getByRole("button", { name: /^New session/ }).click();
  await page.locator("#dlg").getByRole("button", { name: "Start", exact: true }).click();
  const ask = page.locator("#dlg");
  await expect(ask.getByRole("heading", { name: "Restart OpenCode's background service?" })).toBeVisible({ timeout: 30_000 });
  await ask.getByRole("button", { name: "Restart it and start" }).click();
  await expect.poll(async () => (await tabs(page)).map((t) => t.command)).toEqual([expect.stringMatching(/^opencode/)]);
});

test("continuing into OpenCode whose service would restart: the done screen asks before opening it", async ({ page }) => {
  await fresh(page, "?opencode=restart&terminal=here");
  await row(page, "Find the codeword").click();
  await action(page, "move", /^Continue with OpenCode…/);
  const sheet = page.locator("#sheet");
  await sheet.getByRole("button", { name: /Continue in OpenCode/ }).click({ timeout: 30_000 });
  await expect(page.getByRole("heading", { name: "“Find the codeword” is prepared for OpenCode" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Opening it now restarts OpenCode's background service")).toBeVisible();
  await expect(page.getByText(/Before you resume it: OpenCode's background service runs 2\.2\.5/)).toBeVisible();
  await page.locator("#open").click();
  const ask = page.locator("#dlg");
  await expect(ask.getByRole("heading", { name: "Restart OpenCode's background service?" })).toBeVisible({ timeout: 30_000 });
  await ask.getByRole("button", { name: "Restart it and open" }).click();
  await expect.poll(async () => (await tabs(page)).map((t) => t.command)).toEqual([expect.stringMatching(/^opencode -s ses_/)]);
});

test("a Claude Code session run in OpenCode desktop's terminal panel reads as running in OpenCode, not in an app hopsesh shows", async ({ page }) => {
  await fresh(page);
  const got = await page.evaluate(async () => {
    const core = await import("/core.js" as string), actions = await import("/actions.js" as string);
    const e = core.state.scan.groups.flatMap((g: any) => g.entries).find((x: any) => x.title === "Find the codeword");
    delete core.state.presence?.[e.machine + "\u0000" + e.key];
    const live = { ...e, live: true, status: "working", places: [{ kind: "unknown", in: "opencode", count: 1 }] };
    const m = actions.model(live);
    return { status: actions.statusLine(live)[1], primary: m.primary?.label, caption: m.caption, places: m.places.length };
  });
  expect(got.status).toBe("Working in OpenCode");
  expect(got.primary).toMatch(/^Resume in /); // nothing to show it in
  expect(got.caption).toContain("It runs in a terminal inside OpenCode, which hopsesh can't show");
  expect(got.places).toBe(0);
});

test("another machine's hopsesh would restart OpenCode's service there: the window asks, then asks again with the yes (D2)", async ({ page }) => {
  await fresh(page);
  const calls: any[] = [];
  await page.route("**/call", async (route) => {
    const r = route.request().postDataJSON();
    if (r.m === "ContinueOnMachine") {
      calls.push(r);
      if (calls.length === 1) return route.fulfill({ json: { error: "restart-service: OpenCode's background service runs 2.2.5, and the opencode program (2.3.0) is another version: opening the session with it would restart the service. An older hopsesh on the machine you opened it from cannot ask about it: then, on atelier, open the session with hopsesh open --restart-service", result: null } });
      return route.fulfill({ json: { result: { where: "here", tab: "" } } });
    }
    if (r.m === "AllowServiceRestart") { calls.push(r); return route.fulfill({ json: { result: null } }); }
    return route.continue();
  });
  const done = page.evaluate(async () => (await import("/core.js" as string)).onMachine({ machine: "atelier", key: "opencode/ses_1", agentName: "OpenCode" }));
  const ask = page.locator("#dlg");
  await expect(ask.getByRole("heading", { name: "Restart OpenCode's background service?" })).toBeVisible();
  await expect(ask).toContainText("runs 2.2.5");
  await ask.getByRole("button", { name: "Restart it and open" }).click();
  expect(await done).toEqual({ where: "here", tab: "" });
  expect(calls.map((c) => c.m)).toEqual(["ContinueOnMachine", "AllowServiceRestart", "ContinueOnMachine"]);
  expect(calls[1].args).toEqual(["atelier", "opencode/ses_1"]);
});

test("OpenCode's allow rules work through hopsesh's plugin: without it, Settings says they do nothing yet", async ({ page }) => {
  await page.route("**/call", async (route) => {
    if (route.request().postDataJSON().m !== "Settings") return route.continue();
    const body = await (await route.fetch()).json();
    body.result.skill = { ...body.result.skill, state: "current", rules: [{ agent: "Codex", path: "/h/.codex/rules/hopsesh.rules", present: true }, { agent: "OpenCode", path: "/h/.config/opencode/.hopsesh/rules.json", present: true }] };
    body.result.noticeHooks = (body.result.noticeHooks || []).map((x: any) => (x.agent === "opencode" ? { ...x, installed: false } : x));
    await route.fulfill({ json: body });
  });
  await fresh(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  await expect(page.getByText("Read-only hopsesh commands are allowed in Codex and OpenCode; hops ask.")).toBeVisible();
  await expect(page.getByText(/^OpenCode applies these rules through hopsesh's plugin, which isn't installed, so they have no effect there yet\./)).toBeVisible();
});
