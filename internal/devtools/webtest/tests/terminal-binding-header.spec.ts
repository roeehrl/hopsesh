import { test, expect } from "@playwright/test";

// Renderer acceptance only. The real demo service owns a test PTY; the list
// transport supplies explicit binding states. Go regressions separately prove
// native PID evidence, observation ordering and ownership/lifetime guards.
test("terminal header distinguishes checking from lost native association", async ({ page, context }) => {
  expect((await page.request.post("/reset?terminal=here")).ok()).toBeTruthy();
  const opened = await page.request.post("/terminal-test/open?title=Binding%20fixture");
  expect(opened.ok()).toBeTruthy();
  const { id } = await opened.json();
  const terminal = await context.newPage();
  let current: any;
  let publish: (info: any) => void;
  let updateInfo: (info: any) => void;
  await terminal.routeWebSocket(/\/stream\?name=hopsesh\.terminal$/, route => {
    route.connectToServer();
    updateInfo = info => route.send(Buffer.concat([Buffer.from("s"), Buffer.from(JSON.stringify(info))]));
  });
  await terminal.routeWebSocket(/\/stream\?.*name=hopsesh\.terminal\.tabs/, route => {
    const server = route.connectToServer();
    publish = info => route.send(JSON.stringify({ tabs: [info] }));
    server.onMessage(message => {
      const text = typeof message === "string" ? message : message.toString();
      const frame = JSON.parse(text);
      if (frame.tabs) {
        current = frame.tabs.find((tab: any) => tab.id === id);
        // Only the first actual metadata snapshot initializes the fixture.
        if (current) {
          route.send(JSON.stringify(frame));
          server.onMessage(() => {});
        }
      } else route.send(message);
    });
  });
  await terminal.goto("/terminal/?renderer=dom");
  await expect(terminal.locator("#connection")).toBeHidden();
  expect(current.pid).toBeGreaterThan(0);
  const key = "codex@fixture/original";
  const base = { ...current, kind: "session", machine: "fixture", key, launchedKey: key, command: "codex resume --no-daemon original" };
  const verified = { ...base, revision: current.revision + 1, association: "Confirmed by native process registry", relationship: { family: "verified-family", branchName: "Last verified branch" } };
  publish!(verified);
  await expect(terminal.locator("#runs")).toContainText("Last verified branch");
  await expect(terminal.locator("#runs")).not.toContainText("Session association not confirmed");

  publish!({ ...verified, revision: verified.revision + 1, external: false, rerun: false, association: "Checking session association" });
  await expect(terminal.locator("#runs")).toContainText("Checking session association");
  await expect(terminal.locator("#runs")).toContainText(`PTY PID ${current.pid}`);
  await expect(terminal.locator("#runs")).toContainText("Last verified branch");
  await expect(terminal.locator("#runs")).not.toContainText("Session association not confirmed");
  await expect(terminal.locator("#runs")).toContainText("Last verified branch:");
  await expect(terminal.locator("#t-external")).toBeHidden();

  publish!({ ...base, revision: verified.revision + 2, association: "Session association not confirmed" });
  await expect(terminal.locator("#runs")).toContainText("Session association not confirmed");
  await expect(terminal.locator("#runs")).not.toContainText("Checking session association");
  await expect(terminal.locator("#runs")).not.toContainText("Last verified branch");

  publish!({ ...verified, revision: verified.revision + 3, key: "codex@fixture/native-new", external: false, rerun: false, relationship: { family: "new-family", branchName: "Native new branch" } });
  await expect(terminal.locator("#runs")).toContainText("Native new branch");
  await expect(terminal.locator("#runs")).not.toContainText("Session association not confirmed");
  await expect(terminal.locator("#t-external")).toBeHidden();
  // Neither a deliberately older full list nor a metadata-free PTY Info frame
  // may erase the newer branch. Normal production sends lists serially.
  publish!({ ...base, revision: verified.revision + 2, association: "Session association not confirmed" });
  updateInfo!({ id, state: "running", pid: current.pid, started: current.started });
  await expect(terminal.locator("#runs")).toContainText("Native new branch");
  await expect(terminal.locator("#runs")).not.toContainText("Session association not confirmed");
});

test("main terminal map rejects delayed startup RPC, older events and resurrection", async ({ page }) => {
  expect((await page.request.post("/reset?terminal=here")).ok()).toBeTruthy();
  const id = "map-order-fixture";
  const old = { id, revision: 1, state: "running", kind: "session", machine: "fixture", key: "codex@fixture/original", association: "Session association not confirmed", title: "Binding fixture" };
  let started!: () => void, release!: () => void, replied!: () => void;
  const entered = new Promise<void>(resolve => started = resolve);
  const gate = new Promise<void>(resolve => release = resolve);
  const delivered = new Promise<void>(resolve => replied = resolve);
  await page.route("**/call", async route => {
    if (route.request().postDataJSON().m !== "TerminalTabs") { await route.continue(); return; }
    started();
    await gate;
    await route.fulfill({ json: { result: [old] } });
    replied();
  });
  await page.goto("/");
  await entered;
  const confirmed = { ...old, revision: 2, association: "Confirmed by native process registry" };
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), confirmed);
  release();
  await delivered;
  const value = () => page.evaluate(async id => (await import("/term.js")).tabs.get(id), id);
  await expect.poll(async () => (await value())?.association).toBe(confirmed.association);
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), old);
  expect((await value()).association).toBe(confirmed.association);
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), { ...confirmed, revision: 3, association: "Checking session association" });
  expect(await page.evaluate(async key => !!(await import("/term.js")).tabFor({ machine: "fixture", key }), old.key)).toBe(false);
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), { ...old, revision: 4, state: "" });
  await page.evaluate(async () => (await import("/term.js")).loadTabs());
  expect(await value()).toBeUndefined();
  // Positive tombstones reject an unversioned resurrection too.
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), { ...old, revision: undefined });
  expect(await value()).toBeUndefined();
  // Old backends/test fixtures can make repeated zero/absent updates; ordering
  // begins only when a positive revision arrives for that identity.
  const legacy = { ...old, id: "legacy-fixture", revision: 0 };
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), legacy);
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), { ...legacy, revision: undefined, state: "exited" });
  expect(await page.evaluate(async () => (await import("/term.js")).tabs.get("legacy-fixture").state)).toBe("exited");
  await page.evaluate(t => (window as any).__emit("hopsesh:terminal", t), { ...legacy, state: "" });
  expect(await page.evaluate(async () => (await import("/term.js")).tabs.has("legacy-fixture"))).toBe(false);
});
