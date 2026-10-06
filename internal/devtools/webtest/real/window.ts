import { test, expect, type Browser, type BrowserContext, type Page } from "@playwright/test";

// CDP can answer before the main webview has navigated. Never fall back to the
// first page: that can be Quick access or a terminal instead of the main window.
export async function mainWindow(browser: Browser): Promise<Page> {
  let main: Page | undefined;
  await expect.poll(() => {
    main = browser.contexts().flatMap(c => c.pages()).find(p => {
      if (!URL.canParse(p.url())) return false;
      const url = new URL(p.url());
      return url.host === "wails.localhost" && ["/", "/index.html"].includes(url.pathname);
    });
    return Boolean(main);
  }, { timeout: 30_000, message: "The native main window must finish its initial navigation" }).toBe(true);
  main!.context().setDefaultTimeout(30_000);
  return main!;
}

// These pages come from connectOverCDP, not Playwright's page fixture, so fixture
// screenshot/trace options do not apply. Record the real native context explicitly.
export function nativeDiagnostics(getBrowser: () => Browser) {
  let traced: BrowserContext[] = [];
  test.beforeEach(async () => {
    traced = [];
    for (const context of getBrowser().contexts()) {
      await context.tracing.start({ screenshots: true, snapshots: true, sources: true });
      traced.push(context);
    }
  });
  test.afterEach(async ({}, info) => {
    const failed = info.status !== info.expectedStatus;
    for (const [i, context] of traced.entries()) {
      if (failed) {
        for (const [j, page] of context.pages().entries()) {
          const path = info.outputPath(`window-${i}-${j}.png`);
          try {
            await page.screenshot({ path, timeout: 5_000 });
            await info.attach(`Window ${i}-${j}`, { path, contentType: "image/png" });
          } catch { /* A crashed or closed webview cannot supply a screenshot. */ }
        }
      }
      const path = failed ? info.outputPath(`trace-${i}.zip`) : undefined;
      await context.tracing.stop({ path });
      if (path) await info.attach(`Native trace ${i}`, { path, contentType: "application/zip" });
    }
  });
}
