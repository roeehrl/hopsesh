import { test, expect, type Page, type Route } from "@playwright/test";
import { menu } from "./helpers";

function gate() {
  let release!: () => void;
  const promise = new Promise<void>(resolve => { release = resolve; });
  return { promise, release };
}

const release = (latest: string) => ({ latest, newer: latest === "0.4.1", canInstall: false, url: `https://github.com/roeehrl/hopsesh/releases/tag/v${latest}` });

// Only release RPCs are mocked; the rest runs against the real service on a demo home.
// Neither the scheduled nor manual checks can reach GitHub in these tests.
async function start(page: Page, preference: "on" | "off" | "", manual: (r: Route) => Promise<void>, daily?: (r: Route) => Promise<void>, configNewer = false) {
  const reset = await page.request.post("/reset");
  expect(reset.ok()).toBeTruthy();
  if (preference !== "") {
    const set = await page.request.post("/call", { data: { m: "SetUpdateCheck", args: [preference === "on"] } });
    expect((await set.json()).error).toBeUndefined();
  }
  const calls = { manual: 0, daily: 0, install: 0 };
  await page.route("**/call", async r => {
    const method = r.request().postDataJSON().m;
    if (method === "LatestRelease") { calls.manual++; await manual(r); }
    else if (method === "InstallUpdate") { calls.install++; await r.fulfill({ json: { result: null } }); }
    else if (method === "CheckUpdate") {
      calls.daily++;
      if (daily) await daily(r);
      else await r.fulfill({ json: { result: release("0.4.0") } });
    } else if (method === "Bootstrap" || method === "Settings") {
      const response = await r.fetch(), body = await response.json();
      body.result.version = "0.4.0";
      if (method === "Bootstrap" && configNewer) {
        body.result.configError = "A newer hopsesh wrote these settings";
        body.result.configNewer = true;
      }
      await r.fulfill({ response, json: body });
    } else await r.continue();
  });
  await page.goto("/");
  await expect(page.getByRole("heading", { name: configNewer ? "Your hopsesh settings are from a newer version" : "All sessions" })).toBeVisible({ timeout: 30_000 });
  return calls;
}

async function updates(page: Page) {
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Updates", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Updates", exact: true })).toBeVisible();
}

for (const preference of ["on", "off"] as const) {
  test(`manual check fetches beyond cached 0.4.0 with daily checks ${preference}`, async ({ page }) => {
    const wait = gate();
    const calls = await start(page, preference, async r => {
      await wait.promise;
      await r.fulfill({ json: { result: release("0.4.1") } });
    });
    await updates(page);
    const automatic = page.getByRole("checkbox", { name: /Check GitHub once a day/ });
    if (preference === "on") await expect(page.getByText("This is the newest version.", { exact: true })).toBeVisible();
    else await expect(automatic).not.toBeChecked();
    await page.getByRole("button", { name: "Check now", exact: true }).click();
    await expect(page.getByRole("button", { name: "Checking…", exact: true })).toBeDisabled();
    await expect(page.getByText("Checking GitHub for the newest release…", { exact: true })).toBeVisible();
    await expect(page.getByText("This is the newest version.", { exact: true })).toHaveCount(0);
    // A rerender must keep the request disabled instead of offering a duplicate click.
    await page.getByRole("tab", { name: "General", exact: true }).click();
    await page.getByRole("tab", { name: "Updates", exact: true }).click();
    await expect(page.getByRole("button", { name: "Checking…", exact: true })).toBeDisabled();
    expect(calls.manual).toBe(1);
    wait.release();
    await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "Check now", exact: true })).toBeEnabled();
    await expect(page.getByRole("button", { name: "See what's new", exact: true })).toBeVisible();
    await expect(automatic).toBeChecked({ checked: preference === "on" });
    expect(calls.daily).toBe(preference === "on" ? 1 : 0);
  });
}

test("manual failure clears newest claim, remains visible and retries", async ({ page }) => {
  let attempts = 0;
  const calls = await start(page, "on", async r => {
    attempts++;
    if (attempts === 1) await r.fulfill({ json: { error: "could not check for updates: offline" } });
    else await r.fulfill({ json: { result: release("0.4.1") } });
  });
  await updates(page);
  await expect(page.getByText("This is the newest version.", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await expect(page.getByRole("alert")).toContainText("could not check for updates: offline");
  await expect(page.getByText("This is the newest version.", { exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Check now", exact: true })).toBeEnabled();
  await menu(page, "sessions");
  await updates(page);
  await expect(page.getByRole("alert")).toContainText("offline");
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
  await expect(page.getByRole("alert")).toHaveCount(0);
  expect(calls.manual).toBe(2);
});

test("an unavailable daily result never claims newest", async ({ page }) => {
  await start(page, "on", r => r.fulfill({ json: { result: release("0.4.1") } }), r => r.fulfill({ json: { result: {} } }));
  await updates(page);
  await expect(page.getByRole("button", { name: "Check now", exact: true })).toBeEnabled();
  await expect(page.getByText("This is the newest version.", { exact: true })).toHaveCount(0);
});

test("manual check is available before daily-check consent", async ({ page }) => {
  const calls = await start(page, "", r => r.fulfill({ json: { result: release("0.4.1") } }));
  await updates(page);
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
  expect(calls.daily).toBe(0);
  expect(calls.manual).toBe(1);
  await menu(page, "sessions");
  await expect(page.getByText("Check GitHub daily for new versions?", { exact: false })).toBeVisible();
});

test("late consent daily result cannot replace a fresh manual check", async ({ page }) => {
  const wait = gate();
  const calls = await start(page, "", r => r.fulfill({ json: { result: release("0.4.1") } }), async r => {
    await wait.promise;
    await r.fulfill({ json: { result: release("0.4.0") } });
  });
  await page.getByRole("button", { name: "Yes", exact: true }).click();
  await expect.poll(() => calls.daily).toBe(1);
  await updates(page);
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
  const response = page.waitForResponse(r => r.url().endsWith("/call") && r.request().postDataJSON().m === "CheckUpdate");
  wait.release();
  await response;
  await page.evaluate(async () => { const { api } = await import("/core.js"); await api("Bootstrap"); });
  await menu(page, "sessions");
  await updates(page);
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
});

test("late startup daily result cannot replace a fresh manual check", async ({ page }) => {
  const wait = gate();
  await start(page, "on", r => r.fulfill({ json: { result: release("0.4.1") } }), async r => {
    await wait.promise;
    await r.fulfill({ json: { result: release("0.4.0") } });
  });
  await updates(page);
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
  const response = page.waitForResponse(r => r.url().endsWith("/call") && r.request().postDataJSON().m === "CheckUpdate");
  wait.release();
  await response;
  // A bridge RPC provides a barrier after the delayed daily callback runs.
  await page.evaluate(async () => { const { api } = await import("/core.js"); await api("Bootstrap"); });
  await page.getByRole("tab", { name: "General", exact: true }).click();
  await page.getByRole("tab", { name: "Updates", exact: true }).click();
  await expect(page.getByText("hopsesh 0.4.1 is available.", { exact: true })).toBeVisible();
});

test("newer-settings recovery checks manually with daily checks off and reports failure", async ({ page }) => {
  const wait = gate();
  const calls = await start(page, "off", async r => {
    await wait.promise;
    await r.fulfill({ json: { error: "could not check for updates: offline" } });
  }, undefined, true);
  await page.getByRole("button", { name: "Update hopsesh", exact: true }).click();
  await expect(page.getByRole("button", { name: "Checking for updates…", exact: true })).toBeDisabled();
  wait.release();
  await expect(page.locator("#toast")).toContainText("could not check for updates: offline");
  await expect(page.getByRole("button", { name: "Update hopsesh", exact: true })).toBeEnabled();
  expect(calls.manual).toBe(1);
  expect(calls.daily).toBe(0);
});

// A tab as the service reports it (the page's list of tabs follows these events).
const busyTab = (state: string, reason = "") => ({ id: "busy1", revision: Date.now(), kind: "session", machine: "studio", key: "claude/default/busy", agent: "Claude Code", title: "Busy agent · claude", state, reason, attention: false });
const installable = { ...release("0.4.1"), canInstall: true };

test("installing while an agent works asks first, and can wait until it is idle", async ({ page }) => {
  const calls = await start(page, "off", r => r.fulfill({ json: { result: installable } }));
  await page.evaluate((t) => (window as any).__emit("hopsesh:terminal", t), busyTab("running"));
  await updates(page);
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await page.getByRole("button", { name: "Install and restart", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "Restart and end 1 program?" })).toBeVisible();
  await expect(dlg).toContainText("the new version resumes them in tabs");
  await expect(dlg).toContainText("1 agent is in the middle of a turn: “Busy agent · claude”");
  await expect(dlg.getByRole("list", { name: "Programs running in tabs" })).toContainText("hopsesh resumes it after the update");
  await dlg.getByRole("button", { name: "Update when they're idle" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Installs once the agents in your tabs finish their turns" })).toBeVisible();
  await page.waitForTimeout(1_500);
  expect(calls.install).toBe(0);
  await page.evaluate((t) => (window as any).__emit("hopsesh:terminal", t), busyTab("waiting", "idle"));
  await expect.poll(() => calls.install, { timeout: 20_000 }).toBe(1);
  await expect(page.getByRole("button", { name: "Restarting…", exact: true })).toBeDisabled();
});

test("Don't wait cancels a deferred update", async ({ page }) => {
  const calls = await start(page, "off", r => r.fulfill({ json: { result: installable } }));
  await page.evaluate((t) => (window as any).__emit("hopsesh:terminal", t), busyTab("running"));
  await updates(page);
  await page.getByRole("button", { name: "Check now", exact: true }).click();
  await page.getByRole("button", { name: "Install and restart", exact: true }).click();
  await page.locator("#dlg").getByRole("button", { name: "Update when they're idle" }).click();
  await page.getByRole("button", { name: "Don't wait", exact: true }).click();
  await expect(page.getByRole("button", { name: "Install and restart", exact: true })).toBeEnabled();
  await page.evaluate((t) => (window as any).__emit("hopsesh:terminal", t), busyTab("waiting", "idle"));
  await page.waitForTimeout(12_000);
  expect(calls.install).toBe(0);
});

test("updating from the newer-settings screen asks about the programs in tabs too", async ({ page }) => {
  const calls = await start(page, "", r => r.fulfill({ json: { result: installable } }), undefined, true);
  await page.evaluate((t) => (window as any).__emit("hopsesh:terminal", t), busyTab("waiting", "idle"));
  await page.getByRole("button", { name: "Update hopsesh", exact: true }).click();
  const dlg = page.locator("#dlg");
  await expect(dlg.getByRole("heading", { name: "Restart and end 1 program?" })).toBeVisible();
  await expect(dlg.getByRole("button", { name: "Update when they're idle" })).toHaveCount(0); // nobody works
  await dlg.getByRole("button", { name: "Cancel", exact: true }).click();
  expect(calls.install).toBe(0);
  await expect(page.getByRole("button", { name: "Update hopsesh", exact: true })).toBeEnabled();
  await page.getByRole("button", { name: "Update hopsesh", exact: true }).click();
  await dlg.getByRole("button", { name: "Install and end 1 program" }).click();
  await expect.poll(() => calls.install).toBe(1);
});
