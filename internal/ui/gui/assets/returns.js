// Candidates come only from the app's verified branch inventory. Never infer a
// return from copy timestamps or from a fork's parent journey.
import { refreshSelection } from "./sessions.js";
import { api, h, dialog, here, sys, state, entries, when, ago, agentBadge, fail, go, manyAccounts, icon, ICONS, toast } from "./core.js";
import { removeBlock, restoreBlock, withCLI, loadHookHealth, movedTo, activeAt, protectionOf, protectionLine, driftOf, stillOpen, oldCopiesOf, day } from "./guard.js";
import { planFor } from "./plan.js";
import { sessionExitHelp } from "./session-exit.js";

export const returnPlace = (r) => `${r.agentName || r.agent}${manyAccounts(r.agent) && r.profileLabel ? ` (${r.profileLabel})` : ""} on ${r.local ? sys.here : r.machine}`;
export const returnDestination = (r) => entries().find(e => e.machine === r.machine && e.key === r.key && (!r.profile || e.profile?.id === r.profile));

export async function resolveDestination(r) {
  const listed=returnDestination(r);
  if (listed) return listed;
  const e=await api("ResolveEntry",r.machine,r.key);
  const group=state.scan?.groups.find(g=>g.entries.some(x=>(x.copies || []).some(c=>c.machine===r.machine && c.key===r.key)));
  return Object.assign({group},e);
}

export function returnActions(e, open) {
  return (e.returns || []).map((r) => {
    const place = returnPlace(r);
    const inspect = ["same", "behind"].includes(r.status);
    const label = inspect ? `${r.local ? "Open existing session" : "Show destination"} in ${place}` : r.status === "available" ? `Move back to ${place}…`
      : r.status === "live" ? `Move back to ${r.agentName || r.agent}…` : r.status === "verify" ? `Verify move back to ${place}…` : `Review move back to ${place}…`;
    const plan = () => planFor(e, { target: r.agent, targetProfile: r.profile, targetSession: r.status === "missing" ? "" : r.key,
      sendTo: r.local ? "" : r.machine, returnCandidate: r });
    const canPlan = r.local || e.machine === here();
    const showOriginal = async () => {
      try { return open(await resolveDestination(r), {showOnly:true}); } catch (err) { fail(err); }
    };
    const review = async () => {
      if (["live", "diverged", "verify", "missing"].includes(r.status) && canPlan) return plan();
      const destination = returnDestination(r);
      const explanation = r.status === "diverged" ? "The conversations contain different work. Compare the histories before reviewing a separate session."
        : r.status === "live" ? `The original session is still running on ${r.machine}. ${sessionExitHelp(!!destination?.app)}`
        : r.status === "verify" ? "This destination has not been verified. Planning must reach it and check the exact session before any change."
        : r.status === "missing" ? "The original destination session is missing. You can review creating a new session there; this does not reuse or restore the missing original."
        : r.status === "same" ? "The destination already has this conversation."
        : r.status === "behind" ? "The destination has newer work. Open that copy to continue."
        : "Review this return destination.";
      const d = dialog(h("h2", {}, label.replace(/…$/, "")), h("p", {}, explanation),
        h("p", {class:"muted"}, r.reason || ""), h("p", {class:"mono"}, r.key),
        !canPlan ? h("p", {}, `To return to ${r.machine}, run Hopsesh on ${e.machine} or ${r.machine}.`) : null,
        h("div", {class:"dlg-foot"}, h("button", {class:"btn",onclick:()=>d.close()}, "Close"),
          destination ? h("button", {class:"btn",onclick:()=>{d.close();return open(destination)}}, "Show destination") : null,
          canPlan && ["available", "verify", "diverged", "missing"].includes(r.status) ? h("button", {class:"btn primary",onclick:()=>{d.close();return plan()}},
            r.status === "missing" ? "Review new session there" : r.status === "diverged" ? "Review plan keeping both branches" : "Verify and review plan") : null));
    };
    return { id: `return:${r.replica || r.machine + ":" + r.key}`, label, sub: r.status === "live" ? `Original still running on ${r.local ? sys.here : r.machine}. Review how to add your new work.` : `${r.status} · ${r.reason || r.key}`, candidate: r, showOriginal,
      run: () => {
        if (inspect) return resolveDestination(r).then(open).catch(() => review());
        if (r.status === "available" && canPlan) return plan();
        return review();
      } };
  });
}

export function returnCard(a) {
  const r=a.candidate;
  if (r.status !== "live") return h("div", {class:"return-choice"}, h("button", {class:"btn",onclick:a.run}, a.label),
    h("span", {class:"muted"}, a.sub), h("span", {class:"mono"}, r.key));
  const destination=returnDestination(r);
  return h("div", {class:"return-blocked"},
    h("div", {class:"return-blocked-head"}, agentBadge(r.agent,r.agentName), h("strong", {}, "Original conversation still open")),
    h("span", {class:"return-original-name"}, destination?.title || r.title || "Original conversation"),
    h("span", {class:"muted"}, `${r.local ? sys.Here : r.machine}${manyAccounts(r.agent) && r.profileLabel ? " · " + r.profileLabel : ""}`),
    h("p", {}, destination?.guard?.mode === "block" && destination.guard.effective
      ? "Hopsesh can close this protected conversation’s agent, add your new work, and reopen the same original. Nothing is archived."
      : "Review the move back to check whether this open original can receive your new work."),
    h("div", {class:"return-card-actions"}, h("button", {class:"btn outline",onclick:a.run}, "Review move back…"),
      h("button", {class:"btn",onclick:a.showOriginal}, "Show original session")));
}

export function returnChooser(actions) {
  const d = dialog(h("h2", {}, "Choose where to move back"),
    ...actions.map(a => returnCard({...a,run:()=>{d.close();return a.run()},showOriginal:()=>{d.close();return a.showOriginal()}})),
    h("div", {class:"dlg-foot"}, h("button", {class:"btn",onclick:()=>d.close()}, "Cancel")));
}

export async function showMovementDestination(n, show) {
  try { return show(await resolveDestination(n)); } catch { /* not in raw inventory */ }
  const d=dialog(h("h2",{},"Destination not in this scan"),h("p",{},`Scan ${n.machine} to show ${n.key}.`),
    h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Close")));
}

export function movementNotice(e, show, viewJourney) {
  const n = e.departure || e.movement;
  if (!n) return null;
  const g = e.guard, local = e.machine === here();
  const to = n.cloud || `${n.agentName || n.agent} on ${n.machine}`;
  const checked = Date.parse(n.checkedAt) > 0;
  const summary = {prepared:`The ${n.agentName || n.agent} session is ready; no new work there yet.`, continued:`Work continued in ${n.agentName || n.agent}.`, diverged:"Both copies have new work; compare them before moving back.", forked:"A separate fork; it does not return into this original."}[n.status] || n.status;
  const explanation = n.status !== "forked" && !checked ? `Checking for work in ${n.agentName || n.agent}; the saved movement receipt does not verify its current state.` : (e.cached ? "Last known: " : "") + summary;
  const protection = !g ? null
    : g.checking || e.cached ? ["muted", "Checking current protection for this original…"]
    : g.mode === "block" ? (g.effective ? ["ok", "Blocked until you move the session back. New prompts here are refused."] : ["warn", "Not blocked: " + (g.problem || "the agent's hook is not ready.")])
    : g.mode === "advise" ? (g.effective ? ["ok", "Advised: the agent warns before you continue here."] : ["warn", "Not advised: " + (g.problem || "the agent's hook is not ready.")])
    : g.mode === "released" ? ["warn", "Block removed. Continuing here makes the copies diverge; moving back then needs a comparison."]
    : ["muted", "Not protected (Settings › General)."];
  return h("section", {class:"sec movement-notice", "aria-label":"Where it went"},
    h("span", {class:"sec-h"}, n.status === "forked" ? "Separate copy" : `Moved to ${n.cloud || n.agentName || n.agent}`),
    h("span", {}, `${n.status === "forked" ? "Copied to" : "Moved to"} ${to}${manyAccounts(n.agent) && n.profileLabel ? " · " + n.profileLabel : ""}`),
    h("span", {class:"muted"}, explanation),
    protection ? h("span", {class: protection[0]}, protection[1]) : null,
    g && !g.effective && g.fix ? h("span", {class:"muted", style:"font-size:12px"}, g.fix) : null,
    checked ? h("span", {class:"muted"}, `${e.cached ? "Destination last checked" : "Destination checked"} ${when(n.checkedAt)}`) : null,
    h("div", {style:"display:flex;gap:6px;flex-wrap:wrap"},
      h("button", {class:"btn small primary",onclick:()=>showMovementDestination(n,show)}, `Open in ${n.cloud || n.agentName || n.agent}`),
      local && g?.mode === "block" ? h("button", {class:"btn small danger",onclick:()=>removeBlock(e,()=>refreshSelection())}, "Remove block…") : null,
      local && g?.mode === "released" ? h("button", {class:"btn small",onclick:()=>restoreBlock(e,()=>refreshSelection())}, "Block again") : null,
      h("button", {class:"btn small",onclick:viewJourney}, "View journey"),
      null));
}

// ---- Old copies ----

const machineWord = (m) => (m === here() ? sys.here : m);

// recheck reads a machine's hooks again (this machine's, or every machine's status), then
// the selected session and the list.
async function recheck(machine) {
  if (machine === here()) await loadHookHealth(true);
  else await api("MachineStatuses", true).catch(() => null);
  return refreshSelection();
}

// fixFor is the one fix a protection problem offers: set the hooks up on that machine now,
// or the place that can (Machines, Settings).
function fixFor(p) {
  const m = machineWord(p.machine);
  switch (p.action) {
    case "setup": return h("button", { class: "btn small primary", onclick: async () => {
      try { await withCLI(() => api("SetUpProtection", p.machine === here() ? "" : p.machine, false)); toast(`Protection set up on ${m}`); } catch (err) { fail(err); return; }
      return recheck(p.machine);
    } }, `Set up protection on ${m}`);
    case "update": return h("button", { class: "btn small", onclick: () => go("machines") }, `Update hopsesh on ${m}…`);
    case "approve": return h("button", { class: "btn small", onclick: () => go("machines") }, `Approve the hook on ${m}…`);
    case "machines": return h("button", { class: "btn small", onclick: () => go("machines") }, "Open Machines");
    case "settings": return h("button", { class: "btn small", onclick: () => go("settings", "general") }, "Open Settings");
  }
  return null;
}

const ocLine = (tone, d, text, ...extra) => h("div", { class: "oc-line " + tone }, icon(d, 14), h("span", { class: "oc-t" }, text), extra.length ? h("span", { class: "oc-acts" }, extra) : null);

// protectionRow is an old copy's protection: its words, and the one thing to do about it.
function protectionRow(e) {
  const p = protectionOf(e), l = protectionLine(e), local = e.machine === here();
  const words = [l.text, l.checked ? ` · checked ${ago(l.checked)}` : ""].join("");
  const acts = p.state === "unprotected" ? [fixFor(p)]
    : p.state === "unknown" && !p.checking ? [h("button", { class: "btn small", onclick: () => recheck(p.machine) }, "Check again")]
    : p.state === "released" && local ? [h("button", { class: "btn small", onclick: () => restoreBlock(e, () => refreshSelection()) }, "Block again")]
    : [];
  const row = ocLine(l.tone, l.icon, words, ...acts.filter(Boolean));
  if (l.checking) row.firstChild.replaceWith(h("span", { class: "mini-spin", "aria-hidden": "true" }));
  return row;
}

// driftRow and openRow say the copies drifted apart, and that the old copy is still open
// (from the active copy: fromActive).
function driftRow(e, fromActive = false) {
  const drift = driftOf(e), last = when(e.departure.sourceChangedAt || e.lastActive);
  if (drift === "diverged") return ocLine("err", ICONS.diverged, "Both copies changed after the move: they have diverged. Moving back will need a comparison, or keep them as separate conversations.");
  if (drift === "used") return ocLine("err", ICONS.usedAfter, fromActive ? `The old copy was used after the move (last on ${last}): that work isn't in this copy.` : `This copy was used after the move (last on ${last}): that work isn't in the active copy.`);
  return null;
}
const openRow = (e) => stillOpen(e) ? ocLine("warn", ICONS.open, `Still open on ${machineWord(e.machine)}. Close it there: a session that was already open may not pick up protection set up later, and anything typed there won't reach the active copy.`) : null;
const toneOf = (e) => driftOf(e) ? "err" : protectionLine(e).tone === "warn" || stillOpen(e) ? "warn" : "neutral";

// oldCopyBanner stands under an old copy's title, in place of its journey line, and can't
// be dismissed: where the session went and where to continue, whether the copies drifted
// apart, whether this one is still open, and how it is protected. Its tone is its most
// severe line.
export function oldCopyBanner(e, viewJourney) {
  const n = e.departure, tone = toneOf(e);
  const checked = Date.parse(n.checkedAt) > 0;
  const work = !checked ? `Checking for work in the active copy; the saved movement receipt doesn't show its current state.`
    : driftOf(e) === "diverged" ? "" : (e.cached ? "Last known: " : "") + ({ prepared: "the active copy has no new work yet.", continued: "work continued in the active copy." }[n.status] || "");
  return h("section", { class: "oldcopy " + tone, role: tone === "err" ? "alert" : "status", "aria-label": "Old copy" },
    h("b", { class: "oc-h" }, icon(ICONS.movedOut, 14), `Old copy · moved to ${movedTo(e)}${day(n.at) ? " on " + day(n.at) : ""}`),
    h("span", {}, `The active copy is ${activeAt(e)}. Continue there so the work stays in one conversation.`),
    driftRow(e), openRow(e), protectionRow(e),
    work || checked ? h("span", { class: "oc-note" }, work ? work[0].toUpperCase() + work.slice(1) : "", checked ? `${work ? " · " : ""}Active copy ${e.cached ? "last checked" : "checked"} ${when(n.checkedAt)}` : "") : null,
    h("span", { class: "oc-foot" }, h("button", { class: "link", onclick: viewJourney }, "View journey")));
}

// oldCopiesSection is the active copy's view of the copies its moves left behind: where
// each is and when it was last used, whether it drifted, is still open or is protected,
// and the way to it (another place in the sidebar when this one doesn't show it).
export function oldCopiesSection(e, show) {
  const xs = oldCopiesOf(e);
  if (!xs.length) return null;
  const title = xs.length === 1 ? "Old copy" : "Old copies";
  return h("section", { class: "sec oldcopies", "aria-label": title }, h("span", { class: "sec-h" }, title),
    xs.map((x) => h("div", { class: "oc-item " + toneOf(x) },
      h("span", { class: "oc-where" }, agentBadge(x.agent, x.agentName), `On ${machineWord(x.machine)}${x.agent !== e.agent ? " in " + x.agentName : ""} · last used ${ago(x.lastActive)}`),
      driftRow(x, true), openRow(x), protectionRow(x),
      h("span", { class: "oc-foot" }, h("button", { class: "btn small", onclick: () => show(x) }, "Show old copy")))));
}
