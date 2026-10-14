import { test, expect, Page } from "@playwright/test";
import { fresh } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

// actionsOn is what the session panel offers for a copy of a session on another machine,
// hopsesh's presence there as given.
async function actionsOn(page: Page, machine: string, presence: string) {
  return page.evaluate(async ([machine, presence]) => {
    const core = await import("/core.js" as string), actions = await import("/actions.js" as string);
    core.state.scan.machines.push({ name: machine, status: "ok", local: false, os: "darwin", agents: [], agentNames: [], sessions: 1, hopsesh: presence === "ssh" ? "" : "0.5.0", presence });
    const e = core.state.scan.groups.flatMap((g: any) => g.entries).find((x: any) => x.title === "Find the codeword");
    const m = actions.model({ ...e, machine, live: false, places: [] });
    return { primary: m.primary?.label, chevron: m.chevron.map((c: any) => ({ label: c.label, disabled: !!c.disabled, why: c.why || "" })) };
  }, [machine, presence]);
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

test("an older hopsesh there asks for an update first", async ({ page }) => {
  const m = await actionsOn(page, "roys-mbp", "older");
  expect(m.chevron.find((c) => c.label === "Continue on roys-mbp")?.why).toContain("Update hopsesh on roys-mbp");
});
