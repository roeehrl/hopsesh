// The plan sheet (what a hop, continuation or send will do, with its choices), its
// progress, and the Done screen.
import { api, on, h, fill, view, state, screen, go, current, toast, fail, errText, cap, agentChip, here, $, count, sys, keys, cloudChip, cloudOf } from "./core.js";
import { openMenu, closeAll } from "./menu.js";
import { undo } from "./activity.js";
import { openResult, where as opensIn } from "./term.js";

const sheet = $("#sheet");
let cur = null; // { e, target, sendTo, opts, plan, busy, applying }

function defaults() {
  const d = state.info.defaults;
  return { worktree: "auto", remoteControl: false, notify: d.movementNotices !== false, fork: false, redact: false, clone: false, targetDir: "", reposDir: "",
    mark: d.markMoved, syncCode: d.syncCode, push: d.pushSource, stopLocal: false, app: false, conflict: "",
    fidelity: "history", native: false, note: "", go: false, carryRules: false, ruleFiles: [], via: "", codeOnly: false, append: false };
}

// planFor opens the sheet for a session: target "" keeps its agent, sendTo pushes it;
// codeOnly brings a cloud session's branch alone.
export async function planFor(e, { target = "", sendTo = "", codeOnly = false, targetProfile = "", targetSession = "", bounded = false, fork = false, returnCandidate = null }) {
  cur = { e, target, sendTo, opts: Object.assign(defaults(), { operationId: crypto.randomUUID() }, { codeOnly, targetProfile, targetSession, bounded, fork, newReplica: fork || returnCandidate?.status === "missing", conflict: returnCandidate?.status === "diverged" ? "keep-both" : "" }), returnCandidate, plan: null, busy: false, applying: false };
  fill(sheet, h("div", { class: "sheet-in" }, h("div", { class: "loading", role: "status", style: "min-height:240px" },
    sendTo ? `Asking hopsesh on ${sendTo} to plan it…` : "Working out the plan…")));
  if (!sheet.open) sheet.showModal();
  const c=cur;
  c.launch = state.info.places?.[target || e.agent] || opensIn();
  try {c.profiles = e.cloud?[]:await api("AccountDestinations", sendTo, target || e.agent);} catch (err) { if (cur === c) problem(errText(err), { label: "Try again", run: () => planFor(e, { target, sendTo, codeOnly, targetProfile, targetSession, bounded, fork, returnCandidate }) }); return; }
  if(cur!==c)return;
  if(!c.returnCandidate && !c.opts.targetProfile && c.profiles.length && !c.profiles.some(p=>p.default)) {
    fill(sheet,h("div",{class:"sheet-in"},h("div",{class:"sheet-body"},h("h2",{},"Choose a destination account"),
      ...c.profiles.map(p=>h("button",{class:"btn",onclick:()=>{c.opts.targetProfile=p.id;return replan()}},`${p.name} · ${p.account?.email||p.agent}`))),
      h("div",{class:"sheet-foot"},h("button",{class:"btn",onclick:()=>sheet.close()},"Cancel"))));return;
  }
  await replan();
}

// planPicked opens the sheet for a cloud session that is not a row: id "" leaves the
// choice to the vendor's own picker, in a new worktree of the checkout chosen here.
export async function planPicked(cloud, id, checkout, target = "") {
  cur = { e: null, picked: { cloud, id, checkout }, target, sendTo: "", opts: Object.assign(defaults(), { operationId: crypto.randomUUID() }, { targetDir: checkout }), plan: null, busy: false, applying: false };
  fill(sheet, h("div", { class: "sheet-in" }, h("div", { class: "loading", role: "status", style: "min-height:240px" }, "Working out the plan…")));
  if (!sheet.open) sheet.showModal();
  await replan();
}

let planning = Promise.resolve();
async function replan() {
  const c = cur;
  if (!c) return;
  const revision = c.revision = (c.revision || 0) + 1;
  c.busy = true;
  const btn = sheet.querySelector("#go");
  if (btn) { btn.disabled = true; btn.firstChild.textContent = "Updating the plan…"; }
  try {
    const request = planning.then(() => {
      if (cur !== c || c.revision !== revision) return null;
      return c.picked ? api("PlanPicked", c.picked.cloud, c.picked.id, c.opts.targetDir || c.picked.checkout, c.target, c.opts)
      : c.sendTo ? api("PushPlan", c.e.key, c.sendTo, c.target, c.opts) : api("Plan", c.e.machine, c.e.key, c.target, c.opts);
    });
    planning = request.catch(() => {});
    const p = await request;
    if (cur !== c || c.revision !== revision || !p) return;
    c.plan = p;
    if(!c.sendTo && p.kind!=="fetch") {
      if(c.launch==="app"&&!p.can.app){c.launch=opensIn();c.launchNotice=p.can.appWhy||"Desktop opening is unavailable for this destination.";}
      const app=c.launch==="app";
      if(c.opts.app!==app){c.opts.app=app;return replan();}
    }
    c.busy = false;
    const scrollTop=sheet.querySelector('.sheet-body')?.scrollTop||0;
    render();
    const body=sheet.querySelector('.sheet-body');if(body)body.scrollTop=scrollTop;
  } catch (err) {
    if (cur !== c || c.revision !== revision) return;
    c.busy = false;
    if (cur === c) problem(errText(err), { label: "Try again", run: replan });
  }
}

const set = (k, v) => { cur.opts[k] = v; return replan(); };

function problem(msg, back) {
  fill(sheet, h("div", { class: "sheet-in" },
    h("div", { class: "sheet-body" }, h("div", { class: "item" }, h("span", { class: "badge err" }, "✕"), h("div", { class: "err", style: "line-height:1.5" }, msg))),
    h("div", { class: "sheet-foot" }, h("span", { class: "spacer" }),
      back ? h("button", { class: "btn", onclick: back.run }, back.label) : null,
      h("button", { class: "btn primary", onclick: () => sheet.close() }, "Close"))));
}

function item(kind, title, desc, ...extra) {
  return h("div", { class: "item" }, h("span", { class: "badge " + kind }, kind === "ok" ? "✓" : kind === "err" ? "✕" : "!"),
    h("div", { style: "display:flex;flex-direction:column;gap:6px;min-width:0" }, h("div", {}, title), desc ? h("div", { class: "muted", style: "font-size:12px" }, desc) : null, ...extra));
}

// plain drops the command-line advice from a plan's message; the sheet offers buttons.
const plain = (s) => cap(s.split(/;\s*/).filter((x) => !/--[a-z]/.test(x)).join("; ").replace(/\s*\([^)]*--[a-z][^)]*\)/g, ""));

function check(label, key, desc, value = cur.opts[key], to = (on) => on) {
  return h("label", { class: "opt" }, h("input", { type: "checkbox", checked: value, onchange: (ev) => set(key, to(ev.target.checked)) }),
    h("span", {}, h("b", {}, label), desc ? h("span", { class: "muted" }, desc) : null));
}

async function chooseFolder() {
  const d = await api("ChooseFolder", "Where should the session continue?").catch(fail);
  if (d) { cur.opts.targetDir = d; cur.opts.clone = false; return replan(); }
}

function summary(p) {
 const profiles=cur.profiles||[];
 const chosen=profiles.find(x=>x.id===cur.opts.targetProfile)||profiles.find(x=>x.default);
 const identity=x=>x?.account?.email||x?.account?.label||x?.name||"Account not identified";
 const source=cur.e?.profile;
 const accountChoice=profiles.length?h("div",{class:"transfer-accounts"},
  h("div",{},h("span",{class:"sec-h"},"Source account"),h("b",{},identity(source)),h("span",{class:"muted"},source?.account?.email||source?.account?.label ? source?.name||p.sourceProfile||p.fromAgent : "Email unavailable")),
  h("span",{class:"account-arrow","aria-hidden":"true"},"→"),
  h("div",{},h("span",{class:"sec-h"},"Destination account"),
   !cur.returnCandidate&&profiles.length>1?h("select",{"aria-label":"Destination account",onchange:ev=>{cur.opts.targetSession="";return set("targetProfile",ev.target.value)}},
    profiles.map(x=>h("option",{value:x.id,selected:chosen?.id===x.id},`${identity(x)}${identity(x)!==x.name?' · '+x.name:''}${x.default?' · Default profile':''}`)))
   :h("b",{},identity(chosen)),h("span",{class:"muted"},chosen?`${identity(chosen)!==chosen.name?chosen.name+' · ':''}${chosen.default?'Default profile':'Named profile'}${!chosen.account?.email&&!chosen.account?.label?' · Email unavailable':''}`:"Account not identified"))):null;
 const summaryBody=summaryContent(p);
 return h("div",{},cur.returnCandidate ? h("div",{class:"summary"}, `Move back destination: ${cur.returnCandidate.agentName} · ${cur.returnCandidate.profileLabel || cur.returnCandidate.profile || "Default account"} · ${cur.returnCandidate.machine}`, h("span",{class:"mono"},cur.opts.targetSession || (cur.opts.fork ? "New separate branch; the original session will be preserved" : "New session; the missing original will not be reused"))) : null,accountChoice,summaryBody);
}
function summaryContent(p) {
 if(p.noWork) return h("div",{class:"summary","aria-label":"What changes"},"Conversation already synchronized. Update lineage receipts; 0 new messages, 0 transfers.");
 if(p.destinations?.length) {return h("label",{class:"summary"},"Choose the destination session",h("select",{onchange:ev=>set("targetSession",ev.target.value)},h("option",{value:""},"Select a session…"),p.destinations.map(s=>h("option",{value:`${s.key.agent}${s.key.profile?"@"+s.key.profile:""}/${s.key.session}`},`${s.title || s.key.session} · ${s.key.session}`))))}
  const cont = p.continue, r = p.repo, there = p.machine ? `on ${p.machine}` : "here";
  const add = [], chg = [];
  if (cont) {
    if (cont.relation === "append") add.push(`new work added to “${cont.appendTo}”`);
    else if (cont.relation === "new") add.push(`1 new ${p.agent} session ${there}`);
    if (p.nativeCopy) add.push(`1 ${p.nativeCopy.agent} copy kept ${there}`);
  } else add.push(`1 ${p.agent} session ${there}`);
  if (r.action === "clone") add.push("1 clone");
  if (r.worktree) add.push("1 worktree");
  if (p.setAside) chg.push(`${count(p.setAside, "older copy", "older copies")} set aside`);
  if (p.stopHere) chg.push("1 quit here first");
  if (p.mark !== "off") chg.push(`1 marked on ${sourcePlace(p)}${p.mark === "when-stopped" ? " when it ends" : ""}`);
  return h("div", { class: "summary", "aria-label": "What changes" },
    add.map((x) => h("span", { class: "add" }, "+ " + x)), chg.map((x) => h("span", { class: "chg" }, "~ " + x)),
    h("span", { class: "none" }, "0 removed"), h("span", { class: "spacer" }), h("span", { class: "muted" }, "Undo any time from Activity"));
}

// boxes splits a conversion report into what is carried, changed and left out.
function boxes(p) {
  const c = p.continue, rep = c.report;
  const carried = rep.method === "vendor-import" ? ["History converted by the agent’s importer", "Hopsesh briefing added"] : rep.fidelity === "note" ? ["A briefing only"] : rep.stepsSummarised ? ["Bounded history", "Full portable text in the archive"] : [count(rep.messages, "message"),
    rep.nativeCalls ? `${count(rep.nativeCalls, "command")} as ${p.agent}'s own, ${count(rep.toolCalls - rep.nativeCalls, "tool call")} as text` : `${count(rep.toolCalls, "tool call")}, as text`];
  const changed = [rep.outputsShortened && `${count(rep.outputsShortened, "long output")} shortened`, rep.stepsSummarised && `${count(rep.stepsSummarised, "oldest step")} summarised to fit`,
    rep.briefShortened && "Briefing shortened to fit", rep.pathsMapped && `${count(rep.pathsMapped, "path")} mapped to ${p.machine || "this machine"}`, rep.redactions && `${count(rep.redactions, "likely secret")} redacted`,
    rep.attachmentsAsPlaceholders && `${count(rep.attachmentsAsPlaceholders, "attachment")} as placeholders`].filter(Boolean);
  const lost = rep.method === "vendor-import" ? ["Vendor import fidelity has not been verified"] : rep.reasoningDropped ? [count(rep.reasoningDropped, "reasoning block"), `private to ${c.from}`] : ["Nothing"];
  const box = (cls, title, lines) => h("div", { class: "box " + cls }, h("b", {}, title), lines.map((l, i) => h("span", { class: i ? "muted" : "" }, l)));
  return h("div", { class: "three-boxes" }, box("kept", "Carried over", carried), box("changed", "Changed", changed.length ? changed : ["Nothing"]), box("lost", "Left out", lost));
}

function conversation(p) {
  const c = p.continue, o = cur.opts;
  const relation = {
    new: null,
    append: `The ${p.agent} session “${c.appendTo}” here gets only the new work since it was left; its own part stays exactly as it was.`,
    same: "Nothing new on either side.", behind: "Only the copy here changed; it is already the newest.", diverged: "Both copies changed since they parted.",
  }[c.relation];
  const note = h("textarea", { class: "field", rows: 2, id: "note", placeholder: "What you were doing, what's next" });
  note.value = o.note;
  note.onchange = () => set("note", note.value);
  const imported = o.via === "import";
  return h("section", { class: "sec", style: "border:0;padding:0;gap:10px" },
    h("div", { style: "display:flex;align-items:center;gap:12px;flex-wrap:wrap" }, h("span", { class: "sec-h" }, "The conversation"), h("span", { class: "spacer" }),
      imported ? null : h("div", { class: "seg", role: "radiogroup", "aria-label": "What to carry" },
        [["history", "History + bounded context"], ["note", "Briefing only"]].map(([v, t]) => h("button", { role: "radio", "aria-checked": o.fidelity === v ? "true" : "false", onclick: () => set("fidelity", v) }, t)))),
    relation ? item("ok", relation, "") : null,
    item("ok", `${c.report.method === "vendor-import" ? "Import input upper estimate" : "Working context"}: ${c.report.usedTokens.toLocaleString()} / ${c.report.budgetTokens.toLocaleString()} upper estimate`, `${c.report.capacity?.source || "Conservative static check"}. A successful model continuation has not been observed.`),
    c.report.archive ? item("ok", "Portable history preserved separately", c.report.archive) : null,
    imported ? item("ok", `${p.agent}'s own importer converts the conversation`, "hopsesh adds its briefing at the end and keeps the rest of the plan.") : boxes(p),
    h("label", { for: "note", style: "font-size:12.5px" }, `A note for ${p.agent} (optional)`), note,
    h("details", {}, h("summary", { style: "cursor:pointer;font-size:12.5px" }, `What ${p.agent} is told`),
      h("div", { style: "display:flex;flex-direction:column;gap:6px;margin-top:8px" },
        h("button", { class: "btn small", style: "align-self:flex-end", onclick: async () => { await api("CopyText", c.briefing); toast("Copied"); } }, "Copy"),
        h("div", { class: "brief" }, c.briefing))),
    instructionPicker(p),
    h("div", { class: "opts-grid" },
      p.can.import && !o.bounded ? check(`Let ${p.agent}'s own importer convert it`, "via", "Instead of hopsesh's conversion; hopsesh still adds its briefing.", imported, (on) => (on ? "import" : "")) : null,
      p.can.native && !imported && !o.bounded ? check(`Replay shell commands as ${p.agent}'s own`, "native", "Experimental: exact commands and outputs instead of text.") : null));
}

function instructionPicker(p) {
 const files=p.continue.instructions||[], selected=files.filter(f=>f.selected), c=cur;
 const pick=(path,on)=>{const paths=new Set(files.filter(f=>f.selected).map(f=>f.path));if(on)paths.add(path);else paths.delete(path);c.opts.ruleFiles=[...paths];c.opts.carryRules=paths.size>0;return replan()};
 return h("details",{class:"instruction-picker",open:!!c.rulesExpanded,ontoggle:ev=>{if(ev.target.isConnected)c.rulesExpanded=ev.target.open}},
  h("summary",{},`Source instructions · ${selected.length} of ${files.length} files selected`),
  h("p",{class:"muted"},"Choose which source files to quote in the handoff briefing. Destination instruction files are never created or overwritten."),
  h("p",{class:"muted"},"On a return trip, earlier snapshots remain conversation history. Your original instruction files stay in place; edits are not synchronized back. Review the files again for each transfer."),
  files.length?h("label",{class:"opt"},h("input",{type:"checkbox",checked:files.some(f=>!f.error)&&files.filter(f=>!f.error).every(f=>f.selected),onchange:ev=>{c.opts.ruleFiles=ev.target.checked?files.filter(f=>!f.error).map(f=>f.path):[];return set("carryRules",ev.target.checked)}}),h("span",{},"Include all listed instruction files")):h("p",{},"No supported instruction files found."),
  files.map(f=>h("div",{class:"instruction-file"},h("label",{class:"opt"},h("input",{type:"checkbox",checked:f.selected,disabled:!!f.error,'aria-label':`Include ${f.path}`,onchange:ev=>pick(f.path,ev.target.checked)}),h("span",{},h("b",{class:"mono"},f.path),h("span",{class:"muted"},`${f.scope} · ${sourcePlace(p)} · ${f.bytes.toLocaleString()} bytes${f.shortened?' · Preview and carried text shortened':''}`))),
   f.error?h("p",{class:"err"},f.error):h("details",{},h("summary",{},"View instructions"),h("pre",{class:"instruction-text"},f.text)))),
  h("p",{class:"muted"},"Shows declared global files and instruction files in the session’s project directory. Imported files, parent-directory rules, skills, settings and automatic memory are not included. The briefing may shorten selected text to fit; review “What the agent is told” above."));
}
const sourcePlace=p=>p.sourceHost===here()?`${p.sourceHost} (${sys.here})`:p.sourceHost;
function launchControl(p,blocked) {
 const enabled=!p.machine&&!p.noWork&&p.kind!=="fetch", c=cur;
 const label=place=>place==='app'?`${p.agent} app`:place==='here'?'Hopsesh Terminal':sys.terminal;
 const choices=[{id:'app',label:`Open in ${p.agent} app`,disabled:!p.can.app,why:p.can.appWhy||(!p.can.app?'Desktop opening is unavailable':null)},{id:'here',label:'Open in Hopsesh Terminal'},{id:'terminal',label:`Open in ${sys.terminal}`}];
 return h("div",{class:"launch-choice"},c.launchNotice?h("span",{class:"muted launch-notice"},c.launchNotice):null,
  p.continue && c.launch === "app" ? h("span", {class:"muted launch-notice"}, `Opens the conversation only. Send your next message in ${p.agent} to start a turn.`) : null,
  h("div",{class:"split transfer-launch"},
  h("button",{class:"btn primary big",id:"go",disabled:blocked,onclick:apply},h("span",{},h("span",{},verb(p)),enabled?h("small",{},`Open in ${label(c.launch||opensIn())}`):null),h("span",{class:"kbd"},keys("mod+enter"))),
  enabled?h("button",{class:"btn primary big split-chevron",disabled:!!c.busy||!!c.applying,'aria-label':'Choose where to open the continued session','aria-haspopup':'menu','aria-expanded':'false',onclick:ev=>openMenu(ev.currentTarget,choices.map(choice=>({...choice,radio:(c.launch||opensIn())===choice.id,run:async()=>{await api('SetPlace',c.target||c.e.agent,choice.id);state.info.places||={};state.info.places[c.target||c.e.agent]=choice.id;c.launch=choice.id;c.launchNotice='';await replan();sheet.querySelector('#go')?.focus()}})),{label:'Open continued session in',align:'end',width:300})},'▾'):null));
}

// sameFolder: the session stays in its folder on this machine (another agent here).
const sameFolder = (p) => !p.machine && p.sourceCwd === p.targetCwd && p.sourceHost === here();

function repository(p) {
  const r = p.repo, o = cur.opts, there = p.machine ? `on ${p.machine}` : "here";
  if (sameFolder(p)) return h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Repository and code"),
    item("ok", "The same folder", p.targetCwd + (r.sourceBranch ? `, on ${r.sourceBranch}` : "")));
  const out = [];
  if (r.action === "use") out.push(item("ok", `Found ${there} at ${r.localPath}` + (r.localBranch ? ` (on ${r.localBranch})` : ""), r.identity ? `Matched by its remote, ${r.identity}.` : ""));
  if (r.action === "clone") out.push(item("ok", `Will clone into ${r.localPath}`, `From ${r.remote}.`));
  if (r.action === "dir") out.push(item("ok", `Continues in ${r.localPath}`, "The folder you chose.", p.machine ? null : h("button", { class: "btn small", style: "align-self:flex-start", onclick: chooseFolder }, "Choose another folder…")));
  if (r.action === "needs-clone") {
    const dest = h("input", { class: "field mono", style: "flex:1 1 260px", value: r.localPath, "aria-label": "Clone into" });
    out.push(item("warn", `Not found ${there}`, `Matched by its remote, ${r.identity}. hopsesh looked in your repos folder and the usual places.`,
      h("div", { style: "display:flex;gap:8px;align-items:center;flex-wrap:wrap" }, h("span", { class: "muted", style: "font-size:12px" }, "Clone into"), dest,
        h("button", { class: "btn primary small", onclick: () => { o.clone = true; o.reposDir = dest.value.replace(/[/\\][^/\\]+[/\\]?$/, ""); return replan(); } }, "Clone for me"),
        p.machine ? null : h("button", { class: "btn small", onclick: chooseFolder }, "I already have it…"))));
  }
  if (r.action === "none") out.push(p.sourceCwd === p.targetCwd ? item("ok", "The same folder", "The session stays where it was started.")
    : item("warn", "No repository to match", "The folder has no git remote, so hopsesh cannot find or clone it.", p.machine ? null : h("button", { class: "btn small", style: "align-self:flex-start", onclick: chooseFolder }, "Choose a folder…")));
  if (r.sourceBranch) {
    const where = r.sourceAgentWorktree ? "an agent worktree" : r.sourceInWorktree ? "a git worktree" : "the main folder";
    out.push(item("ok", `Branch ${r.sourceBranch}, in ${where} on ${p.sourceHost}`,
      r.worktree ? `A matching worktree is created ${there}: ${r.worktree}` : r.sourceInWorktree ? `It continues in the main checkout ${there}.` : "",
      h("label", { style: "display:flex;gap:8px;align-items:center;font-size:12px" }, h("span", { class: "muted" }, "Worktree"),
        h("select", { onchange: (ev) => set("worktree", ev.target.value) },
          [["auto", "Recreate one if the session used one"], ["create", "Always use a worktree on this branch"], ["main", "Use the main checkout"]].map(([v, t]) => h("option", { value: v, selected: o.worktree === v }, t))))));
  }
  if (r.unpushed) out.push(p.syncFromSource || p.push
    ? item("ok", `${count(r.unpushed, "unpushed commit")} ${r.unpushed === 1 ? "comes" : "come"} along`, p.push ? `Pushed on ${p.sourceHost} first.` : `Fetched straight from ${p.sourceHost}.`)
    : item("warn", `${count(r.unpushed, "unpushed commit")} ${r.unpushed === 1 ? "stays" : "stay"} on ${p.sourceHost}`, "Turn on “Bring the code” below to fetch them."));
  if (r.dirty) out.push(item("warn", `${count(r.dirty, "uncommitted file")} ${r.dirty === 1 ? "stays" : "stay"} on ${p.sourceHost}`, "Commit them there to bring them."));
  if (p.sync) out.push(item("ok", "Code: " + p.sync, ""));
  if (p.stopHere) out.push(item("warn", "The copy open here is quit first", "It gets the normal quit signal and saves its session before the newer copy replaces it."));
  return h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Repository and code"), out);
}

// blocker turns a reason the plan cannot go ahead into words and the buttons that fix it.
function blocker(p, b) {
  const o = cur.opts, cont = p.continue;
  if (cur.returnCandidate && /cannot append to a native replica under an unverified account binding/.test(b)) return item("err", plain(b),
    "The original cannot be safely updated under this account binding. Review creating a new session on a separate branch; both existing sessions will be preserved.",
    h("button", {class:"btn small",onclick:()=>{Object.assign(cur.opts,{targetSession:"",fork:true,newReplica:true,conflict:"keep-both"});return replan();}}, "Review keeping both as separate sessions"));
  if (/^--via import only/.test(b)) return item("err", `${p.agent}'s importer only starts a new session`, "", h("button", { class: "btn small", style: "align-self:flex-start", onclick: () => set("via", "") }, "Use hopsesh's conversion instead"));
  if (/^--via import reads/.test(b)) return item("err", `${p.agent}'s importer needs ${cont.from} installed here`, "", h("button", { class: "btn small", style: "align-self:flex-start", onclick: () => set("via", "") }, "Use hopsesh's conversion instead"));
  if (p.conflict && b.startsWith(p.conflict)) return item("err", "Both copies changed: " + p.conflict, "Nothing is merged. Pick what to keep.",
    h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" },
      h("button", { class: "btn small", onclick: () => set("conflict", "keep-both") }, "Keep both, as separate sessions"),
      cont || cur.returnCandidate ? null : h("button", { class: "btn small", onclick: () => set("conflict", "replace") }, "Replace the copy here"),
      h("button", { class: "btn small", onclick: () => sheet.close() }, "Keep only the copy here")));
  if (/open on this machine|running on this machine/.test(b)) return item("err", plain(b), "", h("button", { class: "btn primary small", style: "align-self:flex-start", onclick: () => set("stopLocal", true) }, "Quit it and continue"));
  if (/--to\b/.test(b) && !p.machine) return item("err", plain(b), "", h("button", { class: "btn small", style: "align-self:flex-start", onclick: chooseFolder }, "Choose a folder…"));
  return item("err", plain(b), "");
}

function checks(p) {
  const r = p.repo;
  const shown = /unpushed commit\(s\) and|ran in a worktree on branch|what does not carry over|do not carry over to|uncommitted file\(s\) stay behind/;
  const warnings = (p.warnings || []).filter((w) => !shown.test(w)).map((w) => item("warn", plain(w), ""));
  const blockers = (p.blockers || []).filter((b) => !(r.action === "needs-clone" && /is not cloned/.test(b))).map((b) => blocker(p, b));
  if (!warnings.length && !blockers.length) return null;
  return h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, blockers.length ? "Before it can go ahead" : "Worth knowing"), blockers, warnings);
}

function options(p) {
  const r = p.repo, o = cur.opts, cont = p.continue;
  const markDesc = p.mark === "when-stopped" ? "Adds a moved label after the source process stops. This option does not stop it or synchronize later messages."
    : "Its title says where the work went, so it isn't resumed by mistake.";
  const opts = [
    p.mark !== "off" || !o.mark ? check(`Mark the source on ${sourcePlace(p)}`, "mark", markDesc) : null,
    r.sourceHead && !sameFolder(p) ? check("Bring the code to the session's commit", "syncCode", "Fetches if needed; fast-forwards only a clean checkout on the same branch.") : null,
    r.unpushed && r.sourceUpstream && !sameFolder(p) ? check(`Push ${count(r.unpushed, "commit")} on ${p.sourceHost} first`, "push", "With that machine's own git credentials.") : null,
    cont && cur.launch !== "app" ? check("Send “Continue” when opening", "go", `The terminal launch sends the first message to ${p.agent}. Progress appears in the agent.`) : null,
    p.can.remoteControl ? check("Turn on Remote Control", "remoteControl", `Reach it from your phone or other machines, as ${p.newName}.`) : null,
    check("Record a movement notice", "notify", "Keep a durable Hopsesh notice on the source. Prepared means the destination was written; continued requires observed new work."),
    p.live && p.can.fork ? check("Keep the old session running too", "fork", "Both copies continue, instead of a hand-off.") : null,
    check("Redact likely secrets", "redact", "In this copy only."),
  ];
  return h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Options"), h("div", { class: "opts-grid" }, opts),
    null);
}

function paths(p) {
  if (!p.mappings.length) return null;
  return h("details", { class: "sec" }, h("summary", { style: "cursor:pointer;font-size:12.5px" }, `${count(p.mappings.length, "folder")} mapped to ${p.machine || "this machine"}`),
    h("div", { class: "maprow", style: "margin-top:8px" }, p.mappings.flatMap((m) => [h("span", {}, m.from), h("span", { style: "color:var(--accent)" }, "→"), h("span", {}, m.to)])),
    h("span", { class: "muted", style: "font-size:11.5px" }, p.continue ? "Paths in the conversation are mapped too." : "Signed and encrypted content, message ids and the session id are never changed."));
}

function verb(p) {
 if (p.noWork) return "Sync lineage receipts";
  if (p.machine) return `Send to ${p.machine}`;
  if (p.continue) return `Continue in ${p.agent}`;
  return p.repo.action === "clone" ? "Clone and hop here" : "Hop here";
}

function render() {
  const p = cur.plan;
  if (p.kind === "fetch") return renderFetch(p);
  const there = p.machine ? `on ${p.machine}` : `on ${sys.here}`;
  const title = p.machine ? `Send “${p.title}” to ${p.machine}` : p.continue ? `Continue “${p.title}” in ${p.agent}` : `Bring “${p.title}” here`;
  const blocked = (p.blockers || []).length > 0;
  fill(sheet, h("div", { class: "sheet-in" },
    h("header", { class: "sheet-head" },
      h("h2", { id: "sheet-title" }, title),
      h("div", { class: "fromto" },
        agentChip(p.sourceAgent, p.fromAgent), h("span", {}, `on ${sourcePlace(p)}`), h("span", { class: "mono muted", style: "font-size:11.5px" }, p.sourceCwd),
        h("span", { style: "color:var(--accent)", "aria-label": "to" }, "→"),
        agentChip(p.continue ? cur.target : p.sourceAgent, p.agent), h("span", {}, there), h("span", { class: "mono muted", style: "font-size:11.5px" }, p.targetCwd)),
      summary(p)),
    h("div", { class: "sheet-body" }, p.continue ? conversation(p) : null, repository(p), checks(p), options(p), paths(p)),
    h("footer", { class: "sheet-foot" },
      h("span", { class: "muted", style: "font-size:12px;flex:1 1 260px" }, `Nothing changes until you ${p.continue ? "continue" : p.machine ? "send it" : "hop"}. The original on ${p.machine ? sys.here : sourcePlace(p)} is never deleted.`),
      h("button", { class: "btn", onclick: () => sheet.close() }, "Cancel"),
      launchControl(p,blocked))));
}

async function apply() {
  closeAll(false);
  const c = cur;
  if (!c?.plan || c.busy || c.applying || (c.plan.blockers || []).length) return;
  if (c.plan.kind === "fetch") return applyFetch(c);
  c.applying = true;
  const steps = h("span", { class: "mono muted", style: "font-size:12px" }, "starting");
  sheet.querySelector(".sheet-body").inert = true;
  fill(sheet.querySelector(".sheet-foot"), h("div", { role: "status", style: "display:flex;flex-direction:column;gap:2px;flex:1" },
    h("b", {}, c.sendTo ? `Sending to ${c.sendTo}…` : c.plan.continue ? `Continuing in ${c.plan.agent}…` : "Copying, rewriting and checking…"), steps));
  const off = on("hopsesh:progress", (s) => { steps.textContent = String(s); });
  try {
    const d = await api(c.sendTo ? "PushApply" : "Apply");
    cur = null; // the plan stays for OpenResult
    state.stale = true;
    sheet.close();
    c.opts.launch = c.launch;
    go("done", d, c.plan, c.opts);
    if(!d.machine&&!d.noWork&&c.launch) await openResult(c.launch==='app'?'':c.launch);
  } catch (err) {
    c.applying = false;
    problem(errText(err), { label: "Back to the plan", run: () => replan() });
  } finally {
    off();
  }
}

// ---- Bringing a session from a cloud ----
const FID = { native: ["Full", "ok"], text: ["Lossy", "warn"], code: ["Code only", "warn"], brief: ["Lossy", "warn"] };

function renderFetch(p) {
  const outside = opensIn() === "terminal"; // the user chose their terminal app for steps
  const f = p.fetch, o = cur.opts;
  const blocked = (p.blockers || []).length > 0;
  // A cloud-only agent (Copilot, Amp) has no sessions here: its messages go into one of
  // the agents here, the one it came from by default (bringIn).
  const into = cur.e && cur.e.bringIn;
  const own = into || { id: p.sourceAgent, name: p.fromAgent };
  const targets = [own, ...((cur.e && cur.e.continueIn) || [])];
  const target = targets.find((t) => t.id === (cur.target || own.id)) || own;
  const [fidLabel, fidKind] = f.codeOnly ? ["Code only", "warn"] : f.continueIn && !into ? ["Lossy", "warn"] : FID[f.fidelity] || ["Lossy", "warn"];
  const add = [];
  const noun = f.noun || "session";
  if (!f.codeOnly) add.push(f.append ? `the cloud's work added to “${f.original.title}”` : `1 ${p.agent} session here`);
  add.push("1 worktree");
  if (f.codeOnly || f.diff) add.push("1 branch");
  const kv = (label, ...value) => [h("dt", {}, label), h("dd", {}, ...value)];
  const checks = (f.checks || []).map((c) => checkItem(p, c));
  fill(sheet, h("div", { class: "sheet-in" },
    h("header", { class: "sheet-head" },
      h("h2", { id: "sheet-title" }, f.codeOnly ? `Bring the code of “${p.title}” here from ${f.cloudTitle}` : `Bring “${p.title}” here from ${f.cloudTitle}`),
      h("div", { class: "fromto" },
        cloudChip(f.cloud), f.session ? h("span", { class: "mono", style: "font-size:12px" }, f.session) : h("span", { class: "muted" }, `chosen in ${p.fromAgent}'s own picker`),
        h("span", { style: "color:var(--accent)", "aria-label": "to" }, "→"),
        f.codeOnly ? null : agentChip(target.id, target.name), h("span", {}, `on ${here()} (${sys.here}), in a new worktree`)),
      h("div", { class: "summary", "aria-label": "What changes" },
        add.map((x) => h("span", { class: "add" }, "+ " + x)), h("span", { class: "none" }, `The cloud ${noun} is not changed`),
        h("span", { class: "spacer" }), h("span", { class: "muted" }, "Undo any time from Activity"))),
    h("div", { class: "sheet-body" },
      f.codeOnly ? null : h("section", { class: "sec", style: "border:0;padding:0;gap:12px" },
        h("div", { style: "display:flex;align-items:center;gap:10px;flex-wrap:wrap" }, h("span", { class: "sec-h" }, "The conversation"), h("span", { class: "spacer" }),
          targets.length > 1 ? [h("span", { class: "muted", style: "font-size:12px" }, into ? "Write it into" : "Continue in"),
            h("div", { class: "seg", role: "radiogroup", "aria-label": into ? "Write it into" : "Continue in" }, targets.map((t) => h("button", { role: "radio", "aria-checked": t.id === target.id ? "true" : "false",
              onclick: () => { cur.target = t.id === own.id && !into ? "" : t.id; return replan(); } }, t.name)))] : null),
        h("div", { class: "fid " + fidKind }, h("b", {}, fidLabel), h("span", {}, f.conversation)),
        !f.continueIn && targets.length > 1 && f.terminal ? h("span", { class: "muted", style: "font-size:12px" }, `In ${targets[1].name} instead: copied by ${p.fromAgent}, then converted (tool calls become text).`) : null,
        f.write ? h("span", { class: "muted", style: "font-size:12px" }, `hopsesh writes it as a new ${f.writer} session (${count(f.messages, "message")}), in the worktree with the ${noun}'s code.`) : null),
      h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Repository and code"),
        h("dl", { class: "kv wide" },
          kv("Repository", f.repo ? h("span", { class: "mono", style: "font-size:12px" }, f.repo) : h("span", { class: "muted" }, "Not known yet"),
            f.checkout ? h("span", { class: "muted mono", style: "font-size:11.5px" }, " · " + f.checkout) : null,
            " ", h("button", { class: "link", onclick: chooseCheckout }, f.checkout ? "Another checkout…" : "Choose its checkout…")),
          f.diff ? kv("Started from", f.cloudBranch ? h("span", { class: "mono", style: "font-size:12px" }, f.cloudBranch) : h("span", { class: "muted" }, "Not known: the patch goes on the checkout's HEAD"),
            f.branchState === "pushed" ? h("span", { class: "muted" }, " · fetched into " + f.ref) : null)
          : kv("Cloud branch", f.cloudBranch ? h("span", { class: "mono", style: "font-size:12px" }, f.cloudBranch)
            : f.write ? h("span", { class: "muted" }, (cloudOf(f.cloud)?.codeDown || []).length ? "None yet: only the messages come" : `None: the code stays in ${f.cloudTitle}`)
            : h("span", { class: "muted" }, `The session's own; ${p.fromAgent} fetches and checks it out`),
            f.branchState === "pushed" ? h("span", { class: "muted" }, " · fetched into " + f.ref) : null),
          f.worktree ? kv("New worktree", h("span", { class: "mono", style: "font-size:12px" }, f.worktree)) : null,
          kv("Local branch", f.diff ? [h("span", { class: "mono", style: "font-size:12px" }, f.localBranch || `hopsesh/from/${f.cloud}/${f.session}`), h("span", { class: "muted" }, ` · the ${noun}'s patch${f.changes ? " (" + f.changes + ")" : ""}, committed`)]
            : f.fastForward ? [h("span", { class: "mono", style: "font-size:12px" }, f.localBranch), h("span", { class: "muted" }, " is here already: it moves forward to the cloud's work")]
            : f.localBranch && f.localBranch !== f.cloudBranch ? [h("span", { class: "mono", style: "font-size:12px" }, f.localBranch), h("span", { class: "muted" }, " renamed from " + f.cloudBranch)]
            : f.rename ? h("span", {}, "A claude/… branch is renamed to ", h("span", { class: "mono", style: "font-size:12px" }, `hopsesh/from/${f.cloud}/…`)) : h("span", { class: "muted" }, "Kept as the cloud names it")),
          f.base ? kv("Starts at", h("span", { class: "ok", style: "font-weight:600" }, "✓ "), f.baseNote || h("span", { class: "mono" }, f.base.slice(0, 7))) : null),
        f.command ? h("div", { class: "runbox" }, h("span", { style: "font-size:12px;font-weight:500" }, outside ? `Runs in ${sys.terminal}` : "Runs in a tab of the hopsesh Terminal window"), h("span", { class: "mono", style: "font-size:11.5px;overflow-wrap:anywhere" }, f.command),
          f.note ? h("span", { class: "warn", id: "copy-note", style: "font-size:12px" }, f.note + ".") : null) : null),
      h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Options"),
        cloudOf(f.cloud)?.codeOnly ? h("span", { class: "muted", style: "font-size:12px" }, `Only the code comes from ${f.cloudTitle}; its conversation stays there for now.`)
        : !(cloudOf(f.cloud)?.codeDown || []).length ? h("span", { class: "muted", style: "font-size:12px" }, `${f.cloudTitle} brings no code here; only its messages come.`)
        : f.diff ? check("Get the code only, without the conversation", "codeOnly", `The ${noun}'s patch, committed on a new branch in a new worktree.`)
        : f.cloudBranch || o.codeOnly ? check("Fetch the cloud branch only, without the conversation", "codeOnly", "Into a new worktree; nothing runs in a terminal.")
          : h("label", { class: "opt", style: "cursor:default" }, h("input", { type: "checkbox", disabled: true }),
            h("span", {}, h("b", { class: "muted" }, "Fetch the cloud branch only, without the conversation"), h("span", { class: "muted" }, "hopsesh knows its branch once it has been brought with its conversation."))),
        f.canAppend ? check(`Add the cloud's work to “${f.original.title}” instead`, "append", "It is as it was handed off, so only the new turns are added; its own turns stay exactly as they were.") : null),
      checks.length ? h("section", { class: "sec", style: "gap:8px" }, h("span", { class: "sec-h" }, "Checks"), checks) : null,
      (f.loss || []).length ? h("details", { class: "sec" }, h("summary", { style: "cursor:pointer;font-size:12.5px" }, "What stays in the cloud"),
        h("ul", { style: "margin:6px 0 0;padding-left:18px;font-size:12.5px" }, f.loss.map((l) => h("li", {}, cap(l))))) : null),
    h("footer", { class: "sheet-foot" },
      h("span", { class: "muted", style: "font-size:12px;flex:1 1 260px" }, f.codeOnly ? "The code comes into a new worktree; your checkout stays as it is."
        : !f.terminal ? "Nothing runs in a terminal; your checkout stays as it is."
        : outside ? `${p.fromAgent} copies it in ${sys.terminal}; hopsesh picks it up when it appears.`
        : `${p.fromAgent} copies it in a tab of the hopsesh Terminal window; hopsesh picks it up when it appears.`),
      h("button", { class: "btn", onclick: () => sheet.close() }, "Cancel"),
      f.terminal && !f.codeOnly ? h("button", { class: "btn", disabled: blocked, onclick: () => { cur.where = outside ? "here" : "terminal"; apply(); } }, outside ? "Bring it to this window instead" : `Bring here in ${sys.terminal}`) : null,
      h("button", { class: "btn primary big", id: "go", disabled: blocked, onclick: () => { cur.where = f.terminal ? (outside ? "terminal" : "here") : ""; apply(); } },
        h("span", {}, f.codeOnly ? "Get the code" : f.terminal && outside ? `Bring here in ${sys.terminal}` : "Bring here"), h("span", { class: "kbd" }, keys("mod+enter"))))));
}

function checkItem(p, c) {
  const kind = c.state === "err" ? "err" : c.state === "warn" ? "warn" : "ok";
  const fix = [];
  if (/^You continued the local session/.test(c.text) && kind === "err") fix.push(h("button", { class: "btn primary small", onclick: () => set("conflict", "keep-both") }, "Keep both"));
  if (/choose its checkout here/.test(c.text)) fix.push(h("button", { class: "btn small", onclick: chooseCheckout }, "Choose its checkout…"));
  if (/no code to bring/.test(c.text) && cur.opts.codeOnly) fix.push(h("button", { class: "btn small", onclick: () => set("codeOnly", false) }, "Bring the conversation only"));
  if (/claude\.ai login/.test(c.text)) fix.push(h("span", { class: "muted", style: "font-size:12px" }, "Run ", h("span", { class: "mono" }, "claude /login"), ", then Refresh"));
  if (/ChatGPT login/.test(c.text)) fix.push(h("span", { class: "muted", style: "font-size:12px" }, "Run ", h("span", { class: "mono" }, "codex login"), ", then Refresh"));
  return item(kind, cap(c.text), "", fix.length ? h("div", { style: "display:flex;gap:8px;flex-wrap:wrap;align-items:center" }, fix) : null);
}

// chooseCheckout picks the repository checkout a cloud session comes into.
async function chooseCheckout() {
  const c = cur;
  const ks = await api("Checkouts");
  if (cur !== c || !sheet.open) return;
  const sel = h("select", { style: "width:100%", "aria-label": "Repository" }, ks.map((k) => h("option", { value: k.path }, `${k.name} · ${k.identity}`)));
  const box = h("div", { class: "item" }, sel, h("button", { class: "btn small", onclick: () => { cur.opts.targetDir = sel.value; return replan(); } }, "Use it"),
    h("button", { class: "btn small", onclick: async () => { const d = await api("ChooseFolder", "The repository's checkout").catch(fail); if (d) { cur.opts.targetDir = d; return replan(); } } }, "Another folder…"));
  sheet.querySelector(".sheet-body")?.prepend(box);
}

async function applyFetch(c) {
  c.applying = true;
  const steps = h("span", { class: "mono muted", style: "font-size:12px" }, "starting");
  sheet.querySelector(".sheet-body").inert = true;
  fill(sheet.querySelector(".sheet-foot"), h("div", { role: "status", style: "display:flex;flex-direction:column;gap:2px;flex:1" }, h("b", {}, "Preparing the worktree…"), steps));
  const off = on("hopsesh:progress", (s) => { steps.textContent = String(s); });
  try {
    const d = await api("Apply");
    cur = null;
    state.stale = true;
    sheet.close();
    if (d.fetch.outcome === "waiting") await openResult(c.where || "");
    go("brought", d.fetch, c.plan);
  } catch (err) {
    c.applying = false;
    problem(errText(err), { label: "Back to the plan", run: () => replan() });
  } finally {
    off();
  }
}

sheet.addEventListener("cancel", (ev) => { if (cur?.applying) ev.preventDefault(); });
sheet.addEventListener("close", () => { if (cur && !cur.applying) { cur = null; api("ClosePlan").catch(() => {}); } });
sheet.addEventListener("keydown", (ev) => {
  if (ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); apply(); }
});

// ---- Done ----
function happened(d, p) {
  const out = [];
  if(d.noWork) {out.push(item("ok","Lineage receipts synchronized","0 new messages, 0 transfers. The existing conversation is ready to open."));}
  else if (d.kind === "continue") {
    const rep = p.continue.report;
    out.push(item("ok", `${d.agent} session written`, rep.method === "vendor-import" ? "Vendor-imported history plus briefing; model continuation not yet verified." : rep.fidelity === "note" ? "With a briefing only." : `${count(rep.messages, "message")}, ${count(rep.toolCalls, "tool call")}.`));
    if (p.nativeCopy) out.push(item("ok", `The ${p.nativeCopy.agent} session is kept ${d.machine ? "on " + d.machine : "here"} too`, `Coming back to ${p.nativeCopy.agent} later adds only the new work to it.`));
  } else {
    out.push(item("ok", `${count(d.files, "file")}, ${d.bytes} copied`, `${count(d.paths, "path")} rewritten.`));
  }
  if (d.cloned) out.push(item("ok", "Repository cloned", ""));
  if (d.worktree) out.push(item("ok", "Worktree created", d.worktree));
  if (d.secrets) out.push(item(d.redacted ? "ok" : "warn", `${count(d.secrets, "likely secret")} ${d.redacted ? "redacted" : "found in the copy"}`, ""));
  if (d.stopped) out.push(item("ok", "Quit the copy that was open here", ""));
  if (d.pushError) out.push(item("warn", `Could not push on ${d.sourceHost}`, d.pushError));
  else if (d.pushed) out.push(item("ok", `Pushed the session's branch on ${d.sourceHost}`, ""));
  if (d.syncNote) out.push(item(["up-to-date", "fast-forwarded", "ahead"].includes(d.syncState) ? "ok" : "warn", "Code: " + d.syncNote, ""));
  if (d.mark === "done") out.push(item("ok", `The copy on ${d.sourceHost} is marked`, "Its session list there shows where the work went."));
  if (d.mark === "pending") out.push(item("ok", `The copy on ${d.sourceHost} is still open`, "It is marked once it ends; Activity shows it until then."));
  if (d.mark === "failed") out.push(item("warn", `Could not mark the copy on ${d.sourceHost}`, d.markError));
  for (const w of d.warnings || []) out.push(item("warn", cap(w), ""));
  return out;
}

screen("done", (d, p, o) => {
  const where = d.machine ? `on ${d.machine}` : `on ${sys.here}`;
  const resultPlace=o.launch==='here'||o.launch==='terminal'?o.launch:opensIn();
  const open = () => (d.inApp ? api("OpenResult", "").catch(fail) : openResult(resultPlace));
  const other = () => openResult(resultPlace === "terminal" ? "here" : "terminal");
  const copy = async () => { await api("CopyText", d.command); toast("Copied"); };
  const doUndo = async () => { if (await undo(d.journal, d.title)) go("sessions", true); };
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:720px" },
    h("div", { style: "display:flex;gap:14px;align-items:center" }, h("span", { class: "badge ok", style: "width:40px;height:40px;font-size:20px" }, "✓"),
      h("div", {}, h("h1", {}, d.noWork ? `“${d.title}” is already synchronized` : d.kind === "continue" ? `“${d.title}” is prepared for ${d.agent}` : `“${d.title}” is ${d.machine ? "on " + d.machine : "here"}`),
        h("div", { class: "muted" }, `${cap(where)}, in `, h("span", { class: "mono" }, p.targetCwd)))),
    d.continuationHint ? h("div", { class: "card continuation-hint", role: "status" }, h("div", { class: "dlg-body" }, d.continuationHint)) : null,
    h("div", { class: "card" }, h("div", { class: "dlg-body" },
      d.machine ? h("b", {}, `Start it on ${d.machine}`) : h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" },
        h("button", { class: "btn primary big", id: "open", onclick: open }, d.inApp ? `Open in the ${d.agent} app` : resultPlace === "terminal" ? `Resume in ${sys.terminal}` : "Resume in hopsesh Terminal", h("span", { class: "kbd" }, "↩")),
        d.inApp ? null : h("button", { class: "btn big", onclick: other }, resultPlace === "terminal" ? "Resume in hopsesh Terminal" : `Resume in ${sys.terminal}`),
        h("button", { class: "btn big", onclick: copy }, "Copy the command")),
      h("div", { style: "display:flex;gap:8px;align-items:flex-start" }, h("div", { class: "term", style: "flex:1" }, d.command), d.machine ? h("button", { class: "btn", onclick: copy }, "Copy") : null),
      h("span", { class: "muted", style: "font-size:12px" }, d.noWork ? "Opens the existing session; no new conversation or briefing was written." : d.kind === "continue" ? "Hopsesh prepared the history and briefing. Import notices are written by Hopsesh, not by the destination agent."
        : `Its first message tells ${d.agent} where the session came from and asks it to check the repository and files before going on.`))),
    h("section", { class: "card" }, h("div", { class: "dlg-body" }, h("span", { class: "sec-h" }, "What happened"), happened(d, p),
      p.continue && !d.noWork ? h("details", {}, h("summary", { style: "cursor:pointer;font-size:12.5px" }, "Show the loss report"), h("div", { style: "margin-top:10px" }, boxes(p))) : null)),
    d.notice ? h("section", { class: "card" }, h("div", { class: "dlg-body" }, h("b", {}, "Movement notice"),
      h("span", { class: "muted", style: "font-size:12px" }, "The source records where the destination was prepared. Observed new work is reported separately."),
      h("div", { style: "display:flex;gap:8px;align-items:flex-start" }, h("div", { class: "term", style: "flex:1" }, d.notice),
        h("button", { class: "btn", onclick: async () => { await api("CopyText", d.notice); toast("Copied"); } }, "Copy")))) : null,
    h("div", { style: "display:flex;gap:10px;align-items:center;flex-wrap:wrap" },
      h("button", { class: "btn", title: d.machine ? `Undoes both machines: the copy on ${d.machine} and the mark here` : "", onclick: doUndo }, "Undo", h("span", { class: "kbd" }, keys("mod+alt+Z"))),
      h("span", { class: "muted", style: "font-size:12px" }, "Undo stays in Activity for as long as nothing happens on top of it. ",
        h("button", { class: "link", onclick: () => api("Reveal", d.auditDir).catch(fail) }, "Audit log"))))));
  view.querySelector("#open")?.focus();
});

// On the Done screen, Esc goes back to the sessions.
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && current === "done" && !document.querySelector("dialog[open]")) go("sessions", true);
});
