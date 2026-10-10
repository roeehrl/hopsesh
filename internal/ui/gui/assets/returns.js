// Candidates come only from the app's verified branch inventory. Never infer a
// return from copy timestamps or from a fork's parent journey.
import { api, h, dialog, here, sys, state, entries, when, agentBadge, fail } from "./core.js";
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
      let destination = returnDestination(r);
      if (r.status === "live" && !destination) {
        try { destination = await resolveDestination(r); } catch { /* instructions still available */ }
      }
      if (r.status === "live") {
        const name = r.agentName || r.agent;
        const d = dialog(h("div", {class:"return-guide"},
          h("span", {class:"chip st-warn"}, "Action needed"),
          h("h2", {}, `Move back to ${name}`),
          h("div", {class:"return-original"}, agentBadge(r.agent, name), h("strong", {}, destination?.title || r.title || "Original conversation"),
            h("span", {class:"muted"}, `${r.local ? sys.Here : r.machine} · ${r.profileLabel || "Default account"}`)),
          h("p", {}, "The original conversation is still open. Exit it first so its running agent cannot overwrite the new work Hopsesh adds."),
          h("ol", {class:"return-steps"},
            h("li", {}, h("strong", {}, "Show the original conversation"), h("button", {class:"btn small",onclick:()=>{d.close(); return showOriginal();}}, "Show original session")),
            h("li", {}, h("strong", {}, "Review the return"), h("p", {}, sessionExitHelp(!!destination?.app))),
            h("li", {}, h("strong", {}, "End the original from the review"), h("p", {}, "When supported by this agent and machine, choose “End original session and check again”. This interrupts any work in the original and refreshes the review. Adding the new work still requires your approval."))),
          h("p", {class:"return-preserved"}, "Saved history is preserved. If the original is open in several places, exit it in each place."),
          !canPlan ? h("p", {}, `To review the return, run Hopsesh on ${e.machine} or ${r.machine}.`) : null,
          h("div", {class:"dlg-foot"}, h("button", {class:"btn",onclick:()=>d.close()}, "Cancel"),
            canPlan ? h("button", {class:"btn primary",onclick:()=>{d.close(); return plan();}}, "Review return") : null)));
        return;
      }
      const explanation = r.status === "diverged" ? "Both copies changed. Review a plan that keeps both branches as separate sessions."
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
    return { id: `return:${r.replica || r.machine + ":" + r.key}`, label, sub: r.status === "live" ? `Original still open on ${r.local ? sys.here : r.machine}. Exit it first; click for instructions.` : `${r.status} · ${r.reason || r.key}`, candidate: r, showOriginal,
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
    h("div", {class:"return-card-actions"}, h("button", {class:"btn outline",onclick:a.run}, "How to move back…"),
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
  const n = e.movement;
  if (!n) return null;
  const explanation = {prepared:"Destination prepared; new work has not been observed.", continued:"New work was observed at the destination.", diverged:"Both copies changed; preserve both branches when reviewing a return.", forked:"A separate branch was prepared; it does not return into its parent."}[n.status] || n.status;
  return h("section", {class:"sec movement-notice", "aria-label":"Movement notice"},
    h("span", {class:"sec-h"}, `Movement · ${n.status}`), h("span", {}, (n.text || "").replace(/ Last checked \d{4}-\d{2}-\d{2}T\S+\.$/, "")), h("span", {class:"muted"}, explanation),
    h("span", {}, `${n.agentName || n.agent} · ${n.profileLabel || n.profile || "Default account"} on ${n.machine}`),
    h("span", {class:"mono"}, n.key),
    Date.parse(n.checkedAt) > 0 ? h("span", {class:"muted"}, `Checked ${when(n.checkedAt)}`) : null,
    n.delivery ? h("span", {class:"muted"}, n.delivery === "supplied-to-hook" ? "Supplied to the agent hook; model reading is not verified." : `Delivery: ${n.delivery}`) : null,
    h("button", {class:"btn small",onclick:()=>showMovementDestination(n,show)}, "Show destination"),
    h("button", {class:"btn small",onclick:viewJourney}, "View journey"),
    e.machine === here() ? h("button", {class:"btn small",onclick:()=>planFor(e,{target:e.agent,targetProfile:e.profile?.id || "",fork:true})}, "Continue separately here…") : null);
}
