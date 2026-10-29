// Movement protection: how the copy left behind by a move is blocked or advised until
// the session moves back, whether the agents' hooks can actually do it, and the moved /
// original / returned indicators every view shares. Titles are never changed: status
// lives in hopsesh's own views.
import { api, h, dialog, ask, toast, fail, errText, state, here, sys, icon, ICONS, scanEntry, selected, onRefusal } from "./core.js";

// ---- CLI requirement -------------------------------------------------------------
// The skill tells agents to run hopsesh and the protection hooks run it, so installing
// either needs the command. withCLI offers to install it, then retries.
export async function withCLI(fn) {
  try { return await fn(); }
  catch (e) {
    const msg = errText(e);
    if (!msg.startsWith("cli-required: ")) throw e;
    const yes = await ask({ title: "Install the hopsesh command first?", ok: "Install command and continue",
      body: (() => { const why = msg.slice("cli-required: ".length).replace(/ Install the command first.*$/, "").replace(/\.$/, ""); return why[0].toUpperCase() + why.slice(1) + ". Installing links the command into ~/.local/bin (no password)."; })() });
    if (!yes) return null;
    await api("InstallCLI", true);
    return fn();
  }
}

// ---- Hook health -----------------------------------------------------------------
let health = null, checking = null;
export const hookHealth = () => health;
export async function loadHookHealth(force = false) {
  if (checking && !force) return checking;
  checking = api("HookHealth", force).then(d => (health = d)).catch(() => health).finally(() => { checking = null; });
  return checking;
}

const AGENT = { claude: "Claude Code", codex: "Codex" };
const agentName = id => AGENT[id] || id;

// healthNotice is the Sessions view's one card for what protection still needs: a short
// checklist (the command, then every detected agent, even one that can't take the hook,
// with why) and one action that sets up everything hopsesh can, plus the one step only
// the agent can do (approving its hooks in Codex). Once the user sets up or checks again,
// the results stay on the card until Done; "Not now" lasts until the problems change.
let session = null, busy = false;
const hookKey = (x) => x.agent + "\u0000" + (x.profile || "");

// tools says what one agent's hook covers here: "for Terminal and Claude desktop", "for
// ChatGPT desktop (built-in Codex 0.162.0-alpha.17.2)".
function tools(x) {
  const app = x.app && x.app !== "desktop" ? x.app : agentName(x.agent);
  if (x.runtime === "app") return `for ${app} desktop (built-in ${agentName(x.agent)}${x.version ? " " + x.version : ""})`;
  const parts = [];
  if (x.cli) parts.push("Terminal");
  if (x.app) parts.push(app + " desktop");
  return parts.length ? "for " + parts.join(" and ") : "";
}

function hookRow(x, r, name) {
  const t = x.trust?.state, app = agentName(x.agent), base = { name, tools: tools(x), x };
  if (!x.supported) return { ...base, na: true, note: x.reason || "not available here" };
  const failed = (!x.installed && r?.result === "failed" && r) || (x.result === "failed" && x);
  if (failed) return { ...base, todo: true, failed: true, note: "couldn't set up: " + (failed.reason || "unknown error") };
  if (!x.enabled) return { ...base, wait: true, note: x.reason || "turned off in " + app };
  if (!x.installed) return { ...base, todo: true, note: "not set up" };
  if (t === "needs-review") return { ...base, wait: true, review: true, note: "installed · waiting for your approval in " + app };
  if (t === "disabled") return { ...base, wait: true, review: true, note: "installed · turned off in " + app };
  if (t === "missing") return { ...base, todo: true, note: "not seen by " + app };
  if (t === "unknown") return { ...base, ok: true, note: "set up (approval not checked)" };
  return { ...base, ok: true, note: r ? "installed" : "set up" };
}

export function healthNotice(onChange) {
  const d = health;
  if (!d || d.mode === "off") return null;
  const hooks = d.hooks || [];
  const several = (id) => hooks.filter((x) => x.agent === id).length > 1;
  const cliNote = session?.cli ? "couldn't install: " + session.cli : d.cli?.state === "dangling" ? "points to a moved or deleted app" : "not installed";
  const rows = [d.cliReady ? { ok: true, name: "hopsesh command", note: "" } : { todo: true, failed: !!session?.cli, name: "hopsesh command", note: cliNote }];
  for (const x of hooks) rows.push(hookRow(x, session?.results?.[hookKey(x)], agentName(x.agent) + " hook" + (several(x.agent) ? ` (${x.profileLabel || x.profile || "default account"})` : "")));
  const todo = rows.filter((r) => r.todo), waits = rows.filter((r) => r.wait), na = rows.filter((r) => r.na);
  const allSet = !todo.length && !waits.length && !na.length;
  if (!session && (allSet || d.dismissed)) return null;
  const skill = state.info?.skillState;
  const skillRow = skill && skill !== "checking" ? { ok: skill === "current", skill: true, name: "hopsesh skill", note: skill === "current" ? "recommended · set up" : skill === "stale" ? "recommended · out of date (Settings › Agents)" : "recommended · not installed (Settings › Agents)" } : null;
  const waiting = waits.find((r) => r.review)?.x;
  const blocking = d.mode === "block";
  const why = blocking
    ? "When you move a session, hopsesh blocks the copy you left until you move it back, so the two never drift apart."
    : "When you move a session, hopsesh warns in the copy you left until you move it back.";
  const title = allSet ? "Protection is set up"
    : todo.length ? (session?.ran ? "Protection isn't fully set up" : "Protect moved sessions")
    : waiting ? `Approve hopsesh in ${agentName(waiting.agent)} to finish`
    : waits.length ? "Protection is turned off in an agent" : "Some agents can't be protected yet";
  const lead = allSet ? "Moved sessions are protected on this machine."
    : todo.length ? (session?.ran ? "Some steps did not finish; each says why." : why + " This needs:")
    : waiting ? (waiting.app && waiting.agent === "codex"
      ? `In ${waiting.app} desktop, open Codex and choose Review hooks (or run /hooks in the terminal), then trust the two hopsesh hooks. hopsesh never approves hooks for you.`
      : `hopsesh set up its hook; ${agentName(waiting.agent)} runs a new hook only after you approve it. hopsesh never approves hooks for you.`)
    : "Each agent says what it needs.";
  const run = async () => {
    busy = true; onChange();
    const s = { ran: true, results: {}, cli: "" };
    try { if (!d.cliReady) await api("InstallCLI", true); } catch (e) { s.cli = errText(e); }
    if (!s.cli) {
      try { for (const x of (await api("InstallNoticeHooks", "", "")) || []) s.results[hookKey(x)] = x; }
      catch (e) { s.error = errText(e); }
    }
    await loadHookHealth(true);
    session = s; busy = false; onChange();
  };
  const recheck = async () => { session = session || { results: {} }; busy = true; onChange(); await loadHookHealth(true); busy = false; onChange(); };
  const mark = (r) => r.ok ? "✓" : r.wait ? "!" : r.na ? "–" : r.failed ? "✕" : "○";
  const item = (r) => h("li", { class: r.ok ? "ok" : r.wait ? "wait" : r.na ? "na" : r.failed ? "fail" : "todo" },
    h("span", { class: "mark", "aria-hidden": "true" }, mark(r)),
    h("span", { class: "protect-item" }, h("span", {}, r.name, r.note ? h("span", { class: "muted" }, " · " + r.note) : null),
      r.tools ? h("span", { class: "protect-tools muted" }, r.tools) : null));
  return h("section", { class: "card notice protect-card" + (allSet ? "" : " warn-card"), role: "status", "aria-label": "Movement protection", "aria-busy": busy ? "true" : "false" },
    h("div", { class: "protect-body" },
      h("b", {}, title),
      h("span", { class: "muted" }, lead),
      session?.error ? h("span", { class: "protect-error" }, "hopsesh could not set up protection: " + session.error) : null,
      h("ul", { class: "protect-list" }, rows.map(item), skillRow ? item(skillRow) : null)),
    h("div", { class: "protect-actions" },
      todo.length ? h("button", { class: "btn primary", disabled: busy, onclick: run }, session?.ran ? "Try again" : "Set up protection") : null,
      waiting ? h("button", { class: todo.length ? "btn" : "btn primary", onclick: () => trustHelp(waiting, onChange) }, `Approve in ${agentName(waiting.agent)}…`) : null,
      waiting?.app ? h("button", { class: "btn", onclick: () => api("OpenAgentApp", waiting.agent).catch(fail) }, `Open ${waiting.app} desktop`) : null,
      session ? h("button", { class: allSet ? "btn primary" : "btn", onclick: () => { session = null; onChange(); } }, "Done")
        : h("button", { class: "btn", onclick: async () => { try { await api("DismissProtection", d.fingerprint || ""); d.dismissed = true; } catch (e) { fail(e); } onChange(); } }, "Not now"),
      h("button", { class: "link", disabled: busy, onclick: recheck }, "Check again")));
}

function trustHelp(p, onChange) {
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Approve the hopsesh hooks in ${agentName(p.agent)}`),
    h("p", { style: "margin:0;line-height:1.5" }, `${agentName(p.agent)} runs a new or changed hook only after you approve it, so hopsesh can't block or warn there yet.`),
    h("ol", { style: "margin:0;padding-left:18px;line-height:1.6" },
      h("li", {}, p.app && p.agent === "codex" ? `Open ${p.app} desktop, open Codex and choose Review hooks; in the terminal, run ` : `Open ${agentName(p.agent)}. In the app, answer its Review hooks prompt; in the terminal, run `, h("code", {}, "/hooks"), "."),
      h("li", {}, "Check that each of the two hopsesh hooks runs ", h("code", {}, "hopsesh notice-hook"), ", then trust it (Allow all, or select both)."),
      h("li", {}, "Come back here and choose Check again.")),
    h("p", { class: "muted", style: "margin:0;font-size:12px" }, `${agentName(p.agent)} asks again whenever a hook changes. hopsesh never approves hooks for you.`),
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Close"),
      h("button", { class: "btn primary", onclick: async () => { d.close(); await loadHookHealth(true); onChange(); } }, "Check again")));
}

// ---- Indicators ------------------------------------------------------------------
const where = (agent, machine) => `${agent || "?"} on ${machine || "?"}`;
export const day = t => { const d = new Date(t); return !t || isNaN(d) || d.getFullYear() < 2000 ? "" : d.toLocaleDateString(undefined, { month: "short", day: "numeric" }); };
const machineWord = (m) => (m === here() ? sys.here : m);

// role is what a copy is in its session's journey, for chips, glyphs and words. The list
// and the inspector draw an old copy's own marks and banner instead (below).
export function role(e) {
  const dep = e.departure, ar = e.arrival;
  // What a chip names: the agent, and the machine when it is not this copy's own.
  const short = (name, agent, machine, cloud) => cloud || ((name || agent || "?") + (machine && machine !== e.machine ? ` on ${machine}` : ""));
  if (isOldCopy(e)) {
    const drift = driftOf(e);
    return { kind: drift || "moved-out", icon: ICONS.movedOut, chip: `Moved to ${movedTo(e)}`, tone: drift ? "st-error" : "st-away", short: `Moved to ${movedTo(e)}`, line: protectionLine(e).text };
  }
  if (dep && dep.status === "forked") { const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine); return { kind: "forked-out", glyph: "⑂", chip: "Separate copy", tone: "st-idle", short: `Copied to ${short(dep.agentName, dep.agent, dep.machine, dep.cloud)}`, line: `A separate conversation continues in ${to}; this one is not blocked` }; }
  if (ar) {
    // The chip names the machine alone when the agent stayed the same.
    const from = where(ar.agentName || ar.agent, ar.machine), fromShort = ar.agent === e.agent && ar.machine && ar.machine !== e.machine ? ar.machine : short(ar.agentName, ar.agent, ar.machine, "");
    if (ar.kind === "returned") return { kind: "returned", glyph: "↩", chip: `Moved back from ${fromShort}`, tone: "st-idle", line: `Moved back from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · block cleared` };
    if (ar.kind === "continuation") return { kind: "continuation", glyph: "↳", chip: "Continuation", tone: "st-idle", line: `Bounded continuation of ${from} · the original is kept` };
    if (ar.fork) return { kind: "fork", glyph: "⑂", chip: "Separate copy", tone: "st-idle", line: `Separate conversation from ${from}${day(ar.at) ? " · " + day(ar.at) : ""}` };
    return { kind: "moved", icon: ICONS.movedIn, chip: `Moved from ${fromShort}`, tone: "st-moved", line: `Moved from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · continue here` };
  }
  return null;
}

// chip is the small status lozenge (word + glyph or icon, never color alone).
export function chip(e) {
  const r = role(e);
  if (!r) return null;
  return h("span", { class: "chip role-chip " + r.tone, title: r.line, "aria-label": `${r.chip}: ${r.line}` }, r.icon ? icon(r.icon, 11) : h("span", { "aria-hidden": "true" }, r.glyph + " "), r.chip);
}

// ---- The copy a move left behind -------------------------------------------------
// An old copy is the copy a move left behind; the active copy is where the session went
// (a fork is a separate conversation, never an old copy). The window never continues,
// moves or brings an old copy without asking (Continue this old copy anyway…).
export const isOldCopy = (e) => !!(e && !e.cloud && e.departure && e.departure.status !== "forked");

// movedTo is where an old copy went, for "Moved to …": the machine (this one's own word),
// with the agent when it changed; a cloud by its title.
export function movedTo(e) {
  const d = e.departure || {};
  if (d.cloud) return d.cloud;
  const agent = d.agentName || agentName(d.agent);
  const changed = d.agent && d.agent !== e.agent;
  if (!d.machine || d.machine === e.machine) return changed ? agent : agent + (d.profileLabel ? ` (${d.profileLabel})` : "");
  return changed ? `${agent} on ${machineWord(d.machine)}` : machineWord(d.machine);
}
// activeAt names the active copy's place in a sentence: "on studio in Codex", "on this
// Mac in Claude Code", "in Claude Code cloud".
export function activeAt(e) {
  const d = e.departure || {};
  return d.cloud ? `in ${d.cloud}` : `on ${machineWord(d.machine)} in ${d.agentName || agentName(d.agent)}`;
}
// destination is the active copy's place, for the buttons: "here", "on studio", "in …".
export const destination = (e) => { const d = e.departure || {}; return d.cloud ? `in ${d.cloud}` : d.machine === here() ? "here" : `on ${d.machine}`; };

const liveNow = (e) => { const p = state.presence?.[e.machine + "\u0000" + e.key]; return p ? !!p.live : !!e.live; };
export const stillOpen = liveNow;

// protectionOf is whether the hook on the old copy's own machine refuses new prompts in
// it: protected, unprotected, unknown, off (the settings leave old copies alone) or
// released (the block was removed), as the backend read it, with why and the fix it
// offers. Checking lasts only while that machine's hooks are being read.
export function protectionOf(e) {
  const g = e.guard || {};
  let st = g.state;
  if (!st) st = g.mode === "released" ? "released" : g.mode === "off" ? "off" : g.effective ? "protected" : (g.noHopsesh || g.problem) && !g.checking && !e.cached ? "unprotected" : "unknown";
  const checking = st === "unknown" && !!(g.checking || (!g.state && e.cached));
  const machine = g.machine || e.machine;
  const problem = g.problem || (st === "unprotected" ? `Nothing on ${machineWord(machine)} stops new prompts in this copy.` : st === "unknown" ? `hopsesh hasn't read ${machineWord(machine)}'s hooks yet.` : "");
  return { state: st, mode: g.mode === "advise" ? "advise" : "block", checking, problem, action: g.action || "", machine, checkedAt: Date.parse(g.checkedAt) > 0 ? g.checkedAt : "" };
}

// driftOf is how the copies parted after the move: "diverged" (both changed), "used"
// (this old copy gained work the active copy lacks), or "".
export function driftOf(e) {
  const d = e.departure;
  if (!isOldCopy(e)) return "";
  if (d.status === "diverged" && (!e.cached || Date.parse(d.checkedAt) > 0)) return "diverged";
  if (d.sourceChanged) return d.status === "continued" || d.status === "diverged" ? "diverged" : "used";
  return "";
}

// protectionLine says how an old copy is protected, in one sentence for its tone.
export function protectionLine(e) {
  const p = protectionOf(e), m = machineWord(p.machine);
  switch (p.state) {
    case "protected": return { tone: "neutral", icon: ICONS.shieldCheck, text: p.mode === "advise" ? `Warns on ${m}: the agent warns before anyone continues this copy.` : `Blocked on ${m}: new prompts in this copy are refused.` };
    case "unprotected": return { tone: "warn", icon: ICONS.shieldAlert, text: `${p.mode === "advise" ? "No warning" : "Not blocked"}: ${p.problem}` };
    case "off": return { tone: "neutral", icon: ICONS.shieldOff, text: "Not protected: your settings leave old copies alone (Settings › General)." };
    case "released": return { tone: "warn", icon: ICONS.shieldOff, text: "Unblocked: the block was removed, so this copy can be continued; anything done in it won't reach the active copy." };
  }
  if (p.checking) return { tone: "neutral", icon: ICONS.shieldUnknown, checking: true, text: `Checking current protection on ${m}…` };
  return { tone: "neutral", icon: ICONS.shieldUnknown, text: `Protection unknown: ${p.problem}`, checked: p.checkedAt };
}

// oldCopyMarks are an old copy's indicators in the list, the most severe first: each an
// icon and words, never colour alone.
export function oldCopyMarks(e) {
  const p = protectionOf(e), drift = driftOf(e), out = [];
  if (drift === "diverged") out.push({ tone: "st-error", icon: ICONS.diverged, words: "Diverged", title: "Both copies changed after the move" });
  else if (drift === "used") out.push({ tone: "st-error", icon: ICONS.usedAfter, words: "Used after the move", title: "This copy has work the active copy doesn't" });
  if (p.state === "unprotected") out.push({ tone: "st-warn", icon: ICONS.shieldAlert, words: p.mode === "advise" ? "No warning" : "Not blocked", title: p.problem });
  if (liveNow(e)) out.push({ tone: "st-warn", icon: ICONS.open, words: "Still open", title: `Still open on ${machineWord(e.machine)}` });
  if (p.state === "released") out.push({ tone: "st-warn", icon: ICONS.shieldOff, words: "Unblocked", title: "The block was removed from this copy" });
  if (p.state === "unknown") out.push({ tone: "st-ended", icon: ICONS.shieldUnknown, words: "Protection unknown", title: p.problem, spin: p.checking });
  if (p.state === "protected") out.push({ tone: "st-ended", icon: ICONS.shieldCheck, words: p.mode === "advise" ? "Warns" : "Blocked", title: protectionLine(e).text });
  return out;
}

// markChip draws one indicator.
export function markChip(m, extra = "") {
  return h("span", { class: `chip mark ${m.tone}${extra ? " " + extra : ""}`, title: m.title || m.words },
    m.spin ? h("span", { class: "mini-spin", "aria-hidden": "true" }) : icon(m.icon, 11), h("span", { class: "chip-t" }, m.words));
}

// oldCopiesOf are a session's old copies the scan carries (the active copy's view of them).
export function oldCopiesOf(e) {
  if (isOldCopy(e)) return [];
  return (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key))
    .map((c) => scanEntry(c.machine, c.key)).filter(isOldCopy);
}

// oldCopySummary is the active copy's one chip for its old copies: the most severe of
// drift, no protection, or protection not known; none when they are protected (or left
// alone by the settings).
export function oldCopySummary(e) {
  let best = null;
  const rank = { "st-error": 3, "st-warn": 2, "st-ended": 1 };
  for (const x of oldCopiesOf(e)) {
    const p = protectionOf(x), drift = driftOf(x);
    const m = drift === "diverged" ? { tone: "st-error", icon: ICONS.diverged, words: "Old copy diverged", title: `The old copy on ${machineWord(x.machine)} changed after the move too` }
      : drift === "used" ? { tone: "st-error", icon: ICONS.usedAfter, words: "Old copy used after the move", title: `The old copy on ${machineWord(x.machine)} has work this copy doesn't` }
      : p.state === "unprotected" ? { tone: "st-warn", icon: ICONS.shieldAlert, words: p.mode === "advise" ? "Old copy: no warning" : "Old copy not blocked", title: p.problem }
      : p.state === "unknown" ? { tone: "st-ended", icon: ICONS.shieldUnknown, words: "Old copy: protection unknown", title: p.problem }
      : null;
    if (m && (!best || rank[m.tone] > rank[best.tone])) best = m;
  }
  return best;
}

// askOldCopy asks before an old copy is continued: where the session went, what
// continuing here does to the two copies, and the way to the active copy instead. It
// answers "continue", "go" or null (Cancel, the default).
export function askOldCopy(e) {
  const d = e.departure, p = protectionOf(e), drift = driftOf(e);
  const on = day(d.at) ? ` on ${day(d.at)}` : "";
  const to = d.cloud || `${d.agentName || agentName(d.agent)} on ${machineWord(d.machine)}`;
  const guarded = p.state !== "off" && p.state !== "released";
  return new Promise((resolve) => {
    const dlg = dialog(
      h("h2", { style: "margin:0;font-size:16px" }, "Continue the old copy?"),
      h("div", { class: "oc-ask" },
        drift ? h("p", { class: "err" }, drift === "diverged" ? "Both copies already changed after the move." : "This copy was already used after the move.") : null,
        h("p", {}, `This conversation moved to ${to}${on}. Anything you do in this old copy won't reach that copy, and the two will diverge: moving back then needs a comparison, or keeping both as separate conversations.`),
        guarded ? h("p", { class: "muted" }, `hopsesh removes this copy's ${p.mode === "advise" ? "warning" : "block"} first, so its hook lets you continue.`) : null),
      h("div", { class: "dlg-foot" },
        h("button", { class: "btn", onclick: () => dlg.close("no") }, "Cancel"),
        h("button", { class: "btn", onclick: () => dlg.close("go") }, "Go to the active copy"),
        h("button", { class: "btn danger", onclick: () => dlg.close("continue") }, "Continue old copy")));
    dlg.addEventListener("close", () => resolve(dlg.returnValue === "continue" || dlg.returnValue === "go" ? dlg.returnValue : null), { once: true });
    dlg.returnValue = "";
    dlg.querySelector(".dlg-foot .btn")?.focus(); // Cancel: continuing is never the default
  });
}

// The backend refuses to continue, move or plan from an old copy ("moved-original: " and
// a sentence): the window asks the same question as Continue this old copy anyway…
const MOVED = "moved-original: ";
let continueOld = null;
export function onContinueOld(fn) { continueOld = fn; }
// heldBack: a plan, a hand-off or a folder change was asked of an old copy; the
// question goes instead (true), and nothing starts from it.
export function heldBack(e) {
  if (!isOldCopy(e) || !continueOld) return false;
  continueOld(e);
  return true;
}
export const isRefusal = (err) => errText(err).startsWith(MOVED);
export function refused(err, e = selected()) {
  const msg = errText(err);
  if (!msg.startsWith(MOVED)) return false;
  if (isOldCopy(e) && continueOld) continueOld(e);
  else toast(msg.slice(MOVED.length));
  return true;
}
onRefusal((err) => refused(err));

// ---- Removing a block ------------------------------------------------------------
export async function removeBlock(e, onDone) {
  const yes = await ask({ title: "Remove the block from this old copy?", ok: "Remove block", danger: true,
    body: `The active copy ${activeAt(e)} stays the one to continue. If you continue this old copy, the two copies diverge, and moving back will need a comparison, or keeping both as separate conversations, instead of a clean return. You can put the block back until you continue here.` });
  if (!yes) return;
  try { await api("ReleaseOriginal", e.machine, e.key); toast("Block removed from this old copy"); onDone?.(); } catch (err) { fail(err); }
}
export async function restoreBlock(e, onDone) {
  try { await api("RestoreBlock", e.machine, e.key); toast("Block restored"); onDone?.(); } catch (err) { fail(err); }
}
export const guardMode = () => state.info?.defaults?.original || "block";
