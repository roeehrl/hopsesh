// The app window's side of the hopsesh Terminal window: the open tabs (TerminalTabs, then
// each hopsesh:terminal event), their chips on session rows and in the sidebar, "Needs
// you", the entry points' routing (In this window / In my terminal), and the quit
// confirmation that lists the programs still running.
import {showWorkspace} from "./terminal-workspace.js";
import { api, on, h, state, toast, fail, sys, dialog, count, icon, ago } from "./core.js";

export const tabs = new Map(); // id → the tab (TermTab)
const revisions = new Map(); // retain removals so a delayed startup RPC cannot resurrect them
function applyTab(t) {
  const revision = t.revision || 0;
  // Zero/absent revisions are the legacy unordered contract. Accept repeated
  // legacy updates until this ID has positive version evidence; afterwards even
  // a legacy RPC/event cannot overwrite or resurrect its newer metadata.
  if ((revisions.get(t.id) || 0) > 0 && revision <= revisions.get(t.id)) return;
  revisions.set(t.id, revision);
  if (!t.state) tabs.delete(t.id); else tabs.set(t.id, t);
}
const listeners = [];
// onTabs calls fn whenever a tab changes (the screens draw their chips again).
export function onTabs(fn) { listeners.push(fn); }
const changed = () => { for (const f of listeners) f(); badge(); };

export async function loadTabs() {
  try {
    for (const t of await api("TerminalTabs")) applyTab(t);
    for (const x of await api("ExternalExits")) exits.set(x.machine + "\u0000" + x.key, x);
    // Native startup can open a tab before this document registers its event
    // listeners. Recover the pending view from backend state, not event timing.
    if (tabs.size) {
      const workspace = await api("TerminalWorkspace");
      if (workspace.placement !== "separate") await showWorkspace(workspace);
    }
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
  applyTab(t);
  changed();
});

const live = (t) => t.state !== "exited";
// tabFor is the live tab a session runs in here, if any: its own, or the bring-back's
// that saved it and still runs it.
export const tabFor = (e) => [...tabs.values()].find((t) => (t.kind === "session" || t.kind === "bring") && t.association !== "Session association not confirmed" && t.association !== "Checking session association" && t.key && t.machine === e.machine && t.key === e.key && live(t));
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
  b.hidden = false;
  const dot = b.querySelector(".term-n");
  dot.textContent = n ? String(n) : "";
  dot.hidden = !n;
  b.setAttribute("aria-label", n ? `Terminal, ${count(n, "tab")} waiting for you` : `Terminal, ${count(r, "program")} running`);
}

// working: an agent's tab whose program is busy. Claude Code and Codex redraw their
// status line all through a turn, so a tab quiet for a few seconds is between turns (or
// waits for the user).
export const working = (t) => live(t) && (t.kind === "session" || t.kind === "bring") && t.state === "running";
// reopens: hopsesh writes the tab's session down when it quits, and resumes it in a tab
// when it starts again (a new session has no key until its agent saved it).
const reopens = (t) => (t.kind === "session" && t.key && t.association !== "Checking session association") || (t.kind === "bring" && t.saved && t.key) || (t.kind === "remote" && t.agent && (t.key || t.remote));

// consequence is what quitting (or restarting for an update) does to a tab's program.
function consequence(t, update) {
  const later = update ? "after the update" : "next time";
  if (t.kind === "remote") return reopens(t) ? `it keeps running on ${t.machine}, and hopsesh shows it again ${later}` : `it keeps running on ${t.machine}`;
  if (t.kind === "bring" && !t.saved) return `no copy saved yet: ${t.agent || "the agent"} saves it only after you send one message`;
  if (t.kind === "step") return "the hand-off stops waiting for it";
  if (t.kind === "signin") return "the sign-in stops";
  if (t.kind === "shell") return "the shell and what runs in it end";
  const back = !reopens(t) ? "" : update ? ", and hopsesh resumes it after the update" : ", and hopsesh offers to resume it next time";
  if (working(t)) return "its turn in progress stops: the conversation is kept up to its last finished turn" + back;
  if (t.attention) return "it waits for you; the conversation is kept as it is" + back;
  return "between turns; the conversation is kept as it is" + back;
}
const chipOf = (t) => t.attention ? ["st-needs", "waiting"] : working(t) ? ["st-running", "working"] : (t.kind === "session" || t.kind === "bring" || t.kind === "remote") ? ["st-idle", "idle"] : ["st-running", "running"];

// endDialog asks before hopsesh quits or restarts while programs run in tabs: what each
// one loses, and, while agents are mid-turn, the choice to wait for them. It resolves to
// "now", "idle" (once no agent works) or "" (cancelled). what: "quit" or "update".
export function endDialog(what, list) {
  const run = (list || [...tabs.values()]).filter(live);
  const ending = run.filter((t) => t.kind !== "remote");
  const busy = run.filter(working);
  const n = ending.length;
  const title = what === "update" ? (n ? `Restart and end ${count(n, "program")}?` : "Restart hopsesh?") : (n ? `Quit hopsesh and end ${count(n, "program")}?` : "Quit hopsesh?");
  const lead = (what === "update" ? "Installing the update restarts hopsesh, which ends the programs in its tabs." : "Quitting ends the programs in hopsesh's tabs.") +
    " Claude Code and Codex keep each conversation up to its last finished turn" + (what === "update" ? ", and the new version resumes them in tabs." : ", and hopsesh offers to resume them when it starts again.");
  const ok = what === "update" ? (n ? `Install and end ${count(n, "program")}` : "Install and restart") : (n ? `Quit and end ${count(n, "program")}` : "Quit");
  return new Promise((resolve) => {
    let answer = "";
    const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, title),
      h("div", { class: "muted", style: "line-height:1.5" }, lead),
      busy.length ? h("div", { class: "end-busy", style: "line-height:1.5" }, `${count(busy.length, "agent is", "agents are")} in the middle of a turn: ${busy.map((t) => "“" + t.title + "”").join(", ")}. What a turn has done so far is lost if it stops now.`) : null,
      h("ul", { class: "quit-list", "aria-label": "Programs running in tabs" }, run.map((t) => {
        const [cls, word] = chipOf(t);
        return h("li", { "data-tab": t.id }, h("span", { class: "chip " + cls }, word), " ",
          h("b", { style: "font-weight:500" }, t.title), h("span", { class: "muted" }, " · " + consequence(t, what === "update")));
      })),
      h("div", { class: "dlg-foot" },
        h("button", { class: "btn", onclick: () => { d.close(); showTerminal(); } }, "Show the tabs"),
        h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
        busy.length ? h("button", { class: "btn primary", "data-act": "idle", onclick: () => { answer = "idle"; d.close(); } }, what === "update" ? "Update when they're idle" : "Quit when they're idle") : null,
        h("button", { class: "btn danger", "data-act": "now", onclick: () => { answer = "now"; d.close(); } }, ok)));
    d.addEventListener("close", () => resolve(answer), { once: true });
    d.querySelector(busy.length ? '[data-act="idle"]' : ".btn.danger").focus();
  });
}

// IDLE_FOR is how long every agent must have been between turns before a deferred quit
// or update goes ahead: a turn can pause between two steps.
const IDLE_FOR = 10_000;
let deferred = null; // { what, cancel }

// whenIdle runs go once no agent in a tab has worked for IDLE_FOR (what: "quit" or
// "update", for the notice). Starting another replaces it; cancelDeferred stops it.
export function whenIdle(what, go) {
  cancelDeferred();
  let quietSince = 0, timer = 0;
  const check = () => {
    const busy = [...tabs.values()].filter(working).length;
    if (busy) { quietSince = 0; return; }
    if (!quietSince) quietSince = Date.now();
    if (Date.now() - quietSince >= IDLE_FOR) { const d = deferred; cancelDeferred(); if (d) go(); }
  };
  timer = setInterval(check, 1000);
  listeners.push(check);
  deferred = { what, cancel: () => { clearInterval(timer); const i = listeners.indexOf(check); if (i >= 0) listeners.splice(i, 1); } };
  const n = [...tabs.values()].filter(working).length;
  toast(`hopsesh ${what === "update" ? "updates" : "quits"} once ${n === 1 ? "the agent finishes its turn" : `the ${n} agents finish their turns`}.`, { label: "Cancel", run: cancelDeferred });
  for (const f of deferredListeners) f();
  check();
}
export function cancelDeferred() {
  if (!deferred) return;
  deferred.cancel();
  deferred = null;
  for (const f of deferredListeners) f();
}
// waitingFor is what waits for the agents: "quit", "update" or "".
export const waitingFor = () => deferred?.what || "";
const deferredListeners = [];
export function onDeferred(fn) { deferredListeners.push(fn); }

// installGuarded installs the update (InstallUpdate, which restarts the app), asking
// first while programs run in tabs; progress(text) shows how it goes ("" once it failed
// or was cancelled). With agents mid-turn the user may let it wait for them.
export async function installGuarded(latest, progress) {
  if (running().length) {
    const a = await endDialog("update");
    if (!a) return;
    if (a === "idle") { whenIdle("update", () => install(latest, progress)); return; }
  }
  await install(latest, progress);
}
async function install(latest, progress) {
  progress(`Downloading and checking ${latest}…`);
  try {
    await api("InstallUpdate");
    progress("Restarting…");
  } catch (e) {
    fail(e);
    progress("");
  }
}

// quitDialog is the confirmation when quitting (or closing the window, with the setting
// off) while programs run in tabs.
async function quitDialog(list) {
  const run = (list || [...tabs.values()]).filter(live);
  if (!run.length) { api("QuitAndEnd"); return; }
  const a = await endDialog("quit", list);
  if (a === "now") api("QuitAndEnd").catch(fail);
  else if (a === "idle") whenIdle("quit", () => api("QuitAndEnd").catch(fail));
}
on("hopsesh:quit", (list) => quitDialog(list));

// reopenOffer resumes, in tabs, the sessions the tabs ran when hopsesh last quit: at once
// after an update (its dialog said so), else when the user says so.
export async function reopenOffer() {
  const r = await api("ReopenOffer").catch(() => null);
  if (!r?.tabs?.length) return;
  const reopen = async () => {
    try {
      const done = await api("Reopen");
      if (done.failed.length) toast(`Resumed ${count(done.opened, "session")}. Not resumed: ${done.failed.join("; ")}`);
      else toast(`Resumed ${count(done.opened, "session")} in tabs`);
    } catch (e) { fail(e); }
  };
  if (r.update) { reopen(); return; }
  const n = r.tabs.length;
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Resume ${count(n, "session")} that ${n === 1 ? "was" : "were"} running?`),
    h("div", { class: "muted", style: "line-height:1.5" }, `When hopsesh quit ${ago(r.at)}, ${n === 1 ? "this session was" : "these sessions were"} open in its tabs. hopsesh resumes each in a tab, from its last finished turn.`),
    h("ul", { class: "quit-list", "aria-label": "Sessions to resume" }, r.tabs.map((t) => h("li", {},
      h("b", { style: "font-weight:500" }, t.title),
      h("span", { class: "muted" }, " · " + [t.agent, t.kind === "remote" ? "on " + t.machine : "", t.working ? "a turn was in progress" : ""].filter(Boolean).join(" · "))))),
    h("div", { class: "dlg-foot" },
      h("button", { class: "btn", onclick: () => { d.close(); api("ForgetReopen").catch(fail); } }, "Not now"),
      h("button", { class: "btn primary", onclick: () => { d.close(); reopen(); } }, `Resume ${count(n, "session")}`)));
  d.querySelector(".btn.primary").focus();
}

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

export function associateShell(e) {
 const shells=[...tabs.values()].filter(t=>t.kind==="shell" && live(t));
 if(!shells.length){toast("Open a shell first.");return;}
 const choose=h("select",{"aria-label":"Shell tab",class:"field"},shells.map(t=>h("option",{value:t.id},t.title)));
 const d=dialog(h("h2",{},"Organize a shell with this conversation"),h("p",{class:"muted"},"This groups the tab for convenience. It does not change the shell’s folder or create a conversation fork."),choose,h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Cancel"),h("button",{class:"btn",onclick:async()=>{try{await api("AssociateShell",choose.value,"","");d.close();}catch(err){fail(err);}}},"Move to Other terminals"),h("button",{class:"btn primary",onclick:async()=>{try{await api("AssociateShell",choose.value,e.machine,e.key);d.close();toast("Shell grouped with this conversation");}catch(err){fail(err);}}},"Group shell here")));
}
