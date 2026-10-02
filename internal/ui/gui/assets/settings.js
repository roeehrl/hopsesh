// Settings screen: agents, the hopsesh skill, command-line tool, moving defaults, updates, this Mac.
import { h, api, toast, view, setTitlebar } from "./app.js";

const SKILL_TEXT = {
  absent: ["Not installed", ""],
  current: ["Installed · up to date", "ok"],
  stale: ["Out of date", "warn"],
  modified: ["Installed · edited by you", "warn"],
  foreign: ["Another skill named hopsesh", "warn"],
  broken: ["Damaged", "err"],
};

const CLI_TEXT = {
  missing: ["Not installed", ""],
  ours: ["Installed · runs this app's hopsesh", "ok"],
  "other-app": ["Points to another copy of hopsesh.app", "warn"],
  dangling: ["Broken link (the app moved or was removed)", "err"],
  standalone: ["A separately installed hopsesh is there", "warn"],
  foreign: ["Points to a different program", "warn"],
};

function pill(text, kind) {
  return h("span", { class: "pill" + (kind ? " " + kind : "") }, text);
}

function section(title, ...kids) {
  return h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, title)), h("div", { class: "sec" }, ...kids.filter(Boolean)));
}

function row(...kids) {
  return h("div", { class: "set-row" }, ...kids.filter(Boolean));
}

async function run(fn, done) {
  try {
    const r = await fn();
    if (done) toast(done);
    return r;
  } catch (e) {
    toast(String(e.message || e));
    return null;
  } finally {
    showSettings();
  }
}

export async function showSettings() {
  setTitlebar("settings");
  const s = await api("Settings");
  const sk = s.skill || {};
  const copies = sk.copies || [], rules = sk.rules || [];
  const [skText, skKind] = SKILL_TEXT[sk.state] || [sk.state, ""];
  const addRules = h("input", { type: "checkbox", checked: false });
  const install = (force, withRules, done) => run(() => api("InstallSkill", force, withRules), done);
  const skillActions = [];
  switch (sk.state) {
    case "absent":
      skillActions.push(h("button", { class: "btn primary", onclick: () => install(false, addRules.checked, "Installed the hopsesh skill") }, "Install the skill"));
      break;
    case "stale":
    case "broken":
      skillActions.push(h("button", { class: "btn primary", onclick: () => install(false, addRules.checked, "Updated the hopsesh skill") }, sk.state === "broken" ? "Repair" : "Update"));
      break;
    case "modified":
      skillActions.push(h("button", { class: "btn", onclick: () => install(true, false, "Replaced it; your version is kept as a backup") }, "Replace with hopsesh's version"));
      break;
    case "foreign":
      skillActions.push(h("button", { class: "btn", onclick: () => install(true, addRules.checked, "Replaced it; the old one is kept as a backup") }, "Replace it (keeps a backup)"));
      break;
  }
  if (sk.state && sk.state !== "absent" && sk.state !== "foreign") {
    skillActions.push(h("button", { class: "btn", onclick: () => run(() => api("RemoveSkill", sk.state === "modified"), "Removed the hopsesh skill") }, "Remove"));
  }
  skillActions.push(h("button", { class: "btn", onclick: previewSkill }, "What it tells your agents"));
  const missingRules = rules.filter((r) => !r.present);

  const c = s.cli || {};
  const [cliText, cliKind] = CLI_TEXT[c.state] || [c.state, ""];
  const cliActions = [];
  if (c.cannotInstall && c.state !== "ours") {
    cliActions.push(h("span", { class: "muted", style: "font-size:12px" }, c.cannotInstall));
  } else if (c.state === "missing" || c.state === "other-app" || c.state === "dangling") {
    cliActions.push(h("button", { class: "btn primary", onclick: () => run(() => api("InstallCLI", false), "The hopsesh command now runs this app's version") },
      c.state === "missing" ? "Install command" : c.state === "dangling" ? "Repair" : "Use this app's version"));
  } else if (c.state === "standalone" || c.state === "foreign") {
    cliActions.push(h("button", { class: "btn", onclick: () => run(() => api("InstallCLI", true), "Replaced; the previous one is kept as a backup") }, "Replace with this app's (keeps a backup)"));
  }
  if (c.state === "ours" || c.state === "other-app" || c.state === "dangling") {
    cliActions.push(h("button", { class: "btn", onclick: () => run(() => api("UninstallCLI"), "Removed the hopsesh command") }, "Uninstall command"));
  }

  const save = (patch) => run(() => api("SaveSettings", Object.assign({
    layout: s.layout, markMoved: s.markMoved, syncCode: s.syncCode, pushSource: s.pushSource, updateCheck: s.updateCheck || "off", receive: s.receive,
  }, patch)), "Saved");
  const toggle = (key, title, desc) => h("label", { class: "opt" },
    h("input", { type: "checkbox", checked: s[key], onchange: (e) => save({ [key]: e.target.checked }) }),
    h("span", {}, h("b", {}, title), h("span", { class: "muted" }, desc)));
  const agentRow = (a) => {
    const rc = (a.capabilities || []).includes("remote-control");
    return h("div", { class: "sec", style: "border-bottom:1px solid var(--line2)" },
      row(h("div", {}, h("span", { style: "font-weight:500" }, a.name), " ",
        a.stability === "experimental" ? pill("experimental", "warn") : null),
        h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.enabled,
          onchange: (e) => run(() => api("SetAgent", a.id, e.target.checked, a.remoteControl), e.target.checked ? `${a.name} is on` : `${a.name} is off`) }), "On")),
      rc && a.enabled ? h("label", { class: "opt" }, h("input", { type: "checkbox", checked: a.remoteControl,
        onchange: (e) => run(() => api("SetAgent", a.id, a.enabled, e.target.checked), "Saved") }),
        h("span", {}, h("b", {}, "Turn on Remote Control for sessions moved here"), h("span", { class: "muted" }, "Reach them from your phone or other machines. Needs the agent's own subscription login."))) : null);
  };

  view.replaceChildren(h("div", { class: "settings" },
    h("div", { class: "settings-col" },
      h("div", { class: "card" }, h("div", { class: "card-h" }, h("span", { class: "name" }, "Agents"),
        h("span", { class: "muted", style: "font-size:12px" }, "hopsesh reads and moves sessions of the agents that are on")),
        ...(s.agents || []).map(agentRow)),
      section("Let your agents use hopsesh",
        row(h("div", {}, h("div", { style: "font-weight:500" }, "The hopsesh skill"),
          h("div", { class: "muted", style: "font-size:12px" }, "Teaches each installed agent to find your sessions, bring one here or continue it in another agent. It shows you the plan and acts only after you say yes. Every agent gets the same files.")),
          pill(skText, skKind)),
        ...copies.map((cp) => h("div", { class: "item" }, h("span", { class: "badge " + (cp.status.state === "current" ? "ok" : cp.status.state === "absent" ? "warn" : "err") }, cp.status.state === "current" ? "✓" : "!"),
          h("div", {}, h("div", { style: "font-weight:500" }, (cp.agents || []).join(", ") + " · " + (SKILL_TEXT[cp.status.state] || [cp.status.state])[0]),
            h("div", { class: "muted mono", style: "font-size:11px" }, cp.dir),
            cp.status.state === "stale" ? h("div", { class: "muted", style: "font-size:12px" }, `Installed by hopsesh ${cp.status.installedVersion || "(older)"}; this is ${s.version}.`) : null,
            cp.status.state === "modified" ? h("div", { class: "muted", style: "font-size:12px" }, `You edited ${(cp.status.changedFiles || []).join(", ")}, so hopsesh leaves it alone.`) : null))),
        sk.state === "absent" || sk.state === "stale" || sk.state === "broken" || sk.state === "foreign" ? h("label", { class: "opt" }, addRules,
          h("span", {}, h("b", {}, "Let agents run read-only hopsesh commands without asking"), h("span", { class: "muted" }, "Listing and planning; moving or continuing a session always asks. Adds rules to each agent's settings."))) : null,
        rules.length && !missingRules.length ? h("div", { class: "muted", style: "font-size:12px" }, "Read-only hopsesh commands are allowed in " + rules.map((r) => r.agent).join(" and ") + "; moves ask.")
          : missingRules.length && (sk.state === "current" || sk.state === "modified") ? h("div", { style: "display:flex;gap:8px;align-items:center" },
            h("span", { class: "muted", style: "font-size:12px" }, missingRules.map((r) => r.agent).join(" and ") + " ask you before every hopsesh command."),
            h("button", { class: "btn", onclick: () => install(sk.state === "modified", true, "Agents can now list and plan without asking; moves still ask") }, "Allow read-only commands")) : null,
        h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" }, ...skillActions),
        h("div", { class: "muted mono", style: "font-size:11px" }, `runs ${s.skillBin}`)),
      section("Command-line tool",
        row(h("div", {}, h("div", { style: "font-weight:500" }, "The hopsesh command in Terminal"),
          h("div", { class: "muted", style: "font-size:12px" }, "Links hopsesh into ~/.local/bin, so Terminal (and your agents) can run it. It updates with the app.")),
          pill(cliText, cliKind)),
        c.target ? h("div", { class: "muted mono", style: "font-size:11px" }, `${c.path} → ${c.target}`) : null,
        h("div", { style: "display:flex;gap:8px;flex-wrap:wrap;align-items:center" }, ...cliActions),
        !c.dirOnPath ? h("div", { class: "item" }, h("span", { class: "badge warn" }, "!"),
          h("div", {}, h("div", { style: "font-weight:500" }, "~/.local/bin is not on your PATH"),
            h("div", { class: "muted", style: "font-size:12px" }, c.pathAdded ? `hopsesh added it to ${c.profile}; new terminal windows pick it up.` : `Add this line to ${c.profile}, or let hopsesh add it:`),
            c.pathAdded ? null : h("div", { style: "display:flex;gap:8px;margin-top:6px" }, h("div", { class: "term" }, c.pathLine),
              h("button", { class: "btn", onclick: async () => { await api("CopyText", c.pathLine); toast("Copied"); } }, "Copy"),
              h("button", { class: "btn", onclick: () => run(() => api("AddCLIToPath"), "Added; open a new terminal window") }, "Add it for me")))) : null,
        c.resolves ? h("div", { class: "muted", style: "font-size:12px" }, "In Terminal, hopsesh runs ", h("span", { class: "mono" }, c.resolves)) : null),
    ),
    h("div", { class: "settings-col" },
      section("Moving sessions",
        row(h("div", {}, h("div", { style: "font-weight:500" }, "Repos folder"), h("div", { class: "muted mono", style: "font-size:12px" }, s.reposDir)),
          h("button", { class: "btn", onclick: async () => { const d = await api("ChooseFolder", "Where should hopsesh clone repositories?"); if (d) run(() => api("SetReposDir", d), "Saved"); } }, "Change…")),
        row(h("span", {}, "Clone layout"), h("select", { onchange: (e) => save({ layout: e.target.value }) },
          h("option", { value: "flat", selected: s.layout === "flat" }, "<repos>/<name>"), h("option", { value: "ghq", selected: s.layout === "ghq" }, "<repos>/<host>/<owner>/<name>"))),
        toggle("markMoved", "Mark the copy left behind", "Its title becomes “↪ moved to <this Mac> · …” (or “continued in …”), so it isn't resumed by mistake."),
        toggle("syncCode", "Bring the code along", "Fetch the session's commit (from the other machine if it wasn't pushed) and fast-forward a clean checkout."),
        toggle("pushSource", "Push unpushed commits on the other machine first", "Off: commits are fetched straight from the other machine instead."),
        toggle("receive", "Receive sessions from your other machines", "hopsesh on a machine that reaches this one over SSH may send sessions here (hopsesh push). Only the session's files arrive; hopsesh here installs them and keeps its own undo.")),
      section("Updates",
        h("label", { class: "opt" }, h("input", { type: "checkbox", checked: s.updateCheck === "on", onchange: (e) => save({ updateCheck: e.target.checked ? "on" : "off" }) }),
          h("span", {}, h("b", {}, "Check GitHub once a day for new versions"), h("span", { class: "muted" }, `You have hopsesh ${s.version}.`)))),
      section("This Mac",
        s.localNetworkGated ? row(h("div", {}, h("div", { style: "font-weight:500" }, "Local network access"),
          h("div", { class: "muted", style: "font-size:12px" }, "macOS asks before hopsesh reaches machines on your local network. Machines on Tailscale don't need it.")),
          h("button", { class: "btn", onclick: () => api("OpenLocalNetworkSettings").catch(() => {}) }, "Privacy & Security…")) : null,
        row(h("span", { class: "muted", style: "font-size:12px" }, "Settings ", h("span", { class: "mono" }, s.configDir)), h("button", { class: "btn", onclick: () => api("Reveal", s.configDir) }, "Show")),
        row(h("span", { class: "muted", style: "font-size:12px" }, "Logs, undo and moves ", h("span", { class: "mono" }, s.stateDir)), h("button", { class: "btn", onclick: () => api("Reveal", s.stateDir) }, "Show"))),
    )));
}

async function previewSkill() {
  const text = await api("SkillPreview");
  const dlg = document.querySelector("#dlg"), body = document.querySelector("#dlg-body");
  body.replaceChildren(h("div", { style: "font-weight:600" }, "What the skill tells your agents"),
    h("pre", { class: "term", style: "max-height:60vh;overflow:auto;white-space:pre-wrap" }, text),
    h("div", { style: "display:flex;justify-content:flex-end" }, h("button", { class: "btn", value: "close" }, "Close")));
  dlg.showModal();
}
