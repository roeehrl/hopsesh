// hopsesh desktop frontend. Plain ES modules, no build step. All data from transcripts is
// untrusted: it is only ever inserted with textContent (see h()).
import { Call } from "/wails/runtime.js";
import { showSettings } from "./settings.js";

const SVC = "github.com/roeehrl/hopsesh/internal/gui.App.";
export const api = (method, ...args) => Call.ByName(SVC + method, ...args);

const $ = (sel) => document.querySelector(sel);
export const view = $("#view");

// h(tag, attrs, ...children): build DOM without innerHTML.
export function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "style") el.setAttribute("style", v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat()) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

export function toast(msg) {
  const t = $("#toast");
  t.textContent = msg;
  t.classList.add("show");
  setTimeout(() => t.classList.remove("show"), 2200);
}

function ago(iso) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 60) return "just now";
  if (d < 3600) return `${Math.floor(d / 60)} min ago`;
  if (d < 86400) return `${Math.floor(d / 3600)} h ago`;
  if (d < 7 * 86400) return `${Math.floor(d / 86400)} d ago`;
  return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}

const state = { scan: null, machine: "", filter: "", info: null, sel: null, opts: {}, plan: null };

export function setTitlebar(mode) {
  $("#q").hidden = mode !== "sessions";
  $("#btn-refresh").hidden = mode !== "sessions";
  $("#btn-settings").hidden = mode === "settings";
  $("#btn-settings").onclick = () => showSettings();
  $("#btn-machines").textContent = mode === "machines" ? "Sessions" : "Machines";
  $("#btn-machines").onclick = mode === "machines" ? () => showSessions() : () => showMachines();
}

function loading(text) {
  view.replaceChildren(h("div", { class: "loading" }, text));
}

// ---------- machines (consent) ----------
async function showMachines() {
  setTitlebar("machines");
  loading("Looking for your machines (nothing is contacted yet)…");
  const hosts = await api("Discover");
  const rows = hosts.map((m) => {
    const cb = h("input", { type: "checkbox", "aria-label": `Allow ${m.name}`, checked: m.allowed,
      onchange: async (e) => { await api("SetAllowed", m.name, m.destination, e.target.checked); } });
    const online = m.online === null || m.online === undefined ? "" : m.online ? "online" : "offline";
    return h("div", { class: "mrow" },
      cb,
      h("div", {}, h("div", { style: "font-weight:500" }, m.name), h("div", { class: "mono muted", style: "font-size:11px" }, m.destination + (m.os ? ` · ${m.os}` : ""))),
      h("span", {}, (m.via || []).join(" + ")),
      h("div", { style: "display:flex;gap:8px;align-items:center;flex-wrap:wrap" },
        h("span", { class: "muted", style: "font-size:12px" }, online + (m.otherOwner ? ` · shared by ${m.owner}` : "")),
        h("button", { class: "btn", onclick: () => trustDialog(m.name) }, "Check host key")));
  });
  const add = h("button", { class: "btn", onclick: addDialog }, "Add by address…");
  view.replaceChildren(h("div", { class: "machines" },
    h("div", { style: "max-width:820px;color:var(--ink2)" },
      "hopsesh found these machines. It connects only to the ones you turn on, using your own ",
      h("span", { class: "mono" }, "ssh"), " and keys, and only reads ", h("span", { class: "mono" }, "~/.claude"),
      " until you choose to move a session. Machines owned by someone else stay off unless you turn them on."),
    h("div", { class: "card" }, h("div", { class: "mrow h" }, h("span", {}, "Allow"), h("span", {}, "Machine"), h("span", {}, "Found via"), h("span", {}, "Status")), ...rows),
    h("div", { style: "display:flex;gap:12px" }, h("span", { class: "spacer" }), add, h("button", { class: "btn primary", onclick: () => showSessions(true) }, "Scan allowed machines"))));
}

async function trustDialog(name) {
  const dlg = $("#dlg"), body = $("#dlg-body");
  body.replaceChildren(h("div", {}, `Fetching ${name}'s host keys…`));
  dlg.showModal();
  try {
    const k = await api("ScanKeys", name);
    body.replaceChildren(
      h("div", { style: "font-weight:600;font-size:14px" }, `Host keys of ${name}`),
      h("div", { class: "mono muted", style: "font-size:11px" }, k.address),
      ...k.fingerprints.map((f) => h("div", { class: "mono", style: "font-size:12px" }, f)),
      k.verified ? h("div", { class: "ok" }, "✓ " + k.verified)
        : h("div", { class: "warn" }, "Not independently verified. Compare with ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub on that machine."),
      h("div", { style: "display:flex;gap:8px;justify-content:flex-end" },
        h("button", { class: "btn", value: "cancel" }, "Cancel"),
        h("button", { class: "btn primary", onclick: async (e) => { e.preventDefault(); await api("TrustHost", name); dlg.close(); toast(`${name} trusted`); } }, "Trust these keys")));
  } catch (e) {
    body.replaceChildren(h("div", { class: "err" }, String(e.message || e)), h("button", { class: "btn", value: "close" }, "Close"));
  }
}

function addDialog() {
  const dlg = $("#dlg"), body = $("#dlg-body");
  const name = h("input", { class: "field", placeholder: "name, e.g. build-box", "aria-label": "Name" });
  const dest = h("input", { class: "field mono", placeholder: "ssh destination, e.g. me@10.0.0.5 or an ssh alias", "aria-label": "SSH destination" });
  body.replaceChildren(h("div", { style: "font-weight:600" }, "Add a machine"), name, dest,
    h("div", { style: "display:flex;gap:8px;justify-content:flex-end" }, h("button", { class: "btn", value: "cancel" }, "Cancel"),
      h("button", { class: "btn primary", onclick: async (e) => { e.preventDefault(); if (!name.value || !dest.value) return; await api("AddHost", name.value, dest.value); dlg.close(); showMachines(); } }, "Add and allow")));
  dlg.showModal();
}

// ---------- sessions ----------
async function showSessions(rescan = false) {
  setTitlebar("sessions");
  if (!state.scan || rescan) {
    loading("Scanning this machine and your allowed machines…");
    if (state.info?.localNetwork?.gated && state.info.localNetwork.firstRun) {
      // Explain the macOS prompt before it appears (it names hopsesh but not the reason).
      view.firstChild?.append(h("div", { class: "muted", style: "font-size:12px;max-width:520px;margin-top:10px;line-height:1.5" },
        "macOS may ask whether hopsesh can find devices on your local network. Choose Allow to reach machines on this network. Machines reached over Tailscale work either way."));
    }
    try { state.scan = await api("Scan"); } catch (e) { view.replaceChildren(h("div", { class: "loading err" }, String(e.message || e))); return; }
  }
  renderSessions();
}

function machineDot(m) {
  if (m.status === "ok" || m.status === "local") return "ok";
  if (m.status === "host-key" || m.status === "tailscale-check" || m.status === "local-network") return "warn";
  return "err";
}

function renderSessions() {
  const s = state.scan;
  const side = h("div", { class: "sidebar" }, h("div", { class: "side-h" }, "Machines"),
    h("button", { class: "mach" + (state.machine === "" ? " sel" : ""), onclick: () => { state.machine = ""; renderSessions(); } },
      h("span", { class: "dot ok" }), h("span", {}, h("span", { style: "font-weight:500" }, "All machines"), h("small", {}, `${s.total} sessions · ${s.groups.length} groups`))),
    ...s.machines.map((m) => h("button", { class: "mach" + (state.machine === m.name ? " sel" : ""), onclick: () => { state.machine = m.name; renderSessions(); } },
      h("span", { class: "dot " + machineDot(m) }),
      h("span", {}, h("span", { style: "font-weight:500" }, m.name + (m.local ? " (this one)" : "")),
        h("small", { class: machineDot(m) === "ok" ? "" : machineDot(m) }, machineDot(m) === "ok" ? `${m.os || ""} · ${m.sessions} sessions` : (m.hint || m.status))))),
    h("span", { class: "spacer" }),
    h("div", { class: "muted", style: "font-size:11px;padding:10px 8px;border-top:1px solid var(--line)" }, "Repos folder: ", h("span", { class: "mono" }, state.info?.reposDir || "")));
  const f = state.filter.toLowerCase();
  const cards = [];
  for (const g of s.groups) {
    const entries = g.entries.filter((e) => (!state.machine || e.machine === state.machine) &&
      (!f || [e.title, e.lastPrompt, e.cwd, g.name, e.machine].join(" ").toLowerCase().includes(f)));
    if (!entries.length) continue;
    const where = g.noRepo ? h("span", { class: "tag" }, "Sessions started outside a git checkout")
      : g.local ? h("span", { class: "ok", style: "font-size:11.5px" }, "cloned here · " + g.local)
      : g.remote ? h("span", { class: "warn", style: "font-size:11.5px" }, "not on this machine · clone needed") : null;
    cards.push(h("div", { class: "card" },
      h("div", { class: "card-h" }, h("span", { class: "name" }, g.name), g.remote ? h("span", { class: "mono", style: "font-size:11.5px;color:var(--ink2)" }, g.remote) : null, h("span", { class: "spacer" }), where),
      ...entries.map((e) => sessionRow(e))));
  }
  const blocked = s.machines.filter((m) => m.status === "local-network");
  if (blocked.length) cards.unshift(localNetworkBanner(blocked));
  const skillOffer = skillBanner();
  if (skillOffer) cards.unshift(skillOffer);
  const content = h("div", { class: "content" },
    h("div", { class: "toolbar" }, h("span", { class: "muted", style: "font-size:12px" }, "Read-only scan · grouped by repository"), h("span", { class: "spacer" })),
    h("div", { class: "list" }, cards.length ? cards : h("div", { class: "loading" }, "No sessions match.")),
    h("div", { class: "status" }, h("span", {}, `${s.machines.filter((m) => machineDot(m) === "ok").length} of ${s.machines.length} machines reachable`), h("span", { class: "spacer" }), updateNote(), h("span", {}, "Nothing leaves your machines · audit log on")));
  view.replaceChildren(side, content);
}

// localNetworkBanner explains a macOS Local Network block. macOS has no API that says
// whether the person declined or simply has not answered yet, so the text covers both.
function localNetworkBanner(machines) {
  const names = machines.map((m) => m.name).join(", ");
  return h("div", { class: "card banner warn-card", role: "status" },
    h("div", { style: "font-weight:600" }, `macOS is blocking hopsesh from ${names}`),
    h("div", { style: "font-size:12.5px;line-height:1.5" },
      "These machines are on your local network, and macOS asks before an app may reach devices there. ",
      "If a macOS prompt is open, choose Allow. If you chose Don't Allow before, open Privacy & Security, scroll to Local Network, turn hopsesh on, then try again."),
    h("div", { class: "muted", style: "font-size:12px" }, "Machines reached over Tailscale are not affected. Adding a machine by its Tailscale name also avoids this."),
    h("div", { style: "display:flex;gap:8px" },
      h("button", { class: "btn primary", onclick: () => api("OpenLocalNetworkSettings").catch(() => {}) }, "Open Privacy & Security"),
      h("button", { class: "btn", onclick: async () => { await api("RetryLocalNetwork"); state.info = await api("Info"); showSessions(true); } }, "Try again")));
}

// skillBanner offers the Claude Code skill once (until installed or dismissed), and an
// update when the installed skill is out of date.
function skillBanner() {
  const st = state.info?.skillState, prompt = state.info?.skillPrompt;
  const refresh = async () => { state.info = await api("Info"); renderSessions(); };
  if (st === "absent" && prompt !== "declined") {
    return h("div", { class: "card banner", role: "status" },
      h("div", { style: "font-weight:600" }, "Let Claude Code use hopsesh"),
      h("div", { style: "font-size:12.5px;line-height:1.5" }, "Ask Claude \u201cbring my laptop session here\u201d or \u201cwhat's running on the mini?\u201d. A small skill teaches Claude to use hopsesh: it shows you the plan and moves only after you say yes."),
      h("div", { style: "display:flex;gap:8px" },
        h("button", { class: "btn primary", onclick: async () => { try { await api("InstallSkill", false, false); toast("Installed the hopsesh skill"); } catch (e) { toast(String(e.message || e)); } refresh(); } }, "Install the skill"),
        h("button", { class: "btn", onclick: () => showSettings() }, "Options…"),
        h("button", { class: "btn", onclick: async () => { await api("DismissSkillOffer"); refresh(); } }, "Not now")));
  }
  if (state.info?.cliOffer) {
    return h("div", { class: "card banner", role: "status" },
      h("div", { style: "font-weight:600" }, "Use hopsesh in Terminal too"),
      h("div", { style: "font-size:12.5px;line-height:1.5" }, "Link the hopsesh command into ~/.local/bin (no password needed). It always runs this app's version."),
      h("div", { style: "display:flex;gap:8px" },
        h("button", { class: "btn primary", onclick: async () => { try { await api("InstallCLI", false); toast("hopsesh is ready in Terminal"); } catch (e) { toast(String(e.message || e)); } refresh(); } }, "Install command"),
        h("button", { class: "btn", onclick: async () => { await api("DismissCLIOffer"); refresh(); } }, "Not now")));
  }
  if (st === "stale" || st === "broken") {
    return h("div", { class: "card banner", role: "status" },
      h("div", { style: "font-weight:600" }, st === "stale" ? "The hopsesh skill for Claude Code is out of date" : "The hopsesh skill for Claude Code is damaged"),
      h("div", { style: "display:flex;gap:8px" },
        h("button", { class: "btn primary", onclick: async () => { try { await api("InstallSkill", false, false); toast("Updated the hopsesh skill"); } catch (e) { toast(String(e.message || e)); } refresh(); } }, st === "stale" ? "Update it" : "Repair it"),
        h("button", { class: "btn", onclick: () => showSettings() }, "Settings…")));
  }
  return null;
}

function sessionRow(e) {
  const bits = [];
  if (e.branch) bits.push(e.worktree ? `${e.branch} (${e.worktree})` : e.branch);
  if (e.worktree && e.mainBranch) bits.push(`main folder on ${e.mainBranch}`);
  if (e.unpushed) bits.push(`+${e.unpushed} unpushed`);
  if (e.dirty) bits.push(`${e.dirty} uncommitted`);
  const others = (e.copies || []).filter((c) => !c.newest);
  if (others.length) bits.push("also on " + others.map((c) => c.machine + (c.local ? " (here)" : "") + (c.movedTo ? `, moved to ${c.movedTo}` : ", older")).join("; "));
  const action = e.hereNewest
    ? h("button", { class: "btn outline", style: "justify-self:end", title: "The newest copy is already on this machine", onclick: async () => { try { await api("OpenInTerminal", e.resumeCommand); } catch (err) { toast(String(err.message || err)); } } }, "Resume")
    : h("button", { class: "btn outline", style: "justify-self:end", title: e.staleHere ? "An older copy is on this machine; bring the newest one back" : "", onclick: () => preflight(e) }, e.staleHere ? "Hop back" : "Hop here");
  return h("div", { class: "row" },
    h("div", {}, h("div", { class: "t", title: e.title }, e.title), h("div", { class: "s" }, bits.join(" · ") || " ")),
    h("div", { style: "font-size:12px;min-width:0" }, h("div", {}, e.machine), h("div", { class: "mono s", title: e.cwd }, e.cwd)),
    h("div", { class: "prompt", title: e.lastPrompt }, e.lastPrompt ? `“${e.lastPrompt}”` : ""),
    h("div", { style: "font-size:12px" }, h("span", { class: "pill" + (e.live ? " live" : "") }, e.status), h("div", { class: "s" }, ago(e.lastActive))),
    action);
}

// ---------- preflight ----------
async function preflight(e, keepOpts = false) {
  state.sel = e;
  const dflt = state.info?.defaults || { markMoved: true, syncCode: true, pushSource: false };
  if (!keepOpts) state.opts = { worktree: "auto", remoteControl: false, notifyOld: false, fork: false, redact: false, clone: false, otherAccount: false, memory: false, targetDir: "",
    markSource: dflt.markMoved, syncCode: dflt.syncCode, pushSource: dflt.pushSource, stopLocal: false };
  setTitlebar("plan");
  loading("Working out the plan…");
  try { state.plan = await api("Plan", e.machine, e.id, state.opts); } catch (err) { view.replaceChildren(h("div", { class: "loading err" }, String(err.message || err))); return; }
  renderPlan();
}

function renderPlan() {
  const { plan: p, kinds } = state.plan;
  const r = p.repo, o = state.opts, e = state.sel;
  const replan = () => preflight(e, true);
  const opt = (key, title, desc) => h("label", { class: "opt" },
    h("input", { type: "checkbox", checked: o[key], onchange: (ev) => { o[key] = ev.target.checked; replan(); } }), h("span", {}, h("b", {}, title), h("span", { class: "muted" }, desc)));

  const repoItems = [];
  if (r.action === "use") repoItems.push(item("ok", `Found on this machine at ${r.localPath}` + (r.localBranch ? ` (on ${r.localBranch})` : ""), `Matched by remote ${r.identity}.`));
  if (r.action === "clone") repoItems.push(item("ok", `Will clone into ${r.localPath}`, `From ${r.remote}.`));
  if (r.action === "needs-clone") {
    const dest = h("input", { class: "field mono", style: "flex:1", value: r.localPath, "aria-label": "Clone into" });
    repoItems.push(item("warn", "Not found on this machine", `Matched by remote ${r.identity}. Searched your repos folder and the usual places.`));
    repoItems.push(h("div", { style: "display:flex;gap:8px;align-items:center;padding-left:28px" }, h("span", { class: "muted", style: "font-size:12px" }, "Clone into"), dest,
      h("button", { class: "btn primary", onclick: () => { o.clone = true; o.reposDir = dest.value.replace(/\/[^/]+\/?$/, ""); replan(); } }, "Clone for me"),
      h("button", { class: "btn", onclick: async () => { const d = await api("ChooseFolder", "Where is your checkout?"); if (d) { o.targetDir = d; replan(); } } }, "I already have it…")));
  }
  if (r.action === "dir") repoItems.push(item("ok", `Resume in ${r.localPath}`, "Chosen by you."));
  if (r.action === "none") repoItems.push(item("warn", "Not a git repository", "hopsesh can't match or clone it; choose a folder."));
  if (r.sourceBranch) {
    const where = r.sourceClaudeWorktree ? "a Claude worktree" : r.sourceInWorktree ? "a git worktree" : "the main folder";
    repoItems.push(item(r.sourceInWorktree ? "warn" : "ok", `Branch ${r.sourceBranch} — the session ran in ${where} on ${p.sourceHost}` + (r.sourceInWorktree && r.sourceMainBranch ? ` (its main folder is on ${r.sourceMainBranch})` : ""),
      r.worktree ? `A matching worktree will be created here: ${r.worktree}` : (r.sourceInWorktree ? "It will resume in the main checkout here." : "")));
    repoItems.push(h("div", { style: "display:flex;gap:8px;align-items:center;padding-left:28px" }, h("span", { class: "muted", style: "font-size:12px" }, "Worktree"),
      h("select", { "aria-label": "Worktree mode", onchange: (ev) => { o.worktree = ev.target.value; replan(); } },
        ...[["auto", "Recreate it if the session used one"], ["create", "Always use a worktree on this branch"], ["main", "Use the main checkout"]].map(([v, t]) => h("option", { value: v, selected: o.worktree === v }, t)))));
  }
  if (r.unpushed || r.dirty) {
    const parts = [];
    if (r.unpushed) parts.push(p.syncFromSource || p.push ? `${r.unpushed} unpushed commit(s) come along (${p.push ? "pushed first" : "fetched straight from " + p.sourceHost})` : `${r.unpushed} unpushed commit(s) won't be here`);
    if (r.dirty) parts.push(`${r.dirty} uncommitted file(s) stay on ${p.sourceHost} (commit them there to bring them)`);
    repoItems.push(item(r.dirty || !(p.syncFromSource || p.push) ? "warn" : "ok", "Work on " + p.sourceHost, parts.join("; ") + "."));
  }
  if (p.sync) repoItems.push(item("ok", "Code", p.sync[0].toUpperCase() + p.sync.slice(1) + "."));
  if (p.stopPid) repoItems.push(item("warn", `The copy running here (pid ${p.stopPid}) will be quit first`, "It gets the normal quit signal and saves its transcript before the newer copy replaces it."));

  const warnings = (p.warnings || []).filter((w) => !w.includes("unpushed") && !w.includes("worktree"));
  const main = h("div", { class: "pre-main" },
    h("div", { class: "fromto" },
      h("div", { class: "box" }, h("small", {}, "From"), h("span", { style: "font-weight:500" }, `${p.sourceHost}${p.sourceOs ? " (" + p.sourceOs + ")" : ""}`), h("span", { class: "mono", style: "font-size:11px" }, p.sourceCwd)),
      h("span", { style: "color:var(--accent);font-size:18px" }, "→"),
      h("div", { class: "box to" }, h("small", {}, "To"), h("span", { style: "font-weight:500" }, "this machine"), h("span", { class: "mono", style: "font-size:11px" }, p.targetCwd))),
    h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Repository")), h("div", { class: "sec" }, ...repoItems)),
    h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Path rewrite")),
      h("div", { class: "sec" }, h("div", { class: "maprow" }, ...p.mappings.flatMap((m) => [h("span", {}, m.from), h("span", { style: "color:var(--accent)" }, "→"), h("span", {}, m.to)])),
        h("div", { class: "muted", style: "font-size:11.5px" }, "Signed reasoning blocks, message ids and the session id are never changed."))),
    warnings.length || (p.blockers || []).length ? h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Check before hopping")),
      h("div", { class: "sec" }, ...warnings.map((w) => item("warn", w, "")), ...(p.blockers || []).map((b) => b.includes("running on this machine")
        ? h("div", {}, item("err", b, ""), h("div", { style: "padding-left:28px" }, h("button", { class: "btn primary", onclick: () => { o.stopLocal = true; replan(); } }, "Quit it and continue")))
        : p.conflict && b.startsWith(p.conflict)
        ? h("div", {}, item("err", "Both copies changed: " + p.conflict, "Nothing is merged. Pick which to keep."),
            h("div", { style: "display:flex;gap:8px;padding-left:28px" },
              h("button", { class: "btn", onclick: () => { o.conflict = "keep-both"; replan(); } }, "Keep both (bring this one in separately)"),
              h("button", { class: "btn", onclick: () => { o.conflict = "replace"; replan(); } }, "Replace the copy here"),
              h("button", { class: "btn", onclick: () => showSessions() }, "Keep the copy here")))
        : item("err", b, ""))))
      : null);

  const blocked = (p.blockers || []).length > 0;
  const side = h("div", { class: "pre-side" },
    h("div", { style: "font-weight:600" }, "What gets moved"),
    ...kinds.map((k) => h("div", { class: "kv" }, h("span", {}, k))),
    h("div", { class: "kv muted" }, h("span", {}, `Total ${fmtBytes(p.totalBytes)}`), h("span", {}, "logins, keys, live sockets: never")),
    h("div", { style: "height:1px;background:var(--line)" }),
    h("div", { style: "font-weight:600" }, "After the move"),
    opt("remoteControl", "Turn on Remote Control", `Reach the new session from your phone or other machines, as ${p.newName}.`),
    opt("notifyOld", "Tell the old session it moved", "The new session's first message asks Claude to notify it (needs Remote Control on both)."),
    p.live ? opt("fork", "Keep the old session running", "Fork instead of handing off: both copies continue.") : null,
    p.mark !== "off" || !o.markSource ? opt("markSource", `Mark the copy on ${p.sourceHost} as moved`,
      p.mark === "when-stopped" ? `It is still running; once it stops, its title becomes “↪ moved to ${state.info?.host || "here"} · …” so it isn't resumed by mistake.`
        : `Its title becomes “↪ moved to ${state.info?.host || "here"} · ${p.title}”, so Claude Code's resume list there shows it moved.`) : null,
    r.sourceHead ? opt("syncCode", "Bring the code here to the session's commit", "Fetches if needed; fast-forwards only a clean checkout on the same branch. Never merges.") : null,
    r.unpushed && r.sourceUpstream ? opt("pushSource", `Push them first (${r.unpushed} commit(s) on ${p.sourceHost})`, `Runs git push on ${p.sourceHost} with its own credentials before copying.`) : null,
    opt("redact", "Redact likely secrets", "In this copy only."),
    opt("otherAccount", "This machine uses a different Anthropic account", "Drops signed reasoning blocks, which only work under the original account."),
    h("span", { class: "spacer" }),
    h("div", { class: "muted", style: "font-size:11.5px" }, "The new session starts with a message explaining the move and asking Claude to check that nothing is missing."),
    h("button", { class: "btn primary big", disabled: blocked, onclick: doApply }, r.action === "clone" ? "Clone and hop" : "Hop here"),
    h("button", { class: "btn", onclick: () => showSessions() }, "Back"),
    h("div", { class: "muted", style: "font-size:11px;text-align:center" }, `The original on ${p.sourceHost} is not deleted.`));
  view.replaceChildren(h("div", { class: "pre" }, main, side));
}

// roundTripCard reports what happened around the move: quitting the copy here, pushing on
// the source, bringing the code here, and marking the copy left behind.
function roundTripCard(d) {
  const rows = [];
  if (d.stoppedPid) rows.push(item("ok", `Quit the copy that was running here (pid ${d.stoppedPid})`, ""));
  if (d.pushError) rows.push(item("warn", `Could not push on ${d.sourceHost}`, d.pushError));
  else if (d.pushed) rows.push(item("ok", `Pushed the session's branch on ${d.sourceHost}`, ""));
  if (d.syncNote) {
    const ok = ["up-to-date", "fast-forwarded", "ahead"].includes(d.syncState);
    rows.push(item(ok ? "ok" : "warn", "Code: " + d.syncNote, ""));
  }
  if (d.mark === "done") rows.push(item("ok", `The copy on ${d.sourceHost} is now titled “${d.markedTitle}”`, "Claude Code's resume list there shows that it moved."));
  if (d.mark === "pending") rows.push(item("ok", `The copy on ${d.sourceHost} is still running`, "It will be marked as moved once it stops (hopsesh checks on its next scan)."));
  if (d.mark === "failed") rows.push(item("warn", `Could not mark the copy on ${d.sourceHost} as moved`, d.markError || ""));
  if (!rows.length) return null;
  return h("div", { class: "card" }, h("div", { class: "sec" }, ...rows));
}

function item(kind, title, desc) {
  return h("div", { class: "item" }, h("span", { class: "badge " + kind }, kind === "ok" ? "✓" : kind === "err" ? "✕" : "!"),
    h("div", {}, h("div", { style: "font-weight:500" }, title), desc ? h("div", { class: "muted", style: "font-size:12px" }, desc) : null));
}

function fmtBytes(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return Math.round(n / 1024) + " KB";
  return n + " B";
}

async function doApply() {
  loading("Copying, rewriting and verifying…");
  try {
    const d = await api("Apply");
    renderDone(d);
  } catch (e) {
    view.replaceChildren(h("div", { class: "loading" }, h("div", {}, h("div", { class: "err", style: "margin-bottom:12px" }, String(e.message || e)), h("button", { class: "btn", onclick: () => renderPlan() }, "Back to the plan"))));
  }
}

function renderDone(d) {
  setTitlebar("done");
  const facts = [`${d.paths} paths rewritten`, `${d.files} file(s), ${d.bytes}`];
  if (d.cloned) facts.unshift("repository cloned");
  if (d.worktree) facts.push("worktree created");
  if (d.secrets) facts.push(`${d.secrets} likely secret(s)`);
  view.replaceChildren(h("div", { class: "center" }, h("div", { class: "done" },
    h("div", { style: "display:flex;gap:14px;align-items:center" }, h("span", { class: "badge ok", style: "width:40px;height:40px;font-size:20px" }, "✓"),
      h("div", {}, h("div", { style: "font-size:20px;font-weight:600" }, `“${d.title}” is on this machine`), h("div", { class: "muted" }, facts.join(" · ")))),
    h("div", { class: "card" }, h("div", { class: "sec" }, h("div", { style: "font-weight:600" }, "Start it"),
      h("div", { style: "display:flex;gap:8px" }, h("div", { class: "term" }, d.command), h("button", { class: "btn", onclick: async () => { await api("CopyText", d.command); toast("Copied"); } }, "Copy")),
      h("div", { style: "display:flex;gap:8px" }, h("button", { class: "btn primary", onclick: async () => { try { await api("OpenInTerminal", d.command); } catch (e) { toast(String(e.message || e)); } } }, "Open in Terminal"),
        d.desktop ? h("button", { class: "btn", onclick: async () => { try { await api("OpenInDesktop"); } catch (e) { toast(String(e.message || e)); } } }, "Open in Claude desktop") : null),
      h("div", { class: "muted", style: "font-size:12px" }, "Its first message tells Claude it was moved and asks it to check the repository, files, tools and environment before continuing."))),
    roundTripCard(d),
    d.notifyOld && !d.remoteControl ? h("div", { class: "card" }, h("div", { class: "sec" }, h("div", { style: "font-weight:600" }, `Tell the session on ${d.sourceHost}`),
      h("div", { class: "muted", style: "font-size:12px" }, "Automatic notification needs Remote Control on both sessions. Paste this into the old session instead:"),
      h("div", { style: "display:flex;gap:8px" }, h("div", { class: "term" }, d.oldNotice), h("button", { class: "btn", onclick: async () => { await api("CopyText", d.oldNotice); toast("Copied"); } }, "Copy")))) : null,
    h("div", { style: "display:flex;gap:16px;font-size:12px" },
      h("button", { class: "btn", onclick: async () => { await api("Undo", d.sessionId); toast("Undone"); showSessions(true); } }, "Undo this hop"),
      h("button", { class: "btn", onclick: () => showSessions(true) }, "Back to sessions"),
      h("span", { class: "muted", style: "align-self:center" }, "Audit log: ", h("span", { class: "mono" }, d.auditDir))))));
}

// ---------- boot ----------
$("#btn-refresh").onclick = () => showSessions(true);
$("#q").addEventListener("input", (e) => { state.filter = e.target.value; if (state.scan) renderSessions(); });
(async () => {
  state.info = await api("Info");
  $("#subtitle").textContent = `Sessions on your machines · this is ${state.info.host}`;
  if (state.info.hasHosts) showSessions(); else showMachines();
  if (state.info.updateCheck === "on") {
    try { state.update = await api("CheckUpdate"); if (state.update?.newer && state.scan) renderSessions(); } catch { /* offline */ }
  }
})();

// updateNote is the status-bar item about new releases: a one-time question, then a
// quiet link when a newer release exists. Nothing is contacted until the person says yes.
function updateNote() {
  if (state.info?.updateCheck === "") {
    const answer = async (on) => { await api("SetUpdateCheck", on); state.info = await api("Info"); if (on) state.update = await api("CheckUpdate").catch(() => null); renderSessions(); };
    return h("span", {}, "Check GitHub once a day for new versions? ",
      h("button", { class: "link", onclick: () => answer(true) }, "Yes"), " · ",
      h("button", { class: "link", onclick: () => answer(false) }, "No"));
  }
  if (state.update?.newer) {
    return h("button", { class: "link", onclick: () => api("OpenURL", state.update.url).catch(() => {}) }, `hopsesh ${state.update.latest} is available`);
  }
  return null;
}
