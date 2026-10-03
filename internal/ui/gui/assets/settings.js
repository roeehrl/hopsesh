// The Settings screen, in tabs: General, Agents, Skill, Command line, Updates.
import { api, h, fill, view, state, screen, go, loading, toast, fail, dialog } from "./core.js";

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
  standalone: ["A separately installed hopsesh is there", "st-warn"],
  foreign: ["Points to a different program", "st-warn"],
};
const CAPS = {
  live: "sees open sessions", stop: "quits open sessions", mark: "marks copies left behind", account: "knows its account",
  sanitize: "moves between accounts", read: "continues in other agents", write: "takes sessions from other agents",
  "native-replay": "replays commands natively", integrate: "can use the hopsesh skill", fork: "can fork", "remote-control": "remote control",
  app: "desktop app", notify: "tells the old session", import: "has its own importer", "post-install": "registers moved sessions",
};

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
  return run(() => api("SaveSettings", Object.assign({ layout: s.layout, markMoved: s.markMoved, syncCode: s.syncCode, pushSource: s.pushSource, updateCheck: s.updateCheck || "off" }, patch)), "Saved");
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
    card(h("span", { class: "sec-h" }, "This Mac"),
      h("div", { class: "set-row" }, title("Receiving sessions", state.info.receive ? "On: your other machines can send sessions here." : "Off: this Mac refuses sessions sent from other machines."),
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
        title(a.name, a.version ? `${a.version} on this Mac${a.folder ? " · " + a.folder : ""}` : "Not installed on this Mac"),
        a.stability === "experimental" ? h("span", { class: "chip st-warn" }, "experimental") : null,
        h("button", { class: "switch", role: "switch", "aria-checked": a.enabled ? "true" : "false", "aria-label": `${a.name} on`,
          onclick: () => set(!a.enabled, a.remoteControl, a.import, a.enabled ? `${a.name} is off` : `${a.name} is on`) })),
      h("div", { style: "display:flex;gap:5px;flex-wrap:wrap" }, caps.map((c) => CAPS[c] || c).sort().map((c) => h("span", { class: "cap" }, c))),
      a.tested?.length ? h("span", { class: "muted", style: "font-size:12px" }, "Tested with " + a.tested.join(", ")) : null,
      a.enabled && caps.includes("remote-control") ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.remoteControl, onchange: (e) => set(true, e.target.checked, a.import, "Saved") }),
        h("span", {}, h("b", {}, `Turn on Remote Control for sessions hopped into ${a.name}`), h("span", { class: "muted" }, "Reach them from your phone or other machines. Needs the agent's own subscription login."))) : null,
      a.enabled && caps.includes("import") ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.import, onchange: (e) => set(true, a.remoteControl, e.target.checked, "Saved") }),
        h("span", {}, h("b", {}, `Continue in ${a.name} with its own importer`), h("span", { class: "muted" }, "Instead of hopsesh's conversion, when it can read the other agent; hopsesh still adds its briefing. The plan can change it each time."))) : null);
  });
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
  else if (c.state === "standalone" || c.state === "foreign") acts.push(h("button", { class: "btn", onclick: () => run(() => api("InstallCLI", true), "Replaced; the previous one is kept as a backup") }, "Replace with this app's (keeps a backup)"));
  if (["ours", "other-app", "dangling"].includes(c.state)) acts.push(h("button", { class: "btn", onclick: () => run(() => api("UninstallCLI"), "Removed the hopsesh command") }, "Uninstall command"));
  return [card(
    h("div", { class: "set-row" }, title("The hopsesh command in Terminal", "Links hopsesh into ~/.local/bin, so Terminal, your agents and your other machines can run it. It updates with the app."), chip(CLI_TEXT[c.state] || [c.state, ""])),
    c.target ? h("span", { class: "muted mono", style: "font-size:11px;overflow-wrap:anywhere" }, `${c.path} → ${c.target}`) : null,
    h("div", { style: "display:flex;gap:8px;flex-wrap:wrap;align-items:center" }, acts),
    !c.dirOnPath ? h("div", { class: "item" }, h("span", { class: "badge warn" }, "!"),
      h("div", { style: "display:flex;flex-direction:column;gap:6px;min-width:0" }, h("span", {}, "~/.local/bin is not on your PATH"),
        h("span", { class: "muted", style: "font-size:12px" }, c.pathAdded ? `hopsesh added it to ${c.profile}; new terminal windows pick it up.` : `Add this line to ${c.profile}, or let hopsesh add it:`),
        c.pathAdded ? null : h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, h("div", { class: "term", style: "flex:1 1 260px" }, c.pathLine),
          h("button", { class: "btn", onclick: async () => { await api("CopyText", c.pathLine); toast("Copied"); } }, "Copy"),
          h("button", { class: "btn", onclick: () => run(() => api("AddCLIToPath"), "Added; open a new terminal window") }, "Add it for me")))) : null,
    c.resolves ? h("span", { class: "muted", style: "font-size:12px" }, "In Terminal, hopsesh runs ", h("span", { class: "mono" }, c.resolves)) : null)];
}

function updates() {
  const u = state.update;
  return [card(
    h("div", { class: "set-row" }, title(`hopsesh ${s.version}`, u?.newer ? `hopsesh ${u.latest} is available.` : u ? "This is the newest version." : ""),
      u?.newer ? h("button", { class: "btn primary", onclick: () => api("OpenURL", u.url).catch(fail) }, "See what's new") : null,
      s.updateCheck === "on" ? h("button", { class: "btn", onclick: async () => { try { state.update = await api("CheckUpdate"); } catch (e) { fail(e); } render(); } }, "Check now") : null),
    h("label", { class: "opt" }, h("input", { type: "checkbox", checked: s.updateCheck === "on", onchange: (e) => save({ updateCheck: e.target.checked ? "on" : "off" }) }),
      h("span", {}, h("b", {}, "Check GitHub once a day for new versions"), h("span", { class: "muted" }, "Only the release list is fetched; nothing about you is sent."))))];
}

async function preview() {
  try {
    const text = await api("SkillPreview");
    const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, "What the skill tells your agents"),
      h("div", { class: "brief", style: "max-height:60vh" }, text),
      h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Close")));
  } catch (e) { fail(e); }
}

const TABS = [["general", "General", general], ["agents", "Agents", agents], ["skill", "Skill", skill], ["cli", "Command line", cli], ["updates", "Updates", updates]];

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
  render();
}

screen("settings", async (which) => {
  if (which) tab = which;
  if (!s) loading("Reading the settings…");
  await load();
});
