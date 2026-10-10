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
      body: msg.slice("cli-required: ".length).replace(/ Install the command first.*$/, "") + ". It links the command into ~/.local/bin (no password)." });
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

// healthNotice is the Sessions view's warning when originals cannot be protected.
export function healthNotice(onChange) {
  const d = health;
  if (!d) return null;
  const items = [];
  if (!d.cliReady && d.mode !== "off") items.push({ title: d.cli?.state === "dangling" ? "The hopsesh command points to a missing app" : "The hopsesh command is not installed",
    body: "The protection hooks and the skill run it; without it, originals are not blocked or advised.",
    act: ["Install the command", async () => { try { await api("InstallCLI", true); toast("Installed the hopsesh command"); await loadHookHealth(true); onChange(); } catch (e) { fail(e); } }] });
  for (const p of d.problems || []) {
    if (p.state === "settings" || p.state === "unsupported" || !p.agent) continue; // shown in Settings
    items.push({ title: `${agentName(p.agent)}${p.label ? " · " + p.label : ""}: ${p.state === "needs-review" ? "approve the hopsesh hooks" : p.state === "not-installed" ? "protection hook not installed" : "hooks not working"}`,
      body: p.message, fix: p.fix, events: p.events,
      act: p.state === "not-installed" ? ["Install hook", async () => { try { await withCLI(() => api("InstallNoticeHooks", p.agent, p.profile)); toast("Installed the protection hook"); onChange(); } catch (e) { fail(e); } }]
        : ["How to approve", () => trustHelp(p, onChange)] });
  }
  if (!items.length) return null;
  return h("div", { class: "card notice warn-card", role: "status", "aria-label": "Movement protection" },
    h("div", { style: "flex:1 1 360px;display:flex;flex-direction:column;gap:4px" },
      h("b", {}, d.mode === "block" ? "Moved sessions' originals are not blocked" : "Moved sessions' originals are not advised"),
      items.map(it => h("div", { style: "font-size:12.5px" }, h("b", {}, it.title + ". "), it.body))),
    items.map(it => h("button", { class: "btn", onclick: it.act[1] }, it.act[0])),
    h("button", { class: "btn", onclick: async () => { await loadHookHealth(true); onChange(); } }, "Check again"));
}

function trustHelp(p, onChange) {
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Approve the hopsesh hooks in ${agentName(p.agent)}`),
    h("p", { style: "margin:0;line-height:1.5" }, p.message),
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
      return { kind: "diverged", glyph: "!", chip: "Diverged", tone: "st-warn", line: `Original · continued after moving to ${to} · moving back needs a comparison` };
    if (g?.mode === "released")
      return { kind: "unblocked", glyph: "!", chip: "Unblocked", tone: "st-warn", line: `Original · moved to ${to} · block removed; continuing here diverges` };
    const blocked = g?.mode === "block";
    return { kind: blocked ? "blocked" : "moved-out", glyph: blocked && g.effective ? "◆" : "◇", chip: "Moved out", tone: blocked && g.effective ? "st-ended" : "st-warn", icon: blocked ? "lock" : "warn",
      line: `Original · moved to ${to}${blocked ? (g.effective ? " · blocked until you move back" : " · not blocked: " + (g.problem || "hooks not ready")) : g?.mode === "advise" ? (g.effective ? " · advised" : " · not advised: " + (g.problem || "hooks not ready")) : ""}` };
  }
  if (dep && dep.status === "forked") return { kind: "forked-out", glyph: "⑂", chip: "Fork made", tone: "st-idle", line: `Original · a separate fork continues in ${dep.cloud || where(dep.agentName || dep.agent, dep.machine)}` };
  if (ar) {
    const from = where(ar.agentName || ar.agent, ar.machine);
    if (ar.kind === "returned") return { kind: "returned", glyph: "↩", chip: "Returned", tone: "st-idle", line: `Moved back from ${from}${day(ar.at) ? " · " + day(ar.at) : ""} · block cleared` };
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
