import { test, expect } from "@playwright/test";

// Intentionally avoid fresh(): these tests assert the window before its first scan.
test.beforeEach(async ({ page }) => {
  expect((await page.request.post("/reset")).ok()).toBeTruthy();
});

test("startup is visible before the app module loads, with a slow-start recovery", async ({ page }) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/app.js", async (route) => { await gate; await route.continue(); });
  await page.clock.install();
  await page.goto("/", { waitUntil: "domcontentloaded" });
  await expect(page.getByRole("heading", { name: "Opening hopsesh…" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Search sessions or run a command" })).toBeDisabled();
  await page.clock.fastForward(15001);
  await expect(page.getByRole("button", { name: "Reload window" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Still loading" })).toBeVisible();
  release();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30000 });
  await expect(page.getByRole("button", { name: "Search sessions or run a command" })).toBeEnabled();
});

test("slow initial scan shows what is happening instead of an empty window", async ({ page }) => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => { release = resolve; });
  await page.route("**/call", async (route) => {
    if (route.request().postDataJSON().m === "InitialScan") await gate;
    await route.continue();
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Search sessions or run a command" })).toBeEnabled();
  await expect(page.locator("#fresh")).toContainText("Finding sessions");
  release();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30000 });
});

for (const target of ["module", "bridge"]) {
  test(`startup ${target} failure is visible and reload recovers`, async ({ page }) => {
    let failed = false;
    await page.route(target === "module" ? "**/app.js" : "**/call", async (route) => {
      const method = target === "module" ? "" : route.request().postDataJSON().m;
      const hit = target === "module" || method === ({ bridge: "Bootstrap", tabs: "TerminalTabs", scan: "InitialScan" } as Record<string, string>)[target];
      if (!failed && hit) {
        failed = true;
        if (target === "module") await route.abort("failed");
        else await route.fulfill({ status: 500, contentType: "application/json", body: JSON.stringify({ error: "Synthetic startup failure" }) });
      } else await route.continue();
    });
    await page.goto("/");
    await expect(page.getByRole("heading", { name: "Hopsesh couldn’t finish opening" })).toBeVisible();
    if (target !== "module") await expect(page.getByRole("alert")).toContainText("Synthetic startup failure");
    await page.getByRole("button", { name: "Reload window" }).click();
    await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible({ timeout: 30000 });
  });
}

 test("initial scan failure leaves the browser usable and retry recovers",async({page})=>{
  let failed=false;
  await page.route("**/call",async route=>{
   if(!failed && route.request().postDataJSON().m==="InitialScan") { failed=true;await route.fulfill({status:500,contentType:"application/json",body:JSON.stringify({error:"Synthetic startup failure"})});}
   else await route.continue();
  });
  await page.goto("/");
  await expect(page.getByRole("heading",{name:"All sessions"})).toBeVisible();
  await expect(page.getByRole("alert")).toContainText("Synthetic startup failure");
  await page.getByRole("button",{name:"Retry refresh"}).click();
  await expect(page.locator("#fresh")).toContainText("updated");
 });
