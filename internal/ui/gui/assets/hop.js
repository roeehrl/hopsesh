// Handing a cloud session on to another cloud through this machine (Move ▾ on a cloud
// session, from actions.js): the sheet with both legs and what the trip loses, the wait
// while the first leg runs in the user's terminal (Claude Code's teleport), and the
// hand-off's done screen, which says it came through here.
import { api, on, h, fill, state, go, toast, fail, errText, cap, cloudChip, cloudTitle, $, sys, keys } from "./core.js";
import { stepBox, menuItem } from "./handoff.js";
import { undo } from "./activity.js";

const sheet = $("#sheet");
let hop = null; // { e, cloud, opts, plan, busy, applying, waiting }

const mono = (s) => h("span", { class: "mono", style: "font-size:12px" }, s);
const tick = (kind) => h("span", { class: "badge " + kind }, kind === "ok" ? "✓" : kind === "err" ? "✕" : "!");

// planHop opens the hop sheet for a cloud session.
export async function planHop(e, cloud) {
  const opening = {};
  hop = opening;
  fill(sheet, h("div", { class: "sheet-in" }, h("div", { class: "loading", role: "status" }, "Reading hand-off settings…"), h("button", { class: "btn", onclick: () => sheet.close() }, "Cancel")));
  if (!sheet.open) sheet.showModal();
  let d = {};
  try { d = await api("HandoffDefaults", cloud); } catch { /* the defaults below */ }
  if (hop !== opening || !sheet.open) return;
  hop = { e, cloud, opts: { untracked: [], historyFile: false, bundle: false, mark: d.mark !== false, cleanup: d.cleanup || "", brief: "", note: "", carryRules: false, env: "", startingDiff: false },
    plan: null, busy: false, applying: false };
  fill(sheet, h("div", { class: "sheet-in" }, h("div", { class: "loading", role: "status", style: "min-height:240px" }, "Working out both legs…")));
  if (!sheet.open) sheet.showModal();
  await replan();
}

let planning = Promise.resolve();
async function replan() {
  const c = hop;
  if (!c) return;
  const revision = c.revision = (c.revision || 0) + 1;
  c.busy = true;
  const btn = sheet.querySelector("#hop-go");
  if (btn) btn.disabled = true;
  try {
    const request = planning.then(() => {
      if (hop !== c || c.revision !== revision) return null;
      return api("PlanHop", c.e.machine, c.e.key, c.cloud, c.opts);
    });
    planning = request.catch(() => {});
    const p = await request;
    if (hop !== c || c.revision !== revision || !p) return;
    c.plan = p;
    c.busy = false;
    render();
  } catch (err) {
    if (hop !== c || c.revision !== revision) return;
    c.busy = false;
    if (hop === c) problem(errText(err));
  }
}

const set = (k, v) => { hop.opts[k] = v; return replan(); };

function problem(msg, journal) {
  fill(sheet, h("div", { class: "sheet-in", role: "alertdialog", "aria-labelledby": "hop-msg" },
    h("div", { class: "sheet-body" }, h("div", { class: "item" }, tick("err"), h("div", { id: "hop-msg", class: "err", style: "line-height:1.5" }, msg))),
    h("div", { class: "sheet-foot" }, h("span", { class: "spacer" }),
      journal ? h("button", { class: "btn", onclick: async () => { if (await undo(journal, hop?.plan?.title || "the hop")) { hop = null; sheet.close(); go("sessions", true); } } }, "Undo") : null,
      h("button", { class: "btn primary", onclick: () => { hop = null; sheet.close(); go("sessions", true); } }, "Close"))));
}

function envPicker(t) {
  const known = (t.envs || []).map((e) => ({ value: e.value, label: e.label }));
  if (t.env && !known.some((e) => e.value === t.env)) known.unshift({ value: t.env, label: t.envName || t.env });
  const sel = h("select", { id: "hop-env", style: "flex:1 1 320px;max-width:460px", class: t.env ? "" : "needs", onchange: (ev) => { if (ev.target.value) set("env", ev.target.value); } },
    t.env ? null : h("option", { value: "", selected: true }, "Pick an environment…"),
    known.map((e) => h("option", { value: e.value, selected: e.value === t.env }, e.label)));
  return h("div", { style: "display:flex;flex-direction:column;gap:6px" },
    h("div", { style: "display:flex;align-items:center;gap:12px;flex-wrap:wrap" },
      h("label", { for: "hop-env", style: "font-size:12.5px;font-weight:500;flex:0 0 100px" }, "Environment"), sel),
    t.envNote ? h("span", { class: "warn", style: "font-size:12px" }, t.envNote) : null);
}

function render() {
  const p = hop.plan, x = p.hop, t = x.then || {};
  const blocked = (p.blockers || []).length > 0;
  const checks = [...(x.bring?.fetch?.checks || []), ...(t.checks || [])].filter((c) => c.state !== "err");
  fill(sheet, h("div", { class: "sheet-in" },
    h("header", { class: "sheet-head" },
      h("h2", { id: "sheet-title" }, `Hand “${p.title}” on to ${x.toTitle}`),
      h("div", { class: "fromto" }, cloudChip(x.from), h("span", { style: "color:var(--accent)", "aria-label": "to" }, "→"), h("span", {}, `${sys.here} (${x.agent})`),
        h("span", { style: "color:var(--accent)", "aria-label": "to" }, "→"), cloudChip(x.to)),
      h("div", { class: "summary", "aria-label": "What changes" }, h("span", { class: "add" }, `+ 1 ${x.agent} session here, in a new worktree`),
        h("span", { class: "add" }, `+ 1 ${t.cloudTitle || x.toTitle} ${t.noun || "session"}`), h("span", { class: "none" }, "0 removed"), h("span", { class: "spacer" }),
        h("span", { class: "muted" }, "Undo from Activity takes both legs back"))),
    h("div", { class: "sheet-body" },
      h("section", { class: "sec", style: "gap:10px", "aria-label": "Both legs" }, h("span", { class: "sec-h" }, "Two legs, through " + sys.here),
        x.legs.map((l, i) => h("div", { class: "item hop-leg", "data-leg": String(i + 1) }, h("span", { class: "badge" }, String(i + 1)),
          h("div", { style: "display:flex;flex-direction:column;gap:2px" }, h("b", {}, `${l.verb}: ${l.fromTitle || cloudTitle(l.from)} → ${l.toTitle || cloudTitle(l.to)}`, h("span", { class: "muted", style: "font-weight:400" }, ` · ${l.fidelity}`)),
            h("span", { style: "font-size:12.5px" }, l.words))))),
      h("section", { class: "sec", style: "gap:8px" }, h("span", { class: "sec-h" }, "The conversation and the code"),
        h("p", { style: "margin:0;font-size:13.5px;font-weight:500" }, x.conversation),
        h("span", { style: "font-size:12.5px" }, x.code),
        x.terminal ? h("span", { class: "warn", id: "hop-terminal", style: "font-size:12.5px;line-height:1.5" }, x.terminal) : null,
        t.terminal ? h("span", { class: "muted", style: "font-size:12px;line-height:1.5" }, t.terminal) : null),
      h("section", { class: "sec", style: "gap:12px" }, h("span", { class: "sec-h" }, "Options"),
        t.envNeeded ? envPicker(t) : null,
        h("label", { class: "opt" }, h("input", { type: "checkbox", checked: p.mark !== "off" && hop.opts.mark, onchange: (ev) => set("mark", ev.target.checked) }),
          h("span", {}, `Label the title here “${(t.markTitle || "").replace(/^↪\s*/, "")}”`, h("span", {class:"muted",style:"display:block"}, "Visual reminder only; it does not lock the conversation or block further work.")))),
      h("section", { class: "sec", style: "gap:8px" }, h("span", { class: "sec-h" }, "Checks"),
        h("div", { class: "checks-line" }, checks.map((c) => h("span", {}, tick(c.state === "warn" ? "warn" : "ok"), " ", c.text))),
        (p.blockers || []).map((b) => h("div", { class: "item" }, tick("err"), h("span", { class: "err" }, cap(b)))),
        t.usage ? h("span", { style: "font-size:12px" }, t.usage) : null),
      h("details", { class: "sec" }, h("summary", { style: "cursor:pointer;font-size:12.5px" }, "Everything the trip leaves behind"),
        h("ul", { style: "margin:6px 0 0;padding-left:18px;font-size:12.5px" }, (x.loss || []).map((l) => h("li", {}, cap(l)))))),
    h("footer", { class: "sheet-foot" },
      h("span", { class: "muted", style: "font-size:12px;flex:1 1 260px" }, "Nothing changes until you hand it on. Both cloud sessions stay where they are."),
      h("button", { class: "btn", onclick: () => sheet.close() }, "Cancel"),
      h("button", { class: "btn primary big", id: "hop-go", disabled: blocked || hop.busy, onclick: apply }, h("span", {}, "Hand it on"), h("span", { class: "kbd" }, keys("mod+enter"))))));
}

async function apply() {
  const c = hop;
  if (!c?.plan || c.busy || c.applying || (c.plan.blockers || []).length) return;
  c.applying = true;
  const p = c.plan, x = p.hop;
  const lines = h("div", { class: "steps", role: "list" });
  const term = h("div", { class: "sheet-body", hidden: true, style: "border-top:1px solid var(--line)" });
  fill(sheet, h("div", { class: "sheet-in" }, h("header", { class: "sheet-head" }, h("span", { class: "sec-h" }, "While it applies"),
    h("h2", { id: "sheet-title" }, `Handing it on to ${x.toTitle}…`), h("span", { class: "muted", style: "font-size:12.5px" }, `“${p.title}” · through ${sys.here}`)),
    h("div", { class: "sheet-body" }, lines), term));
  const off = on("hopsesh:progress", (label) => { lines.append(h("div", { class: "step", role: "listitem" }, h("span", { class: "step-ic ok" }, "•"), h("span", {}, label))); });
  let shown = "";
  const poll = setInterval(async () => {
    const st = await api("HandoffStep").catch(() => null);
    const key = st ? `${st.state}|${st.message || ""}` : "";
    if (key !== shown) { shown = key; stepBox(term, st); }
  }, 500);
  try {
    const d = await api("ApplyHop");
    state.stale = true;
    settle(c, d);
  } catch (err) {
    c.applying = false;
    problem(errText(err));
  } finally {
    off();
    clearInterval(poll);
  }
}

// settle shows where the hop ended: the done screen, a failure, or the wait for the copy.
function settle(c, d) {
  const st = d.hop?.state;
  if (st === "waiting") { waiting(c, d); return; }
  c.applying = false;
  if (st === "done" && d.handoff) {
    hop = null;
    sheet.close();
    go("handedoff", Object.assign({}, d.handoff, { journal: d.journal, title: d.title || c.plan.title,
      via: { fromTitle: c.plan.hop.fromTitle, key: d.hop.key, worktree: d.brought?.worktree } }));
    return;
  }
  problem(d.error || d.hop?.message || "The hop stopped.", d.journal);
}

// waiting is the first leg's wait: the teleport runs in the user's terminal, and the copy
// is saved once a message is sent in it.
function waiting(c, d) {
  const x = c.plan.hop;
  const box = h("div", { class: "waiting", role: "status" }, h("span", { class: "spinner", "aria-hidden": "true" }),
    h("span", {}, `${cap(x.bring?.agent || "The agent")} is bringing “${c.plan.title}” here in ${sys.terminal}; then it goes on to ${x.toTitle}.`),
    d.hop.note ? h("span", { class: "warn", id: "hop-note", style: "font-size:12.5px" }, d.hop.note + ".") : null);
  fill(sheet, h("div", { class: "sheet-in" }, h("header", { class: "sheet-head" }, h("span", { class: "sec-h" }, "First leg"),
    h("h2", { id: "sheet-title" }, `Bringing it here from ${x.fromTitle}…`)),
    h("div", { class: "sheet-body" }, box, h("div", { class: "term" }, d.hop.command), (d.warnings || []).map((w) => h("span", { class: "warn", style: "font-size:12px" }, w))),
    h("div", { class: "sheet-foot" },
      h("button", { class: "btn", onclick: () => api("OpenHop", d.journal).catch(fail) }, `Open in ${sys.terminal} again`),
      h("span", { class: "spacer" }),
      h("button", { class: "btn", onclick: async () => { stop(); if (await undo(d.journal, c.plan.title)) { hop = null; sheet.close(); go("sessions", true); } } }, "Undo"),
      h("button", { class: "btn", onclick: () => { stop(); hop = null; sheet.close(); toast("It goes on once the copy is here: Activity shows the hop"); } }, "Close"))));
  let busy = false;
  const timer = setInterval(async () => {
    if (busy) return;
    busy = true;
    try {
      const n = await api("ContinueHop", d.journal);
      if (n.hop?.state !== "waiting") { stop(); settle(c, n); }
    } catch (err) { stop(); c.applying = false; problem(errText(err), d.journal); }
    busy = false;
  }, 1500);
  const stop = () => clearInterval(timer);
}

sheet.addEventListener("cancel", (ev) => { if (hop?.applying) ev.preventDefault(); });
sheet.addEventListener("close", () => { if (hop && !hop.applying) { hop = null; api("ClosePlan").catch(() => {}); } });
sheet.addEventListener("keydown", (ev) => {
  if (hop && !hop.applying && ev.key === "Enter" && (ev.metaKey || ev.ctrlKey)) { ev.preventDefault(); apply(); }
});
