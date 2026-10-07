import { api, h, dialog, fail, ask, go } from "./core.js";

const field = (label, control, help = "") => h("label", {class:"opt",style:"display:flex;flex-direction:column;align-items:stretch;gap:6px"}, h("b",{},label), control, help ? h("span",{class:"muted"},help) : null);

function pairing(kind, refresh, receivingEnabled, publicIdentity) {
 const cloud=kind==="cloud-session";
 const identity=h("textarea",{"aria-label":"Public pairing identity",rows:4,maxlength:4096,style:"width:100%;box-sizing:border-box"});
 if(publicIdentity)identity.value=JSON.stringify(publicIdentity,null,2);
 const fingerprint=h("input",{"aria-label":"Verified fingerprint",maxlength:64,autocomplete:"off"});
 const name=h("input",{"aria-label":"Machine name",maxlength:100});
 const send=h("input",{type:"checkbox"}),bring=h("input",{type:"checkbox"}),receive=h("input",{type:"checkbox",disabled:!receivingEnabled}),exportData=h("input",{type:"checkbox"});
 const rootNames=h("span",{class:"mono muted"},"No repositories selected");const roots=[];
 const lease=h("select",{"aria-label":"Approval lifetime"},[900,3600,14400,86400].map(seconds=>h("option",{value:seconds,selected:seconds===3600},seconds===900 ? "15 minutes" : seconds===3600 ? "1 hour" : `${seconds/3600} hours`)));
 const error=h("p",{class:"err",role:"alert"});
 const form=h("fieldset",{style:"border:0;padding:0;margin:0"},
  field("Public identity from the other endpoint",identity,"Share only the public pairing identity. Private keys and account credentials stay on their endpoint."),
  field("Fingerprint checked independently",fingerprint,"Compare it on the other endpoint or through a trusted channel before approving."),
  cloud ? field("Approval lifetime",lease) : field("Machine name",name),
  cloud ? field("Allow conversation export",exportData,"The cloud connector must also have transcript export enabled. This grants no access to this computer.") : [
   field("Allow sending sessions to this machine",send,"The other machine must independently approve receiving and its repository roots."),
   field("Allow bringing conversations from this machine",bring,"The other machine must independently approve sharing from its repositories."),
   field("Allow this machine to receive into selected repositories",receive,receivingEnabled ? "Only the local repositories selected below are approved." : "Enable receiving on this computer in Machines first."),
   field("Allow this peer to bring conversations from selected repositories",exportData),
   h("div",{class:"set-row"},rootNames,h("button",{type:"button",class:"btn",onclick:async()=>{const root=await api("ChooseFolder","Approve a local repository for this peer");if(root && !roots.includes(root))roots.push(root);rootNames.textContent=roots.length ? roots.join("\n") : "No repositories selected";}},"Choose repository…"))]);
 const button=h("button",{class:"btn primary",onclick:async()=>{
  form.disabled=true;button.disabled=true;error.textContent="";
  try {await api("RelayPair",{identity:identity.value,fingerprint:fingerprint.value.trim(),kind,name:cloud ? "" : name.value.trim(),roots:cloud ? [] : roots,send:!cloud && send.checked,bring:!cloud && bring.checked,receive:!cloud && receive.checked,export:exportData.checked,expiresSeconds:cloud ? Number(lease.value) : 0});d.close();await refresh();}
  catch(e){error.textContent=String(e?.message || e);}
  finally {form.disabled=false;button.disabled=false;}
 }},"Approve endpoint");
 const d=dialog(h("h2",{},cloud ? "Approve one cloud session" : "Pair a machine"),h("p",{class:"muted"},cloud ? "Approval applies to this incarnation only. A rebuilt or resumed session needs fresh identity approval." : "Internet delivery, receiving and conversation sharing are separate permissions."),form,error,h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Cancel"),button));
}

function enrollment(refresh) {
 const origin=h("input",{"aria-label":"Relay HTTPS origin",type:"url",placeholder:"https://relay.hopsesh.codonic.dev"});
 const credential=h("input",{"aria-label":"Scoped enrollment credential JSON",type:"password",maxlength:8192,autocomplete:"off"});
 const error=h("p",{class:"err",role:"alert"});
 const button=h("button",{class:"btn primary",onclick:async()=>{
  button.disabled=true;error.textContent="";
  try {await api("RelayConnect",origin.value.trim(),credential.value);credential.value="";d.close();await refresh();}
  catch(e){error.textContent=String(e?.message || e);}
  finally{button.disabled=false;}
 }},"Enroll this endpoint");
 const d=dialog(h("h2",{},"Use a scoped relay credential"),h("p",{class:"muted"},"An operator-issued credential must be scoped to this public endpoint. Enrollment routes encrypted messages; peer permissions are approved separately."),field("Relay HTTPS origin",origin),field("Scoped credential JSON",credential,"Kept in private local state. It is excluded from settings exports and diagnostics."),error,h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>{credential.value="";d.close();}},"Cancel"),button));
}

function browserEnrollment(identity, refresh) {
 const origin=h("input",{"aria-label":"Browser login relay origin",type:"url",value:"https://relay.hopsesh.codonic.dev"});
 const status=h("p",{role:"status"}),error=h("p",{class:"err",role:"alert"});
 let running=false,canceled=false;
 const cancel=()=>d.close();
 const button=h("button",{class:"btn primary",onclick:async()=>{
  running=true;button.disabled=true;origin.disabled=true;error.textContent="";status.textContent="Waiting for approval in your browser…";
  try{await api("RelayLogin",origin.value.trim());running=false;if(!canceled){d.close();await refresh();}}
  catch(e){error.textContent=String(e?.message || e);status.textContent="";}
  finally{running=false;button.disabled=false;origin.disabled=false;}
 }},"Open browser to connect");
 const d=dialog(h("h2",{},"Connect internet delivery"),h("p",{class:"muted"},"Sign in and approve this machine in your browser. Compare this full fingerprint before allowing delivery:"),h("p",{class:"mono",style:"overflow-wrap:anywhere"},identity.id),field("Relay HTTPS origin",origin),status,error,h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:cancel},"Cancel"),button));
 d.addEventListener("close",()=>{canceled=true;if(running)void api("RelayCancelLogin");},{once:true});
}

function cloudInvitation(refresh) {
 const provider=h("select",{"aria-label":"Cloud invitation provider"},h("option",{value:"claude-hosted"},"Claude Code cloud"),h("option",{value:"codex-current"},"Codex cloud"));
 const session=h("input",{"aria-label":"Actual cloud session ID",maxlength:128,autocomplete:"off"});
 const lease=h("select",{"aria-label":"Cloud routing lifetime"},[900,3600,14400,86400].map(seconds=>h("option",{value:seconds,selected:seconds===3600},seconds===900 ? "15 minutes" : `${seconds/3600} hour${seconds===3600 ? "" : "s"}`)));
 const error=h("p",{class:"err",role:"alert"}),status=h("div",{role:"status"});
 const form=h("fieldset",{style:"border:0;padding:0;margin:0"},field("Provider",provider),field("Actual session ID",session,"Use the task's session ID, not its environment or repository name."),field("Routing lifetime",lease));
 const button=h("button",{class:"btn primary",onclick:async()=>{
  button.disabled=true;form.disabled=true;error.textContent="";
  try{
   const record=await api("RelayIssueCloudAdmission",provider.value,session.value.trim(),Number(lease.value));
   status.replaceChildren(h("p",{},"Invitation saved privately. It does not approve a cloud endpoint or enable sharing."),h("p",{class:"mono",style:"overflow-wrap:anywhere"},record.path),h("p",{},`Claim before ${new Date(record.expires*1000).toLocaleString()}. Compare owner fingerprint independently:`),h("p",{class:"mono",style:"overflow-wrap:anywhere"},record.ownerFingerprint),h("p",{class:"muted"},"Supply the file through stdin to cloud-integration claim in the actual session. Keep it out of setup scripts, environment variables and cached images. Then approve the fresh session's public identity here."),h("button",{class:"btn",onclick:()=>api("Reveal",record.path).catch(fail)},"Show private invitation file"));
   await refresh();
  }catch(e){error.textContent=String(e?.message || e);button.disabled=false;form.disabled=false;}
 }},"Create private invitation");
 const d=dialog(h("h2",{},"Invite one cloud session"),h("p",{class:"muted"},"The invitation can admit one fresh session identity for a bounded routing lease. A resume or rebuild needs a new invitation and approval."),form,error,status,h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Close"),button));
}

function invitationRow(ticket,r,expired,action,refresh) {
 const error=h("p",{class:"err",role:"alert"});
 const check=h("button",{class:"btn",disabled:!r.enrolled || expired,onclick:async()=>{
  check.disabled=true;error.textContent="";
  try{
   const state=await api("RelayCheckCloudAdmission",ticket.id);
   const label={pending:"Waiting for the actual cloud session to claim",claimed:"Claimed: delivery is provisional until fingerprint approval",revoked:"Delivery revoked",expired:"Delivery expired"}[state.status];
   const d=dialog(h("h2",{},"Cloud invitation status"),h("p",{role:"status"},label),h("p",{class:"muted"},`${ticket.provider} · ${ticket.session}`),state.leaseExpires ? h("p",{},`Claimed routing lease ends ${new Date(state.leaseExpires*1000).toLocaleString()}.`) : null,
    state.public ? h("p",{class:"mono",style:"overflow-wrap:anywhere"},state.public.id) : null,
    state.status==="claimed" ? h("p",{class:"muted"},"Compare this fingerprint with the actual cloud session's output independently. Server claim status does not grant permission to share data.") : null,
    h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Close"),state.status==="claimed" ? h("button",{class:"btn primary",onclick:()=>{d.close();pairing("cloud-session",refresh,false,state.public);}},"Review cloud approval…") : null));
  }catch(e){error.textContent=String(e?.message||e)}finally{check.disabled=false}
 }},"Check claim");
 return h("div",{class:"set-row"},h("div",{style:"min-width:0;overflow-wrap:anywhere"},h("b",{},`${ticket.provider === "claude-hosted" ? "Claude Code" : "Codex"} · ${ticket.session}`),h("p",{class:"muted"},ticket.revoked ? "Delivery revoked" : ticket.expires*1000>Date.now() ? "Claim window open; claim status unconfirmed" : "Claim window closed; a claimed delivery lease may still be active"),h("p",{class:"muted"},`Claim deadline ${new Date(ticket.expires*1000).toLocaleString()} · routing up to ${ticket.leaseSeconds/3600} hours`),error),check,h("button",{class:"btn",onclick:()=>api("Reveal",ticket.path).catch(fail)},"Show file"),h("button",{class:"btn danger",disabled:ticket.revoked || !r.enrolled || expired,onclick:async()=>{if(await ask({title:"Revoke cloud invitation?",body:"This also revokes its claimed session's routing lease. Independent forks, peer approvals and previously delivered conversations remain available.",ok:"Revoke",danger:true}))await action(()=>api("RelayRevokeCloudAdmission",ticket.id));}},"Revoke invitation"));
}

export function relaySettings(data, refresh) {
 const r=data || {peers:[]};
 const expired=r.enrolled && r.expires*1000<=Date.now();
 const status=r.health?.connected && r.enabled && !expired ? "Connected" : !r.initialized ? "Not initialized" : !r.enrolled ? "Not enrolled" : expired ? "Credential expired" : !r.enabled ? "Disabled" : "Disconnected";
 const action=async fn=>{try{await fn();await refresh();}catch(e){fail(e);}};
 return [h("section",{class:"card"},h("div",{class:"dlg-body"},
  h("h2",{},"Optional internet delivery"),h("p",{class:"muted"},"Pair approved endpoints to move supported sessions without a VPN. Sessions and conversations are encrypted between endpoints. Connect through your relay's browser approval, or use an operator-issued scoped credential."),
  h("p",{role:"status"},status,r.url ? ` · ${r.url}` : ""),r.error ? h("p",{class:"err",role:"alert"},r.error) : null,
  h("div",{class:"set-row"},r.initialized ? h("button",{class:"btn",onclick:()=>{const text=JSON.stringify(r.identity,null,2);const d=dialog(h("h2",{},"This endpoint's public identity"),h("pre",{class:"brief",style:"white-space:pre-wrap;overflow-wrap:anywhere;max-height:50vh"},text),h("p",{class:"muted"},`Fingerprint: ${r.identity.id}`),h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Close")));}},"Show public identity") : h("button",{class:"btn",onclick:()=>action(()=>api("RelayInitialize"))},"Create endpoint identity"),
   h("button",{class:"btn",disabled:!r.initialized,onclick:()=>browserEnrollment(r.identity,refresh)},r.enrolled ? "Renew in browser…" : "Connect in browser…"),
   h("button",{class:"btn",disabled:!r.initialized,onclick:()=>enrollment(refresh)},r.enrolled ? "Renew enrollment…" : "Enroll endpoint…"),
   h("button",{class:"btn",disabled:!r.enrolled,onclick:()=>action(()=>api("RelayEnable",!r.enabled))},r.enabled ? "Disable internet delivery" : "Enable internet delivery"),
   h("button",{class:"btn",onclick:refresh},"Refresh status")),
  r.enrolled ? h("p",{class:"muted"},`Routing credential expires ${new Date(r.expires*1000).toLocaleString()}. Receiving on this computer: ${r.receiveEnabled ? "enabled" : "disabled"}.`) : null,
  h("button",{class:"btn",onclick:()=>go("machines")},"Receiving and machines…"))),
  h("section",{class:"card"},h("div",{class:"dlg-body"},h("h2",{},"Cloud invitations"),h("p",{class:"muted"},"Invitations authorize provisional delivery. Approve the fresh cloud fingerprint separately before requesting its conversation."),h("button",{class:"btn",disabled:!r.enrolled || expired,onclick:()=>cloudInvitation(refresh)},"Invite cloud session…"),
   !(r.admissions || []).length ? h("p",{class:"muted"},"No cloud invitations saved.") : r.admissions.map(ticket=>invitationRow(ticket,r,expired,action,refresh)))),

  h("section",{class:"card"},h("div",{class:"dlg-body"},h("h2",{},"Approved endpoints"),
   h("div",{class:"set-row"},h("button",{class:"btn",disabled:!r.initialized,onclick:()=>pairing("device",refresh,r.receiveEnabled)},"Pair machine…"),h("button",{class:"btn",disabled:!r.initialized,onclick:()=>pairing("cloud-session",refresh,false)},"Approve cloud session…")),
   !(r.peers || []).length ? h("p",{class:"muted"},"No endpoints have been approved.") : r.peers.map(p=>h("div",{class:"set-row"},h("div",{style:"min-width:0;overflow-wrap:anywhere"},h("b",{},p.name),h("p",{class:"muted"},`${p.kind === "cloud-session" ? "Cloud session" : "Machine"} · ${p.revoked ? "Revoked" : p.expires && p.expires*1000<=Date.now() ? "Expired" : "Approved"}`),h("p",{class:"mono muted",style:"overflow-wrap:anywhere"},p.fingerprint),h("p",{class:"muted"},permissions(p)),p.roots?.length ? h("p",{class:"mono muted",style:"white-space:pre-wrap;overflow-wrap:anywhere"},p.roots.join("\n")) : null),h("button",{class:"btn danger",disabled:p.revoked,onclick:async()=>{if(await ask({title:`Revoke ${p.name}?`,body:"This endpoint will no longer have the approved local access. Previously delivered conversations stay on their endpoints.",ok:"Revoke",danger:true}))await action(()=>api("RelayRevoke",p.id));}},"Revoke")))) )];
}

function permissions(peer) {
 const incoming=new Set(peer.methods || []),outgoing=new Set(peer.sendMethods || []);
 return [outgoing.has("apply") ? "Send sessions to this machine" : null,outgoing.has("export") ? "Bring conversations from this endpoint" : null,incoming.has("apply") ? "Receive into approved repositories here" : null,incoming.has("export") ? "Share conversations from approved repositories here" : null].filter(Boolean).join(" · ") || "Session inventory only";
}
