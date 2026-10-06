import { test, expect } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("Back to sessions stays in one toolbar position across pages and scrolling", async ({ page }, testInfo) => {
  for (const width of [900, 1280, 1920]) {
    await page.setViewportSize({ width, height: 600 });
    let position: { x: number; y: number } | undefined;
    for (const name of ["settings", "machines", "activity"]) {
      await menu(page, name);
      const back = page.getByRole("button", { name: "Back to sessions", exact: true });
      await expect(back).toHaveCount(1);
      await expect(back).toBeVisible();
      await expect(page.locator(".page")).toBeVisible();
      const bounds = (await back.boundingBox())!;
      if (position) expect({ x: bounds.x, y: bounds.y }).toEqual(position);
      position = { x: bounds.x, y: bounds.y };
      const mac = await page.locator("html").evaluate(el => el.getAttribute("data-os") === "darwin");
      expect(bounds.x).toBeGreaterThanOrEqual(mac ? 96 : 8); // native buttons exist on macOS only
      await page.locator(".page").evaluate(el => { el.scrollTop = el.scrollHeight; });
      expect(await back.boundingBox()).toEqual(bounds);
      const controls = await page.locator(".titlebar > :not([hidden])").evaluateAll(els =>
        els.filter(el => getComputedStyle(el).display !== "none").map(el => {
          const b = el.getBoundingClientRect(); return { left: b.left, right: b.right };
        }));
      for (let i = 1; i < controls.length; i++) expect(controls[i].left).toBeGreaterThanOrEqual(controls[i - 1].right - 0.5);
      expect(controls.at(-1)!.right).toBeLessThanOrEqual(width);
      if (width === 900 && name === "machines") await page.screenshot({ path: testInfo.outputPath("machines-navigation.png") });
      await back.click();
      await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
      await expect(back).toBeHidden();
    }
  }
});

for (const method of ["Activity", "Machines", "Settings"]) {
  test(`Back leaves a loading ${method} page and late responses cannot replace Sessions`, async ({ page }) => {
    let release!: () => void;
    const gate = new Promise<void>(resolve => { release = resolve; });
    let finished!: () => void;
    const done = new Promise<void>(resolve => { finished = resolve; });
    await page.route("**/call", async route => {
      if (route.request().postDataJSON().m !== method) return route.continue();
      await gate;
      await route.continue();
      finished();
    });
    await menu(page, method.toLowerCase());
    await expect(page.locator("#view .loading")).toBeVisible();
    await page.getByRole("button", { name: "Back to sessions" }).click();
    await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
    release();
    await done;
    // Wait for the released RPC to settle and a subsequent backend round trip.
    await page.evaluate(async () => {
      const { api } = await import(/* @vite-ignore */ "/core.js");
      await api("Info");
    });
    await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
    await expect(page.locator("#btn-back-sessions")).toBeHidden();
  });
}

test("an Activity error retains the same usable Back control", async ({ page }) => {
  await page.route("**/call", route => route.request().postDataJSON().m === "Activity"
    ? route.fulfill({ json: { error: "Activity temporarily unavailable" } }) : route.continue());
  await menu(page, "activity");
  await expect(page.locator("#view")).toContainText("Activity temporarily unavailable");
  await page.getByRole("button", { name: "Back to sessions" }).click();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
});
