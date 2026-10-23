// Movement protection: how the copy left behind by a move is blocked or advised until
// the session moves back, whether the agents' hooks can actually do it, and the moved /
// original / returned indicators every view shares. Titles are never changed: status
// lives in hopsesh's own views.
import { api, h, dialog, ask, toast, fail, errText, state } from "./core.js";

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
const day = t => { const d = new Date(t); return isNaN(d) ? "" : d.toLocaleDateString(undefined, { month: "short", day: "numeric" }); };

// role is what a copy is in its session's journey, for chips, glyphs and words.
export function role(e) {
  const dep = e.departure, ar = e.arrival, g = e.guard;
  // What a chip names: the agent, and the machine when it is not this copy's own.
  const short = (name, agent, machine, cloud) => cloud || ((name || agent || "?") + (machine && machine !== e.machine ? ` on ${machine}` : ""));
  if (dep && dep.status !== "forked") {
    const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine), toShort = short(dep.agentName, dep.agent, dep.machine, dep.cloud);
    if (dep.status === "diverged" && (!e.cached || Date.parse(dep.checkedAt) > 0))
      return { kind: "diverged", glyph: "!", chip: "Both changed", tone: "st-warn", short: `Moved to ${toShort}`, line: (e.cached ? "Last known: " : "") + "Both copies changed; moving back needs a comparison" };
    if (g?.mode === "released")
      return { kind: "unblocked", glyph: "!", chip: "Unblocked", tone: "st-warn", short: `Moved to ${toShort}`, line: `Moved to ${to}; block removed, continuing here makes the copies diverge` };
    const blocked = g?.mode === "block";
    return { kind: blocked ? "blocked" : "moved-out", glyph: blocked && g.effective ? "◆" : "◇", chip: `Moved to ${toShort}`, tone: blocked && g.effective ? "st-ended" : "st-warn", icon: blocked ? "lock" : "warn",
      short: `Moved to ${toShort}`,
      line: e.cached || g?.checking ? "Checking current movement and protection status" : g?.noHopsesh ? `Not protected: ${e.machine} has no hopsesh` : blocked ? (g.effective ? "Blocked until you move it back" : "Not blocked yet: protection isn't set up") : g?.mode === "advise" ? (g.effective ? "Warns before you continue here" : "No warning yet: protection isn't set up") : "Left as it is" };
  }
  if (dep && dep.status === "forked") { const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine); return { kind: "forked-out", glyph: "⑂", chip: "Separate copy", tone: "st-idle", short: `Copied to ${short(dep.agentName, dep.agent, dep.machine, dep.cloud)}`, line: `A separate conversation continues in ${to}; this one is not blocked` }; }
  if (ar) {
    const from = where(ar.agentName || ar.agent, ar.machine), fromShort = short(ar.agentName, ar.agent, ar.machine, "");
    if (ar.kind === "returned") return { kind: "returned", glyph: "↩", chip: `Moved back from ${fromShort}`, tone: "st-idle", line: `Moved back from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · block cleared` };
    if (ar.kind === "continuation") return { kind: "continuation", glyph: "↳", chip: "Continuation", tone: "st-idle", line: `Bounded continuation of ${from} · the original is kept` };
    if (ar.fork) return { kind: "fork", glyph: "⑂", chip: "Separate copy", tone: "st-idle", line: `Separate conversation from ${from}${day(ar.at) ? " · " + day(ar.at) : ""}` };
    return { kind: "moved", glyph: "●", chip: `Moved from ${fromShort}`, tone: "st-working", line: `Moved from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · continue here` };
  }
  return null;
}

// chip is the small status lozenge (word + glyph, never color alone).
export function chip(e) {
  const r = role(e);
  if (!r) return null;
  return h("span", { class: "chip role-chip " + r.tone, title: r.line, "aria-label": `${r.chip}: ${r.line}` }, h("span", { "aria-hidden": "true" }, r.glyph + " "), r.chip);
}

// ---- Removing a block ------------------------------------------------------------
export async function removeBlock(e, onDone) {
  const dep = e.departure;
  const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine);
  const yes = await ask({ title: "Remove the block from this original?", ok: "Remove block", danger: true,
    body: `The moved copy in ${to} stays the active session. If you continue here, the two copies diverge, and moving back will need a comparison, or keeping both as separate conversations, instead of a clean return. You can put the block back until you continue here.` });
  if (!yes) return;
  try { await api("ReleaseOriginal", e.machine, e.key); toast("Block removed from this original"); onDone?.(); } catch (err) { fail(err); }
}
export async function restoreBlock(e, onDone) {
  try { await api("RestoreBlock", e.machine, e.key); toast("Block restored"); onDone?.(); } catch (err) { fail(err); }
}
export const guardMode = () => state.info?.defaults?.original || "block";
