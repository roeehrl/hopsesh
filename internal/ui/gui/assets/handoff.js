// Handing a session off to a cloud: the Hand off ▸ menu (every cloud; a disabled one says
// why), the plan sheet (the briefing, the code, what stays on this machine, the options),
// the steps while it applies (and a step that failed), and the done screen with the
// session's link and Undo.
import { api, on, h, fill, view, state, screen, go, current, toast, fail, errText, cap, agentChip, cloudChip, $, count, sys, keys, ask, dialog } from "./core.js";
import { undo } from "./activity.js";
import { showTerminal, tabFor, IN_A_TAB } from "./term.js";

const sheet = $("#sheet");
let hc = null; // { e, cloud, opts, plan, busy, applying }

const STEP = { snapshot: "Snapshot", push: "Push branch", start: "Start cloud session", lineage: "Record lineage", mark: "Mark this session" };

// handoffMenu is the Hand off ▸ button and, open, its menu: one entry per cloud, a
// disabled one with its reason inline.
export function handoffMenu(e, open, toggle) {
  const targets = e.handoff || [];
  if (!targets.length) return null;
  const btn = h("button", { class: "btn", "aria-haspopup": "menu", "aria-expanded": open ? "true" : "false", onclick: toggle }, "Hand off ▸");
  if (!open) return btn;
  return h("div", { style: "display:flex;flex-direction:column;gap:6px" }, btn,
    h("div", { class: "menu", role: "menu", "aria-label": "Hand off to" },
      h("div", { class: "menu-h", role: "presentation" }, "Hand off to"),
      targets.map((t) => h("button", { class: "menu-item", role: "menuitem", "aria-disabled": t.ok ? null : "true", disabled: !t.ok,
          onclick: () => { if (t.ok) planHandoff(e, t.cloud, t.bundle); } },
        cloudChip(t.cloud), h("span", { style: "display:flex;flex-direction:column;gap:2px;min-width:0" },
          h("span", { class: "menu-t" }, t.title),
          t.ok ? h("span", { class: "muted", style: "font-size:11.5px" }, t.note) : h("span", { class: "err", style: "font-size:11.5px" }, `${t.title}: ${t.why}`),
          (t.limits || []).length ? h("span", { class: "muted", style: "font-size:11px" }, t.limits[0]) : null))),
      h("div", { class: "muted", role: "presentation", style: "font-size:11.5px;padding:4px 8px" }, "A cloud gets a briefing, not this conversation.")));
}

// pickHandoff asks which cloud (the palette's Hand off to…).
export function pickHandoff(e) {
  if (tabFor(e)) { toast(IN_A_TAB); return; }
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Hand off “${e.title}” to…`),
    h("div", { class: "menu", role: "menu", "aria-label": "Hand off to", style: "position:static" },
      (e.handoff || []).map((t) => h("button", { class: "menu-item", role: "menuitem", "aria-disabled": t.ok ? null : "true", disabled: !t.ok,
          onclick: () => { d.close(); planHandoff(e, t.cloud, t.bundle); } },
        cloudChip(t.cloud), h("span", { style: "display:flex;flex-direction:column;gap:2px" }, h("span", { class: "menu-t" }, t.title),
          h("span", { class: t.ok ? "muted" : "err", style: "font-size:11.5px" }, t.ok ? t.note : `${t.title}: ${t.why}`),
          (t.limits || []).length ? h("span", { class: "muted", style: "font-size:11px" }, t.limits[0]) : null)))),
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel")));
}

// planHandoff opens the hand-off sheet for a session.
export async function planHandoff(e, cloud, bundle = false) {
  if (tabFor(e)) { toast(IN_A_TAB); return; }
  let d = {};
  try { d = await api("HandoffDefaults", cloud); } catch { /* the defaults below */ }
  hc = { e, cloud, opts: { untracked: [], historyFile: !!d.historyFile, bundle: bundle || !!d.bundle, mark: d.mark !== false, cleanup: d.cleanup || "", brief: "", note: "", carryRules: false,
    env: "", startingDiff: false },
    plan: null, busy: false, applying: false };
  fill(sheet, h("div", { class: "sheet-in" }, h("div", { class: "loading", role: "status", style: "min-height:240px" }, "Working out the hand-off…")));
  if (!sheet.open) sheet.showModal();
  await replan();
}

async function replan() {
  const c = hc;
  c.busy = true;
  const btn = sheet.querySelector("#ho-go");
  if (btn) { btn.disabled = true; btn.firstChild.textContent = "Updating the plan…"; }
  try {
    const p = await api("PlanHandoff", c.e.machine, c.e.key, c.cloud, c.opts);
    if (hc !== c) return;
    c.plan = p;
    c.busy = false;
    render();
  } catch (err) {
    c.busy = false;
    if (hc === c) problem(errText(err));
  }
}

const set = (k, v) => { hc.opts[k] = v; replan(); };

function problem(msg) {
  fill(sheet, h("div", { class: "sheet-in" },
    h("div", { class: "sheet-body" }, h("div", { class: "item" }, h("span", { class: "badge err" }, "✕"), h("div", { class: "err", style: "line-height:1.5" }, msg))),
    h("div", { class: "sheet-foot" }, h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => sheet.close() }, "Close"))));
}

const mono = (s) => h("span", { class: "mono", style: "font-size:12px" }, s);
const tick = (kind) => h("span", { class: "badge " + kind }, kind === "ok" ? "✓" : kind === "err" ? "✕" : "!");
const thousands = (n) => n.toLocaleString("en-US");
const markWords = (t) => t.replace(/^↪\s*/, "");

function summary(p) {
  const x = p.handoff;
  const add = [`1 ${x.cloudTitle} ${x.noun || "session"}`];
  if (x.code === "bundle") add.push("1 upload");
  else if (x.code === "starting-diff") add.push("1 starting diff");
  else if (!x.reuse && x.branch) add.push(`1 branch on ${x.host}`);
  const chg = p.mark !== "off" ? ["this session marked" + (p.mark === "when-stopped" ? " when it ends" : "")] : [];
  return h("div", { class: "summary", "aria-label": "What changes" },
    add.map((s) => h("span", { class: "add" }, "+ " + s)), chg.map((s) => h("span", { class: "chg" }, "~ " + s)),
    h("span", { class: "none" }, "0 removed"), h("span", { class: "spacer" }),
    h("span", { class: "muted" }, `Undo from Activity removes ${x.reuse || x.code !== "branch" ? "" : "the branch and "}the mark`));
}

function conversation(p) {
  const x = p.handoff;
  const box = (cls, title, lines) => h("div", { class: "box " + cls }, h("b", {}, title), lines.map((l) => h("span", {}, l)));
  const area = h("textarea", { id: "ho-brief", class: "mono brief", rows: 9, spellcheck: "false", "aria-label": "Briefing" });
  area.value = x.brief;
  const tokens = h("span", { class: "muted", style: "font-size:12px", "aria-live": "polite" }, `${thousands(x.tokens)} tokens`);
  area.oninput = () => { tokens.textContent = `${thousands(Math.round(area.value.length / 4))} tokens`; };
  area.onchange = () => { if (area.value !== x.brief) set("brief", area.value); };
  const masked = h("div", { class: "masked", hidden: true, style: "font-size:12px" },
    x.masked ? ["Masked (best effort, not a guarantee): ", (x.maskedRules || []).map((r, i) => [i ? ", " : "", mono(r)])] : "Nothing looked like a secret. The scanner is best effort, not a guarantee.");
  return h("section", { class: "sec", style: "border:0;padding:0;gap:12px" },
    h("span", { class: "sec-h" }, "The conversation"),
    h("p", { style: "margin:0;font-size:13.5px;font-weight:500" }, x.conversation),
    h("div", { class: "three-boxes" }, box("kept", "Carried over", x.carried), box("changed", "Changed", x.changed), box("lost", "Stays here", x.stays)),
    h("div", { class: "brief-box" },
      h("div", { class: "brief-head" }, h("label", { for: "ho-brief", style: "font-size:12.5px;font-weight:500" }, "Briefing"), tokens,
        h("button", { class: "link", onclick: () => { masked.hidden = !masked.hidden; } }, `masked: ${x.masked}`),
        h("span", { class: "spacer" }),
        x.edited ? h("button", { class: "btn small", onclick: () => set("brief", "") }, "Reset") : null,
        h("button", { class: "btn small", onclick: async () => { await api("CopyText", area.value); toast("Copied"); } }, "Copy")),
      masked, area));
}

function repository(p) {
  const x = p.handoff;
  const items = [];
  const carries = [];
  if (x.unpushed) carries.push(count(x.unpushed, "unpushed commit"));
  const files = [...(x.tracked || []), ...(x.untracked || [])];
  if (files.length) carries.push(count(files.length, "changed file"));
  if (x.branch && x.code === "starting-diff") {
    items.push(h("div", { class: "chk" }, tick("ok"), h("span", {}, `Sends the changes with the ${x.noun} as a starting diff, on `, mono(x.branch), `, already on ${x.host}. Nothing is pushed.`)));
    if (files.length) items.push(h("div", { class: "chk" }, tick("ok"), h("span", {}, `Carries ${count(files.length, "changed file")}: `, files.slice(0, 6).map((f, i) => [i ? ", " : "", mono(f)]), files.length > 6 ? ` and ${files.length - 6} more` : "")));
  } else if (x.branch) {
    if (x.reuse) items.push(h("div", { class: "chk" }, tick("ok"), h("span", {}, x.code === "bundle" ? ["Uploads ", mono(x.branch), " as it is. Your checkout here is not touched."]
      : ["Uses branch ", mono(x.branch), `, already on ${x.host}. Nothing is pushed.`])));
    else items.push(h("div", { class: "chk" }, tick("ok"), h("span", {}, x.code === "bundle" ? ["Uploads a snapshot from ", mono(x.base.slice(0, 7)), ` (${x.baseBranch}), kept here as `, mono(x.branch), ". Nothing is pushed."]
      : ["Pushes branch ", mono(x.branch), " from ", mono(x.base.slice(0, 7)), ` (${x.baseBranch}). Your checkout here is not touched.`])));
    if (carries.length) items.push(h("div", { class: "chk" }, tick("ok"), h("span", {}, `Carries ${carries.join(" and ")}`, files.length ? [": ", files.slice(0, 6).map((f, i) => [i ? ", " : "", mono(f)]), files.length > 6 ? ` and ${files.length - 6} more` : ""] : "")));
  }
  const offered = [...(x.untracked || []).map((u) => ({ path: u, on: true })), ...(x.offered || []).map((c) => ({ path: c.path, size: c.size, on: false }))];
  const toggle = (path, on) => set("untracked", on ? [...hc.opts.untracked, path] : hc.opts.untracked.filter((u) => u !== path));
  return h("section", { class: "sec", style: "gap:10px" }, h("span", { class: "sec-h" }, "Repository and code"), items,
    offered.length ? h("div", { style: "display:flex;flex-direction:column;gap:6px;padding-left:28px" },
      h("span", { class: "muted", style: "font-size:12px" }, "Untracked files go up only if you tick them"),
      offered.map((o) => h("label", { class: "opt" }, h("input", { type: "checkbox", checked: o.on, onchange: (ev) => toggle(o.path, ev.target.checked) }),
        mono(o.path), h("span", { class: "muted", style: "display:inline" }, "untracked" + (o.size >= 0 && o.size !== undefined ? " · " + (o.size < 1024 ? o.size + " B" : Math.round(o.size / 1024) + " KB") : ""))))) : null,
    (x.withheld || []).length ? h("div", { class: "stays", role: "group", "aria-label": `Stays on ${p.local ? sys.here : p.sourceHost}` },
      h("div", { style: "display:flex;align-items:center;gap:10px" }, h("b", {}, `Stays on ${p.local ? sys.here : p.sourceHost}`), h("span", { class: "spacer" }),
        h("button", { class: "link", onclick: (ev) => { const w = ev.target.closest(".stays").querySelector(".why"); w.hidden = !w.hidden; } }, "Why?")),
      h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, x.withheld.map((w) => h("span", { class: "mono file-chip" }, w.path))),
      h("span", { class: "muted", style: "font-size:11.5px" }, "Files that look like credentials never go to a cloud."),
      h("span", { class: "why muted", hidden: true, style: "font-size:11.5px" }, "Credential-like names (.env, *.pem, *.key, id_rsa, …), files Git LFS manages and files over 50 MB stay, whatever you tick: a branch on a host can be read by anyone who can see it, and a deleted branch may not be gone everywhere.")) : null,
    x.bundleOffer && x.code !== "bundle" ? h("div", { class: "blockbox" }, h("span", {}, x.bundleOffer),
      h("button", { class: "btn", style: "align-self:flex-start", onclick: () => set("bundle", true) }, `Hand off to ${x.cloudTitle} as an upload`)) : null);
}

// envPicker chooses the cloud's environment (Codex cloud runs every task in one): the ones
// recent tasks used, the repository's own first; Other… takes an id or a name.
function envPicker(x) {
  const known = (x.envs || []).map((e) => ({ value: e.value, label: e.label }));
  if (x.env && !known.some((e) => e.value === x.env)) known.unshift({ value: x.env, label: x.envName || x.env });
  const other = h("input", { id: "ho-env-other", class: "field", placeholder: "Environment id or name", "aria-label": "Another environment", hidden: true, style: "flex:1 1 220px;max-width:300px",
    onkeydown: (ev) => { if (ev.key === "Enter" && ev.target.value.trim()) { ev.preventDefault(); set("env", ev.target.value.trim()); } },
    onchange: (ev) => { if (ev.target.value.trim()) set("env", ev.target.value.trim()); } });
  const sel = h("select", { id: "ho-env", style: "flex:1 1 320px;max-width:460px", class: x.env ? "" : "needs",
    onchange: (ev) => { if (ev.target.value === "__other__") { other.hidden = false; other.focus(); } else if (ev.target.value) set("env", ev.target.value); } },
    x.env ? null : h("option", { value: "", selected: true }, "Pick an environment…"),
    known.map((e) => h("option", { value: e.value, selected: e.value === x.env }, e.label)),
    h("option", { value: "__other__" }, "Other…"));
  return h("div", { style: "display:flex;flex-direction:column;gap:6px" },
    h("div", { style: "display:flex;align-items:center;gap:12px;flex-wrap:wrap" },
      h("label", { for: "ho-env", style: "font-size:12.5px;font-weight:500;flex:0 0 100px" }, "Environment"), sel, other),
    x.envNote ? h("span", { class: "warn", id: "ho-env-note", style: "font-size:12px" }, envNoteWords(x.envNote)) : null);
}

// envNoteWords sets a command in the note in monospace.
function envNoteWords(text) {
  return text.split(/(`[^`]+`)/).map((part) => part.startsWith("`") ? mono(part.slice(1, -1)) : part);
}

function options(p) {
  const x = p.handoff, o = hc.opts;
  return h("section", { class: "sec", style: "gap:12px" }, h("span", { class: "sec-h" }, "Options"),
    x.envNeeded ? envPicker(x) : null,
    x.canStartingDiff ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: x.code === "starting-diff", onchange: (ev) => set("startingDiff", ev.target.checked) }),
      h("span", {}, h("b", {}, `Send the changes with the ${x.noun} as a starting diff instead of a branch`), h("span", { class: "muted", style: "display:block;font-size:12px" }, x.startingDiffOffer + "."))) : null,
    h("label", { class: "opt" }, h("input", { type: "checkbox", checked: x.historyFile, onchange: (ev) => set("historyFile", ev.target.checked) }),
      h("span", {}, h("b", {}, "Also commit the conversation as ", mono(x.historyPath)), h("span", { class: "warn", style: "display:block;font-size:12px" }, x.historyWarning))),
    h("label", { class: "opt" }, h("input", { type: "checkbox", checked: p.mark !== "off" && o.mark, onchange: (ev) => set("mark", ev.target.checked) }),
      h("span", {}, `Mark this session “${markWords(x.markTitle)}”`)),
    x.code === "branch" && !x.reuse ? h("div", { style: "display:flex;align-items:center;gap:12px;flex-wrap:wrap" },
      h("label", { for: "ho-cleanup", style: "font-size:12.5px;font-weight:500;flex:0 0 100px" }, "Branch"),
      h("select", { id: "ho-cleanup", style: "flex:1 1 320px;max-width:460px", onchange: (ev) => set("cleanup", ev.target.value) },
        x.cleanups.map((c) => h("option", { value: c.value, selected: c.value === x.cleanup }, c.label)))) : null,
    x.code === "bundle" && !x.bundleOffer ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: true, onchange: (ev) => set("bundle", ev.target.checked) }),
      h("span", {}, h("b", {}, "Upload the repository instead of pushing a branch"), h("span", { class: "muted" }, `${x.agent} sends it as a bundle; the cloud can't push its work back to the remote.`))) : null);
}

function checks(p) {
  const x = p.handoff;
  return h("section", { class: "sec", style: "gap:8px" }, h("span", { class: "sec-h" }, "Checks"),
    h("div", { class: "checks-line" }, (x.checks || []).filter((c) => c.state !== "err").map((c) => h("span", {}, tick(c.state === "warn" ? "warn" : "ok"), " ", c.text))),
    (p.blockers || []).filter((b) => b !== x.envNote).map((b) => h("div", { class: "item" }, tick("err"), h("span", { class: "err" }, cap(b)),
      /claude\.ai login/.test(b) ? h("span", { class: "muted", style: "font-size:12px" }, " Run ", mono("claude /login"), ", then Refresh") : null,
      /ChatGPT login/.test(b) ? h("span", { class: "muted", style: "font-size:12px" }, " Run ", mono("codex login"), ", then Refresh") : null)),
    h("span", { style: "font-size:12px" }, x.usage),
    x.terminal ? h("span", { class: "muted", id: "ho-terminal", style: "font-size:12px;line-height:1.5" }, envNoteWords(x.terminal),
      x.folder ? [" The folder: ", h("span", { class: "mono", style: "font-size:11.5px;word-break:break-all" }, x.folder)] : null) : null,
    (x.notes || []).map((n) => h("span", { class: "muted", style: "font-size:12px" }, n)),
    (x.limits || []).map((n) => h("span", { class: "muted", style: "font-size:12px" }, n)));
}

function render() {
  const p = hc.plan, x = p.handoff;
  const blocked = (p.blockers || []).length > 0;
  fill(sheet, h("div", { class: "sheet-in" },
    h("header", { class: "sheet-head" },
      h("h2", { id: "sheet-title" }, `Hand off “${p.title}” to ${x.cloudTitle}`),
      h("div", { class: "fromto" }, agentChip(p.agentId, p.agent), h("span", {}, p.local ? `on ${p.sourceHost} (${sys.here})` : `on ${p.sourceHost}`),
        h("span", { style: "color:var(--accent)", "aria-label": "to" }, "→"), cloudChip(x.cloud), x.repo ? h("span", { class: "mono muted", style: "font-size:12px" }, x.repo) : null),
      summary(p)),
    h("div", { class: "sheet-body" }, conversation(p), repository(p), options(p), checks(p),
      h("details", { class: "sec" }, h("summary", { style: "cursor:pointer;font-size:12.5px" }, "Everything that stays here"),
        h("ul", { style: "margin:6px 0 0;padding-left:18px;font-size:12.5px" }, (x.loss || []).map((l) => h("li", {}, l))))),
    h("footer", { class: "sheet-foot" },
      h("span", { class: "muted", style: "font-size:12px;flex:1 1 260px" }, "Nothing changes until you hand off. The session here is never deleted."),
      h("button", { class: "btn", onclick: () => sheet.close() }, "Cancel"),
      h("button", { class: "btn primary big", id: "ho-go", disabled: blocked || hc.busy, onclick: apply }, h("span", {}, "Hand off"), h("span", { class: "kbd" }, keys("mod+enter"))))));
}

// steps is the checklist while it applies, or as it ended.
function steps(names, states) {
  return h("div", { class: "steps", role: "list" }, names.map((n) => {
    const st = states[n] || { state: "todo" };
    const ic = st.state === "done" ? h("span", { class: "step-ic ok" }, "✓") : st.state === "failed" ? h("span", { class: "step-ic err" }, "✕")
      : st.state === "running" ? h("span", { class: "step-ic run" }, "…") : h("span", { class: "step-ic" });
    return h("div", { class: "step", role: "listitem", "data-step": n, "data-state": st.state }, ic,
      h("div", { style: "display:flex;flex-direction:column;gap:2px" }, h("span", { style: st.state === "running" ? "font-weight:600" : st.state === "todo" ? "color:var(--muted)" : "" },
        STEP[n] + (st.notDone ? " · not done" + (n === "mark" ? ", it stays as it was" : "") : "")),
        st.detail ? h("span", { class: st.state === "failed" ? "mono err" : "muted", style: "font-size:11.5px" }, st.detail) : null));
  }));
}

async function apply() {
  const c = hc;
  if (!c?.plan || c.busy || c.applying || (c.plan.blockers || []).length) return;
  c.applying = true;
  const p = c.plan, x = p.handoff;
  const states = {};
  const body = h("div", { class: "sheet-body" });
  const foot = h("div", { class: "sheet-foot" });
  const paint = () => {
    const i = x.steps.findIndex((n) => states[n]?.state === "running");
    fill(body, steps(x.steps, states));
    fill(foot, h("span", { class: "muted", style: "font-size:12px" }, i >= 0 ? `Step ${i + 1} of ${x.steps.length}` : ""), h("span", { class: "spacer" }), h("button", { class: "btn", disabled: true }, "Close"));
  };
  const term = h("div", { class: "sheet-body", id: "ho-term", hidden: true, style: "border-top:1px solid var(--line)" });
  fill(sheet, h("div", { class: "sheet-in" }, h("header", { class: "sheet-head" }, h("span", { class: "sec-h" }, "While it applies"),
    h("h2", { id: "sheet-title" }, `Handing off to ${x.cloudTitle}…`), h("span", { class: "muted", style: "font-size:12.5px" }, `“${p.title}”${x.repo ? " · " + x.repo : ""}`)), body, term, foot));
  paint();
  // A cloud whose driver starts the session in a terminal: hopsesh opened one; say what
  // happens there, and take the session's link by hand when hopsesh sees none.
  let shown = "";
  const poll = setInterval(async () => {
    const st = await api("HandoffStep").catch(() => null);
    const key = st ? `${st.state}|${st.where}|${st.tab}|${st.exit}|${st.message || ""}` : "";
    if (key !== shown) { shown = key; stepBox(term, st); }
  }, 500);
  const off = on("hopsesh:progress", (label) => {
    const n = Object.keys(STEP).find((k) => STEP[k] === label);
    if (!n) return;
    for (const k of x.steps) { if (states[k]?.state === "running") states[k] = { state: "done" }; }
    states[n] = { state: "running" };
    paint();
  });
  try {
    const d = await api("ApplyHandoff");
    c.applying = false;
    state.stale = true;
    if (d.error) { failed(c, d); return; }
    hc = null;
    sheet.close();
    go("handedoff", d);
  } catch (err) {
    c.applying = false;
    problem(errText(err));
  } finally {
    off();
    clearInterval(poll);
  }
}

// stepBox shows the terminal step the hand-off waits for: what happens in the terminal,
// and a field for the session's link.
export function stepBox(box, st) {
  if (!st) { box.hidden = true; fill(box); return; }
  box.hidden = false;
  const input = h("input", { class: "field mono", id: "ho-link", placeholder: "https://claude.ai/code/session_…", autocomplete: "off", spellcheck: "false", style: "flex:1 1 320px" });
  const msg = h("div", { class: "err", role: "alert", style: "font-size:12.5px" });
  const use = async () => {
    if (!input.value.trim()) { msg.textContent = "Paste the session's link first."; return; }
    try { await api("HandoffPasteLink", input.value.trim()); msg.textContent = ""; } catch (e) { msg.textContent = errText(e); }
  };
  input.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); use(); } };
  const lead = st.state === "waiting" && st.where === "here"
    ? [h("b", {}, `${st.cloudTitle} is starting the session in the hopsesh Terminal window`),
      h("span", { style: "font-size:12.5px;line-height:1.5" }, "Its tab runs ", mono(`${st.driver} --cloud`), " in hopsesh's hand-off folder for this repository. If it asks whether you trust this folder, answer it there; hopsesh never answers for you. hopsesh reads that tab only for the session link."),
      h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, h("button", { class: "btn primary", onclick: () => showTerminal(st.tab) }, "Show the terminal")),
      h("span", { class: "mono muted", style: "font-size:11.5px;word-break:break-all" }, st.folder)]
    : st.state === "waiting" && st.exit != null
    ? [h("b", {}, st.exit < 0 ? "The step's tab was closed" : `The step's tab ended (exited ${st.exit})`),
      h("span", { style: "font-size:12.5px;line-height:1.5" }, "hopsesh is reading what it left. If a session started, paste its link; otherwise stop waiting, and Undo removes what the hand-off did.")]
    : st.state === "waiting"
    ? [h("b", {}, `${st.cloudTitle} is starting the session in your terminal`),
      h("span", { style: "font-size:12.5px;line-height:1.5" }, "hopsesh opened a terminal window that runs ", mono(`${st.driver} --cloud`), " in its hand-off folder for this repository. If it asks whether you trust this folder, answer it there; hopsesh picks up the session's link once it is printed."),
      h("span", { class: "mono muted", style: "font-size:11.5px;word-break:break-all" }, st.folder)]
    : st.state === "no-link"
      ? [h("b", {}, "hopsesh saw no session link"), h("span", { style: "font-size:12.5px;line-height:1.5" }, `No session started: ${st.message.replace(/\.$/, "")}. If one did, paste its link; otherwise stop, and Undo removes what the hand-off did.`)]
      : [h("b", {}, "No terminal"), h("span", { class: "err", style: "font-size:12.5px;line-height:1.5" }, st.message)];
  fill(box, h("div", { role: "group", "aria-label": st.where === "here" ? "In the hopsesh Terminal" : "In your terminal", style: "display:flex;flex-direction:column;gap:8px" }, lead,
    h("label", { for: "ho-link", style: "font-size:12.5px;font-weight:500" }, "Paste the link"),
    h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, input, h("button", { class: "btn", onclick: use }, "Use this link"),
      h("button", { class: "btn", onclick: () => api("HandoffStopWaiting").catch(fail) }, "Stop waiting")),
    msg));
}

// failed shows the step that stopped the hand-off and what had happened by then.
function failed(c, d) {
  const r = d.handoff, p = c.plan;
  const states = {};
  for (const s of r.steps) states[s.name] = { state: s.state, detail: s.detail, notDone: s.state === "todo" };
  const label = STEP[r.failed] || r.failed;
  const doUndo = async () => { if (await undo(d.journal, d.title)) { hc = null; sheet.close(); go("sessions", true); } };
  fill(sheet, h("div", { class: "sheet-in", role: "alertdialog", "aria-labelledby": "sheet-title", "aria-describedby": "ho-msg" },
    h("header", { class: "sheet-head" }, h("span", { class: "sec-h" }, "A step failed"), h("h2", { id: "sheet-title" }, `The hand-off stopped at “${label}”`),
      h("span", { class: "muted", style: "font-size:12.5px" }, `“${p.title}”${p.handoff.repo ? " · " + p.handoff.repo : ""}`)),
    h("div", { class: "sheet-body" }, h("div", { id: "ho-msg", class: "failbox" }, r.message), steps(r.steps.map((s) => s.name), states)),
    h("footer", { class: "sheet-foot" },
      r.retry === "bundle" ? h("button", { class: "btn", onclick: () => { c.opts.bundle = true; hc = c; replan(); } }, `Retry as an upload (${r.cloudTitle})`) : null,
      h("span", { class: "spacer" }),
      r.pushed ? h("button", { class: "btn", onclick: () => { hc = null; sheet.close(); go("sessions", true); } }, "Keep the branch")
        : h("button", { class: "btn", onclick: () => { hc = null; sheet.close(); go("sessions", true); } }, "Close"),
      h("button", { class: "btn primary", onclick: doUndo }, "Undo"))));
}

sheet.addEventListener("cancel", (ev) => { if (hc?.applying) ev.preventDefault(); });
sheet.addEventListener("close", () => { if (hc && !hc.applying) { hc = null; api("ClosePlan").catch(() => {}); } });
sheet.addEventListener("keydown", (ev) => {
  if (hc && ev.key === "Enter" && (ev.metaKey || ev.ctrlKey) && ev.target.tagName !== "TEXTAREA") { ev.preventDefault(); apply(); }
});

// ---- Done ----
screen("handedoff", (d) => {
  const r = d.handoff;
  const open = () => api("OpenURL", r.url).catch(fail);
  const copy = async () => { await api("CopyText", r.url); toast("Copied"); };
  const doUndo = async () => {
    let what = r.pushed ? `This deletes the branch ${r.branch}${r.markText ? " and the mark on the session here" : ""}.` : r.markText ? "This takes the mark off the session here." : "There is nothing of hopsesh's to remove here.";
    if (d.via) what = `This undoes both legs: the hand-off (${what.replace(/^This /, "").replace(/\.$/, "")}), then the copy brought here from ${d.via.fromTitle} and its worktree.`;
    const yes = await ask({ title: "Undo the hand-off?", ok: "Undo hand-off", body: `${what} ${r.manual}` });
    if (yes && await undo(d.journal, d.title)) go("sessions", true);
  };
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:760px" },
    h("div", { style: "display:flex;gap:14px;align-items:center" }, h("span", { class: "badge ok", style: "width:40px;height:40px;font-size:20px" }, "✓"),
      h("div", {}, h("h1", {}, `Handed off to ${r.cloudTitle}`),
        h("div", { class: "muted" }, cap(r.noun || "session") + " ", h("span", { class: "mono", style: "font-size:12px" }, r.session), ` is running · “${d.title}”`))),
    h("section", { class: "card" }, h("div", { class: "dlg-body" },
      h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, h("button", { class: "btn primary big", id: "ho-open", onclick: open }, "Open in browser"),
        h("button", { class: "btn big", onclick: copy }, "Copy link")),
      h("div", { class: "term" }, r.url),
      r.branch && (r.code === "branch" || r.code === "starting-diff") ? h("span", { style: "font-size:12.5px" }, "Branch ",
        r.branchUrl ? h("button", { class: "link mono", style: "font-size:12px", onclick: () => api("OpenURL", r.branchUrl).catch(fail) }, r.branch) : mono(r.branch),
        r.repo ? ` on ${r.repo}` : "") : null,
      r.envName ? h("span", { style: "font-size:12.5px" }, "Environment ", mono(r.envName)) : null)),
    h("section", { class: "card" }, h("div", { class: "dlg-body" }, h("span", { class: "sec-h" }, "What happened"),
      h("div", { class: "item" }, tick("ok"), h("span", {}, `Briefing sent · ${thousands(r.tokens)} tokens${r.masked ? `, ${count(r.masked, "secret")} masked` : ""}`)),
      r.pasted ? h("div", { class: "item" }, tick("ok"), h("span", {}, "The session's link is the one you pasted")) : null,
      r.pushed ? h("div", { class: "item" }, tick("ok"), h("span", {}, "Branch pushed")) : r.code === "bundle" ? h("div", { class: "item" }, tick("ok"), h("span", {}, "Uploaded by the agent; nothing pushed"))
        : r.code === "starting-diff" ? h("div", { class: "item" }, tick("ok"), h("span", {}, "The changes went with it as a starting diff, on ", mono(r.branch), "; nothing pushed")) : null,
      (r.stayed || []).length ? h("div", { class: "item" }, h("span", { class: "badge warn" }, "•"), h("span", {}, `Stayed on ${sys.here}: `, r.stayed.map((s, i) => [i ? ", " : "", s]))) : null,
      d.via ? h("div", { class: "item", id: "ho-via" }, tick("ok"), h("span", {}, `Brought here from ${d.via.fromTitle} first: `, mono(d.via.key || ""), d.via.worktree ? [" in ", mono(d.via.worktree)] : null)) : null,
      r.markText ? h("div", { class: "item" }, tick("ok"), h("span", {}, `The session here is marked “${markWords(r.markText)}”`)) : null,
      (d.warnings || []).map((w) => h("div", { class: "item" }, tick("warn"), h("span", {}, cap(w)))))),
    h("div", { class: "hint", role: "note" }, r.hint),
    h("div", { style: "display:flex;gap:10px;align-items:center;flex-wrap:wrap" },
      h("button", { class: "btn", onclick: doUndo }, "Undo"),
      h("button", { class: "btn", onclick: () => go("sessions", true) }, "Back to sessions", h("span", { class: "kbd" }, "esc"))),
    r.noFollowUp ? h("span", { id: "ho-nofollow", class: "muted", style: "font-size:12px" }, r.noFollowUp) : null)));
  view.querySelector("#ho-open")?.focus();
});

document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && current === "handedoff" && !document.querySelector("dialog[open]")) go("sessions", true);
});
