// The Settings screen, in tabs: General, Agents, Terminal, Skill, Command line, Updates.
import { api, h, fill, view, state, screen, go, loading, toast, fail, dialog, agentBadge, sys, cliHow, icon, ask, count, current } from "./core.js";
import { running } from "./term.js";

const SKILL_TEXT = {
  absent: ["Not installed", "st-ended"],
  current: ["Installed, up to date", "st-idle"],
  stale: ["Out of date", "st-warn"],
  modified: ["Installed, edited by you", "st-warn"],
  foreign: ["Another skill named hopsesh", "st-warn"],
  broken: ["Damaged", "st-error"],
};
const CLI_TEXT = {
  missing: ["Not installed", "st-ended"],
  ours: ["Installed, runs this app's hopsesh", "st-idle"],
  "other-app": ["Points to another copy of hopsesh.app", "st-warn"],
  dangling: ["Broken link: the app moved or was removed", "st-error"],
  standalone: ["A separately installed hopsesh is there", "st-warn"], // Windows: comes first on PATH
  foreign: ["Points to a different program", "st-warn"],
};
const CAPS = {
  live: "sees open sessions", stop: "quits open sessions", mark: "marks copies left behind", account: "knows its account",
  sanitize: "moves between accounts", read: "continues in other agents", write: "takes sessions from other agents",
  "native-replay": "replays commands natively", integrate: "can use the hopsesh skill", fork: "can fork", "remote-control": "remote control",
  app: "desktop app", notify: "tells the old session", import: "has its own importer", "post-install": "registers moved sessions",
  preview: "previews conversations", rename: "renames sessions",
  "cloud-list": "cloud list", "cloud-send": "cloud send", "cloud-fetch": "cloud bring", "cloud-follow": "follow-up", "cloud-archive": "cloud archive",
};

// cloudRow is one of an agent's clouds: what hopsesh can do there, in blue chips.
function cloudRow(a, c) {
  const order = ["cloud-list", "cloud-send", "cloud-fetch", "cloud-follow", "cloud-archive"];
  const caps = [...c.capabilities].sort((x, y) => order.indexOf(x) - order.indexOf(y)).map((cp) => cp === "cloud-list" && c.partial ? ["cloud list: only what hopsesh knows", "can part"] : [CAPS[cp] || cp, "can cl"]);
  const note = !caps.length ? `hopsesh does not reach ${c.title} yet.`
    : c.partial ? `${a.name} has no list command; Find in ${a.name} shows the rest. Archive is on the vendor's site only.`
    : c.fidelity === "native" ? "The whole conversation comes back." : "";
  return h("div", { class: "cloud-row" }, h("span", { style: "font-size:12.5px;font-weight:500" }, c.title),
    h("div", { style: "display:flex;gap:5px;flex-wrap:wrap" }, caps.map(([t, cls]) => h("span", { class: cls }, t))),
    note ? h("span", { class: "muted", style: "font-size:12px" }, note) : null);
}

let tab = "general";
let s = null;

const chip = ([text, cls]) => h("span", { class: "chip " + cls }, text);
const title = (t, desc) => h("div", { style: "display:flex;flex-direction:column;gap:2px;min-width:0;flex:1 1 300px" }, h("span", { style: "font-weight:500" }, t), desc ? h("span", { class: "muted", style: "font-size:12px" }, desc) : null);
const card = (...kids) => h("section", { class: "card" }, h("div", { class: "dlg-body" }, kids));

// run calls the backend, says how it went, and shows the settings again.
async function run(fn, done) {
  try { await fn(); if (done) toast(done); } catch (e) { fail(e); }
  await load();
  state.info = await api("Info").catch(() => state.info);
}

function save(patch) {
  // Apply the privacy switch before navigating away: returning to Sessions must
  // never briefly reveal a preview while SaveSettings/Info are still in flight.
  if (patch.previews !== undefined) state.info.previews = patch.previews;
  Object.assign(s, patch);
  return run(() => api("SaveSettings", Object.assign({ layout: s.layout, markMoved: s.markMoved, syncCode: s.syncCode, pushSource: s.pushSource, updateCheck: s.updateCheck || "off", appIcons: s.appIcons, previews: s.previews }, patch)), "Saved");
}

function toggle(key, label, desc) {
  return h("label", { class: "opt" }, h("input", { type: "checkbox", checked: s[key], onchange: (e) => save({ [key]: e.target.checked }) }),
    h("span", {}, h("b", {}, label), h("span", { class: "muted" }, desc)));
}

function general() {
  return [
    card(h("span", { class: "sec-h" }, "Hopping sessions"),
      h("div", { class: "set-row" }, title("Repos folder", s.reposDir),
        h("button", { class: "btn", onclick: async () => { const d = await api("ChooseFolder", "Where should hopsesh clone repositories?").catch(fail); if (d) run(() => api("SetReposDir", d), "Saved"); } }, "Change…")),
      h("div", { class: "set-row" }, title("Clone layout", "Where a repository goes inside the repos folder."),
        h("select", { "aria-label": "Clone layout", onchange: (e) => save({ layout: e.target.value }) },
          h("option", { value: "flat", selected: s.layout === "flat" }, "<repos>/<name>"), h("option", { value: "ghq", selected: s.layout === "ghq" }, "<repos>/<host>/<owner>/<name>"))),
      toggle("markMoved", "Mark the copy left behind", "Its title says where the work went (“↪ moved to …”), so it isn't resumed by mistake."),
      toggle("syncCode", "Bring the code along", "Fetch the session's commit (from the other machine if it isn't pushed) and fast-forward a clean checkout."),
      toggle("pushSource", "Push unpushed commits on the other machine first", "Off: commits are fetched straight from the other machine.")),
    card(h("span", { class: "sec-h" }, "Appearance"),
      toggle("appIcons", "Show each agent's own app icon", "When the agent's desktop app is installed here, its icon pictures the agent; otherwise hopsesh's own mark does."),
      toggle("previews", "Show conversation previews", "The inspector shows the end of the selected session's conversation, with Markdown formatting, read on its machine. Turn it off when you share your screen.")),
    card(h("span", { class: "sec-h" }, sys.Here),
      h("div", { class: "set-row" }, title("Receiving sessions", state.info.receive ? "On: your other machines can send sessions here." : `Off: ${sys.here} refuses sessions sent from other machines.`),
        h("button", { class: "btn", onclick: () => go("machines") }, "Machines…")),
      s.localNetworkGated ? h("div", { class: "set-row" }, title("Local network access", "macOS asks before hopsesh reaches machines on your local network. Machines on Tailscale don't need it."),
        h("button", { class: "btn", onclick: () => api("OpenLocalNetworkSettings").catch(fail) }, "Privacy & Security…")) : null,
      h("div", { class: "set-row" }, title("Settings", s.configDir), h("button", { class: "btn", onclick: () => api("Reveal", s.configDir).catch(fail) }, "Show")),
      h("div", { class: "set-row" }, title("Logs, undo and audit", s.stateDir), h("button", { class: "btn", onclick: () => api("Reveal", s.stateDir).catch(fail) }, "Show"))),
  ];
}

function agents() {
  return s.agents.map((a) => {
    const caps = a.capabilities || [];
    const set = (enabled, rc, imp, done) => run(() => api("SetAgent", a.id, enabled, rc, imp), done);
    return card(
      h("div", { class: "set-row" },
        agentBadge(a.id, a.name),
        title(a.name, a.version ? `${a.version} on ${sys.here}${a.folder ? " · " + a.folder : ""}` : `Not installed on ${sys.here}`),
        a.stability === "experimental" ? h("span", { class: "chip st-warn" }, "experimental") : null,
        h("button", { class: "switch", role: "switch", "aria-checked": a.enabled ? "true" : "false", "aria-label": `${a.name} on`,
          onclick: () => set(!a.enabled, a.remoteControl, a.import, a.enabled ? `${a.name} is off` : `${a.name} is on`) })),
      h("div", { style: "display:flex;gap:5px;flex-wrap:wrap" }, caps.filter((c) => !c.startsWith("cloud-")).map((c) => CAPS[c] || c).sort().map((c) => h("span", { class: "cap" }, c))),
      (a.clouds || []).map((c) => cloudRow(a, c)),
      a.tested?.length ? h("span", { class: "muted", style: "font-size:12px" }, "Tested with " + a.tested.join(", ")) : null,
      a.enabled && caps.includes("remote-control") ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.remoteControl, onchange: (e) => set(true, e.target.checked, a.import, "Saved") }),
        h("span", {}, h("b", {}, `Turn on Remote Control for sessions hopped into ${a.name}`), h("span", { class: "muted" }, "Reach them from your phone or other machines. Needs the agent's own subscription login."))) : null,
      a.enabled && caps.includes("import") ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.import, onchange: (e) => set(true, a.remoteControl, e.target.checked, "Saved") }),
        h("span", {}, h("b", {}, `Continue in ${a.name} with its own importer`), h("span", { class: "muted" }, "Instead of hopsesh's conversion, when it can read the other agent; hopsesh still adds its briefing. The plan can change it each time."))) : null);
  });
}

// terminal is Settings → Terminal: where sessions and steps open, the user's terminal app,
// and the hopsesh Terminal window.
let ts = null;
async function setTerm(patch) {
  const next = Object.assign({ app: ts.app, where: ts.where, font: ts.font, fontSize: ts.fontSize, scrollback: ts.scrollback, keepTabs: ts.keepTabs,
    notify: ts.notify, closeEnded: ts.closeEnded, screenReader: ts.screenReader, systemConsole: ts.systemConsole }, patch);
  ts = Object.assign({}, ts, patch); // a second change before this one's answer builds on it
  try { await api("SetTerminalSettings", next); toast("Saved"); } catch (e) { fail(e); }
  ts = await api("TerminalSettings").catch(() => ts);
  state.info = await api("Info").catch(() => state.info);
  if (current === "settings") render(); // unless the user went on meanwhile
}
function seg(label, value, choices, onpick) {
  return h("div", { class: "seg", role: "radiogroup", "aria-label": label },
    choices.map(([v, text]) => h("button", { role: "radio", "aria-checked": value === v ? "true" : "false", onclick: () => onpick(v) }, text)));
}
const SHIELD = "M12 3 5 6v6c0 4.2 2.9 7.6 7 9 4.1-1.4 7-4.8 7-9V6z";
function terminal() {
  if (!ts) return [h("div", { class: "loading", role: "status" }, "Reading the terminal settings…")];
  const name = ts.name || sys.terminal;
  const installed = (ts.apps || []).filter((a) => a.installed);
  const iterm = installed.some((a) => a.id === "iterm2");
  const sw = (key, label, desc) => h("label", { class: "opt" }, h("input", { type: "checkbox", checked: ts[key], onchange: (e) => setTerm({ [key]: e.target.checked }) }),
    h("span", {}, h("b", {}, label), h("span", { class: "muted" }, desc)));
  return [
    h("span", { class: "muted", style: "font-size:12.5px" }, "Where hopsesh runs Claude Code, Codex and your shell when you resume, bring back, hand off or sign in."),
    card(h("span", { class: "sec-h" }, "Where sessions open"),
      h("div", { class: "set-row" }, title("Resume sessions, hand-offs and bring-backs", `Hand-offs, bring-backs and sign-ins use this window unless you choose ${name}, because hopsesh needs to see how they end. Every tab keeps Open in my terminal.`),
        seg("Where sessions open", ts.where, [["here", "In this window"], ["terminal", `In ${name}`]], (v) => setTerm({ where: v }))),
      h("span", { class: "muted", style: "font-size:12px" }, (ts.where === "terminal" ? `${name} keeps running after hopsesh quits.` : "In this window: a tab of the hopsesh Terminal window, which ends when hopsesh quits.")
        + " This is where a session first resumes; a session's Resume menu picks another place, and hopsesh remembers it for that agent."),
      h("div", { class: "set-row" }, title("My terminal", `Where Open in my terminal goes. Now: ${ts.name}.`),
        h("select", { "aria-label": "My terminal", onchange: (e) => setTerm({ app: e.target.value }) },
          h("option", { value: "", selected: !ts.app }, `Automatic (${ts.name})`),
          installed.map((a) => h("option", { value: a.id, selected: ts.app === a.id }, a.name)))),
      iterm ? h("span", { class: "muted", style: "font-size:12px" }, "iTerm2: hopsesh opens a new tab in its front window, labels it with the session, and shows a session's tab instead of opening it twice. It never types into iTerm2 or reads it.") : null,
      sw("keepTabs", "Keep tabs when the window closes", "Closing the window only hides it, and the programs keep running. Quitting hopsesh ends every program in its tabs; hopsesh asks first. For work that must outlive hopsesh, use Open in my terminal."),
      sw("closeEnded", "Close a tab when its program ends", "When it ends well (a hand-off's step once hopsesh has its link). A program that failed keeps its tab, so you can read why."),
      sw("notify", "Tell me when a program waits for me", sys.mac
        ? "A notification when a tab you can't see waits for your answer. hopsesh writes the text; it never shows what the program printed. Claude Code tells hopsesh it's waiting only if you turn on its terminal bell (/config → Notifications); hopsesh never changes Claude Code's settings for you."
        : "The terminal's taskbar button flashes when a tab you can't see waits for your answer. Claude Code tells hopsesh it's waiting only if you turn on its terminal bell (/config → Notifications).")),
    card(h("span", { class: "sec-h" }, "Look"),
      h("div", { class: "set-row" }, title("Font", "Fonts with box-drawing characters draw Claude Code and Codex best. Empty: the system's monospace font."),
        h("input", { class: "field", "aria-label": "Font", list: "term-fonts", value: ts.font, placeholder: sys.mac ? "SF Mono" : sys.win ? "Cascadia Mono" : "monospace", onchange: (e) => setTerm({ font: e.target.value.trim() }) }),
        h("datalist", { id: "term-fonts" }, ["SF Mono", "Menlo", "Monaco", "Cascadia Mono", "Cascadia Code", "Consolas", "JetBrains Mono", "Fira Code", "IBM Plex Mono", "Source Code Pro"].map((f) => h("option", { value: f })))),
      h("div", { class: "set-row" }, title("Size", `${sys.mac ? "⌘= and ⌘-" : "Ctrl+= and Ctrl+-"} in the terminal change it too.`),
        h("select", { "aria-label": "Font size", onchange: (e) => setTerm({ fontSize: Number(e.target.value) }) },
          Array.from({ length: 16 }, (_, i) => i + 9).map((n) => h("option", { value: n, selected: ts.fontSize === n }, `${n} pt`)))),
      h("div", { class: "set-row" }, title("Colors", "The terminal follows the app's light or dark look. Programs that ask for the background color get the real one, so Claude Code and Codex pick a matching theme."),
        h("span", { class: "ro-val", role: "status", "aria-label": "Colors: follow the app (not a setting)", title: "The only choice for now" }, "Follow the app")),
      h("div", { class: "set-row" }, title("Scrollback", "Kept in memory only, and gone when the tab closes."),
        h("select", { "aria-label": "Scrollback", onchange: (e) => setTerm({ scrollback: Number(e.target.value) }) },
          (ts.scrollbacks || []).map((n) => h("option", { value: n, selected: ts.scrollback === n }, `${n.toLocaleString("en")} lines`))))),
    card(h("span", { class: "sec-h" }, "Accessibility"),
      h("div", { class: "set-row" }, title("Screen reader mode", `Makes the terminal's text readable line by line and announces new output. Automatic turns it on while ${sys.mac ? "VoiceOver" : sys.win ? "Narrator or another screen reader" : "a screen reader"} runs.`),
        seg("Screen reader mode", ts.screenReader, [["", "Automatic"], ["on", "On"], ["off", "Off"]], (v) => setTerm({ screenReader: v })))),
    sys.win ? card(h("span", { class: "sec-h" }, "On Windows"),
      h("label", { class: "opt" }, h("input", { type: "checkbox", checked: !ts.systemConsole, disabled: !ts.bundled, onchange: (e) => setTerm({ systemConsole: !e.target.checked }) }),
        h("span", {}, h("b", {}, "Use the bundled console host (recommended)"), h("span", { class: "muted" }, ts.bundled
          ? "hopsesh carries Microsoft's newer console host for its tabs; Claude Code and Codex draw correctly with it. Turn it off only if a security tool blocks it."
          : "This copy of hopsesh has no bundled console host, so tabs use Windows' own.")))) : null,
    card(h("span", { class: "sec-h" }, "Safety"),
      [["Programs can never read your clipboard.", "Pasting is always something you do."],
        ["hopsesh never types into a program.", "It starts the command; you answer every question, including Claude Code's trust question."],
        ["Sign-in and shell tabs are never recorded.", "hopsesh never reads your sign-in tokens."],
        ["Nothing a terminal shows is written to disk,", "to the activity log or to crash reports."],
        ["Links open only after you confirm them.", "Only web links (http and https) open from a terminal, and hopsesh shows you the whole address first."]]
        .map(([b, t]) => h("div", { class: "safety" }, icon(SHIELD, 15), h("span", {}, h("b", {}, b), " ", h("span", { class: "muted" }, t))))),
  ];
}

function skill() {
  const sk = s.skill || {}, copies = sk.copies || [], rules = sk.rules || [];
  const addRules = h("input", { type: "checkbox" });
  const install = (force, withRules, done) => run(() => api("InstallSkill", force, withRules), done);
  const acts = [];
  if (sk.state === "absent") acts.push(h("button", { class: "btn primary", onclick: () => install(false, addRules.checked, "Installed the hopsesh skill") }, "Install the skill"));
  if (sk.state === "stale" || sk.state === "broken") acts.push(h("button", { class: "btn primary", onclick: () => install(false, addRules.checked, "Updated the hopsesh skill") }, sk.state === "broken" ? "Repair" : "Update"));
  if (sk.state === "modified") acts.push(h("button", { class: "btn", onclick: () => install(true, false, "Replaced it; your version is kept as a backup") }, "Replace with hopsesh's version"));
  if (sk.state === "foreign") acts.push(h("button", { class: "btn", onclick: () => install(true, addRules.checked, "Replaced it; the old one is kept as a backup") }, "Replace it (keeps a backup)"));
  if (sk.state && sk.state !== "absent" && sk.state !== "foreign") acts.push(h("button", { class: "btn", onclick: () => run(() => api("RemoveSkill", sk.state === "modified"), "Removed the hopsesh skill") }, "Remove"));
  acts.push(h("button", { class: "btn", onclick: preview }, "What it tells your agents"));
  const missing = rules.filter((r) => !r.present);
  return [card(
    h("div", { class: "set-row" }, title("The hopsesh skill", "Teaches each installed agent to find your sessions, hop one here, continue it in another agent or send it on. It shows you the plan and acts only after you say yes."),
      chip(SKILL_TEXT[sk.state] || [sk.state, ""])),
    copies.map((cp) => h("div", { class: "item" }, h("span", { class: "badge " + (cp.status.state === "current" ? "ok" : cp.status.state === "absent" ? "warn" : "err") }, cp.status.state === "current" ? "✓" : "!"),
      h("div", { style: "display:flex;flex-direction:column;gap:2px;min-width:0" }, h("span", {}, (cp.agents || []).join(", ") + " · " + (SKILL_TEXT[cp.status.state] || [cp.status.state])[0]),
        h("span", { class: "muted mono", style: "font-size:11px;overflow-wrap:anywhere" }, cp.dir),
        cp.status.state === "stale" ? h("span", { class: "muted", style: "font-size:12px" }, `Installed by hopsesh ${cp.status.installedVersion || "(older)"}; this is ${s.version}.`) : null,
        cp.status.state === "modified" ? h("span", { class: "muted", style: "font-size:12px" }, `You edited ${(cp.status.changedFiles || []).join(", ")}, so hopsesh leaves it alone.`) : null))),
    ["absent", "stale", "broken", "foreign"].includes(sk.state) ? h("label", { class: "opt" }, addRules,
      h("span", {}, h("b", {}, "Let agents run read-only hopsesh commands without asking"), h("span", { class: "muted" }, "Listing and planning; hopping a session always asks. Adds rules to each agent's settings."))) : null,
    rules.length && !missing.length ? h("span", { class: "muted", style: "font-size:12px" }, "Read-only hopsesh commands are allowed in " + rules.map((r) => r.agent).join(" and ") + "; hops ask.")
      : missing.length && (sk.state === "current" || sk.state === "modified") ? h("div", { class: "set-row" },
        h("span", { class: "muted", style: "font-size:12px" }, missing.map((r) => r.agent).join(" and ") + " ask you before every hopsesh command."),
        h("button", { class: "btn", onclick: () => install(sk.state === "modified", true, "Agents can now list and plan without asking; hops still ask") }, "Allow read-only commands")) : null,
    h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, acts),
    h("span", { class: "muted mono", style: "font-size:11px" }, `runs ${s.skillBin}`))];
}

function cli() {
  const c = s.cli || {};
  const acts = [];
  if (c.cannotInstall && c.state !== "ours") acts.push(h("span", { class: "muted", style: "font-size:12px" }, c.cannotInstall));
  else if (["missing", "other-app", "dangling"].includes(c.state)) acts.push(h("button", { class: "btn primary", onclick: () => run(() => api("InstallCLI", false), "The hopsesh command now runs this app's version") },
    c.state === "missing" ? "Install command" : c.state === "dangling" ? "Repair" : "Use this app's version"));
  else if (c.state === "standalone" || c.state === "foreign") acts.push(h("button", { class: "btn", onclick: () => run(() => api("InstallCLI", true), sys.win ? "New terminal windows now run this app's hopsesh" : "Replaced; the previous one is kept as a backup") },
    sys.win ? "Use this app's first" : "Replace with this app's (keeps a backup)"));
  if (["ours", "other-app", "dangling"].includes(c.state)) acts.push(h("button", { class: "btn", onclick: () => run(() => api("UninstallCLI"), "Removed the hopsesh command") }, "Uninstall command"));
  return [card(
    h("div", { class: "set-row" }, title("The hopsesh command", `${cliHow()}, so any terminal, your agents and your other machines can run it. It updates with the app.`), chip(CLI_TEXT[c.state] || [c.state, ""])),
    c.target && !sys.win ? h("span", { class: "muted mono", style: "font-size:11px;overflow-wrap:anywhere" }, `${c.path} → ${c.target}`) : null,
    h("div", { style: "display:flex;gap:8px;flex-wrap:wrap;align-items:center" }, acts),
    !c.dirOnPath && !sys.win ? h("div", { class: "item" }, h("span", { class: "badge warn" }, "!"),
      h("div", { style: "display:flex;flex-direction:column;gap:6px;min-width:0" }, h("span", {}, "~/.local/bin is not on your PATH"),
        h("span", { class: "muted", style: "font-size:12px" }, c.pathAdded ? `hopsesh added it to ${c.profile}; new terminal windows pick it up.` : `Add this line to ${c.profile}, or let hopsesh add it:`),
        c.pathAdded ? null : h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, h("div", { class: "term", style: "flex:1 1 260px" }, c.pathLine),
          h("button", { class: "btn", onclick: async () => { await api("CopyText", c.pathLine); toast("Copied"); } }, "Copy"),
          h("button", { class: "btn", onclick: () => run(() => api("AddCLIToPath"), "Added; open a new terminal window") }, "Add it for me")))) : null,
    c.resolves ? h("span", { class: "muted", style: "font-size:12px" }, `In ${sys.terminal}, hopsesh runs `, h("span", { class: "mono" }, c.resolves)) : null)];
}

function updates() {
  const u = state.update;
  return [card(
    h("div", { class: "set-row" }, title(`hopsesh ${s.version}`, u?.newer ? `hopsesh ${u.latest} is available.` : u ? "This is the newest version." : ""),
      u?.newer && u.canInstall ? h("button", { class: "btn primary", onclick: (ev) => installUpdate(ev.currentTarget, u) }, "Install and restart") : null,
      u?.newer ? h("button", { class: u.canInstall ? "btn" : "btn primary", onclick: () => api("OpenURL", u.url).catch(fail) }, "See what's new") : null,
      s.updateCheck === "on" ? h("button", { class: "btn", onclick: async () => { try { state.update = await api("CheckUpdate"); } catch (e) { fail(e); } render(); } }, "Check now") : null),
    h("label", { class: "opt" }, h("input", { type: "checkbox", checked: s.updateCheck === "on", onchange: (e) => save({ updateCheck: e.target.checked ? "on" : "off" }) }),
      h("span", {}, h("b", {}, "Check GitHub once a day for new versions"), h("span", { class: "muted" }, "Only the release list is fetched; nothing about you is sent."))))];
}

// installUpdate installs the new version over this app (the backend verifies it first),
// which then reopens.
async function installUpdate(btn, u) {
  const n = running().length;
  if (n && !await ask({ title: `Restart and end ${count(n, "program")}?`, body: "Installing the update restarts hopsesh, which ends every program in its terminal tabs. Claude Code and Codex keep each conversation up to its last finished turn.", ok: "Install and restart", danger: true })) return;
  btn.disabled = true;
  fill(btn, `Downloading and checking ${u.latest}…`);
  try {
    await api("InstallUpdate");
    fill(btn, "Restarting…");
  } catch (e) {
    fail(e);
    btn.disabled = false;
    fill(btn, "Install and restart");
  }
}

async function preview() {
  try {
    const text = await api("SkillPreview");
    const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, "What the skill tells your agents"),
      h("div", { class: "brief", style: "max-height:60vh" }, text),
      h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Close")));
  } catch (e) { fail(e); }
}

const TABS = [["general", "General", general], ["agents", "Agents", agents], ["terminal", "Terminal", terminal], ["skill", "Skill", skill], ["cli", "Command line", cli], ["updates", "Updates", updates]];

function render() {
  const [, name, body] = TABS.find((t) => t[0] === tab);
  fill(view, h("div", { class: "three" },
    h("nav", { class: "tabs", role: "tablist", "aria-label": "Settings", "aria-orientation": "vertical" },
      TABS.map(([id, label]) => h("button", { class: "tab", role: "tab", id: "tab-" + id, "aria-selected": tab === id ? "true" : "false", tabindex: tab === id ? "0" : "-1",
        onclick: () => { tab = id; render(); view.querySelector("#tab-" + id).focus(); },
        onkeydown: (e) => {
          const i = TABS.findIndex((t) => t[0] === tab);
          const n = e.key === "ArrowDown" ? i + 1 : e.key === "ArrowUp" ? i - 1 : -1;
          if (n >= 0 && n < TABS.length) { e.preventDefault(); tab = TABS[n][0]; render(); view.querySelector("#tab-" + tab).focus(); }
        } }, label)),
      h("span", { class: "spacer" }),
      h("button", { class: "btn", onclick: () => go("sessions") }, "Back to sessions")),
    h("div", { class: "page", role: "tabpanel", "aria-labelledby": "tab-" + tab }, h("div", { class: "page-in", style: "max-width:760px" }, h("h1", {}, name), body()))));
}

async function load() {
  try { s = await api("Settings"); } catch (e) { fail(e); return; }
  if (current !== "settings") return;
  render();
  api("TerminalSettings").then((t) => { ts = t; if (tab === "terminal" && current === "settings") render(); }).catch(() => {});
}

screen("settings", async (which) => {
  if (which) tab = which;
  if (!s) loading("Reading the settings…");
  await load();
});
