import { test, expect, type Page } from "@playwright/test";
import { fresh, menu } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// Every hopsesh on a machine (MachineStatus.installs): this Mac has the app and a separately
// installed command one version behind it; sam-macbook runs hopsesh's own copy while its
// app is far behind.
const ok = (text: string) => ({ state: "ok", text });
const now = () => new Date().toISOString();
const appHere = { path: "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", version: "0.6.4", kind: "app", bundle: "/Applications/hopsesh.app", main: true, service: true, open: true };
const statuses = (here: string) => [
  { machine: here, local: true, version: "0.6.4", newest: "0.6.4", reach: ok("this machine"), hopsesh: ok("0.6.4"), login: ok("in your login session"), receiving: ok("on, listening"),
    cli: ok("on PATH · ~/.local/bin/hopsesh"), agents: [], todo: 1, total: 5, checkedAt: now(), detailsAt: now(),
    copies: { state: "warn", text: "app 0.6.4 · ~/.local/bin/hopsesh 0.6.3", reason: "these copies of hopsesh share its settings and history there; an older one can misread what a newer one wrote", fix: "align" },
    installs: { os: "darwin", home: "/Users/kim", installs: [appHere, { path: "/Users/kim/.local/bin/hopsesh", version: "0.6.3", kind: "command", terminal: true }] } },
  { machine: "sam-macbook", local: false, version: "0.6.4", newest: "0.6.4", reach: ok("reached over SSH"), hopsesh: ok("0.6.4"), login: ok("in your login session"), receiving: ok("on, listening"),
    cli: ok("on PATH · ~/.local/bin/hopsesh"), agents: [], todo: 1, total: 5, checkedAt: now(), detailsAt: now(),
    copies: { state: "warn", text: "hopsesh's copy 0.6.4 · app 0.5.0", reason: "these copies of hopsesh share its settings and history there", fix: "update" },
    installs: { os: "darwin", home: "/Users/sam", service: "/Users/sam/.hopsesh/bin/hopsesh", terminal: "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", installs: [
      { path: "/Users/sam/.hopsesh/bin/hopsesh", version: "0.6.4", kind: "own", service: true },
      { path: "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", links: ["/Users/sam/.local/bin/hopsesh"], version: "0.5.0", kind: "app", bundle: "/Applications/hopsesh.app", main: true, terminal: true }] } },
];
const machines = (here: string) => ({ here: { name: here, agents: [], hopsesh: "0.6.4", receive: true, storage: [] }, found: [], clouds: [],
  machines: [{ name: "sam-macbook", destination: "sam-macbook", os: "darwin", auth: "key", scanned: true, scan: {}, status: "ok", hint: "", error: "", agents: [], hopsesh: "0.6.4", sessions: 0, storage: [], presence: "login", can: [] }] });
const plan = {
  machine: "sam-macbook", version: "0.6.4", restarts: true, ends: 2, installs: {},
  steps: [
    { do: "app", path: "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", text: "Update the app (/Applications/hopsesh.app) 0.5.0 → 0.6.4 with its own updater (it checks the release's signature, Apple's notarization)" },
    { do: "link", path: "/Users/sam/.hopsesh/bin/hopsesh", text: "Point hopsesh's own copy (~/.hopsesh/bin/hopsesh) at the app's hopsesh, so the hooks that run it run that" },
    { do: "service", path: "/Applications/hopsesh.app/Contents/Resources/bin/hopsesh", text: "Start the app's hopsesh at login (it starts ~/.hopsesh/bin/hopsesh now)" },
  ],
};

async function fixtures(page: Page) {
  const calls: { m: string; a: any[] }[] = [];
  let here = "here";
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON(), a = req.a || req.args || [];
    if (req.m === "MachineStatuses") return route.fulfill({ json: { result: statuses(here) } });
    if (req.m === "Machines") {
      // This machine's real name, when the page still wants the answer (a reload can drop it).
      try { const res = await route.fetch(); here = (await res.json()).result.here.name; } catch { /* keep the last */ }
      return route.fulfill({ json: { result: machines(here) } }).catch(() => {});
    }
    if (["PlanUpdate", "SetUpMachine", "AlignHere"].includes(req.m)) {
      calls.push({ m: req.m, a });
      if (req.m === "PlanUpdate") return route.fulfill({ json: { result: plan } });
      if (req.m === "AlignHere") return route.fulfill({ json: { result: { machine: here, version: "0.6.4", steps: [] } } });
      return route.fulfill({ json: { result: { machine: a[0], version: "0.6.4", login: true, steps: [] } } });
    }
    return route.continue();
  });
  return calls;
}

// This Mac's page lists each hopsesh with its version and which one it runs, and Up next
// offers to bring the older one to this version.
test("this Mac's copies of hopsesh are listed and brought to one version", async ({ page }, testInfo) => {
  const calls = await fixtures(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  const detail = page.locator(".mdetail");
  const next = detail.locator(".mnext");
  await expect(next).toContainText("Copies of hopsesh");
  await expect(next).toContainText("app 0.6.4 · ~/.local/bin/hopsesh 0.6.3");
  const list = detail.getByRole("list", { name: "Copies of hopsesh" });
  await expect(list.getByRole("listitem")).toHaveCount(2);
  await expect(list.getByRole("listitem").first()).toContainText("/Applications/hopsesh.app");
  await expect(list.getByRole("listitem").first()).toContainText("the one this machine runs");
  await expect(list.getByRole("listitem").nth(1)).toContainText("~/.local/bin/hopsesh");
  await expect(list.getByRole("listitem").nth(1)).toContainText("your terminal runs it");
  await page.screenshot({ path: testInfo.outputPath("copies-here.png"), fullPage: true });
  await next.getByRole("button", { name: "Update them…" }).click();
  const dlg = page.getByRole("dialog");
  await expect(dlg).toContainText("an older one can misread what a newer one wrote");
  await dlg.getByRole("button", { name: "Update them", exact: true }).click();
  await expect(dlg).toContainText("Every hopsesh on");
  expect(calls.filter((c) => c.m === "AlignHere")).toHaveLength(1);
});

// Updating another machine says first what happens to each hopsesh there, and that the
// restart ends the terminals hopsesh runs there.
test("updating a machine lists what happens to each hopsesh there first", async ({ page }, testInfo) => {
  const calls = await fixtures(page);
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Machines", exact: true }).click();
  await page.getByRole("listbox", { name: "Machines" }).getByRole("option", { name: /sam-macbook/ }).click();
  const next = page.locator(".mdetail .mnext");
  await expect(next).toContainText("hopsesh's copy 0.6.4 · app 0.5.0");
  await next.getByRole("button", { name: "Update them…" }).click();
  const dlg = page.getByRole("dialog");
  await expect(dlg.locator('[data-step="app"]')).toContainText("0.5.0 → 0.6.4");
  await expect(dlg.locator('[data-step="link"]')).toContainText("hooks that run it");
  await expect(dlg.locator('[data-step="service"]')).toBeVisible();
  await expect(dlg).toContainText("ends the 2 terminals it runs there");
  await expect(dlg).not.toContainText("A hopsesh you installed there yourself is left as it is");
  await page.screenshot({ path: testInfo.outputPath("update-plan.png") });
  await dlg.getByRole("button", { name: "Update hopsesh on sam-macbook" }).click();
  await expect.poll(() => calls.filter((c) => c.m === "SetUpMachine").length).toBe(1);
  expect(calls.find((c) => c.m === "PlanUpdate")!.a[0]).toBe("sam-macbook");
});
