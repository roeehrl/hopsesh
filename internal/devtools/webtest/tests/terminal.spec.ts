import { test, expect, type Page, type BrowserContext } from "@playwright/test";
import { row } from "./helpers";

// The hopsesh Terminal window: the real page (under its content security policy) with
// xterm.js, its streams to the real service, and tabs running termfake
// (internal/testkit/termfake), the stand-in Claude Code and the stand-in clouds. The tests
// type as the user would; hopsesh itself never does.

// here starts from a new demo home where sessions and steps open in the app's own terminal.
async function here(page: Page, says?: string) {
  const r = await page.request.post("/reset?terminal=here" + (says !== undefined ? "&says=" + encodeURIComponent(says) : ""));
  expect(r.ok(), await r.text()).toBeTruthy();
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30_000 });
}

// openFake opens a tab running termfake, as one of the app's entry points does.
async function openFake(page: Page, q = ""): Promise<{ id: string }> {
  const r = await page.request.post("/terminal-test/open?" + q);
  expect(r.ok(), await r.text()).toBeTruthy();
  return r.json();
}

// terminal opens the terminal window's page (the DOM renderer, so the rows can be read).
async function terminal(context: BrowserContext): Promise<Page> {
  const t = await context.newPage();
  await t.goto("/terminal/?renderer=dom");
  return t;
}

const screen = (t: Page) => t.evaluate(() => (window as any).hopseshTerminal.text());
const tab = (t: Page, title: string | RegExp) => t.getByRole("tablist", { name: "Terminal tabs" }).getByRole("tab", { name: title });

async function focusTerminal(t: Page) {
  await t.locator(".term:not([hidden]) .xterm-helper-textarea").focus();
}

async function ready(t: Page) {
  await expect.poll(() => screen(t), { timeout: 20_000 }).toContain("termfake: ready");
}

test.beforeEach(async ({ page }) => here(page));

test("a tab runs its program: the emulator answers DA1 and DA2, and keys, Shift+Return and IME text reach it", async ({ page, context }) => {
  await openFake(page, "title=" + encodeURIComponent("Fix the parser · termfake"));
  const t = await terminal(context);
  await expect(tab(t, /Fix the parser/)).toBeVisible();
  await ready(t);
  const text = await screen(t);
  expect(text).toMatch(/da1="\\x1b\[\?[0-9;]+c"/);
  expect(text).toMatch(/da2="\\x1b\[>[0-9;]+c"/);
  await expect(tab(t, /Fix the parser/)).toContainText(/running|idle/);
  await expect(t.locator("#runs")).toContainText("Runs termfake in");

  await focusTerminal(t);
  await t.keyboard.type("hello there");
  await t.keyboard.press("Enter");
  await expect.poll(() => screen(t)).toContain("typed=hello there");
  await t.keyboard.press("Shift+Enter");
  await expect.poll(() => screen(t)).toContain("newline");
  // Text an input method commits (as for Chinese, Japanese or Korean) arrives as typed.
  await t.keyboard.insertText("日本語");
  await t.keyboard.press("Enter");
  await expect.poll(() => screen(t)).toContain("typed=日本語");
});

test("the window's size reaches the program, and exit codes show on the tab", async ({ page, context }) => {
  await openFake(page);
  const t = await terminal(context);
  await ready(t);
  await t.setViewportSize({ width: 900, height: 520 });
  await expect.poll(async () => (await t.evaluate(() => (window as any).hopseshTerminal.size())).cols).toBeLessThan(120);
  const size = await t.evaluate(() => (window as any).hopseshTerminal.size());
  await focusTerminal(t);
  await t.keyboard.press("S");
  await expect.poll(() => screen(t)).toContain(`size=${size.cols}x${size.rows}`);

  await t.keyboard.press("3");
  await expect(tab(t, /termfake/)).toContainText("exited 3");
  const bar = t.locator("#exitbar");
  await expect(bar).toContainText("termfake exited with code 3");
  await expect(bar).toContainText("The output stays until you close the tab.");
  await bar.getByRole("button", { name: "Run again" }).click();
  await expect(tab(t, /termfake/)).toContainText(/starting|running|idle/);
  await ready(t);
  await focusTerminal(t);
  await t.keyboard.press("0");
  await expect(tab(t, /termfake/)).toContainText("exited 0");
  await expect.poll(() => screen(t)).toContain("[program exited with code 0]");
  await expect(bar).toBeHidden();
});

test("a bell makes the tab wait for you, in the window and in the app's Needs you", async ({ page, context }) => {
  await openFake(page, "title=" + encodeURIComponent("Invoice PDF · termfake"));
  const t = await terminal(context);
  await ready(t);
  await focusTerminal(t);
  await t.keyboard.press("B");
  await expect(tab(t, /Invoice PDF/)).toContainText("waiting for you");
  await expect(tab(t, /Invoice PDF/).locator(".wdot")).toBeVisible();
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await expect(sidebar.getByRole("button", { name: /Needs you/ })).toContainText("1");
  await expect(sidebar.getByRole("button", { name: /Terminal/ })).toContainText("1 waiting");
  await expect(page.getByRole("status").filter({ hasText: "“Invoice PDF · termfake” is waiting for you" })).toBeVisible();
  // Typing in the tab is the answer.
  await t.keyboard.type("x");
  await expect(tab(t, /Invoice PDF/)).not.toContainText("waiting for you");
});

test("links: a web link goes to hopsesh's confirmation, an app's link is blocked here", async ({ page, context }) => {
  await openFake(page);
  const t = await terminal(context);
  await ready(t);
  await focusTerminal(t);
  await t.keyboard.press("L");
  await expect.poll(() => screen(t)).toContain("https://example.com/hopsesh-test?x=1");
  await clickText(t, "https://example.com/hopsesh-test");
  await expect.poll(async () => (await page.request.get("/terminal-test/links")).json()).toEqual(["https://example.com/hopsesh-test?x=1"]);

  await t.keyboard.press("O");
  await expect.poll(() => screen(t)).toContain("an app link");
  await clickText(t, "an app link");
  const dlg = t.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "This link can't open from a terminal" })).toBeVisible();
  await expect(dlg).toContainText("vscode://file/etc/passwd");
  await expect(dlg).toContainText("Only web links (http and https) open from hopsesh's tabs.");
  await dlg.getByRole("button", { name: "OK" }).click();
  expect(await (await page.request.get("/terminal-test/links")).json()).toEqual(["https://example.com/hopsesh-test?x=1"]);
});

// clickText clicks a few characters into some text on the active tab's screen.
async function clickText(t: Page, text: string) {
  const find = () => t.evaluate((s) => {
    const term = document.querySelector(".term:not([hidden])")!;
    const rows = [...term.querySelectorAll(".xterm-rows > div")];
    const screen = term.querySelector(".xterm-screen")!.getBoundingClientRect();
    const size = (window as any).hopseshTerminal.size();
    const cw = screen.width / size.cols;
    for (const r of rows) {
      const i = (r.textContent || "").replace(/\u00a0/g, " ").indexOf(s);
      if (i >= 0) {
        const b = r.getBoundingClientRect();
        return { x: screen.left + (i + 3) * cw + cw / 2, y: b.top + b.height / 2 };
      }
    }
    return null;
  }, text);
  await expect.poll(find).not.toBeNull(); // drawn on the next frame
  const at = await find();
  await t.mouse.move(at!.x, at!.y);
  await t.mouse.move(at!.x + 1, at!.y);
  await t.mouse.click(at!.x + 1, at!.y);
}

test("a sign-in tab records nothing and says so; the cloud card checks the login after it", async ({ page, context }) => {
  const t = await terminal(context);
  // From the Machines page: Claude Code cloud, allowed, has its own sign-in command.
  await page.evaluate(() => (window as any).__emit("hopsesh:menu", "machines"));
  const card = page.locator(".cloud-card", { hasText: "Claude Code cloud" });
  await card.getByRole("switch", { name: "Allow Claude Code cloud" }).click();
  await expect(card).toContainText("Signing in runs claude auth login");
  await card.getByRole("button", { name: /^Sign in( again)?$/ }).click();
  const signIn = tab(t, /Sign in · Claude Code cloud/);
  await expect(signIn).toBeVisible();
  await expect(signIn).toHaveAccessibleName(/not recorded/);
  await expect(t.locator("#lock")).toBeVisible();
  await expect(t.locator("#lock")).toHaveText("Not recorded");
  await expect(t.locator("#banner")).toContainText("Nothing in a sign-in tab is recorded. hopsesh doesn't watch, match or keep what appears here");
  await expect(t.locator("#runs")).toContainText("Runs claude auth login · your browser opens the sign-in page; hopsesh never sees it");
  // The stand-in claude signs in at once: the tab ends with code 0, and the card checks.
  await expect(signIn).toContainText("exited 0");
  await expect.poll(() => screen(t)).toContain("this tab closes in 10 s");
  await expect(page.locator("#toast")).toContainText("Claude Code cloud: signed in. hopsesh checked with claude.");
  await expect(signIn).toBeHidden({ timeout: 20_000 });
});

test("a shell tab: the login shell in the session's folder, not recorded", async ({ page, context }) => {
  const t = await terminal(context);
  await row(page, "Find the codeword").click();
  await page.getByRole("complementary", { name: "Session details" }).getByRole("button", { name: "Open a shell here" }).click();
  const shell = tab(t, /^shell · demo/);
  await expect(shell).toBeVisible();
  await expect(t.locator("#lock")).toHaveText("Not recorded");
  await expect(t.locator("#runs")).toContainText("Your login shell");
  await focusTerminal(t);
  await t.keyboard.type("pwd; exit 4");
  await t.keyboard.press("Enter");
  await expect(shell).toContainText("exited 4");
  await expect.poll(() => screen(t)).toMatch(/git[\\/]demo/);
});

test("Resume here opens the session in a tab; Open in my terminal moves it out after asking", async ({ page, context }) => {
  const t = await terminal(context);
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.getByRole("button", { name: /^Resume here/ })).toBeVisible();
  await expect(details).toContainText("Resume here runs it in a tab of the hopsesh Terminal window; it ends when hopsesh quits.");
  await details.getByRole("button", { name: /^Resume here/ }).click();
  const resumed = tab(t, /Find the codeword · claude/);
  await expect(resumed).toBeVisible();
  await expect(t.locator("#runs")).toContainText(/Runs claude --resume \S+ in/);
  // The stand-in claude ends at once; the tab keeps its output.
  await expect(resumed).toContainText("exited 0");
  await t.locator("#t-external").click();
  const dlg = t.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: /Move “Find the codeword · claude” to / })).toBeVisible();
  await dlg.getByRole("button", { name: /^Open in / }).click();
  await expect(resumed).toBeHidden();
});

test("the hand-off step runs in a tab: the trust question's banner, answered by the user, then the link", async ({ page, context }) => {
  const t = await terminal(context);
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.locator(".side-off", { hasText: "Claude Code cloud" }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
  await sidebar.getByRole("button", { name: /All sessions/ }).click();
  await row(page, "Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await details.getByRole("button", { name: "Hand off ▸" }).click();
  await details.getByRole("menu", { name: "Hand off to" }).getByRole("menuitem", { name: /Claude Code cloud/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Hand off “Find the codeword” to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await sheet.getByRole("button", { name: /^Hand off/ }).click();
  const box = sheet.getByRole("group", { name: "In the hopsesh Terminal" });
  await expect(box).toContainText("Claude Code cloud is starting the session in the hopsesh Terminal window", { timeout: 30_000 });
  await expect(box.getByRole("button", { name: "Show the terminal" })).toBeVisible();

  const step = tab(t, /Hand off · Find the codeword/);
  await expect(step).toBeVisible();
  await expect(step).toContainText("waiting for you");
  await expect(t.locator("#banner")).toHaveText("Claude Code is asking whether it trusts hopsesh's hand-off folder. Answer it here; hopsesh never answers for you.", { timeout: 20_000 });
  await expect(t.locator("#note")).toHaveText("hopsesh reads this tab only for the session link. It never types here.");
  // The user answers (the test types as the user; hopsesh never does).
  await focusTerminal(t);
  await t.keyboard.press("Enter");
  await expect(page.getByRole("heading", { name: "Handed off to Claude Code cloud" })).toBeVisible({ timeout: 30_000 });
  await expect(step).toContainText("exited 0");
  await expect(t.locator("#banner")).toContainText("Session link captured: claude.ai/code/session_");
  await expect(t.locator("#banner").getByRole("button", { name: "Copy link" })).toBeVisible();
});

test("bringing a session back runs Claude Code's teleport in a tab with what to do", async ({ page, context }) => {
  await here(page, ""); // the stand-in's user has not sent anything yet
  const t = await terminal(context);
  const id = (await (await page.request.post("/cloud")).json()).id;
  const sidebar = page.getByRole("navigation", { name: "Scopes" });
  await sidebar.locator(".side-off", { hasText: "Claude Code cloud" }).getByRole("button", { name: "Turn on" }).click();
  await expect(sidebar.getByRole("button", { name: /Claude Code cloud/ })).toContainText("ready", { timeout: 30_000 });
  await page.getByRole("button", { name: "Paste a link…" }).click();
  await page.getByLabel("The session's link or id").fill(`https://claude.ai/code/${id}`);
  await page.locator("#dlg").getByRole("button", { name: "Add" }).click();
  await expect(row(page, `Session ${id}`)).toBeVisible({ timeout: 30_000 });
  await page.getByRole("complementary", { name: "Cloud session details" }).getByRole("button", { name: /Bring here \(Claude Code\)/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("button", { name: /^Bring here in / })).toBeVisible({ timeout: 30_000 });
  await expect(sheet).toContainText("Runs in a tab of the hopsesh Terminal window");
  await sheet.getByRole("button", { name: /^Bring here⌘|^Bring here$|^Bring here\s*(⌘↩|Ctrl\+Enter)$/ }).click();

  const bring = tab(t, /Bring here · Session/);
  await expect(bring).toBeVisible({ timeout: 30_000 });
  await expect(bring).toContainText("waiting for you");
  await expect(t.locator("#banner")).toHaveText("Send one message, then type /exit — Claude Code saves the copy only after you continue it.");
  await expect(page.locator(".waiting")).toContainText("It's running in a tab of the hopsesh Terminal window");
  await focusTerminal(t);
  await t.keyboard.type("carry on here");
  await t.keyboard.press("Enter");
  await expect(bring).toContainText("exited 0");
  await expect(page.locator(".outcome.ok")).toBeVisible({ timeout: 60_000 });
});

test("Settings → Terminal: where things open, the look, and the fixed safety lines", async ({ page }) => {
  await page.evaluate(() => (window as any).__emit("hopsesh:menu", "settings"));
  await page.getByRole("tab", { name: "Terminal" }).click();
  const where = page.getByRole("radiogroup", { name: "Where sessions open" });
  await expect(where.getByRole("radio", { name: "In this window" })).toHaveAttribute("aria-checked", "true");
  await where.getByRole("radio", { name: "Ask each time" }).click();
  await expect(where.getByRole("radio", { name: "Ask each time" })).toHaveAttribute("aria-checked", "true");
  await page.getByLabel("Scrollback").selectOption("10000");
  await page.getByLabel("Font size").selectOption("15");
  await expect(page.getByLabel("Scrollback")).toHaveValue("10000");
  await expect(page.getByText("Kept in memory only, and gone when the tab closes.")).toBeVisible();
  await expect(page.getByText("Programs can never read your clipboard.")).toBeVisible();
  await expect(page.getByText("hopsesh never types into a program.")).toBeVisible();
  await expect(page.getByText("Sign-in and shell tabs are never recorded.")).toBeVisible();
  await expect(page.getByText("Keep tabs when the window closes")).toBeVisible();
  const r = await page.request.post("/call", { data: { m: "TerminalSettings", args: [] } });
  const s = (await r.json()).result;
  expect([s.where, s.scrollback, s.fontSize]).toEqual(["ask", 10000, 15]);

  // With Ask, resuming asks first.
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await row(page, "Find the codeword").click();
  await page.getByRole("complementary", { name: "Session details" }).getByRole("button", { name: /^Resume…/ }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("button", { name: "In this window" })).toBeVisible();
  await dlg.getByRole("button", { name: "Cancel" }).click();
});

test("quitting with a program running asks first, lists it, and ends it", async ({ page, context }) => {
  await openFake(page, "title=" + encodeURIComponent("Tidy request logging · termfake"));
  const t = await terminal(context);
  await ready(t);
  const r = await page.request.post("/call", { data: { m: "ShouldQuit", args: [] } });
  expect((await r.json()).result).toBe(false);
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "Quit hopsesh and end 1 program?" })).toBeVisible();
  await expect(dlg).toContainText("Quitting ends every program in hopsesh's tabs.");
  await expect(dlg.getByRole("list", { name: "Programs running in tabs" })).toContainText("Tidy request logging · termfake");
  await expect(dlg.getByRole("button", { name: "Show the tabs" })).toBeVisible();
  await dlg.getByRole("button", { name: "Quit and end 1 program" }).click();
  await expect(tab(t, /Tidy request logging/)).toBeHidden();
  await expect(t.getByText("No programs are running here")).toBeVisible();
});

// A session opened in the user's terminal app that can report its end (iTerm2 with its
// Python API): the window says how it ended, beside the session.
test("a session's run in the user's terminal app shows how it ended", async ({ page }) => {
  await page.getByRole("button", { name: /All sessions/ }).click();
  const r = row(page, "Find the codeword");
  const key = await r.getAttribute("data-key");
  const [machine, k] = key!.split("\u0000");
  await page.evaluate(([machine, key]) => (window as any).__emit("hopsesh:external-exit",
    { kind: "session", machine, key, title: "Find the codeword", terminal: "iTerm2", code: 3, closed: false, at: new Date().toISOString() }), [machine, k]);
  await expect(page.locator("#toast")).toContainText("“Find the codeword” in iTerm2: exited with code 3");
  await expect(r.locator(".chip", { hasText: "exited 3 in iTerm2" })).toBeVisible();
  await page.evaluate(([machine, key]) => (window as any).__emit("hopsesh:external-exit",
    { kind: "session", machine, key, title: "Find the codeword", terminal: "iTerm2", code: -1, closed: true, at: new Date().toISOString() }), [machine, k]);
  await expect(r.locator(".chip", { hasText: "closed in iTerm2" })).toBeVisible();
});
