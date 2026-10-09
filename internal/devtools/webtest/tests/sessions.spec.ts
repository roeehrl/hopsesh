import { test, expect, type Page } from "@playwright/test";
import { fresh, row, menu, details, action, patchScans } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));
test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: "wait" }); });

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Places" });
const group = (page: Page, name: string | RegExp) => page.locator(".grp").filter({ has: page.locator(".gname").getByText(name) });

test("machine sidebar subtitles show detected agents consistently even without sessions", async ({ page }) => {
  await patchScans(page, scan => {
    const local = scan.machines.find((m: any) => m.local);
    if (!scan.discovering) {
      expect(local.agentNames).toContain("Claude Code");
      expect(local.agentNames).toContain("Codex");
    }
    scan.groups = [];
    scan.total = 0;
    scan.machines = [local,
      { name: "other-mac", local: false, status: "ok", os: "darwin", hopsesh: "0.4.0", sessions: 0, agentNames: ["Claude Code", "Codex"], agents: ["Claude Code 2.1.288", "Codex 0.160.1"] },
      { name: "empty-box", local: false, status: "ok", sessions: 0, agentNames: [], agents: [] },
      { name: "offline-box", local: false, status: "unreachable", error: "Connection timed out", sessions: 4, agentNames: ["Claude Code"], agents: ["Claude Code 2.1.288"] },
    ];
  });
  await page.reload();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  const local = sidebar(page).getByRole("button", { name: /^This (Mac|PC|computer)/ });
  const remote = sidebar(page).getByRole("button", { name: /^other-mac/ });
  await expect(local.locator("small")).toHaveText("Claude Code, Codex");
  await expect(remote.locator("small")).toHaveText("Claude Code, Codex");
  await expect(remote.locator("small")).toHaveAttribute("title", "Claude Code 2.1.288, Codex 0.160.1");
  await expect(remote).not.toContainText(/darwin|hopsesh 0.4.0/);
  await expect(sidebar(page).getByRole("button", { name: /^empty-box/ }).locator("small")).toHaveText("No agents detected");
  await expect(sidebar(page).getByRole("button", { name: /^offline-box/ }).locator("small")).toHaveText("Not reachable");
  await remote.click();
  await expect(page.getByRole("heading", { name: "other-mac", exact: true })).toBeVisible();
});

test("lists sessions by repository, in one tree, with their agents and states", async ({ page }) => {
  await expect(page.getByRole("tree", { name: "Sessions" })).toHaveCount(1);
  await expect(group(page, "demo")).toHaveAttribute("aria-expanded", "true");
  await expect(group(page, "demo")).toHaveAttribute("aria-label", /^demo, 2 sessions/);
  await expect(row(page, "Find the codeword")).toBeVisible();
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-level", "2");
  await expect(row(page, "What is the codeword in notes.txt?")).toBeVisible();
  await expect(group(page, "Outside a git checkout")).toBeVisible();
  await expect(sidebar(page).getByRole("button", { name: /All sessions/ })).toContainText("4");
  // Places only: no agents, no "In the cloud".
  await expect(sidebar(page).getByRole("button", { name: /^CO?\s*Codex|^CC?\s*Claude Code/ })).toHaveCount(0);
  await expect(sidebar(page).getByRole("button", { name: /^This (Mac|PC|computer)/ })).toContainText("4");
  await expect(row(page, "Find the codeword").locator(".chip.st-ended")).toHaveText("Ended");
  await expect(page.locator("#list-count")).toHaveText("4 sessions");
});

test("the details pane: a status line, one action row, the conversation and the repository", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const d = details(page);
  await expect(d.getByRole("heading", { name: "Find the codeword" })).toBeVisible();
  await expect(d.locator(".ins-status")).toContainText(/Ended · this (Mac|PC|computer) · /);
  await expect(d.locator("#act-primary")).toHaveText(/^Resume in /);
  await expect(d.getByRole("button", { name: "Move", exact: true })).toBeVisible();
  await expect(d.getByRole("button", { name: "More actions" })).toBeVisible();
  await expect(d).toContainText("https://github.com/example/demo.git");
  // Move: enabled destinations only; an unavailable machine says why.
  await d.getByRole("button", { name: "Move", exact: true }).click();
  const move = page.getByRole("menu", { name: "Move" });
  await expect(move.getByRole("menuitem", { name: /^Continue with Codex…/ })).toBeEnabled();
  const send = move.getByRole("menuitem", { name: /^Send to another machine…/ });
  await expect(send).toHaveAttribute("aria-disabled", "true");
  await expect(send).toContainText("No other machine with hopsesh is reached");
  await expect(move.getByRole("menuitem", { name: /^Hand off to / })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await expect(move).toBeHidden();
  await expect(d.getByRole("button", { name: "Move", exact: true })).toBeFocused();
  // ⋯ holds the rest.
  await d.getByRole("button", { name: "More actions" }).click();
  for (const name of ["Open a shell in its folder", /^(Reveal in Finder|Show in Explorer|Show in Files)/, "Copy resume command", "Copy session ID", "Rename…", "Open transcript"]) {
    await expect(page.getByRole("menuitem", { name })).toBeEnabled();
  }
  await page.keyboard.press("Escape");
});

test("the conversation preview: the last exchange, its tool calls, and the transcript", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const conv = details(page).locator(".conv");
  await expect(conv.locator(".msg.user .msg-x")).toHaveText("What is the codeword in notes.txt?", { timeout: 30_000 });
  await expect(conv.locator(".msg-tools")).toHaveText("Ran 1 command");
  await expect(conv.locator(".msg.agent .msg-x")).toContainText("The codeword is PLUM-7");
  await expect(conv.locator(".msg.agent .msg-l")).toContainText("Claude");
  // Formatted messages, without executable HTML or image requests.
  expect(await conv.evaluate((el) => el.querySelectorAll("a, img, iframe, script").length)).toBe(0);
  await expect(conv.locator(".msg.user .markdown p")).toHaveText("What is the codeword in notes.txt?");
  await conv.getByRole("button", { name: "Open transcript" }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByText("Transcript · read-only")).toBeVisible();
  await expect(sheet).toContainText("The codeword is PLUM-7", { timeout: 30_000 });
  await sheet.getByRole("button", { name: "Close" }).click();
  // Off in Settings: no preview at all.
  await menu(page, "settings");
  await page.getByText("Show conversation previews").click();
  await menu(page, "sessions");
  await row(page, "Find the codeword").click();
  await expect(details(page).locator(".conv")).toHaveCount(0);
  await expect(details(page).getByText("Recent conversation")).toHaveCount(0);
});

test("the keyboard moves through the tree: rows and group headers, ← and → close and open", async ({ page }) => {
  await row(page, "What is the codeword in notes.txt?").click();
  await page.keyboard.press("ArrowDown");
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-selected", "true");
  await expect(row(page, "Find the codeword")).toBeFocused();
  await page.keyboard.press("ArrowUp");
  await expect(row(page, "What is the codeword in notes.txt?")).toHaveAttribute("aria-selected", "true");
  // ← from a row goes to its header, ← again closes the group, → opens it, → again goes in.
  await page.keyboard.press("ArrowLeft");
  const demo = group(page, "demo");
  await expect(demo).toBeFocused();
  await page.keyboard.press("ArrowLeft");
  await expect(demo).toHaveAttribute("aria-expanded", "false");
  await expect(row(page, "Find the codeword")).toHaveCount(0); // a closed group has no rows built
  await page.keyboard.press("ArrowRight");
  await expect(demo).toHaveAttribute("aria-expanded", "true");
  await page.keyboard.press("ArrowRight");
  await expect(row(page, "What is the codeword in notes.txt?")).toBeFocused();
  // ⌥← (Ctrl+← on Windows and Linux) closes every group, * opens them all.
  const mac = await page.evaluate(() => document.documentElement.dataset.os === "darwin");
  await page.keyboard.press(mac ? "Alt+ArrowLeft" : "Control+ArrowLeft");
  await expect(page.locator('.grp[aria-expanded="true"]')).toHaveCount(0);
  await page.keyboard.press("*");
  await expect(page.locator('.grp[aria-expanded="false"]')).toHaveCount(0);
  // Space on a header toggles it; letters go to a title.
  await page.locator(".grp").first().focus();
  await page.keyboard.press(" ");
  await expect(page.locator(".grp").first()).toHaveAttribute("aria-expanded", "false");
  await page.keyboard.press(" ");
  await page.keyboard.type("REA");
  await expect(row(page, "README cleanup")).toBeFocused();
});

test("places narrow the list, and a filter the place decides is not offered", async ({ page }) => {
  await sidebar(page).getByRole("button", { name: /^This (Mac|PC|computer)/ }).click();
  await expect(page.getByRole("heading", { name: /^This (Mac|PC|computer)$/ })).toBeVisible();
  await page.locator("#btn-filter").click();
  await expect(page.getByRole("menuitem", { name: /^Location/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await sidebar(page).getByRole("button", { name: /Needs you/ }).click();
  await expect(page.getByText("Nothing needs you right now.")).toBeVisible();
  await page.locator("#btn-filter").click();
  await expect(page.getByRole("menuitemcheckbox", { name: /^Working/ })).toHaveCount(0);
  await page.keyboard.press("Escape");
});

test("another place drops a selection it doesn't show", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await expect(details(page).getByRole("heading", { name: "Find the codeword" })).toBeVisible();
  await sidebar(page).getByRole("button", { name: /Needs you/ }).click();
  await expect(details(page)).toContainText("Select a session to see what you can do with it.");
  await sidebar(page).getByRole("button", { name: /All sessions/ }).click();
  await expect(row(page, "Find the codeword")).toHaveAttribute("aria-selected", "false");
});

test("the inspector's menus close on Escape, a click elsewhere and another place", async ({ page }) => {
  await row(page, "Find the codeword").click();
  const d = details(page);
  const move = page.getByRole("menu", { name: "Move" });
  const open = async () => { await d.getByRole("button", { name: "Move", exact: true }).click(); await expect(move).toBeVisible(); };
  await open();
  await expect(move.locator(".chip.cloud")).toHaveCount(0); // the clouds by name, ids only in tooltips
  expect((await move.boundingBox())!.width).toBeGreaterThanOrEqual(280);
  await page.keyboard.press("Escape");
  await expect(move).toBeHidden();
  await open();
  await page.getByRole("heading", { name: "All sessions" }).click();
  await expect(move).toBeHidden();
  await open();
  await sidebar(page).getByRole("button", { name: /^This (Mac|PC|computer)/ }).click();
  await expect(move).toBeHidden();
  // The primary's other places: arrows move, Enter picks.
  await row(page, "Find the codeword").click();
  await d.getByRole("button", { name: "Resume elsewhere" }).click();
  const places = page.getByRole("menu", { name: "Resume elsewhere" });
  await expect(places.getByRole("menuitem").first()).toBeFocused();
  await expect(places.getByRole("menuitem", { name: /^Resume in hopsesh Terminal.*Tab ends when hopsesh quits/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(places).toBeHidden();
});

test("a session open in Claude reports unsupported exact opening instead of silently showing the app", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=claude-desktop`)).ok()).toBeTruthy();
  await menu(page, "refresh");
  const r = row(page, "Find the codeword");
  await expect(r.getByRole("button", { name: "Show in Claude app" })).toBeVisible({ timeout: 30_000 });
  await expect(r.locator(".pchip")).toContainText("Claude app");
  await r.click();
  const d = details(page);
  await expect(d.locator(".ins-status")).toContainText("in Claude app");
  await expect(d).not.toContainText("The app opens on its last view");
  await expect(d.getByText("Open in", { exact: true })).toBeVisible();
  await expect(d.locator("#act-primary")).toBeDisabled();
  await expect(d).toContainText(process.platform === "linux" ? "Claude desktop opening requires macOS or Windows x64" : "Update Claude Code to 2.1.285");
  await page.locator("#btn-filter").click();
  await page.getByRole("menuitem", { name: /^Has/ }).click();
  await page.getByRole("menuitemcheckbox", { name: /^Open in a terminal tab/ }).click();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Escape");
  await expect(r).toHaveCount(0); // A desktop-app window is not a terminal tab.
});

test("a session open in a terminal is shown where it runs, never opened twice", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=cli`)).ok()).toBeTruthy();
  await menu(page, "refresh");
  const r = row(page, "Find the codeword");
  await expect(r.getByRole("button", { name: /^Show/ })).toBeVisible({ timeout: 30_000 });
  await expect(r.locator(".pchip")).toHaveCount(1);
  await r.click();
  await expect(details(page).locator("#act-primary")).toHaveText(/^Show/);
  await expect(details(page).locator(".ins-status")).toContainText(/^(Idle|Working) in /);
});

test("a session in a tab of the hopsesh Terminal: Show it, Move waits until it ends, and two places warn", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const [machine, k] = key!.split("\u0000");
  await page.evaluate(([machine, key]) => (window as any).__emit("hopsesh:terminal",
    { id: "t9", kind: "session", machine, key, title: "Find the codeword · claude", state: "running", attention: true }), [machine, k]);
  const r = row(page, "Find the codeword");
  await expect(r.locator(".pchip")).toContainText("1 tab");
  await expect(r.getByRole("button", { name: "Show in hopsesh Terminal" })).toBeVisible();
  await r.click();
  const d = details(page);
  await expect(d.locator("#act-primary")).toHaveText("Show in hopsesh Terminal");
  await expect(d.locator(".ins-status")).toContainText("Waiting for you in hopsesh Terminal");
  await expect(d.locator(".open-line")).toContainText("“Find the codeword · claude”");
  await d.getByRole("button", { name: "Move", exact: true }).click();
  const cont = page.getByRole("menuitem", { name: /^Continue with Codex…/ });
  await expect(cont).toHaveAttribute("aria-disabled", "true");
  await expect(cont).toContainText("End it in hopsesh Terminal first");
  await page.keyboard.press("Escape");
  // A second tab on the same session: open twice.
  await page.evaluate(([machine, key]) => (window as any).__emit("hopsesh:terminal",
    { id: "t10", kind: "session", machine, key, title: "Find the codeword · claude", state: "running" }), [machine, k]);
  await expect(r.locator(".chip.twice")).toHaveText("Open twice");
  await expect(d.locator(".act-caption")).toHaveText("Two copies writing one session can interleave; close one.");
});

test("a copy left behind offers the newer one, and resuming it anyway", async ({ page }) => {
  await row(page, "Feature flag").click();
  const d = details(page);
  await expect(d.locator(".ins-status")).toContainText(/^Moved to studio/);
  await expect(d.locator("#act-primary")).toHaveText("Go to the newer copy");
  await d.getByRole("button", { name: "Other choices" }).click();
  await expect(page.getByRole("menuitem", { name: /^Resume this copy anyway…/ })).toBeVisible();
  await page.keyboard.press("Escape");
});

test("filesystem notifications refresh session presence without client polling", async ({ page }) => {
  await page.goto("/");
  await expect(row(page, "Find the codeword")).toBeVisible({ timeout: 30_000 });
  await expect(row(page, "Find the codeword").locator(".chip.st-ended")).toBeVisible();
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=cli`)).ok()).toBeTruthy();

  await expect(row(page, "Find the codeword").locator(".chip.st-idle")).toBeVisible({ timeout: 30_000 });
});

test("one line of setup left, on All sessions only, closed for good with ✕", async ({ page }) => {
  const setup = page.getByRole("note", { name: "Finish setting up" });
  await expect(setup).toContainText(/Finish setting up \(\d of 3 done\):/);
  await expect(setup.getByRole("button", { name: "Add machines" })).toBeVisible();
  await expect(page.locator(".notice")).toHaveCount(0); // no setup cards
  await sidebar(page).getByRole("button", { name: /^This (Mac|PC|computer)/ }).click();
  await expect(setup).toHaveCount(0);
  await sidebar(page).getByRole("button", { name: /All sessions/ }).click();
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

test("the sidebar lists the clouds that are on, and a way to turn on another", async ({ page }) => {
  await expect(sidebar(page).getByRole("button", { name: /Jules|Devin|Amp|Copilot/ })).toHaveCount(0);
  await sidebar(page).getByRole("button", { name: "Turn on a cloud…" }).click();
  await expect(page.getByRole("heading", { name: "Clouds", exact: true })).toBeInViewport();
});

test("the palette finds a session and shows it; its action is on mod+Enter", async ({ page }) => {
  // Hidden behind a filter and a closed group, it still lands.
  await page.locator("#btn-filter").click();
  await page.getByRole("menuitemcheckbox", { name: /^Moved/ }).click();
  await page.keyboard.press("Escape");
  await expect(page.locator("#list-count")).toHaveText("1 of 4");
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("codeword notes");
  const item = page.locator(".pal-item").first();
  await expect(item).toContainText("demo"); // its repository
  await expect(item).toContainText(/(⌘↩|Ctrl\+Enter) Resume in /);
  await input.press("Enter");
  await expect(page.locator("#palette")).toBeHidden();
  const r = row(page, "What is the codeword in notes.txt?");
  await expect(r).toHaveAttribute("aria-selected", "true");
  await expect(r).toBeInViewport();
  await expect(page.locator("#list-count")).toHaveText("4 sessions");
  await expect(details(page).getByRole("heading", { name: "What is the codeword in notes.txt?" })).toBeVisible();
  // The palette lists the session's actions from all three menus.
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  await input.fill("codeword notes");
  await expect(page.locator(".pal-item", { hasText: "Continue with Claude Code…" })).toBeVisible();
  await expect(page.locator(".pal-item", { hasText: "Copy session ID" })).toBeVisible();
  await page.keyboard.press("Escape");
});

test("mod+Enter cannot bypass a disabled desktop action or run a different action", async ({ page }) => {
  const key = await row(page, "Find the codeword").getAttribute("data-key");
  const id = key!.split("\u0000")[1].split("/")[1];
  expect((await page.request.post(`/live?session=${id}&entrypoint=claude-desktop`)).ok()).toBeTruthy();
  await menu(page, "refresh");
  await expect(row(page, "Find the codeword").getByRole("button", { name: "Show in Claude app" })).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Search sessions or run a command" }).click();
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("Find the codeword");
  await input.press(process.platform === "darwin" ? "Meta+Enter" : "Control+Enter");
  await expect(input).toBeVisible();
  expect((await (await page.request.get("/terminal-test/links")).json()) as string[]).not.toContain("app:Claude");
});

test("rename: the agent's own title, undone from Activity", async ({ page }) => {
  await row(page, "Find the codeword").click();
  await action(page, "more", "Rename…");
  const input = page.getByLabel("Title");
  await expect(input).toHaveValue("Find the codeword");
  await input.fill("Find the secret word");
  await page.locator("#dlg").getByRole("button", { name: "Rename" }).click();
  await expect(row(page, "Find the secret word")).toBeVisible({ timeout: 30_000 });
  await menu(page, "activity");
  const it = page.locator(".line-item", { hasText: "Find the secret word" });
  await expect(it).toContainText("Renamed");
  await it.getByRole("button", { name: /^Undo/ }).click();
  await expect(it.getByText("Undone")).toBeVisible();
  await menu(page, "refresh");
  await expect(row(page, "Find the codeword")).toBeVisible({ timeout: 30_000 });
});
