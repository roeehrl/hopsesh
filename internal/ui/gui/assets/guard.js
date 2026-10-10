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
// checklist (the command, then each agent) and one action that sets up everything hopsesh
// can, plus the one step only the agent can do (approving its hooks in Codex).
let dismissed = false;
export function healthNotice(onChange) {
  const d = health;
  if (!d || dismissed || d.mode === "off") return null;
  const hooks = (d.hooks || []).filter((x) => x.supported && x.enabled);
  const several = (id) => hooks.filter((x) => x.agent === id).length > 1;
  const rows = [];
  rows.push({ ok: d.cliReady, name: "hopsesh command", note: d.cliReady ? "" : d.cli?.state === "dangling" ? "points to a moved or deleted app" : "not installed" });
  for (const x of hooks) {
    const t = x.trust?.state;
    const name = agentName(x.agent) + (several(x.agent) ? ` (${x.profileLabel || x.profile || "default account"})` : "");
    rows.push(!x.installed ? { ok: false, name, note: "not set up" }
      : t === "needs-review" ? { ok: false, wait: true, name, note: "waiting for your approval in " + agentName(x.agent) }
      : t === "disabled" ? { ok: false, wait: true, name, note: "turned off in " + agentName(x.agent) }
      : t === "missing" ? { ok: false, name, note: "not seen by " + agentName(x.agent) }
      : t === "unknown" ? { ok: true, name, note: "set up (approval not checked)" }
      : { ok: true, name, note: "" });
  }
  if (rows.every((r) => r.ok)) return null;
  const setup = rows.some((r) => !r.ok && !r.wait);
  const waiting = hooks.find((x) => x.installed && (x.trust?.state === "needs-review" || x.trust?.state === "disabled"));
  const blocking = d.mode === "block";
  const title = setup ? "Protect moved sessions" : `Approve hopsesh in ${agentName(waiting.agent)} to finish`;
  const why = blocking
    ? "When you move a session, hopsesh blocks the copy you left until you move it back, so the two never drift apart."
    : "When you move a session, hopsesh warns in the copy you left until you move it back.";
  const run = async () => {
    try {
      if (!d.cliReady) await api("InstallCLI", true);
      await api("InstallNoticeHooks", "", "");
      const after = await loadHookHealth(true);
      const left = (after?.hooks || []).some((x) => x.trust?.state === "needs-review");
      toast(left ? "Set up. One step left: approve the hooks in Codex." : "Protection is set up");
    } catch (e) { fail(e); }
    onChange();
  };
  return h("section", { class: "card notice warn-card protect-card", role: "status", "aria-label": "Movement protection" },
    h("div", { class: "protect-body" },
      h("b", {}, title),
      h("span", { class: "muted" }, setup ? why + " This needs:" : "hopsesh set up its hook; Codex runs a new hook only after you approve it."),
      h("ul", { class: "protect-list" }, rows.map((r) => h("li", { class: r.ok ? "ok" : r.wait ? "wait" : "todo" },
        h("span", { class: "mark", "aria-hidden": "true" }, r.ok ? "✓" : r.wait ? "!" : "○"),
        h("span", {}, r.name), r.note ? h("span", { class: "muted" }, " · " + r.note) : null)))),
    h("div", { class: "protect-actions" },
      setup ? h("button", { class: "btn primary", onclick: run }, "Set up protection") : null,
      waiting ? h("button", { class: setup ? "btn" : "btn primary", onclick: () => trustHelp(waiting, onChange) }, `Approve in ${agentName(waiting.agent)}…`) : null,
      h("button", { class: "btn", onclick: () => { dismissed = true; onChange(); } }, "Not now"),
      h("button", { class: "link", onclick: async () => { await loadHookHealth(true); onChange(); } }, "Check again")));
}

function trustHelp(p, onChange) {
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Approve the hopsesh hooks in ${agentName(p.agent)}`),
    h("p", { style: "margin:0;line-height:1.5" }, `${agentName(p.agent)} runs a new or changed hook only after you approve it, so hopsesh can't block or warn there yet.`),
    h("ol", { style: "margin:0;padding-left:18px;line-height:1.6" },
      h("li", {}, `Open ${agentName(p.agent)}. In the app, answer its Review hooks prompt; in the terminal, run `, h("code", {}, "/hooks"), "."),
      h("li", {}, "Check that each hopsesh hook runs ", h("code", {}, "hopsesh notice-hook"), ", then trust it (Allow all, or select both)."),
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
  if (dep && dep.status !== "forked") {
    const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine);
    if (dep.status === "diverged")
      return { kind: "diverged", glyph: "!", chip: "Diverged", tone: "st-warn", short: `Moved to ${to}`, line: "Both copies changed; moving back needs a comparison" };
    if (g?.mode === "released")
      return { kind: "unblocked", glyph: "!", chip: "Unblocked", tone: "st-warn", short: `Moved to ${to}`, line: "Block removed; continuing here makes the copies diverge" };
    const blocked = g?.mode === "block";
    return { kind: blocked ? "blocked" : "moved-out", glyph: blocked && g.effective ? "◆" : "◇", chip: "Moved out", tone: blocked && g.effective ? "st-ended" : "st-warn", icon: blocked ? "lock" : "warn",
      short: `Moved to ${to}`,
      line: blocked ? (g.effective ? "Blocked until you move it back" : "Not blocked yet: protection isn't set up") : g?.mode === "advise" ? (g.effective ? "Warns before you continue here" : "No warning yet: protection isn't set up") : "Left as it is" };
  }
  if (dep && dep.status === "forked") { const to = dep.cloud || where(dep.agentName || dep.agent, dep.machine); return { kind: "forked-out", glyph: "⑂", chip: "Fork made", tone: "st-idle", short: `Forked to ${to}`, line: `A separate fork continues in ${to}` }; }
  if (ar) {
    const from = where(ar.agentName || ar.agent, ar.machine);
    if (ar.kind === "returned") return { kind: "returned", glyph: "↩", chip: "Returned", tone: "st-idle", line: `Moved back from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · block cleared` };
    if (ar.kind === "continuation") return { kind: "continuation", glyph: "↳", chip: "Continuation", tone: "st-idle", line: `Bounded continuation of ${from} · the original is kept` };
    if (ar.fork) return { kind: "fork", glyph: "⑂", chip: "Fork", tone: "st-idle", line: `Forked from ${from}${day(ar.at) ? " · " + day(ar.at) : ""}` };
    return { kind: "moved", glyph: "●", chip: "Moved copy", tone: "st-working", line: `Moved from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · the active copy` };
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
    body: `The moved copy in ${to} stays the active session. If you continue here, the two copies diverge, and moving back will need a comparison or a separate fork instead of a clean return. You can put the block back until you continue here.` });
  if (!yes) return;
  try { await api("ReleaseOriginal", e.machine, e.key); toast("Block removed from this original"); onDone?.(); } catch (err) { fail(err); }
}
export async function restoreBlock(e, onDone) {
  try { await api("RestoreBlock", e.machine, e.key); toast("Block restored"); onDone?.(); } catch (err) { fail(err); }
}
export const guardMode = () => state.info?.defaults?.original || "block";
