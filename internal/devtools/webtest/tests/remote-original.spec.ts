import { test, expect, type Page } from "@playwright/test";
import { fresh, row, oldRow, details, action } from "./helpers";

test.afterEach(async ({ page }) => { await page.unrouteAll({ behavior: "wait" }); });

// The copy a move left on another machine: hopsesh 0.6.4 runs in roee-macbook's login
// session, but the hook for that Claude Code folder isn't set up there, and the copy is
// still open. The demo backend has no other machine, so every scan the window takes in
// (and every discovery event) carries it, as the backend's data contract shapes it.
const REMOTE = "roee-macbook";
const TITLE = "Find the codeword";
const HERE = /this (Mac|PC|computer)/;
const ago = (hours: number) => new Date(Date.now() - hours * 3600e3).toISOString();
const PROBLEM = `The Claude Code hook isn't set up on ${REMOTE} for “Claude Code default”, so nothing stops new prompts in this copy.`;

type Fixture = { status?: string; sourceChanged?: boolean; live?: boolean; guard?: Record<string, unknown>; replies?: Record<string, unknown> };

// world resets the demo with TITLE's Claude Code session here as the active copy of a
// move from REMOTE, and the old copy left there. calls are the window's requests to
// act (each answered without acting); key is the session's key (the same on both).
async function world(page: Page, f: Fixture = {}) {
  const calls: { m: string; args: any[] }[] = [];
  const fixture = { key: "", here: "" };
  const patch = (scan: any) => {
    const local = scan?.machines?.find((m: any) => m.local);
    if (!scan?.groups || !local) return;
    if (!scan.machines.some((m: any) => m.name === REMOTE))
      scan.machines.push({ name: REMOTE, local: false, status: "ok", os: "darwin", hopsesh: "0.6.4", presence: "login", receiving: true, sessions: 1, agentNames: ["Claude Code"], agents: ["Claude Code"] });
    for (const g of scan.groups) for (const e of g.entries) {
      if (e.title !== TITLE || e.agent !== "claude" || (e.copies || []).some((c: any) => c.machine === REMOTE)) continue;
      const moved = ago(96);
      fixture.key = e.key; fixture.here = local.name;
      // Used more recently than its active copy: only its place after that row keeps it there.
      const old = { ...e, machine: REMOTE, live: f.live ?? true, status: "live idle", needs: false, places: [], cached: false, unpushed: 0, dirty: 0, app: "",
        lastActive: ago(0.05), arrival: null, movement: null, returns: [],
        departure: { operation: "op-r", status: f.status ?? "continued", text: "", machine: local.name, agent: "claude", agentName: "Claude Code", key: e.key, profile: e.profile?.id || "",
          checkedAt: ago(0.1), delivery: "delivered", at: moved, ...(f.sourceChanged ? { sourceChanged: true, sourceChangedAt: ago(3) } : {}) },
        guard: { operation: "op-r", mode: "block", effective: false, state: "unprotected", action: "setup", machine: REMOTE, checkedAt: ago(1), problem: PROBLEM, ...f.guard } };
      e.arrival = { operation: "op-r", kind: "moved", machine: REMOTE, agent: "claude", agentName: "Claude Code", at: moved };
      e.copies = old.copies = [{ machine: local.name, key: e.key, agent: "claude", agentName: "Claude Code", local: true, newest: true },
        { machine: REMOTE, key: e.key, agent: "claude", agentName: "Claude Code", local: false, leftBehind: true }];
      (scan.profileCopies ||= []).push({ ...g, entries: [old] });
    }
  };
  await page.routeWebSocket(/\/events$/, (ws) => {
    const server = ws.connectToServer();
    server.onMessage((message) => {
      const m = JSON.parse(String(message));
      if (m.name !== "hopsesh:discovery") return ws.send(message);
      patch(m.data); ws.send(JSON.stringify(m));
    });
  });
  await page.route("**/call", async (route) => {
    const req = route.request().postDataJSON();
    if (["SetUpProtection", "ContinueOriginalAnyway", "ContinueOnMachine", "MachineStatuses", "ReleaseOriginal", "Plan", "PushPlan", "PlanHandoff", "ResumeSession", "ResumeEntry"].includes(req.m)) {
      calls.push(req);
      const reply = f.replies?.[req.m];
      return route.fulfill({ json: reply !== undefined ? reply : { result: req.m === "MachineStatuses" ? [] : null } });
    }
    if (["InitialScan", "Scan", "RefreshHere", "CachedScan", "ScanSnapshot"].includes(req.m)) {
      const body = await (await route.fetch()).json();
      patch(body.result);
      return route.fulfill({ json: body });
    }
    return route.continue();
  });
  await fresh(page);
  await expect(oldRow(page, TITLE)).toHaveCount(1);
  return { calls, ...fixture };
}

const places = (page: Page) => page.getByRole("navigation", { name: "Places" });
const acting = (calls: { m: string }[]) => calls.filter((c) => !["MachineStatuses", "SetUpProtection"].includes(c.m)).map((c) => c.m);

test("an old copy on another machine is a row only where its machine is, dimmed, right after its active copy", async ({ page }) => {
  const { calls } = await world(page);
  const old = oldRow(page, TITLE), active = row(page, TITLE);
  // All sessions: a row at the same level as every other, never a child line.
  await expect(old).toBeVisible();
  await expect(old).not.toContainText("↳");
  await expect(page.locator(".tree .subrow, .tree .sub-ic")).toHaveCount(0);
  expect(await old.getAttribute("aria-level")).toBe(await active.getAttribute("aria-level"));
  const keys = await page.locator(".tree .row").evaluateAll((rs) => rs.map((r) => (r as HTMLElement).dataset.key));
  expect(keys.indexOf(await old.getAttribute("data-key"))).toBe(keys.indexOf(await active.getAttribute("data-key")) + 1);
  // Dimmed by colour (the muted token), never by fading the row.
  const looks = await old.evaluate((el) => {
    const probe = document.createElement("span");
    probe.style.color = "var(--muted)";
    document.body.append(probe);
    const muted = getComputedStyle(probe).color;
    probe.remove();
    return { title: getComputedStyle(el.querySelector(".t")!).color, muted, opacity: getComputedStyle(el).opacity };
  });
  expect(looks.title).toBe(looks.muted);
  expect(looks.opacity).toBe("1");
  // Where it went replaces Idle/Working; what to watch for follows, each in words.
  await expect(old.locator(".chip.st-away")).toHaveText(new RegExp(`^Moved to ${HERE.source}$`));
  await expect(old.locator(".chip.st-away svg")).toHaveCount(1);
  await expect(old.locator(".chip.mark")).toHaveText(["Not blocked", "Still open"]);
  await expect(old.locator(".chip.mark").first()).toHaveClass(/st-warn/);
  await expect(old).not.toContainText(/Idle|Working/);
  await expect(old).toHaveAttribute("aria-label", new RegExp(`^Old copy of “${TITLE}” on ${REMOTE}, moved to ${HERE.source} · Not blocked · Still open$`));
  // The active copy: moved in (not the working tone), and its old copy's worst.
  await expect(active.locator(".role-chip")).toHaveClass(/st-moved/);
  await expect(active.locator(".role-chip")).not.toContainText("●");
  await expect(active.locator(".oldcopy-chip")).toHaveText("Old copy not blocked");
  await expect(active.locator(".oldcopy-chip")).toHaveClass(/st-warn/);

  // This Mac: the other machine's copy is not here; the active copy still says it.
  await places(page).getByRole("button", { name: /^This (Mac|PC|computer)/ }).click();
  await expect(page.getByRole("heading", { name: /^This (Mac|PC|computer)$/ })).toBeVisible();
  await expect(active).toBeVisible();
  await expect(old).toHaveCount(0);
  await expect(active.locator(".oldcopy-chip")).toHaveText("Old copy not blocked");

  // Its own machine: there it is, without the active copy.
  await places(page).getByRole("button", { name: new RegExp("^" + REMOTE) }).click();
  await expect(page.getByRole("heading", { name: REMOTE, exact: true })).toBeVisible();
  await expect(old).toBeVisible();
  await expect(active).toHaveCount(0);

  // Double-click and Enter on it never continue it: they go to the active copy.
  await old.dblclick();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  await expect(active).toHaveAttribute("aria-selected", "true");
  await old.click();
  await old.press("Enter");
  await expect(active).toHaveAttribute("aria-selected", "true");
  expect(acting(calls)).toEqual([]);
});

test("the old copy's panel says where the session went, that it isn't blocked and is still open, and fixes it", async ({ page }) => {
  const { calls, here } = await world(page);
  await oldRow(page, TITLE).click();
  const d = details(page);
  const banner = d.getByRole("status", { name: "Old copy", exact: true });
  await expect(banner).toContainText(new RegExp(`Old copy · moved to ${HERE.source} on \\w+`));
  await expect(banner).toContainText(new RegExp(`The active copy is on ${HERE.source} in Claude Code\\. Continue there so the work stays in one conversation\\.`));
  await expect(banner).toContainText(`Not blocked: ${PROBLEM}`);
  await expect(banner).toContainText(`Still open on ${REMOTE}. Close it there: a session that was already open may not pick up protection set up later, and anything typed there won't reach the active copy.`);
  await expect(banner).toContainText("Work continued in the active copy");
  await expect(banner).toHaveClass(/warn/);
  await expect(d).not.toContainText("Checking");
  await expect(d.locator(".ins-status")).toContainText(`Moved away · last used on ${REMOTE}`);
  await expect(d.locator(".ins-lineage")).toHaveCount(0);
  await expect(d.getByLabel("Where it went", { exact: true })).toHaveCount(0);
  await expect(banner.getByRole("button", { name: "View journey" })).toBeVisible();

  await banner.getByRole("button", { name: `Set up protection on ${REMOTE}` }).click();
  await expect.poll(() => calls.filter((c) => c.m === "SetUpProtection").map((c) => c.args)).toEqual([[REMOTE, false]]);

  // Its actions: the active copy, or continuing this one anyway; nothing that moves it.
  await expect(d.locator("#act-primary")).toHaveText("Go to the active copy here");
  await expect(d.locator("#act-move")).toHaveCount(0);
  await d.locator("#act-chevron").click();
  const items = await page.getByRole("menuitem").allTextContents();
  expect(items).toHaveLength(1);
  expect(items[0]).toMatch(/^Continue this old copy anyway…/);
  await expect(page.getByRole("menuitem").first()).toHaveClass(/danger/);
  await page.keyboard.press("Escape");
  await d.getByRole("button", { name: "More actions" }).click();
  await expect(page.getByRole("menuitem", { name: /^Change folder…/ })).toHaveAttribute("aria-disabled", "true");
  for (const label of await page.getByRole("menuitem").allTextContents()) expect(label).not.toMatch(/^(Continue on|Bring|Resume|Move|Send)/);
  await page.keyboard.press("Escape");

  // The palette's actions for it are the same.
  const flat = await page.evaluate(async (machine) => {
    const { entries } = await import("/core.js");
    const { actionsFor } = await import("/actions.js");
    return actionsFor(entries().find((e: any) => e.machine === machine)).map((a: any) => a.label);
  }, REMOTE);
  expect(flat.filter((l: string) => /^(Continue on|Bring|Resume|Move|Send|Continue with|Hand off)/.test(l))).toEqual([]);

  // Going to the active copy selects it, and it lists its old copy near the top.
  await d.locator("#act-primary").click();
  await expect.poll(() => page.evaluate(async () => (await import("/core.js")).state.sel?.machine)).toBe(here);
  await expect(d.locator(".ins-lineage")).toContainText(`Moved from Claude Code on ${REMOTE}`);
  const section = d.getByRole("region", { name: "Old copy", exact: true });
  await expect(section).toContainText(`On ${REMOTE} · last used`);
  await expect(section).toContainText(`Not blocked: ${PROBLEM}`);
  await expect(section).toContainText(`Still open on ${REMOTE}`);
  await expect(section.getByRole("button", { name: `Set up protection on ${REMOTE}` })).toBeVisible();

  // Showing it from This Mac goes to where it is listed.
  await places(page).getByRole("button", { name: /^This (Mac|PC|computer)/ }).click();
  await row(page, TITLE).click();
  await section.getByRole("button", { name: "Show old copy" }).click();
  await expect(page.getByRole("heading", { name: "All sessions" })).toBeVisible();
  await expect(oldRow(page, TITLE)).toHaveAttribute("aria-selected", "true");
  expect(acting(calls)).toEqual([]);
});

test("continuing the old copy anyway asks first, then continues it there through hopsesh", async ({ page }) => {
  const { calls, key } = await world(page);
  await oldRow(page, TITLE).click();
  await action(page, "places", /^Continue this old copy anyway…/);
  const dlg = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "Continue the old copy?" }) });
  await expect(dlg).toContainText(new RegExp(`This conversation moved to Claude Code on ${HERE.source} on \\w+`));
  await expect(dlg).toContainText("won't reach that copy, and the two will diverge: moving back then needs a comparison, or keeping both as separate conversations.");
  await expect(dlg).toContainText("hopsesh removes this copy's block first");
  await expect(dlg.getByRole("button")).toHaveText(["Cancel", "Go to the active copy", "Continue old copy"]);
  await expect(dlg.getByRole("button", { name: "Continue old copy" })).toHaveClass(/danger/);
  await expect(dlg.getByRole("button", { name: "Cancel" })).toBeFocused();
  await dlg.getByRole("button", { name: "Cancel" }).click();
  await expect(dlg).toBeHidden();
  expect(acting(calls)).toEqual([]);

  // Go to the active copy, from the question.
  await action(page, "places", /^Continue this old copy anyway…/);
  await dlg.getByRole("button", { name: "Go to the active copy" }).click();
  await expect(row(page, TITLE)).toHaveAttribute("aria-selected", "true");
  expect(acting(calls)).toEqual([]);

  await oldRow(page, TITLE).click();
  await action(page, "places", /^Continue this old copy anyway…/);
  await dlg.getByRole("button", { name: "Continue old copy" }).click();
  await expect.poll(() => calls.filter((c) => c.m === "ContinueOriginalAnyway").map((c) => c.args)).toEqual([[REMOTE, key]]);
  expect(acting(calls)).toEqual(["ContinueOriginalAnyway"]);
});

test("a refusal to continue a copy that already moved asks the same question", async ({ page }) => {
  const { calls, key } = await world(page, { replies: { ContinueOnMachine: { error: "moved-original: This copy moved to Claude Code on studio on 22 Oct; continue that copy instead." } } });
  await oldRow(page, TITLE).click();
  // As an older window would ask (or any path that still reaches the backend).
  await page.evaluate(async ([machine, key]) => {
    const { api, fail } = await import("/core.js");
    await api("ContinueOnMachine", machine, key).catch(fail);
  }, [REMOTE, key]);
  const dlg = page.getByRole("dialog").filter({ has: page.getByRole("heading", { name: "Continue the old copy?" }) });
  await expect(dlg).toBeVisible();
  await expect(page.locator("#toast")).not.toContainText("moved-original");
  await dlg.getByRole("button", { name: "Cancel" }).click();
  expect(acting(calls)).toEqual(["ContinueOnMachine"]);
});

const BOTH = "Both copies changed after the move: they have diverged. Moving back will need a comparison, or keep them as separate conversations.";
for (const [status, mark, line, summary, fromActive] of [
  ["continued", "Diverged", BOTH, "Old copy diverged", BOTH],
  ["prepared", "Used after the move", "This copy was used after the move (last on ", "Old copy used after the move", "The old copy was used after the move (last on "],
]) test(`an old copy used after the move (${status} there) is red: ${mark}`, async ({ page }) => {
  await world(page, { status, sourceChanged: true });
  const old = oldRow(page, TITLE);
  await expect(old.locator(".chip.mark")).toHaveText([mark, "Not blocked", "Still open"]);
  await expect(old.locator(".chip.mark").first()).toHaveClass(/st-error/);
  await expect(old).toHaveAttribute("aria-label", new RegExp(` · ${mark} · Not blocked · Still open$`));
  await expect(row(page, TITLE).locator(".oldcopy-chip")).toHaveText(summary);
  await expect(row(page, TITLE).locator(".oldcopy-chip")).toHaveClass(/st-error/);
  await old.click();
  const banner = details(page).getByRole("alert", { name: "Old copy", exact: true });
  await expect(banner).toContainText(line);
  await expect(banner).toHaveClass(/err/);
  if (status === "prepared") await expect(banner).toContainText("that work isn't in the active copy.");
  await row(page, TITLE).click();
  await expect(details(page).getByRole("region", { name: "Old copy", exact: true })).toContainText(fromActive);
});

test("protection not known says why, never Checking, and checks again", async ({ page }) => {
  const why = `${REMOTE} hasn't been reached since Oct 24, so its hooks couldn't be read.`;
  const { calls } = await world(page, { live: false, guard: { state: "unknown", checking: false, action: "", problem: why, checkedAt: ago(50) } });
  const old = oldRow(page, TITLE);
  const mark = old.locator(".chip.mark");
  await expect(mark).toHaveText(["Protection unknown"]);
  await expect(mark).toHaveAttribute("title", why);
  await expect(mark).toHaveClass(/st-ended/);
  await expect(old.locator(".mini-spin")).toHaveCount(0);
  await expect(row(page, TITLE).locator(".oldcopy-chip")).toHaveText("Old copy: protection unknown");
  await old.click();
  const banner = details(page).getByRole("status", { name: "Old copy", exact: true });
  await expect(banner).toContainText(`Protection unknown: ${why} · checked 2 d ago`);
  await expect(banner).not.toContainText("Still open");
  await expect(details(page)).not.toContainText("Checking");
  await expect(old).not.toContainText("Checking");
  await banner.getByRole("button", { name: "Check again" }).click();
  await expect.poll(() => calls.filter((c) => c.m === "MachineStatuses").map((c) => c.args)).toEqual([[true]]);
});

test("a protected old copy is plain: blocked, with nothing for its active copy to flag", async ({ page }) => {
  await world(page, { live: false, guard: { state: "protected", effective: true, action: "", problem: "" } });
  const old = oldRow(page, TITLE);
  await expect(old.locator(".chip.mark")).toHaveText(["Blocked"]);
  await expect(old.locator(".chip.mark")).toHaveClass(/st-ended/);
  await expect(row(page, TITLE).locator(".oldcopy-chip")).toHaveCount(0);
  await old.click();
  const banner = details(page).getByRole("status", { name: "Old copy", exact: true });
  await expect(banner).toContainText(`Blocked on ${REMOTE}: new prompts in this copy are refused.`);
  await expect(banner).not.toHaveClass(/warn|err/);
});
