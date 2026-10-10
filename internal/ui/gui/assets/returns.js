// Candidates come only from the app's verified branch inventory. Never infer a
// return from copy timestamps or from a fork's parent journey.
import { refreshSelection } from "./sessions.js";
import { api, h, dialog, here, sys, state, entries, when, agentBadge, fail, go } from "./core.js";
import { removeBlock, restoreBlock } from "./guard.js";
import { planFor } from "./plan.js";
import { sessionExitHelp } from "./session-exit.js";

export const returnPlace = (r) => `${r.agentName || r.agent} · ${r.profileLabel || r.profile || "Default account"} on ${r.local ? sys.here : r.machine}`;
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
    return { id: `return:${r.replica || r.machine + ":" + r.key}`, label, sub: r.status === "live" ? `Original still running on ${r.local ? sys.here : r.machine}. Review ending it before adding new work.` : `${r.status} · ${r.reason || r.key}`, candidate: r, showOriginal,
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
    h("span", {class:"muted"}, `${r.local ? sys.Here : r.machine} · ${r.profileLabel || "Default account"}`),
    h("p", {}, "Exit the original before adding your new work. Its saved history stays available."),
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
  const explanation = {prepared:"The moved copy is ready; no new work there yet.", continued:"Work continued in the moved copy.", diverged:"Both copies have new work; compare them before moving back.", forked:"A separate fork; it does not return into this original."}[n.status] || n.status;
  const protection = !g ? null
    : g.mode === "block" ? (g.effective ? ["ok", "Blocked until you move the session back. New prompts here are refused."] : ["warn", "Not blocked: " + (g.problem || "the agent's hook is not ready.")])
    : g.mode === "advise" ? (g.effective ? ["ok", "Advised: the agent warns before you continue here."] : ["warn", "Not advised: " + (g.problem || "the agent's hook is not ready.")])
    : g.mode === "released" ? ["warn", "Block removed. Continuing here makes the copies diverge; moving back then needs a comparison."]
    : ["muted", "Not protected (Settings › General)."];
  return h("section", {class:"sec movement-notice", "aria-label":"Moved out"},
    h("span", {class:"sec-h"}, n.status === "forked" ? "Fork made" : "Moved out"),
    h("span", {}, `${n.status === "forked" ? "Forked to" : "Moved to"} ${to}${n.profileLabel ? " · " + n.profileLabel : ""}`),
    h("span", {class:"muted"}, explanation),
    protection ? h("span", {class: protection[0]}, protection[1]) : null,
    g && !g.effective && g.fix ? h("span", {class:"muted", style:"font-size:12px"}, g.fix) : null,
    Date.parse(n.checkedAt) > 0 ? h("span", {class:"muted"}, `Checked ${when(n.checkedAt)}`) : null,
    h("div", {style:"display:flex;gap:6px;flex-wrap:wrap"},
      h("button", {class:"btn small primary",onclick:()=>showMovementDestination(n,show)}, "Open moved copy"),
      local && g?.mode === "block" ? h("button", {class:"btn small danger",onclick:()=>removeBlock(e,()=>refreshSelection())}, "Remove block…") : null,
      local && g?.mode === "released" ? h("button", {class:"btn small",onclick:()=>restoreBlock(e,()=>refreshSelection())}, "Block again") : null,
      h("button", {class:"btn small",onclick:viewJourney}, "View journey"),
      local && n.status !== "forked" ? h("button", {class:"btn small",onclick:()=>planFor(e,{target:e.agent,targetProfile:e.profile?.id || "",fork:true})}, "Fork here instead…") : null));
}
