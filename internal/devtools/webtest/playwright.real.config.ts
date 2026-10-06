import { defineConfig } from "@playwright/test";

// The real app's window: real/setup.ts starts the test build of hopsesh-app (-tags e2e,
// path in HOPSESH_APP_EXE) on a demo home with WebView2's debugging port open, and the
// tests drive that window over the Chrome DevTools Protocol. Windows only.
const screenshots = Boolean(process.env.SHOTS_DIR);
const outputDir = screenshots ? "test-results-shots" : "test-results";

export default defineConfig({
  outputDir,
  testDir: "real",
  globalSetup: "./real/setup.ts",
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  timeout: 60_000,
  reporter: process.env.CI ? [["github"], ["junit", { outputFile: `${outputDir}/junit-${screenshots ? "shots" : "real"}.xml` }], ["html", { open: "never", outputFolder: screenshots ? "playwright-report-shots" : "playwright-report-real" }]] : "list",
});
