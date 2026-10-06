// The app window's side of the hopsesh Terminal window: the open tabs (TerminalTabs, then
// each hopsesh:terminal event), their chips on session rows and in the sidebar, "Needs
// you", the entry points' routing (In this window / In my terminal), and the quit
// confirmation that lists the programs still running.
import { api, on, h, state, toast, fail, sys, dialog, count, icon } from "./core.js";

export const tabs = new Map(); // id → the tab (TermTab)
const listeners = [];
// onTabs calls fn whenever a tab changes (the screens draw their chips again).
export function onTabs(fn) { listeners.push(fn); }
const changed = () => { for (const f of listeners) f(); badge(); };

export async function loadTabs() {
  try {
    for (const t of await api("TerminalTabs")) tabs.set(t.id, t);
    for (const x of await api("ExternalExits")) exits.set(x.machine + "\u0000" + x.key, x);
  } catch { /* no terminal here */ }
  changed();
}

// exits are how launches in the user's terminal app ended, where it can say (iTerm2 with
// its Python API): by session.
export const exits = new Map();
// exitOf is how a session's last run in the user's terminal app ended, if hopsesh knows.
export const exitOf = (e) => exits.get(e.machine + "\u0000" + e.key);
// exitWords are an external exit for people: "exited 0", "exited 3", "closed".
export const exitWords = (x) => (x.closed ? "closed" : `exited ${x.code}`);

on("hopsesh:external-exit", (x) => {
  if (x.key) exits.set(x.machine + "\u0000" + x.key, x);
  const what = x.closed ? "its tab was closed" : `exited with code ${x.code}`;
  toast(`“${x.title}” in ${x.terminal}: ${what}`);
  changed();
});

on("hopsesh:terminal", (t) => {
  if (!t.state) tabs.delete(t.id); else tabs.set(t.id, t);
  changed();
});

const live = (t) => t.state !== "exited";
// tabFor is the live tab a session runs in here, if any: its own, or the bring-back's
// that saved it and still runs it.
export const tabFor = (e) => [...tabs.values()].find((t) => (t.kind === "session" || t.kind === "bring") && t.key && t.machine === e.machine && t.key === e.key && live(t));
export const waiting = () => [...tabs.values()].filter((t) => t.attention);
export const running = () => [...tabs.values()].filter(live);
// strayWaiting are the waiting tabs no session row stands for (steps, sign-ins, shells).
export const strayWaiting = () => waiting().filter((t) => t.kind !== "session" || !(state.scan && state.scan.groups.some((g) => g.entries.some((e) => e.machine === t.machine && e.key === t.key))));

// where is the setting: here or terminal.
export const where = () => (state.info?.where === "terminal" ? "terminal" : "here");
export const IN_A_TAB = "It's running in a tab here. End it first.";

// showTerminal brings the hopsesh Terminal window forward (a tab in front, when given).
export function showTerminal(id) {
  return (id ? api("TerminalFocus", id) : api("TerminalShow")).catch(fail);
}

// opened says what an entry point did when it opened elsewhere than chosen.
function opened(r) {
  if (r?.notice) toast(r.notice);
}

// resume continues a session: "here", "terminal", or "" (the setting).
export async function resume(e, w = "") {
  let r;
  try { r = await api("ResumeSession", e.machine, e.key, w); } catch (err) { fail(err); return; }
  opened(r);
}

// openResult starts what a done screen offers (OpenResult), the same way.
export async function openResult(w = "") {
  let r;
  try { r = await api("OpenResult", w); } catch (err) { fail(err); return; }
  opened(r);
}

// openBrought starts a bring-back's next command (OpenBrought), the same way.
export async function openBrought(journal, w = "") {
  let r;
  try { r = await api("OpenBrought", journal, w); } catch (err) { fail(err); return; }
  opened(r);
}

// openShell opens the login shell in a session's folder (or home).
export async function openShell(e) {
  try { await api("OpenShell", e ? e.machine : "", e ? e.key : ""); } catch (err) { fail(err); }
}

// moveToTerminal ends a session's tab here and runs it in the user's terminal app.
export async function moveToTerminal(t) {
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Move “${t.title}” to ${sys.terminal}?`),
    h("div", { class: "muted", style: "line-height:1.5" }, `hopsesh ends it here and runs the same ${t.command} in ${sys.terminal}. A turn in progress stops.`),
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => { d.close(); try { await api("TerminalOpenElsewhere", t.id); } catch (err) { fail(err); } } }, `End here and open in ${sys.terminal}`)));
}

// signIn runs a cloud's sign-in in a tab that records nothing (or the user's terminal).
export async function signIn(c, w = "") {
  try { opened(await api("SignIn", c.name, w)); } catch (err) { fail(err); }
}

// The title bar's Terminal button, with how many tabs wait.
function badge() {
  const b = document.querySelector("#btn-terminal");
  if (!b) return;
  const n = waiting().length, r = running().length;
  b.hidden = tabs.size === 0;
  const dot = b.querySelector(".term-n");
  dot.textContent = n ? String(n) : "";
  dot.hidden = !n;
  b.setAttribute("aria-label", n ? `Terminal, ${count(n, "tab")} waiting for you` : `Terminal, ${count(r, "program")} running`);
}

// consequence is what quitting does to a tab's program.
function consequence(t) {
  if (t.kind === "bring") return `no copy saved yet: ${t.agent || "the agent"} saves it only after you send one message`;
  if (t.kind === "step") return "the hand-off stops waiting for it";
  if (t.kind === "signin") return "the sign-in stops";
  if (t.kind === "shell") return "the shell and what runs in it end";
  if (t.attention) return "it waits for you; the conversation is kept as it is";
  return "a turn in progress stops";
}

// quitDialog is the confirmation when quitting (or closing the window, with the setting
// off) while programs run in tabs.
function quitDialog(list) {
  const run = (list || [...tabs.values()]).filter(live);
  if (!run.length) { api("QuitAndEnd"); return; }
  const n = run.length;
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Quit hopsesh and end ${count(n, "program")}?`),
    h("div", { class: "muted", style: "line-height:1.5" }, "Quitting ends every program in hopsesh's tabs. Claude Code and Codex keep each conversation up to its last finished turn, so you can resume them later."),
    h("ul", { class: "quit-list", "aria-label": "Programs running in tabs" }, run.map((t) => h("li", {},
      h("span", { class: "chip " + (t.attention ? "st-needs" : "st-running") }, t.attention ? "waiting" : "running"), " ",
      h("b", { style: "font-weight:500" }, t.title), h("span", { class: "muted" }, " · " + consequence(t))))),
    h("div", { class: "dlg-foot" },
      h("button", { class: "btn", onclick: () => { d.close(); showTerminal(); } }, "Show the tabs"),
      h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn danger", onclick: () => { d.close(); api("QuitAndEnd").catch(fail); } }, `Quit and end ${count(n, "program")}`)));
  d.querySelector(".btn.danger").focus();
}
on("hopsesh:quit", (list) => quitDialog(list));

// A sign-in tab ended well: the card checked the login again.
on("hopsesh:signed-in", (s) => {
  const t = s.test;
  if (t?.ok || t?.account) toast(`${s.title}: signed in. hopsesh checked with ${s.driver}.`);
  else toast(`${s.title}: ${t?.error || "still not signed in"}`);
  state.stale = true;
  for (const f of signedIn) f(s);
});
const signedIn = [];
export function onSignedIn(fn) { signedIn.push(fn); }

// Ctrl+` brings the terminal forward (macOS: the menu's shortcut does).
document.addEventListener("keydown", (ev) => {
  if (sys.mac || !ev.ctrlKey || ev.code !== "Backquote") return;
  ev.preventDefault();
  showTerminal();
});
