import { defineConfig, devices } from "@playwright/test";
import { tmpdir } from "node:os";
import { join } from "node:path";

// One server for the run (the real service on a demo home); each test file resets it.
const port = Number(process.env.WEBTEST_PORT || 8765);
const home = join(tmpdir(), `hopsesh-webtest-${process.pid}`);

export default defineConfig({
  testDir: "tests",
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [["github"], ["junit", { outputFile: "test-results/junit.xml" }], ["html", { open: "never" }]] : "list",
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    viewport: { width: 1280, height: 820 },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    { name: "chromium", use: { ...devices["Desktop Chrome"], viewport: { width: 1280, height: 820 } } },
    { name: "webkit", use: { ...devices["Desktop Safari"], viewport: { width: 1280, height: 820 } } },
  ],
  webServer: {
    command: `go run ./internal/devtools/webtest -addr 127.0.0.1:${port} -home ${JSON.stringify(home)}`,
    cwd: "../../..",
    url: `http://127.0.0.1:${port}/`,
    timeout: 180_000,
    reuseExistingServer: false,
  },
});
