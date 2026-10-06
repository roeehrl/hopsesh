import { test, expect, chromium, type Browser, type Page } from "@playwright/test";
import { port } from "./setup";

// The real hopsesh Terminal window on Windows: WebView2, xterm.js, the bundled ConPTY and
// the Go service. A shell opened from the app's window runs in a tab there; the test types
// in it as the user and ends it.
let browser: Browser;
let page: Page;

test.beforeAll(async () => {
  browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
  const pages = browser.contexts().flatMap((c) => c.pages());
  page = pages.find((p) => URL.canParse(p.url()) && new URL(p.url()).host === "wails.localhost" && ["/", "/index.html"].includes(new URL(p.url()).pathname) && !new URL(p.url()).pathname.startsWith("/terminal/")) || pages[0];
  // The app's window may show another screen (the tests before this one end in Settings).
  await expect(page.locator("#view")).not.toBeEmpty({ timeout: 45_000 });
  await page.keyboard.press("Control+1");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 45_000 });
});

test.afterAll(async () => { await browser?.close(); });

// terminalPage waits for the terminal window's page to show up over CDP.
async function terminalPage(): Promise<Page> {
  for (let i = 0; i < 60; i++) {
    const t = browser.contexts().flatMap((c) => c.pages()).find((p) => URL.canParse(p.url()) && new URL(p.url()).pathname.startsWith("/terminal/"));
    if (t) return t;
    await new Promise((r) => setTimeout(r, 500));
  }
  throw new Error("the terminal window never opened");
}

test("Open a shell in its folder opens the hopsesh Terminal window with a tab that runs PowerShell", async () => {
  const row = page.locator(".row").filter({ has: page.locator(".t").getByText("Find the codeword", { exact: true }) });
  await row.click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.locator("#act-primary")).toHaveText(/^Resume in /);
  await details.getByRole("button", { name: "More actions" }).click();
  await page.getByRole("menuitem", { name: "Open a shell in its folder" }).click();

  const t = await terminalPage();
  const tab = t.getByRole("tablist", { name: "Terminal tabs" }).getByRole("tab", { name: /^shell · / });
  await expect(tab).toBeVisible({ timeout: 30_000 });
  await expect(t.locator("#lock")).toHaveText("Not recorded");
  await expect(t.locator("#runs")).toContainText(/Your login shell (pwsh|powershell)/);
  const screen = () => t.evaluate(() => (window as any).hopseshTerminal.text());
  await expect.poll(screen, { timeout: 30_000 }).toMatch(/PS [A-Z]:\\/);
  await t.locator(".term:not([hidden]) .xterm-helper-textarea").focus();
  await t.keyboard.type("Write-Output ('hops' + 'esh-' + 42); exit 7");
  await t.keyboard.press("Enter");
  await expect.poll(screen, { timeout: 30_000 }).toContain("hopsesh-42");
  await expect(tab).toContainText("exited 7", { timeout: 30_000 });
  await expect(t.locator("#exitbar")).toContainText("exited with code 7");

  // The app's window counts the tab, and closing it there ends it.
  await expect(page.locator("#btn-terminal")).toBeVisible();
  await t.locator("#exitbar").getByRole("button", { name: "Close tab" }).click();
  await expect(tab).toBeHidden();
  await expect(t.getByText("No programs are running here")).toBeVisible();
});
