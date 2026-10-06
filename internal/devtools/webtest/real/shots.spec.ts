import { test, expect, chromium, type Browser, type CDPSession, type Page } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { port } from "./setup";

// Screenshots of the real Windows window for the README and the product pages, on
// demoseed's made-up laptop (HOPSESH_DEMO_WORLD=laptop) with its studio in WSL2 over SSH
// (scripts/windows-demo-studio.ps1): 1280×800 at 100% and 200%, light and dark. Runs
// only when SHOTS_DIR is set.
const dir = process.env.SHOTS_DIR || "";
const looks = [
  { theme: "light", scale: 1 }, { theme: "light", scale: 2 },
  { theme: "dark", scale: 1 }, { theme: "dark", scale: 2 },
];

let browser: Browser;
let page: Page;
let cdp: CDPSession;

test.skip(!dir, "set SHOTS_DIR to save screenshots");

test.beforeAll(async () => {
  browser = await chromium.connectOverCDP(`http://127.0.0.1:${port}`);
  const pages = browser.contexts().flatMap((c) => c.pages());
  page = pages.find((p) => URL.canParse(p.url()) && new URL(p.url()).host === "wails.localhost") || pages[0];
  cdp = await page.context().newCDPSession(page);
  mkdirSync(dir, { recursive: true });
});

test.afterAll(async () => { await browser?.close(); });

const row = (title: string) => page.locator(".row").filter({ has: page.locator(".t").getByText(title, { exact: true }) });

async function shoot(name: string) {
  for (const l of looks) {
    await cdp.send("Emulation.setDeviceMetricsOverride", { width: 1280, height: 800, deviceScaleFactor: l.scale, mobile: false });
    await cdp.send("Emulation.setEmulatedMedia", { features: [{ name: "prefers-color-scheme", value: l.theme }] });
    await page.waitForTimeout(400);
    // Through CDP itself: Playwright's own screenshot ignores the emulated scale here.
    const { data } = await cdp.send("Page.captureScreenshot", { format: "png" });
    writeFileSync(join(dir, `${name}-${l.theme}@${l.scale}x.png`), Buffer.from(data, "base64"));
  }
  await cdp.send("Emulation.clearDeviceMetricsOverride");
  await cdp.send("Emulation.setEmulatedMedia", { features: [] });
}

test("screenshots of the real window", async () => {
  test.setTimeout(300_000);
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 60_000 });
  await expect(row("Fix flaky checkout tests")).toBeVisible({ timeout: 90_000 }); // studio, over SSH
  await expect(row("Storybook stories for the header")).toBeVisible();
  await row("Fix flaky checkout tests").click();
  await expect(page.getByRole("complementary", { name: "Session details" })).toBeVisible();
  await shoot("01-sessions");

  await page.keyboard.press("Control+3");
  await expect(page.getByRole("heading", { name: "Machines", exact: true })).toBeVisible();
  await shoot("05-machines");

  await page.keyboard.press("Control+,");
  await page.getByRole("tab", { name: "Updates" }).click();
  await expect(page.getByRole("button", { name: "Install and restart" })).toBeVisible();
  await shoot("06-settings-updates");
  await page.getByRole("tab", { name: "Agents" }).click();
  await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
  await shoot("07-settings-agents");

  await page.keyboard.press("Control+1");
  await page.keyboard.press("Control+K");
  const input = page.getByRole("combobox", { name: "Search sessions or run a command" });
  await input.fill("checkout");
  await expect(page.locator(".pal-item", { hasText: "Continue with" }).first()).toBeVisible();
  await shoot("08-palette");
  await input.press("Escape");

  await row("Fix flaky checkout tests").click();
  await page.getByRole("complementary", { name: "Session details" }).getByRole("button", { name: "Move", exact: true }).click();
  await page.getByRole("menuitem", { name: /^Continue with Codex…/ }).click();
  const sheet = page.locator("#sheet");
  await expect(sheet.getByRole("heading", { name: "Continue “Fix flaky checkout tests” in Codex" })).toBeVisible({ timeout: 60_000 });
  await shoot("02-continue-plan");
  await sheet.getByRole("button", { name: /Continue in Codex/ }).click();
  await expect(page.getByRole("heading", { name: "“Fix flaky checkout tests” continues in Codex" })).toBeVisible({ timeout: 60_000 });
  await shoot("03-continue-result");

  await page.keyboard.press("Control+2");
  await expect(page.getByRole("heading", { name: "Activity" })).toBeVisible();
  await shoot("04-activity");
});
