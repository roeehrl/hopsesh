// hopsesh desktop frontend. Plain ES modules, no build step. All data from transcripts is
// untrusted: it is only ever inserted with textContent (see h()).
import { Call, Events } from "/wails/runtime.js";
import { showSettings } from "./settings.js";

const SVC = "github.com/roeehrl/hopsesh/internal/ui/gui.App.";
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

// fill(el, ...children) replaces el's children, skipping the empty ones (as h() does).
function fill(el, ...kids) {
  el.replaceChildren(...kids.flat().filter((k) => k !== null && k !== undefined && k !== false));
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

const state = { scan: null, machine: "", filter: "", info: null, sel: null, target: "", sendTo: "", opts: {}, plan: null };

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
        h("button", { class: "btn", onclick: () => trustDialog(m.name) }, "Check host key"),
        h("button", { class: "btn", title: "How hopsesh logs in to this machine", onclick: () => loginDialog(m) },
          m.auth === "password" ? (m.keychain ? "Login: password (Keychain)" : "Login: password") : "Login: key")));
  });
  const add = h("button", { class: "btn", onclick: addDialog }, "Add by address…");
  view.replaceChildren(h("div", { class: "machines" },
    h("div", { style: "max-width:820px;color:var(--ink2)" },
      "hopsesh found these machines. It connects only to the ones you turn on, using your own ",
      h("span", { class: "mono" }, "ssh"), " and keys, and only reads your coding agents' session folders",
      " until you choose to move a session. Machines owned by someone else stay off unless you turn them on."),
    h("div", { class: "card" }, h("div", { class: "mrow h" }, h("span", {}, "Allow"), h("span", {}, "Machine"), h("span", {}, "Found via"), h("span", {}, "Status")),
      ...(rows.length ? rows : [h("div", { class: "muted", style: "padding:14px 16px;font-size:12.5px" },
        "No other machines found in Tailscale or ~/.ssh/config. Add one by its address, or scan just this machine (sessions can also move between agents here).")])),
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
  const pw = h("input", { type: "checkbox" });
  const remember = h("input", { type: "checkbox", checked: true });
  const rememberRow = h("label", { class: "opt", hidden: true }, remember, " Remember the password in the Keychain");
  pw.onchange = () => { rememberRow.hidden = !pw.checked; };
  body.replaceChildren(h("div", { style: "font-weight:600" }, "Add a machine"), name, dest,
    h("label", { class: "opt" }, pw, " This machine logs in with a password (hopsesh asks for it when it connects)"),
    rememberRow,
    h("div", { style: "display:flex;gap:8px;justify-content:flex-end" }, h("button", { class: "btn", value: "cancel" }, "Cancel"),
      h("button", { class: "btn primary", onclick: async (e) => { e.preventDefault(); if (!name.value || !dest.value) return; await api("AddHost", name.value, dest.value, pw.checked, remember.checked); dlg.close(); showMachines(); } }, "Add and allow")));
  dlg.showModal();
}

// loginDialog chooses how hopsesh logs in to a machine: keys (the default) or a password,
// and offers to switch a password machine to key login for good.
function loginDialog(m) {
  const dlg = $("#dlg"), body = $("#dlg-body");
  const key = h("input", { type: "radio", name: "auth", checked: m.auth !== "password" });
  const pass = h("input", { type: "radio", name: "auth", checked: m.auth === "password" });
  const remember = h("input", { type: "checkbox", checked: m.auth === "password" ? m.keychain : m.canRemember });
  const rememberRow = h("label", { class: "opt", style: "margin-left:22px", hidden: m.auth !== "password" || !m.canRemember }, remember, " Remember it in the Keychain");
  const sync = () => { rememberRow.hidden = !pass.checked || !m.canRemember; };
  key.onchange = sync; pass.onchange = sync;
  const msg = h("div", { class: "muted", style: "font-size:12px;line-height:1.5" });
  const runSetup = async (createKey) => {
    msg.className = "muted"; msg.replaceChildren(`Logging in to ${m.name} once with its password to add this Mac's SSH key…`);
    try {
      const r = await api("SetupKeyLogin", m.name, createKey);
      dlg.close();
      toast(r.created ? `Created ${r.publicKey}; ${m.name} now logs in with it` : `${m.name} now logs in with your key`);
      showMachines();
    } catch (err) {
      const text = String(err.message || err);
      if (text.includes("no-key")) {
        msg.className = "warn";
        msg.replaceChildren("This Mac has no SSH key that ssh would use for this machine. ",
          h("button", { class: "btn", onclick: (e) => { e.preventDefault(); runSetup(true); } }, "Create ~/.ssh/id_ed25519 and continue"));
      } else { msg.className = "err"; msg.replaceChildren(text); }
    }
  };
  const setupKey = m.auth === "password" ? h("button", { class: "btn", onclick: (e) => { e.preventDefault(); runSetup(false); } }, "Set up key login") : null;
  fill(body,
    h("div", { style: "font-weight:600" }, `How hopsesh logs in to ${m.name}`),
    h("label", { class: "opt" }, key, " SSH key or agent (recommended)"),
    h("label", { class: "opt" }, pass, " Password: hopsesh asks for it when it connects"),
    rememberRow,
    m.auth === "password" ? h("div", { class: "muted", style: "font-size:12px;line-height:1.5" },
      "Set up key login adds this Mac's public SSH key to the machine (one password login), checks that it works, and then stops using the password. macOS and Linux machines.") : null,
    msg,
    h("div", { style: "display:flex;gap:8px;justify-content:flex-end;flex-wrap:wrap" },
      m.auth === "password" && m.keychain ? h("button", { class: "btn", onclick: async (e) => { e.preventDefault(); await api("ForgetPassword", m.name); toast("Remembered password removed"); dlg.close(); } }, "Forget password") : null,
      setupKey,
      h("span", { class: "spacer" }),
      h("button", { class: "btn", value: "cancel" }, "Cancel"),
      h("button", { class: "btn primary", onclick: async (e) => {
        e.preventDefault();
        await api("SetAuth", m.name, pass.checked ? "password" : "key", remember.checked);
        dlg.close(); showMachines();
      } }, "Save")));
  dlg.showModal();
}

// ---------- passwords ----------
// ssh asks for a password machine's password in the middle of a scan or a move; the
// backend raises this dialog and waits. Questions come one at a time.
const pwQueue = [];
function askPassword(req) {
  if (pwQueue.some((r) => r.id === req.id)) return;
  pwQueue.push(req);
  if (pwQueue.length === 1) showPasswordDialog();
}
function showPasswordDialog() {
  const req = pwQueue[0];
  if (!req) return;
  const dlg = $("#pwdlg"), body = $("#pwdlg-body");
  const input = h("input", { class: "field", type: "password", autocomplete: "off", "aria-label": `Password for ${req.machine}` });
  const remember = h("input", { type: "checkbox", checked: req.remember });
  const done = async (ok) => {
    try {
      if (ok) await api("ProvidePassword", req.id, input.value, remember.checked);
      else await api("CancelPassword", req.id);
    } catch (e) { toast(String(e.message || e)); }
    input.value = "";
    dlg.close();
    pwQueue.shift();
    showPasswordDialog();
  };
  fill(body,
    h("div", { style: "font-weight:600" }, `Password for ${req.machine}`),
    h("div", { class: "mono muted", style: "font-size:11px" }, req.destination),
    req.retry ? h("div", { class: "err" }, "That password was not accepted. Try again.") : null,
    input,
    req.canRemember ? h("label", { class: "opt" }, remember, " Remember it in the Keychain") : null,
    h("div", { class: "muted", style: "font-size:12px" }, "Given only to ssh for this machine. Skip leaves this machine out this time."),
    h("div", { style: "display:flex;gap:8px;justify-content:flex-end" },
      h("button", { class: "btn", onclick: (e) => { e.preventDefault(); done(false); } }, "Skip"),
      h("button", { class: "btn primary", onclick: (e) => { e.preventDefault(); if (input.value) done(true); } }, "Log in")));
  dlg.onkeydown = (e) => { if (e.key === "Escape") { e.preventDefault(); done(false); } };
  dlg.showModal();
  input.focus();
}
Events.On("hopsesh:password", (ev) => askPassword(ev.data));

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
        h("small", { class: machineDot(m) === "ok" ? "" : machineDot(m), title: (m.agents || []).join(", ") }, machineDot(m) === "ok" ? `${m.os || ""} · ${m.sessions} sessions` : (m.hint || m.status))))),
    h("span", { class: "spacer" }),
    h("div", { class: "muted", style: "font-size:11px;padding:10px 8px;border-top:1px solid var(--line)" }, "Repos folder: ", h("span", { class: "mono" }, state.info?.reposDir || "")));
  const f = state.filter.toLowerCase();
  const cards = [];
  for (const g of s.groups) {
    const entries = g.entries.filter((e) => (!state.machine || e.machine === state.machine) &&
      (!f || [e.title, e.lastPrompt, e.cwd, g.name, e.machine, e.agentName].join(" ").toLowerCase().includes(f)));
    if (!entries.length) continue;
    const where = g.noRepo ? h("span", { class: "tag" }, "Sessions started outside a git checkout")
      : g.noRemote ? h("span", { class: "tag" }, "A git checkout without a remote")
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

// skillBanner offers the hopsesh skill once (until installed or dismissed), and an update
// when a copy of it is out of date.
function skillBanner() {
  const st = state.info?.skillState, prompt = state.info?.skillPrompt;
  const refresh = async () => { state.info = await api("Info"); renderSessions(); };
  if (st === "absent" && prompt !== "declined") {
    return h("div", { class: "card banner", role: "status" },
      h("div", { style: "font-weight:600" }, "Let your agents use hopsesh"),
      h("div", { style: "font-size:12.5px;line-height:1.5" }, "Ask your agent \u201cbring my laptop session here\u201d or \u201ccontinue this in Codex\u201d. A small skill, the same for every agent, teaches it to use hopsesh: it shows you the plan and acts only after you say yes."),
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
      h("div", { style: "font-weight:600" }, st === "stale" ? "The hopsesh skill is out of date" : "The hopsesh skill is damaged"),
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
  const others = (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key));
  if (others.length) bits.push("also " + others.map((c) => `${c.agentName} on ${c.machine}${c.local ? " (here)" : ""}` + (c.newest ? ", newest" : c.mark ? "" : ", older")).join("; "));
  const err = (x) => toast(String(x.message || x));
  const main = e.hereNewest
    ? h("button", { class: "btn outline", title: "The newest copy is on this machine", onclick: () => api("ResumeEntry", e.machine, e.key, false).catch(err) }, "Resume")
    : h("button", { class: "btn outline", title: e.staleHere ? "An older copy is on this machine; bring the newest one back" : `Bring it here, in ${e.agentName}`, onclick: () => { state.sendTo = ""; preflight(e, ""); } }, e.staleHere ? "Hop back" : "Hop here");
  const cont = (e.continueIn || []).length ? h("select", { class: "cont", "aria-label": "Continue in another agent", onchange: (ev) => { const t = ev.target.value; ev.target.value = ""; if (t) { state.sendTo = ""; preflight(e, t); } } },
    h("option", { value: "" }, "Continue in…"), ...e.continueIn.map((a) => h("option", { value: a.id }, a.name + (a.experimental ? " (experimental)" : "")))) : null;
  const here = (state.scan.machines.find((m) => m.local) || {}).name;
  const peers = e.machine === here ? (state.scan.peers || []) : [];
  const send = peers.length ? h("select", { class: "cont", "aria-label": "Send to another machine", onchange: (ev) => { const m = ev.target.value; ev.target.value = ""; if (m) { state.sendTo = m; preflight(e, ""); } } },
    h("option", { value: "" }, "Send to…"), ...peers.map((m) => h("option", { value: m }, m))) : null;
  return h("div", { class: "row" },
    h("div", {}, h("div", { class: "t", title: e.title }, h("span", { class: "agent" }, e.agentName), e.title), h("div", { class: "s" }, bits.join(" · ") || " ")),
    h("div", { style: "font-size:12px;min-width:0" }, h("div", {}, e.machine), h("div", { class: "mono s", title: e.cwd }, e.cwd)),
    h("div", { class: "prompt", title: e.lastPrompt }, e.lastPrompt ? `“${e.lastPrompt}”` : ""),
    h("div", { style: "font-size:12px" }, h("span", { class: "pill" + (e.live ? " live" : "") }, e.status), h("div", { class: "s" }, ago(e.lastActive))),
    h("div", { class: "actions" }, main, cont, send));
}

// ---------- plan ----------
async function preflight(e, target, keepOpts = false) {
  state.sel = e;
  state.target = target;
  const dflt = state.info?.defaults || { markMoved: true, syncCode: true, pushSource: false };
  if (!keepOpts) state.opts = { worktree: "auto", remoteControl: false, notify: false, fork: false, redact: false, clone: false, targetDir: "",
    mark: dflt.markMoved, syncCode: dflt.syncCode, push: dflt.pushSource, stopLocal: false, app: false, conflict: "",
    fidelity: "history", native: false, note: "", go: false, carryRules: false, via: "" };
  setTitlebar("plan");
  loading("Working out the plan…");
  if (state.sendTo) loading(`Asking hopsesh on ${state.sendTo} to plan it…`);
  try {
    state.plan = state.sendTo ? await api("PushPlan", e.key, state.sendTo, target, state.opts) : await api("Plan", e.machine, e.key, target, state.opts);
  } catch (err) { planError(err); return; }
  renderPlan();
}

function planError(err) {
  view.replaceChildren(h("div", { class: "loading" }, h("div", {}, h("div", { class: "err", style: "margin-bottom:12px" }, String(err.message || err)),
    h("button", { class: "btn", onclick: () => showSessions() }, "Back to sessions"))));
}

function renderPlan() {
  const p = state.plan;
  const r = p.repo, o = state.opts, e = state.sel, cont = p.continue;
  const there = p.machine ? `on ${p.machine}` : "on this machine";
  const replan = () => preflight(e, state.target, true);
  const opt = (key, title, desc) => h("label", { class: "opt" },
    h("input", { type: "checkbox", checked: o[key], onchange: (ev) => { o[key] = ev.target.checked; replan(); } }), h("span", {}, h("b", {}, title), h("span", { class: "muted" }, desc)));

  const repoItems = [];
  if (r.action === "use") repoItems.push(item("ok", `Found ${there} at ${r.localPath}` + (r.localBranch ? ` (on ${r.localBranch})` : ""), r.identity ? `Matched by remote ${r.identity}.` : ""));
  if (r.action === "clone") repoItems.push(item("ok", `Will clone into ${r.localPath}`, `From ${r.remote}.`));
  if (r.action === "needs-clone") {
    const dest = h("input", { class: "field mono", style: "flex:1", value: r.localPath, "aria-label": "Clone into" });
    repoItems.push(item("warn", `Not found ${there}`, `Matched by remote ${r.identity}. Searched the repos folder and the usual places.`));
    repoItems.push(h("div", { style: "display:flex;gap:8px;align-items:center;padding-left:28px" }, h("span", { class: "muted", style: "font-size:12px" }, "Clone into"), dest,
      h("button", { class: "btn primary", onclick: () => { o.clone = true; o.reposDir = dest.value.replace(/\/[^/]+\/?$/, ""); replan(); } }, "Clone for me"),
      p.machine ? null : h("button", { class: "btn", onclick: async () => { const d = await api("ChooseFolder", "Where is your checkout?"); if (d) { o.targetDir = d; replan(); } } }, "I already have it…")));
  }
  if (r.action === "dir") repoItems.push(item("ok", `Continue in ${r.localPath}`, "Chosen by you."));
  if (r.action === "none") repoItems.push(p.sourceCwd === p.targetCwd ? item("ok", "The same folder", "The session stays where it was started.")
    : item("warn", "No repository to match", "The folder has no git remote, so hopsesh can't find or clone it here; choose a folder."));
  if (r.sourceBranch) {
    const where = r.sourceAgentWorktree ? "an agent worktree" : r.sourceInWorktree ? "a git worktree" : "the main folder";
    repoItems.push(item(r.sourceInWorktree ? "warn" : "ok", `Branch ${r.sourceBranch}: the session ran in ${where} on ${p.sourceHost}` + (r.sourceInWorktree && r.sourceMainBranch ? ` (its main folder is on ${r.sourceMainBranch})` : ""),
      r.worktree ? `A matching worktree will be created here: ${r.worktree}` : (r.sourceInWorktree ? "It will continue in the main checkout here." : "")));
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
  if (p.stopHere) repoItems.push(item("warn", "The copy open here will be quit first", "It gets the normal quit signal and saves its session before the newer copy replaces it."));

  const blockers = p.blockers || [];
  const warnings = (p.warnings || []).filter((w) => !w.includes("unpushed") && !w.includes("worktree"));
  const checks = warnings.length || blockers.length ? h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Check before going ahead")),
    h("div", { class: "sec" }, ...warnings.map((w) => item("warn", w, "")), ...blockers.map((b) => b.includes("open on this machine") || b.includes("running on this machine")
      ? h("div", {}, item("err", b, ""), h("div", { style: "padding-left:28px" }, h("button", { class: "btn primary", onclick: () => { o.stopLocal = true; replan(); } }, "Quit it and continue")))
      : p.conflict && b.startsWith(p.conflict)
      ? h("div", {}, item("err", "Both copies changed: " + p.conflict, "Nothing is merged. Pick which to keep."),
          h("div", { style: "display:flex;gap:8px;padding-left:28px" },
            h("button", { class: "btn", onclick: () => { o.conflict = "keep-both"; replan(); } }, "Keep both (bring this one in separately)"),
            h("button", { class: "btn", onclick: () => { o.conflict = "replace"; replan(); } }, "Replace the copy here"),
            h("button", { class: "btn", onclick: () => showSessions() }, "Keep the copy here")))
      : item("err", b, "")))) : null;

  const main = h("div", { class: "pre-main" },
    h("div", { class: "fromto" },
      h("div", { class: "box" }, h("small", {}, "From"), h("span", { style: "font-weight:500" }, `${p.fromAgent} on ${p.sourceHost}${p.sourceOs ? " (" + p.sourceOs + ")" : ""}`), h("span", { class: "mono", style: "font-size:11px" }, p.sourceCwd)),
      h("span", { style: "color:var(--accent);font-size:18px" }, "→"),
      h("div", { class: "box to" }, h("small", {}, "To"), h("span", { style: "font-weight:500" }, `${p.agent} ${there}`), h("span", { class: "mono", style: "font-size:11px" }, p.targetCwd))),
    cont ? continueCard(p, o, replan) : null,
    h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Repository")), h("div", { class: "sec" }, ...repoItems)),
    p.mappings.length ? h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Paths")),
      h("div", { class: "sec" }, h("div", { class: "maprow" }, ...p.mappings.flatMap((m) => [h("span", {}, m.from), h("span", { style: "color:var(--accent)" }, "→"), h("span", {}, m.to)])),
        h("div", { class: "muted", style: "font-size:11.5px" }, cont ? "Paths in the conversation are mapped to this machine's." : "Signed and encrypted content, message ids and the session id are never changed."))) : null,
    checks);

  const what = cont
    ? [h("div", { class: "kv" }, h("span", {}, { append: `New work added to “${cont.appendTo}”`, new: `A new ${p.agent} session` }[cont.relation] || "Nothing (see the check below)"), h("span", {}, cont.fidelity)),
       h("div", { class: "kv muted" }, h("span", {}, cont.report.summary))]
    : [h("div", { class: "kv" }, h("span", {}, `${p.files} file(s)`), h("span", {}, fmtBytes(p.bytes))),
       p.setAside ? h("div", { class: "kv muted" }, h("span", {}, `${p.setAside} older copy here set aside (undo brings it back)`)) : null];
  const side = h("div", { class: "pre-side" },
    h("div", { style: "font-weight:600" }, cont ? "What arrives" : "What gets moved"),
    ...what,
    h("div", { class: "kv muted" }, h("span", {}, "logins, keys, live sockets: never")),
    h("div", { style: "height:1px;background:var(--line)" }),
    h("div", { style: "font-weight:600" }, "Options"),
    p.can.remoteControl ? opt("remoteControl", "Turn on Remote Control", `Reach the new session from your phone or other machines, as ${p.newName}.`) : null,
    p.can.app ? opt("app", `Open it in the ${p.agent} desktop app`, "Instead of a terminal window.") : null,
    !cont ? opt("notify", "Tell the old session it moved", p.can.notify ? "The new session's first message asks the agent to notify it (needs Remote Control on both)." : "You get a line to paste into the old session.") : null,
    p.live && p.can.fork ? opt("fork", "Keep the old session running", "Fork instead of handing off: both copies continue.") : null,
    p.mark !== "off" || !o.mark ? opt("mark", `Mark the copy on ${p.sourceHost}`,
      p.mark === "when-stopped" ? "It is still open; once it ends, its title shows where the work went, so it isn't resumed by mistake."
        : `Its title shows that it ${cont ? "continued in " + p.agent : "moved to " + (state.info?.host || "here")}, so it isn't resumed by mistake.`) : null,
    r.sourceHead ? opt("syncCode", "Bring the code here to the session's commit", "Fetches if needed; fast-forwards only a clean checkout on the same branch. Never merges.") : null,
    r.unpushed && r.sourceUpstream ? opt("push", `Push them first (${r.unpushed} commit(s) on ${p.sourceHost})`, `Runs git push on ${p.sourceHost} with its own credentials before copying.`) : null,
    opt("redact", "Redact likely secrets", "In this copy only."),
    p.otherAccount ? h("div", { class: "muted", style: "font-size:11.5px" }, "This machine is signed in to another account: content bound to the original account is left out.") : null,
    h("span", { class: "spacer" }),
    h("div", { class: "muted", style: "font-size:11.5px" }, cont ? `${p.agent} is told where the work came from and asked to check the repository and files before continuing.`
      : "The new session starts with a message explaining the move and asking the agent to check that nothing is missing."),
    h("button", { class: "btn primary big", disabled: blockers.length > 0, onclick: doApply }, p.machine ? `Send to ${p.machine}` : cont ? `Continue in ${p.agent}` : r.action === "clone" ? "Clone and hop" : "Hop here"),
    h("button", { class: "btn", onclick: () => showSessions() }, "Back"),
    h("div", { class: "muted", style: "font-size:11px;text-align:center" }, p.machine ? "The original here is not deleted." : `The original on ${p.sourceHost} is not deleted.`));
  view.replaceChildren(h("div", { class: "pre" }, main, side));
}

// continueCard shows what a continuation in another agent carries over and what it loses,
// and the choices that change it.
function continueCard(p, o, replan) {
  const c = p.continue, rep = c.report;
  const lost = [];
  if (rep.reasoningDropped) lost.push(`${rep.reasoningDropped} reasoning block(s) (private to ${c.from})`);
  if (rep.stepsSummarised) lost.push(`${rep.stepsSummarised} oldest step(s) summarised to fit ${p.agent}'s context`);
  if (rep.outputsShortened) lost.push(`${rep.outputsShortened} long tool output(s) shortened`);
  if (rep.attachmentsAsPlaceholders) lost.push(`${rep.attachmentsAsPlaceholders} attachment(s) as placeholders`);
  const relation = {
    new: `A new ${p.agent} session with the conversation so far.`,
    append: `The ${p.agent} session “${c.appendTo}” here gets only the new work since it was left; its own part stays exactly as it was.`,
    same: "Nothing new on either side.",
    behind: `Only the copy here changed; it is already the newest.`,
    diverged: "Both copies changed since they parted.",
  }[c.relation] || c.relation;
  const note = h("textarea", { class: "field", rows: 3, placeholder: `Optional: a note for ${p.agent} (what you were doing, what's next)`, "aria-label": "Handoff note" });
  note.value = o.note || "";
  note.onchange = () => { o.note = note.value; replan(); };
  const briefing = h("details", {}, h("summary", { class: "muted", style: "font-size:12px;cursor:pointer" }, `What ${p.agent} is told`),
    h("pre", { class: "term", style: "max-height:240px;overflow:auto;white-space:pre-wrap;margin-top:8px" }, c.briefing));
  return h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, `From ${c.from} to ${p.agent}`)),
    h("div", { class: "sec" },
      item("ok", relation, rep.summary),
      lost.length ? item("warn", "Not carried over exactly", lost.join("; ") + ".") : null,
      p.nativeCopy ? item("ok", `The ${p.nativeCopy.agent} session is kept here too, byte for byte`, `Going back to ${p.nativeCopy.agent} on this machine later adds only the new work to it.`) : null,
      o.via === "import" ? null : h("div", { style: "display:flex;gap:8px;align-items:center" }, h("span", { class: "muted", style: "font-size:12px" }, "Carry"),
        h("select", { "aria-label": "Fidelity", onchange: (ev) => { o.fidelity = ev.target.value; replan(); } },
          h("option", { value: "history", selected: o.fidelity === "history" }, "The conversation (tool activity as text)"),
          h("option", { value: "note", selected: o.fidelity === "note" }, "Only a briefing"))),
      p.can.import ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: o.via === "import", onchange: (ev) => { o.via = ev.target.checked ? "import" : ""; replan(); } }),
        h("span", {}, h("b", {}, `Let ${p.agent} convert it with its own importer`), h("span", { class: "muted" }, `Instead of hopsesh's conversion; hopsesh still adds its briefing. Starts a new ${p.agent} session.`))) : null,
      p.can.native && o.via !== "import" ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: o.native, onchange: (ev) => { o.native = ev.target.checked; replan(); } }),
        h("span", {}, h("b", {}, `Replay shell commands as ${p.agent}'s own (experimental)`), h("span", { class: "muted" }, "Exact commands and outputs instead of text; the rest stays text."))) : null,
      h("label", { class: "opt" }, h("input", { type: "checkbox", checked: o.carryRules, onchange: (ev) => { o.carryRules = ev.target.checked; replan(); } }),
        h("span", {}, h("b", {}, `Bring your ${c.from} instructions along`), h("span", { class: "muted" }, `Your instructions for every ${c.from} project go into the briefing; you can read them under “What ${p.agent} is told”.`))),
      h("label", { class: "opt" }, h("input", { type: "checkbox", checked: o.go, onchange: (ev) => { o.go = ev.target.checked; replan(); } }),
        h("span", {}, h("b", {}, "Start working right away"), h("span", { class: "muted" }, `${p.agent} starts with “Continue.” instead of waiting for you.`))),
      note, briefing));
}

// roundTripCard reports what happened around the move: quitting the copy here, pushing on
// the source, bringing the code here, and marking the copy left behind.
function roundTripCard(d) {
  const rows = [];
  if (d.stopped) rows.push(item("ok", "Quit the copy that was open here", ""));
  if (d.pushError) rows.push(item("warn", `Could not push on ${d.sourceHost}`, d.pushError));
  else if (d.pushed) rows.push(item("ok", `Pushed the session's branch on ${d.sourceHost}`, ""));
  if (d.syncNote) {
    const ok = ["up-to-date", "fast-forwarded", "ahead"].includes(d.syncState);
    rows.push(item(ok ? "ok" : "warn", "Code: " + d.syncNote, ""));
  }
  if (d.mark === "done") rows.push(item("ok", `The copy on ${d.sourceHost} is marked`, "Its session list there shows where the work went."));
  if (d.mark === "pending") rows.push(item("ok", `The copy on ${d.sourceHost} is still open`, "It is marked once it ends (hopsesh checks on its next scan)."));
  if (d.mark === "failed") rows.push(item("warn", `Could not mark the copy on ${d.sourceHost}`, d.markError || ""));
  for (const w of d.warnings || []) rows.push(item("warn", w, ""));
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
  const steps = h("div", { class: "muted mono", style: "font-size:12px;margin-top:12px;min-height:1.5em" });
  view.replaceChildren(h("div", { class: "loading" }, h("div", {}, state.sendTo ? `Sending to ${state.sendTo}…` : state.plan.continue ? `Continuing in ${state.plan.agent}…` : "Copying, rewriting and verifying…"), steps));
  const off = Events.On("hopsesh:progress", (ev) => { steps.textContent = String(ev.data); });
  try {
    renderDone(await api(state.sendTo ? "PushApply" : "Apply"));
  } catch (e) {
    view.replaceChildren(h("div", { class: "loading" }, h("div", {}, h("div", { class: "err", style: "margin-bottom:12px" }, String(e.message || e)), h("button", { class: "btn", onclick: () => renderPlan() }, "Back to the plan"))));
  } finally {
    if (typeof off === "function") off();
  }
}

function renderDone(d) {
  setTitlebar("done");
  const facts = d.kind === "continue" ? [] : [`${d.paths} path(s) rewritten`, `${d.files} file(s), ${d.bytes}`];
  if (d.cloned) facts.unshift("repository cloned");
  if (d.worktree) facts.push("worktree created");
  if (d.secrets) facts.push(`${d.secrets} likely secret(s) ${d.redacted ? "redacted" : "found"}`);
  const open = async () => { try { await api("OpenResult"); } catch (e) { toast(String(e.message || e)); } };
  view.replaceChildren(h("div", { class: "center" }, h("div", { class: "done" },
    h("div", { style: "display:flex;gap:14px;align-items:center" }, h("span", { class: "badge ok", style: "width:40px;height:40px;font-size:20px" }, "✓"),
      h("div", {}, h("div", { style: "font-size:20px;font-weight:600" }, d.kind === "continue" ? `“${d.title}” continues in ${d.agent}${d.machine ? " on " + d.machine : ""}` : `“${d.title}” is on ${d.machine || "this machine"}`), facts.length ? h("div", { class: "muted" }, facts.join(" · ")) : null)),
    h("div", { class: "card" }, h("div", { class: "sec" }, h("div", { style: "font-weight:600" }, d.machine ? `Start it on ${d.machine}` : "Start it"),
      h("div", { style: "display:flex;gap:8px" }, h("div", { class: "term" }, d.command), h("button", { class: "btn", onclick: async () => { await api("CopyText", d.command); toast("Copied"); } }, "Copy")),
      d.machine ? null : h("div", { style: "display:flex;gap:8px" }, h("button", { class: "btn primary", onclick: open }, d.inApp ? `Open in the ${d.agent} app` : "Open in Terminal")),
      h("div", { class: "muted", style: "font-size:12px" }, d.kind === "continue"
        ? `The conversation ends with a briefing that tells ${d.agent} where it came from and asks it to check the repository and files before continuing.`
        : `Its first message tells ${d.agent} where the session came from and asks it to check the repository, files, tools and environment before continuing.`))),
    roundTripCard(d),
    d.notice ? h("div", { class: "card" }, h("div", { class: "sec" }, h("div", { style: "font-weight:600" }, `Tell the session on ${d.sourceHost}`),
      h("div", { class: "muted", style: "font-size:12px" }, "Paste this into the old session:"),
      h("div", { style: "display:flex;gap:8px" }, h("div", { class: "term" }, d.notice), h("button", { class: "btn", onclick: async () => { await api("CopyText", d.notice); toast("Copied"); } }, "Copy")))) : null,
    h("div", { style: "display:flex;gap:16px;font-size:12px" },
      h("button", { class: "btn", title: d.machine ? `Undoes both machines: the copy on ${d.machine} and the mark here` : "", onclick: async () => { try { await api("Undo", d.journal); toast("Undone"); } catch (e) { toast(String(e.message || e)); } showSessions(true); } }, "Undo"),
      h("button", { class: "btn", onclick: () => showSessions(true) }, "Back to sessions"),
      h("span", { class: "muted", style: "align-self:center" }, "Audit log: ", h("span", { class: "mono" }, d.auditDir))))));
}

// ---------- boot ----------
$("#btn-refresh").onclick = () => showSessions(true);
$("#q").addEventListener("input", (e) => { state.filter = e.target.value; if (state.scan) renderSessions(); });
(async () => {
  for (const r of await api("PendingPasswords").catch(() => [])) askPassword(r);
  state.info = await api("Info");
  $("#subtitle").textContent = `Sessions on your machines · this is ${state.info.host}`;
  if (state.info.configError) { showConfigError(); return; }
  if (state.info.hasHosts) showSessions(); else showMachines();
  if (state.info.updateCheck === "on") {
    try { state.update = await api("CheckUpdate"); if (state.update?.newer && state.scan) renderSessions(); } catch { /* offline */ }
  }
})();

// showConfigError explains a configuration an older hopsesh wrote; hopsesh keeps no code
// for old formats, so it offers to set the file aside and start fresh.
function showConfigError() {
  setTitlebar("settings");
  view.replaceChildren(h("div", { class: "center" }, h("div", { class: "done" },
    h("div", { style: "font-size:18px;font-weight:600" }, "Your hopsesh settings are from an older version"),
    h("div", { class: "muted", style: "line-height:1.5" }, "This version of hopsesh works with every coding agent and stores its settings differently. ",
      "Start fresh to keep the old file next to the new one and add your machines again. Your sessions are not affected."),
    h("div", { class: "mono muted", style: "font-size:11.5px" }, state.info.configError),
    h("div", { style: "display:flex;gap:8px" }, h("button", { class: "btn primary", onclick: async () => {
      try { const old = await api("StartFresh"); toast("Old settings kept at " + old); } catch (e) { toast(String(e.message || e)); return; }
      state.info = await api("Info"); showMachines();
    } }, "Start fresh")))));
}

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
