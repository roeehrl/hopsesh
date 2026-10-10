// Read-only causal evidence in the existing transfer review. Agent-specific
// parsing stays in modules; this view never infers changes from timestamps.
import { h, count, here, sys } from "./core.js";
import { markdown } from "./markdown.js";

const place = side => side.identity?.machine === here() ? `${side.identity.machine} (${sys.here})` : side.identity?.machine || "Machine not identified";
const key = side => { const k=side.identity?.key; return k ? `${k.agent}${k.profile?'@'+k.profile:''}/${k.session}` : "Session not identified"; };
const date = value => value && Number.isFinite(Date.parse(value)) ? new Date(value).toLocaleString() : "Time not recorded";

function excerpt(entry, agent) {
  if(entry.kind !== "message") return h("li", {class:"comparison-tool"}, h("b",{},"Tool activity"), " · ", entry.tool || "Tool name not available", h("span",{class:"muted"},date(entry.time)));
  return h("li", {class:`comparison-message ${entry.role==='user'?'user':'agent'}`},
    h("div",{class:"comparison-message-head"},h("b",{},entry.role==='user'?'You':agent),h("time",{datetime:entry.time || undefined,class:"muted"},date(entry.time))),
    markdown(entry.text || "Empty message"), entry.truncated?h("small",{class:"muted"},"Excerpt shortened"):null);
}

function sideCard(side, heading) {
  const identity=side.identity || {}, counts=side.counts || {}, preview=side.preview || [];
  const messages=preview.filter(x=>x.kind==='message'), recent=messages.slice(-3), tools=preview.filter(x=>x.kind!=='message');
  return h("section", {class:"comparison-side", "aria-label":heading},
    h("span",{class:"sec-h"},heading), h("h4",{},identity.title || "Untitled conversation"),
    h("p",{class:"muted comparison-identity"},`${identity.agentName || identity.agent || 'Agent not identified'} · ${identity.profileName || 'Profile not identified'} · ${place(side)}`),
    side.exclusiveKnown ? h("div",{class:"comparison-counts"},h("strong",{},`${count(counts.messages || 0,'message')} · ${count(counts.tools || 0,'tool call')}`),
      h("span",{class:"muted"},`${count(counts.userMessages || 0,'message')} from you · ${count(counts.assistantMessages || 0,'agent reply','agent replies')}${counts.other?` · ${counts.other} other records (contents hidden)`:''}`))
      : h("p",{class:"notice"},side.reason || "Changes could not be verified; counts are unavailable."),
    side.previewBasis === "saved-history" ? h("p",{class:"muted"},"Recent saved messages · relationship unverified. These excerpts are not proof of unique work."):null,
    recent.length?h("ol",{class:"comparison-excerpts"},recent.map(x=>excerpt(x,identity.agentName || 'Agent')))
      : side.exclusiveKnown?h("p",{class:"muted"},counts.messages ? "Message text is unavailable in the bounded preview." : "No ordinary messages unique to this conversation."):null,
    messages.length>3 || tools.length ? h("details",{class:"comparison-more"},h("summary",{},side.exclusiveKnown?"View more changed messages and tool activity":"View more saved messages and tool activity"),
      h("ol",{class:"comparison-excerpts"},preview.map(x=>excerpt(x,identity.agentName || 'Agent')))):null,
    side.truncated || side.previewOmitted ? h("p",{class:"muted comparison-limit"},`Bounded preview${side.previewOmitted?` · ${side.previewOmitted} ordinary message/tool excerpts omitted`:''}. ${side.exclusiveKnown?'Counts include all verified changes; long messages are shortened.':'History excerpts are shortened; exclusive counts are unavailable.'}`):null,
    h("details",{class:"comparison-receipt"},h("summary",{},"Session identity"),h("code",{},key(side))));
}

export function conflictReview(p, {separate}) {
  const comparison=p.continue?.comparison;
  const verified=!!comparison?.verified;
  return h("section",{class:"sec conflict-review", "aria-label":"Conversation differences"},
    h("h3",{tabindex:"-1"},separate ? "Review a separate conversation" : verified ? "The conversations have different work" : "The original conversation cannot be safely updated yet"),
    h("p",{},verified
      ? "The messages below are present in only one conversation. Hopsesh cannot append them into the original as one verified history. This is a conversation-history conflict; repository changes are reviewed separately below."
      : "Hopsesh could not verify how the original and incoming conversation histories relate. This does not prove that both gained new work. The original will remain unchanged."),
    comparison ? h("p",{class:"muted"},verified ? `${comparison.sharedRevisions} shared conversation steps verified. A difference does not prove when the work happened: an earlier move may have omitted already saved messages.` : comparison.reason) : h("p",{class:"notice"},"Comparison evidence is unavailable from this destination. Refresh after checking its connection or review a separate session without modifying the original."),
    comparison ? h("div",{class:"comparison-grid"},sideCard(comparison.source,"Incoming conversation"),sideCard(comparison.destination,"Existing destination conversation")):null,
    h("p",{class:"muted"},"Private reasoning and raw tool inputs/outputs are not displayed. Excerpts show conversation evidence, not a verified diff of code files."),
    h("div",{class:"conflict-outcome"},h("strong",{},`A separate ${p.agent} session`),
      h("p",{},`Uses the incoming ${p.fromAgent} conversation with the conversion shown below. It does not include work found only in the existing destination. Both existing sessions stay intact. A separate conversation branch is recorded; this does not itself create a Git branch.`),
      h("p",{role:separate?"status":undefined,class:separate?"notice":"muted"},separate ? "Separate-session plan selected. Review the conversion and repository details, then use Create below to confirm." : `Use “Review separate ${p.agent} session” below to see the plan. Reviewing changes nothing.`)),
    h("div",{class:"conflict-outcome"},h("strong",{},"Cancel this return"),h("p",{},"Cancel return leaves both existing conversations unchanged. No messages are added and no session is deleted.")));
}
