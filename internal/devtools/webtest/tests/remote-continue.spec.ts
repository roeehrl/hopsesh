import { test, expect, Page } from "@playwright/test";
import { fresh } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// actionsOn is what the session panel offers for a copy of a session on another machine,
// hopsesh's presence there as given; moved: the copy moved away to this machine (an old copy).
async function actionsOn(page: Page, machine: string, presence: string, moved = false) {
  return page.evaluate(async ([machine, presence, moved]) => {
    const core = await import("/core.js" as string), actions = await import("/actions.js" as string);
    core.state.scan.machines.push({ name: machine, status: "ok", local: false, os: "darwin", agents: [], agentNames: [], sessions: 1, hopsesh: presence === "ssh" ? "" : "0.5.0", presence });
    const e = core.state.scan.groups.flatMap((g: any) => g.entries).find((x: any) => x.title === "Find the codeword");
    const away = moved ? { live: true, departure: { operation: "op-1", status: "continued", machine: core.here(), agent: e.agent, agentName: e.agentName, key: e.key, profile: "", checkedAt: new Date().toISOString() },
      guard: { operation: "op-1", mode: "block", effective: false, state: "unprotected", action: "setup", machine, problem: `The hook isn't set up on ${machine}.` } } : {};
    const m = actions.model({ ...e, machine, live: false, places: [], ...away });
    return { primary: m.primary?.label, chevron: m.chevron.map((c: any) => ({ label: c.label, disabled: !!c.disabled, why: c.why || "" })),
      move: m.move.flatMap((g: any) => g.items).map((i: any) => i.label), more: m.more.map((i: any) => ({ label: i.label, disabled: !!i.disabled, why: i.why || "" })),
      flat: actions.actionsFor({ ...e, machine, live: false, places: [], ...away }).map((a: any) => a.label) };
  }, [machine, presence, moved] as const);
}

test("a session on a machine with hopsesh running continues there; bringing it is the other choice", async ({ page }) => {
  const m = await actionsOn(page, "atelier", "login");
  expect(m.primary).toBe("Continue on atelier");
  expect(m.chevron.map((c) => c.label).join("|")).toMatch(/^Bring to .+…$/);
});

test("a session on an SSH-only machine is brought here; continuing there needs hopsesh", async ({ page }) => {
  const m = await actionsOn(page, "lurus", "ssh");
  expect(m.primary).toMatch(/^Bring to /);
  const there = m.chevron.find((c) => c.label === "Continue on lurus");
  expect(there?.disabled).toBe(true);
  expect(there?.why).toContain("Needs hopsesh on lurus");
});

test("a copy that moved away goes to its active copy, never continued, brought or moved without asking", async ({ page }) => {
  const m = await actionsOn(page, "atelier", "login", true);
  expect(m.primary).toBe("Go to the active copy here");
  expect(m.chevron.map((c) => c.label)).toEqual(["Continue this old copy anyway…"]);
  expect(m.move).toEqual([]);
  expect(m.more.find((i) => i.label === "Change folder…")).toMatchObject({ disabled: true, why: expect.stringContaining("change the active copy's folder instead") });
  // The palette's flat list never reaches Continue on, Bring, Resume or Move either.
  expect(m.flat.filter((l) => /^(Continue on|Bring|Resume|Move|Send|Continue with|Hand off)/.test(l))).toEqual([]);
  // Without hopsesh running there, continuing it anyway can't happen at all; it says why.
  const ssh = await actionsOn(page, "lurus", "ssh", true);
  expect(ssh.primary).toBe("Go to the active copy here");
  expect(ssh.chevron[0]).toMatchObject({ label: "Continue this old copy anyway…", disabled: true, why: expect.stringContaining("Needs hopsesh on lurus") });
});

test("an older hopsesh there asks for an update first", async ({ page }) => {
  const m = await actionsOn(page, "roys-mbp", "older");
  expect(m.chevron.find((c) => c.label === "Continue on roys-mbp")?.why).toContain("Update hopsesh on roys-mbp");
});

test("bringing a session that is working on another machine offers to wait or to join it there", async ({ page }) => {
  await page.evaluate(async () => {
    const core = await import("/core.js" as string), plan = await import("/plan.js" as string);
    const e = core.state.scan.groups.flatMap((g: any) => g.entries).find((x: any) => x.title === "Find the codeword");
    plan.bringLive({ ...e, machine: "atelier", live: true });
  });
  const sheet = page.locator("#sheet");
  await expect(sheet).toContainText("It's working on atelier right now");
  await expect(sheet.getByRole("button", { name: /Wait until it's idle/ })).toBeVisible();
  await expect(sheet.getByRole("button", { name: /Join it on atelier instead/ })).toBeVisible();
  await sheet.getByRole("button", { name: "Cancel" }).click();
  await expect(sheet).toBeHidden();
});
