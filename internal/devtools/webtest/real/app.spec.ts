import { test, expect, chromium, type Browser, type Page } from "@playwright/test";
import { port } from "./setup";

// The real Windows app: WebView2, the Wails runtime and the Go service, on a demo home.
let browser: Browser;
let page: Page;

test.beforeAll(async () => {
  browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
  const pages = browser.contexts().flatMap((c) => c.pages());
  page = pages.find((p) => URL.canParse(p.url()) && new URL(p.url()).host === "wails.localhost" && ["/", "/index.html"].includes(new URL(p.url()).pathname)) || pages[0];
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 45_000 });
});

test.afterAll(async () => { await browser?.close(); });

const row = (title: string) => page.locator(".row").filter({ has: page.locator(".t").getByText(title, { exact: true }) });

test("the window lists the demo sessions through the real service", async () => {
  await expect(row("Find the codeword")).toBeVisible();
  await expect(row("What is the codeword in notes.txt?")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.dataset.os)).toBe("windows");
});

test("it speaks Windows: this PC and Ctrl shortcuts", async () => {
  const sidebar = page.getByRole("navigation", { name: "Places" });
  await expect(sidebar.getByRole("button", { name: /^This PC/ })).toBeVisible();
  await expect(page.locator(".titlebar .kbd")).toHaveText("Ctrl+K");
  await page.keyboard.press("Control+K");
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await expect(input).toBeVisible();
  await input.press("Escape");
  await page.keyboard.press("Control+3");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();
  await expect(page.getByText(/This PC/).first()).toBeVisible();
  await page.keyboard.press("Control+1");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
});

test("the details pane and settings work against the real service", async () => {
  await row("Find the codeword").click();
  const details = page.getByRole("complementary", { name: "Session details" });
  await expect(details.locator("#act-primary")).toHaveText(/^Resume in /);
  await details.getByRole("button", { name: "Move", exact: true }).click();
  await expect(page.getByRole("menuitem", { name: /^Continue with Codex…/ })).toBeVisible();
  await page.keyboard.press("Escape");
  await page.keyboard.press("Control+,");
  await page.getByRole("tab", { name: "Command line" }).click();
  await expect(page.getByText(/Puts the app's folder on your PATH/)).toBeVisible();
  await page.getByRole("tab", { name: "Agents" }).click();
  await expect(page.locator(".cap", { hasText: "continues in other agents" }).first()).toBeVisible();
});

// Native WebView2 and Win32 mode changes; preserve real login registration.
test("desktop placement and the native Quick access window", async () => {
  const call = (name: string, ...args: unknown[]) => page.evaluate(async ({name,args}) => {
    // @ts-expect-error runtime module is supplied by the native app
    const {Call} = await import('/wails/runtime.js');
    return Call.ByName('github.com/roeehrl/hopsesh/internal/ui/gui.App.'+name,...args);
  }, {name,args});
  const initial = await call('DesktopSettings');
  expect(initial.capabilities).toMatchObject({tray:true,hideApp:true});
  const input={close:'keep',attention:true,previews:true,login:initial.login};
  try {
    for(const mode of ['app','both','tray']) {
      await call('SaveDesktop',{...input,mode});
      expect((await call('DesktopSettings')).effective).toBe(mode);
    }
    await call('QuickShow');
    const quick=browser.contexts().flatMap(c=>c.pages()).find(p=>new URL(p.url()).pathname==='/quick.html');
    expect(quick).toBeTruthy();
    await quick!.getByRole('button',{name:'Recent',exact:true}).click();
    await quick!.locator('.quick-row').first().click();
    await expect(quick!.locator('.quick-message').first()).toBeVisible();
    await quick!.getByRole('button',{name:'Open session details',exact:true}).click();
    await expect(page.locator('.row[aria-selected="true"]')).toBeVisible();
  } finally { await call('SaveDesktop',{...input,mode:'app',close:'quit'}); }
});
