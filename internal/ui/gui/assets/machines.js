// Settings › Machines and Settings › Clouds: every machine in one place (this one, the
// ones you added and the ones discovery found), as a list beside the selected machine's
// details: what hopsesh can do there, check by check, with the step that fixes each:
// hopsesh itself, the command, and each agent folder's hook and skill. This machine's page
// also holds its command, skill and protection hooks in full. hopsesh connects only to
// machines you added.
import { on, api, h, fill, icon, ICONS, view, state, screen, go, toast, fail, errText, cap, dialog, ask, machineStatus, sys, when, ago, current, rich, navigationID, loadError, bytes, hopMark } from "./core.js";
import { signIn, onSignedIn } from "./term.js";

// A sign-in tab ended well: the card shows the new check.
onSignedIn(() => { if (showing()) reload(); });

// The tab that shows machines sets these (Settings): whether it is showing, and how it
// draws itself again.
let showing = () => false, refresh = () => {};
export function bindMachines(isShowing, redraw) {
  showing = isShowing;
  // A scan can finish between pointerdown and click. Keep its result, but do not replace
  // the button the user is pressing or a dialog they are reading.
  refresh = () => {
    if (pointerHeld || document.querySelector("dialog[open], button:active")) {
      if (!renderTimer) renderTimer = setTimeout(() => { renderTimer = 0; refresh(); }, 100);
      return;
    }
    redraw();
  };
}
export let loadErr = null;

let data = null;
on('hopsesh:discovery',d=>{if(showing()&&!d.discovering&&!document.querySelector('dialog[open]'))reload().catch(fail)});
const scanning = new Set();
const scanErrors = new Map();
let pointerHeld = false, renderTimer = 0;
document.addEventListener("pointerdown", () => { pointerHeld = true; }, true);
window.addEventListener("pointerup", () => { pointerHeld = false; }, true);
window.addEventListener("pointercancel", () => { pointerHeld = false; }, true);


let read = 0, reading = false;
async function reload() {
  const n = ++read, visit = navigationID();
  reading = true;
  try {
    const result = await api("Machines");
    if (n !== read || visit !== navigationID()) return;
    data = result; loadErr = null;
    if (showing()) refresh();
  } catch (e) {
    if (n === read && visit === navigationID()) { loadErr = e; if (showing()) refresh(); }
  } finally { if (n === read) reading = false; }
}
// fresh reads the machines again once per visit to a tab that shows them (as the old
// Machines screen did each time it opened), keeping what it showed until then.
let visited = -1, statusVisited = -1;
function fresh(statusToo) {
  // A new visit reads again even while an older read runs: only the newest read counts.
  if (visited !== navigationID()) { visited = navigationID(); loadErr = null; reload(); }
  if (statusToo && statusVisited !== navigationID()) { statusVisited = navigationID(); if (statuses) loadStatus(); }
}

// retry is the load error's Try again: a new read, from nothing.
const retry = () => { loadErr = null; refresh(); return reload(); };
export { reload };

// after reloads what a change affects; machine changes also need a new scan.
async function after(rescan = true, machine = "") {
  if (rescan) state.stale = true;
  await Promise.all([api("Info").then((i) => { state.info = i; }).catch(fail), reload()]);
  if (machine) scanMachine(machine);
}

const failedScan = (m) => (m.scanned && m.status !== "ok") || scanErrors.has(m.name) || !!m.scan?.error;
const activeScan = (m) => scanning.has(m.name) || ["queued", "scanning"].includes(m.scan?.phase);
async function scanMachine(name) {
  if (scanning.has(name)) return;
  scanning.add(name);
  scanErrors.delete(name);
  if (showing()) refresh();
  try { await api("ScanMachine", name); state.scan=await api("ScanSnapshot"); state.stale = false; }
  catch (e) { scanErrors.set(name, errText(e)); }
  finally { scanning.delete(name); await reload(); loadStatus(true, name); }
}

// Keep a running scan visible across navigation without repeating network discovery.
let polling = false;
setInterval(async () => {
  if (!showing() || document.hidden || polling || !data) return;
  polling = true;
  try {
    const states = await api("MachineScans");
    let finished = false, changed = false;
    for (const m of data.machines) {
      const next = states?.[m.name];
      if (JSON.stringify(m.scan) !== JSON.stringify(next)) {
        finished ||= next?.phase === "done";
        m.scan = next;
        changed = true;
      }
    }
    if (finished) await reload();
    else if (changed && !document.querySelector("dialog[open]")) refresh();
  } catch { /* the next poll retries; the previous result stays */ }
  finally { polling = false; }
}, 1000);

// Added machines scan immediately. Healthy rows refresh every five minutes while
// Machines is visible; failed connections need an explicit Retry (no repeated prompts).
setInterval(() => {
  if (!showing() || document.hidden || !document.hasFocus() || document.querySelector("dialog[open]") || !data) return;
  for (const m of data.machines) {
    const last = Date.parse(m.scan?.finished || "") || 0;
    if (!activeScan(m) && !failedScan(m) && Date.now() - last >= 300000) scanMachine(m.name);
  }
}, 15000);

// storageLine says how much each agent's sessions take on a machine.
function storageLine(storage) {
  const shown = (storage || []).filter((u) => u.bytes > 0);
  if (!shown.length) return null;
  const total = shown.reduce((n, u) => n + u.bytes, 0);
  return h("span", { class: "muted storage", style: "font-size:12px", title: shown.map((u) => `${u.name}: ${u.sessions} session${u.sessions === 1 ? "" : "s"}, ${bytes(u.bytes)}`).join("\n") },
    `Sessions take ${bytes(total)}` + (shown.length > 1 ? `: ${shown.map((u) => `${u.name} ${bytes(u.bytes)}`).join(", ")}` : ""));
}

function statusCell(m) {
  if (activeScan(m)) return h("div", { role: "status", "aria-live": "polite", class: "scan-status" },
    h("span", {}, m.scan?.phase === "queued" ? "Queued…" : "Scanning…"),
    h("span", { class: "muted" }, m.scanned ? `Previous result: ${m.sessions} sessions` : "Connecting and reading sessions"));
  const error = scanErrors.get(m.name) || m.scan?.error;
  if (!m.scanned) return h("div", { role: "status", class: error ? "err" : "muted" }, error || "Not scanned yet");
  const [k, words] = machineStatus(m.status);
  const url = (m.error.match(/https:\/\/login\.tailscale\.com\/\S+/) || [])[0];
  return h("div", { style: "display:flex;flex-direction:column;gap:3px;min-width:0" },
    h("span", { style: "display:flex;gap:7px;align-items:center" }, h("span", { class: "dot " + k }), words),
    m.status === "ok" ? h("span", { class: "muted", style: "font-size:12px" }, [`${m.sessions} session${m.sessions === 1 ? "" : "s"}`, m.agents.join(", ")].filter(Boolean).join(" · ")) : null,
    m.status === "ok" ? storageLine(m.storage) : null,
    m.status === "ok" ? presenceLine(m) : null,
    m.status !== "ok" && m.hint ? h("span", { class: "muted", style: "font-size:12px" }, rich(cap(m.hint))) : null,
    error && error !== m.error ? h("span", { class: "err" }, error) : null,
    m.scan?.finished ? h("span", { class: "muted", style: "font-size:11px" }, "Last checked " + when(m.scan.finished)) : null,
    m.status !== "ok" && m.error && !url ? h("span", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, m.error) : null,
    url ? h("button", { class: "btn small", style: "align-self:flex-start", onclick: () => api("OpenURL", url).catch(fail) }, "Open the Tailscale sign-in") : null);
}

// presenceLine says how hopsesh works with a machine: running in its login session, there
// but not running, older, or not there (SSH only).
function presenceLine(m) {
  const [cls, words] = {
    login: ["ok", `hopsesh ${m.hopsesh} running there`],
    installed: ["warn", `hopsesh ${m.hopsesh} there, not in a login session`],
    older: ["warn", `hopsesh ${m.hopsesh} there · update needed`],
    ssh: ["warn", "SSH only: no hopsesh there"],
  }[m.presence] || [m.hopsesh ? "muted" : "warn", m.hopsesh ? `hopsesh ${m.hopsesh} there` : "SSH only: no hopsesh there"];
  return h("span", { class: cls, style: "font-size:12px" }, words);
}

// presenceBlock is what hopsesh can do on a machine, and the step that lets it do more.
function presenceBlock(m) {
  if (!m.scanned || m.status !== "ok" || !(m.can || []).length) return null;
  const why = {
    older: "Its hopsesh is older than this one, so sending sessions and running sessions there wait until it's updated. Reading sessions still works.",
    installed: `hopsesh is on ${m.name} but not running in a login session: nobody is logged in there, or it doesn't start at login yet. Until then it works over SSH only, as before.`,
    ssh: m.os === "darwin" ? `hopsesh reads ${m.name}'s sessions over SSH, but can't see who is signed in there (macOS keeps sign-ins in the Keychain, which is locked to SSH), protect originals left there, or run sessions there.`
      : `hopsesh reads ${m.name}'s sessions over SSH, but can't protect originals left there or run sessions there.`,
  }[m.presence];
  const action = { ssh: `Set up hopsesh on ${m.name}…`, older: `Update hopsesh on ${m.name}`, installed: `Set up hopsesh on ${m.name} again…` }[m.presence];
  return h("div", { class: "presence", "data-presence": m.presence, "aria-label": `What hopsesh can do on ${m.name}` },
    h("ul", { class: "can" }, m.can.map((c) => h("li", { class: c.ok ? "ok" : "no" }, h("span", { "aria-hidden": "true" }, c.ok ? "✓" : "✕"), h("span", {}, c.text)))),
    why ? h("p", { class: "muted", style: "margin:0;font-size:12.5px" }, why) : null,
    action ? h("div", {}, h("button", { class: "btn small" + (m.presence === "ssh" ? " primary" : ""), onclick: () => setupDialog(m) }, action)) : null);
}

// setupDialog explains setting hopsesh up on a machine, then does it, step by step.
function setupDialog(m) {
  const steps = h("ol", { class: "setup-steps", "aria-live": "polite" });
  const foot = h("div", { class: "dlg-foot" });
  const mac = m.os === "darwin", updating = m.presence === "older" || m.update || m.presence === "login";
  // What it does to each hopsesh there, read from the machine before asking.
  const plan = h("li", { class: "muted", role: "status" }, `Checking which hopsesh ${m.name} has…`);
  api("PlanUpdate", m.name).then((p) => {
    const items = p.steps.map((s) => h("li", { "data-step": s.do }, s.text, s.command ? [": ", h("span", { class: "mono" }, s.command)] : null));
    if (p.ends) items.push(h("li", { class: "warn" }, `Restarts hopsesh in the login session there, which ends the ${p.ends === 1 ? "terminal" : p.ends + " terminals"} it runs there. Claude Code and Codex keep each conversation up to its last finished turn.`));
    plan.replaceWith(...(items.length ? items : [h("li", {}, `Every hopsesh on ${m.name} is ${p.version} or newer already.`)]));
  }).catch(() => fill(plan, `Copies this hopsesh (${state.info?.version || "this version"}) to `, h("span", { class: "mono" }, "~/.hopsesh/bin"), ` on ${m.name} over SSH, or updates the ones there.`));
  const start = async () => {
    fill(foot, h("span", { class: "muted", role: "status" }, `Setting up hopsesh on ${m.name}…`));
    const off = on("hopsesh:machine-setup", (e) => {
      if (e.machine !== m.name) return;
      steps.append(stepItem(e.step));
    });
    try {
      const r = await api("SetUpMachine", m.name);
      fill(foot, h("span", { class: r.login ? "ok" : "warn", role: "status" }, r.login ? `hopsesh ${r.version} runs in the login session on ${m.name}.` : `hopsesh ${r.version} is set up on ${m.name}; it starts when someone logs in there.`),
        h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => d.close() }, "Done"));
      // Check the machine again now, however the dialog is closed.
      after(true, m.name);
    } catch (e) {
      fill(foot, h("span", { class: "err", role: "alert" }, errText(e)), h("span", { class: "spacer" }), h("button", { class: "btn", onclick: () => d.close() }, "Close"), h("button", { class: "btn primary", onclick: () => { fill(steps); start(); } }, "Try again"));
      // Part of it may be done (an app that couldn't be updated): show the machine as it is.
      if (steps.querySelector("li.done")) after(true, m.name);
    } finally { off?.(); }
  };
  fill(foot, h("button", { class: "btn", onclick: () => d.close() }, "Not now"), h("button", { class: "btn primary", onclick: start }, m.presence === "older" || m.update ? `Update hopsesh on ${m.name}` : `Set up hopsesh on ${m.name}`));
  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, m.presence === "older" || m.update ? `Update hopsesh on ${m.name}` : `Set up hopsesh on ${m.name}`),
    h("ul", { class: "setup-what" },
      plan,
      // Setting it up for the first time says what that means; an update says only what it changes.
      updating ? null : [
        h("li", {}, `Starts it in your login session on ${m.name} whenever you log in there${mac ? ", so it can read the Keychain and open apps" : ""}. `, h("span", { class: "mono" }, "hopsesh serve uninstall"), " there turns this off."),
        h("li", {}, `Lets ${m.name} receive the sessions you send it.`),
        h("li", {}, state.info?.setUpIntegrations ? `Adds the movement hooks and the skill there once hopsesh runs in the login session; nothing else on ${m.name} changes.` : `Asks before installing hooks or the skill there; nothing else on ${m.name} changes.`),
        mac ? h("li", {}, `Needs someone logged in to ${m.name}'s desktop for the Keychain and apps. Until then it works over SSH only.`) : null]),
    steps, foot);
}

function foundRow(f) {
  const online = f.online === null || f.online === undefined ? "" : f.online ? "online" : "offline";
  return h("div", { class: "mgrid found" },
    h("div", { style: "min-width:0" }, h("div", { style: "font-weight:500" }, f.name), h("div", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, f.destination + (f.os ? ` · ${f.os}` : ""))),
    h("span", { class: "muted", style: "font-size:12px" }, "Found via " + f.via.join(" and ")),
    h("span", { class: "muted", style: "font-size:12px" }, [online, f.owner ? `shared by ${f.owner}` : ""].filter(Boolean).join(" · ")),
    h("div", { style: "display:flex;justify-content:flex-end" }, h("button", { class: "btn small outline", onclick: async () => {
      if (f.owner && !await ask({ title: `Add ${f.name}?`, body: `It belongs to ${f.owner}. hopsesh would log in to it with your SSH setup and read its coding agents' session folders.`, ok: "Add it" })) return;
      try { await api("SetAllowed", f.name, f.destination, true); toast(`Added ${f.name}`); } catch (e) { fail(e); }
      await after(true, f.name);
    } }, "Add")));
}

// cloudCard is one of the agents' clouds: consent (turned on, as `hopsesh clouds allow`),
// the driver, the login, what comes back, and a read-only test.
function cloudCard(c) {
  const allow = h("button", { class: "switch", role: "switch", "aria-checked": c.allowed ? "true" : "false", "aria-label": `Turn on ${c.title}`,
    onclick: async () => {
      try { await api("SetCloudAllowed", c.name, !c.allowed); } catch (e) { fail(e); return; }
      toast(!c.allowed ? `${c.title} is on: hopsesh reaches it through ${c.driver}, signed in as you` : `${c.title} is off: hopsesh leaves it alone`);
      await after();
    } });
  const kv = (label, ...value) => [h("dt", {}, label), h("dd", {}, ...value)];
  const t = c.test;
  const signed = t ? (t.ok || t.account ? t.account : h("span", { class: "warn" }, t.error || "Not signed in"))
    : c.status === "signed-out" || c.status === "not-eligible" ? h("span", { class: "warn" }, rich(cap(c.hint || c.error))) : c.allowed ? h("span", { class: "muted" }, "Test it to see") : h("span", { class: "muted" }, "Not checked while it is off");
  const result = h("span", { style: "font-size:12px", role: "status" });
  const show = (r) => fill(result, h("b", { class: r.ok ? "ok" : "err", style: "font-weight:600" }, r.ok ? "✓ " : "✕ "),
    h("span", { class: r.ok ? "ok" : "err" }, rich((r.checks.length ? r.checks.map((x) => x.text).join(" · ") : r.error) + " · " + when(r.at))));
  if (t) show(t);
  const brings = c.fidelity === "native" ? `The whole conversation, copied by ${c.agentName}; it saves the copy once you send a message in it`
    : c.codeOnly ? (!(c.codeDown || []).length ? "Nothing yet: the conversation stays in the cloud"
      : `The code (${c.codeDown.includes("diff") ? "its patch, committed on a new branch" : "its branch"}); the conversation stays in the cloud for now`)
    : c.fidelity === "code" ? "The code, title and summary"
    : !(c.codeDown || []).length ? "The messages, as text (no code)" : "The messages, as text, and the code";
  const ways = { branch: "a hand-off branch", bundle: "an upload when the remote isn't GitHub", "starting-diff": "a starting diff for a few changes on a pushed branch" };
  const up = (c.codeUp || []).map((w) => ways[w]).filter(Boolean);
  const hosts = (c.hosts || []).map((x) => (x === "github.com" ? "GitHub" : x)).join(", ");
  return h("section", { class: "card cloud-card", "aria-labelledby": "cc-" + c.name },
    h("div", { class: "set-row" }, h("span", { style: "color:var(--cloud)" }, icon(ICONS.cloud, 16)), h("h3", { id: "cc-" + c.name, style: "margin:0;font-size:15px" }, c.title),
      c.experimental ? h("span", { class: "chip st-warn", title: `hopsesh's ${c.agentName} support is experimental: some of its command's output is not verified yet` }, "experimental") : null,
      h("span", { class: "spacer" }), h("span", { style: "font-size:12.5px;font-weight:500", "aria-hidden": "true" }, c.allowed ? "On" : "Off"), allow),
    h("dl", { class: "kv wide" },
      kv("Driver", c.version ? h("span", { class: "mono", style: "font-size:12px" }, `${c.driver} ${c.version}`) : !c.allowed ? h("span", { class: "muted" }, c.driver + " · not checked while this cloud is off") : h("span", { class: "warn" }, `${c.title} is reached through the `, h("span", { class: "mono" }, c.driver), " command, which isn't installed here"),
        c.version ? [" ", h("span", { class: "chip " + (c.tested ? "st-idle" : "st-warn") }, c.tested ? "tested" : "untested"), c.tested ? null : h("span", { class: "muted", style: "font-size:12px" }, ` hopsesh tested ${c.testedOn}`)] : null),
      kv("Signed in", signed),
      up.length ? kv("Code goes up as", up[0], up.length > 1 ? h("span", { class: "muted" }, " · " + up.slice(1).join(" · ")) : null,
        !(c.codeUp || []).includes("bundle") && hosts ? h("span", { class: "muted" }, ` · ${hosts} only`) : null) : null,
      kv("Brings back", brings),
      c.vendorPrefix ? kv("Branches", c.rename ? [h("span", { class: "mono", style: "font-size:12px" }, c.vendorPrefix + "…"), " kept here as ", h("span", { class: "mono", style: "font-size:12px" }, `hopsesh/from/${c.name}/…`)] : "Kept as the cloud names them") : null,
      kv("Listing", c.partial ? `Only what hopsesh started or brought here: ${c.agentName} has no list command` : `Every ${c.noun || "session"} the cloud lists`)),
    c.needsEnv ? envTable(c) : null,
    (c.limits || []).length ? h("div", { style: "display:flex;flex-direction:column;gap:2px" }, c.limits.map((l) => h("span", { class: "muted", style: "font-size:12px" }, rich(l)))) : null,
    h("div", { class: "set-row", style: "padding-top:10px;border-top:1px solid var(--line2)" },
      h("button", { class: "btn", disabled: !c.allowed, title: c.allowed ? "A read-only look: the login and the flags hopsesh uses" : "Turn it on first", onclick: async (ev) => {
        const btn = ev.currentTarget;
        btn.disabled = true;
        fill(result, h("span", { class: "muted" }, "Checking…"));
        try { await api("TestCloud", c.name); } catch (e) { fill(result, h("span", { class: "err" }, errText(e))); btn.disabled = false; return; }
        reload();
      } }, "Test"), result,
      c.signIn ? h("span", { class: "spacer" }) : null,
      c.signIn ? h("button", { class: "btn" + (signedOut(c) ? " primary" : ""), title: `Runs ${c.signIn} in a tab that records nothing`, onclick: () => signIn(c) }, signedOut(c) ? "Sign in" : "Sign in again") : null,
      c.signIn ? h("button", { class: "btn", title: `Runs ${c.signIn} in ${sys.terminal}`, onclick: () => signIn(c, "terminal") }, `Sign in using ${sys.terminal}`) : null),
    c.signIn ? h("span", { class: "muted", style: "font-size:12px" }, "Signing in runs ", h("span", { class: "mono" }, c.signIn), ": it happens in that command, never in hopsesh, and nothing in its tab is recorded.") : null);
}

// signedOut: the cloud's login is missing or failed its last check.
const signedOut = (c) => c.status === "signed-out" || (c.test && !c.test.ok && !c.test.account);

async function setEnv(c, r, v) {
  try { await api("SetCloudEnvironment", c.name, r.repo, v); } catch (e) { fail(e); return; }
  toast(v ? `${r.repo} runs in ${v}` : `${r.repo}: ask each time`);
  reload();
}

// otherEnv asks for an environment hopsesh has not seen a task use.
function otherEnv(c, r) {
  const field = h("input", { id: "env-other", class: "field", placeholder: "Environment id or name", style: "width:100%" });
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `${c.title} environment for ${r.repo}`),
    h("label", { for: "env-other", class: "muted", style: "font-size:12.5px" }, "Its id or its name, as Codex shows it."), field,
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => { const v = field.value.trim(); if (!v) return; d.close(); await setEnv(c, r, v); } }, "Save")));
  field.focus();
}

// envTable is a cloud's environment per repository (Codex cloud runs every task in one):
// what each repository's hand-offs run in, or "Ask each time".
function envTable(c) {
  const choices = c.envs || [];
  const row = (r) => {
    const cell = r.unsupported ? h("td", { class: "muted" }, r.unsupported) : (() => {
      const known = choices.map((e) => ({ value: e.value, label: e.name }));
      if (r.env && !known.some((e) => e.value === r.env || e.label === r.env)) known.unshift({ value: r.env, label: r.env });
      const id = "env-" + r.repo.replace(/[^a-z0-9]+/gi, "-");
      const sel = h("select", { id, onchange: async (ev) => {
        const v = ev.target.value;
        if (v === "__other__") { ev.target.value = r.env || ""; otherEnv(c, r); return; }
        await setEnv(c, r, v);
      } }, h("option", { value: "", selected: !r.env }, "Ask each time"),
        known.map((e) => h("option", { value: e.value, selected: e.value === r.env || e.label === r.env }, e.label)),
        h("option", { value: "__other__" }, "Other…"));
      return h("td", {}, h("label", { for: id, class: "sr-only" }, `Environment for ${r.repo}`), sel);
    })();
    return h("tr", {}, h("td", { class: "mono" }, r.repo), cell);
  };
  return h("div", { style: "display:flex;flex-direction:column;gap:6px" },
    h("span", { style: "font-size:12px;font-weight:500" }, "Environment per repository"),
    (c.repos || []).length ? h("div", { class: "env-table" }, h("table", { "aria-label": `${c.title} environment per repository` },
      h("thead", {}, h("tr", {}, h("th", {}, "Repository"), h("th", {}, "Environment"))),
      h("tbody", {}, c.repos.map(row))))
      : h("span", { class: "muted", style: "font-size:12px" }, "No repository yet: hopsesh asks for one when you hand a session off."),
    c.envHint ? h("span", { class: "muted", style: "font-size:11.5px" }, rich(`No environment yet? ${cap(c.envHint)}.`)) : null);
}

function trustDialog(name) {
  const progress = h("div", { class: "loading", role: "status" }, `Fetching ${name}'s host keys…`);
  const d = dialog(progress, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"));
  return api("ScanKeys", name).then((k) => {
    if (!d.open || !progress.isConnected) return;
    dialog(
      h("h2", { style: "margin:0;font-size:16px" }, `Host keys of ${name}`),
      h("div", { class: "mono muted", style: "font-size:11px" }, k.address),
      k.fingerprints.map((f) => h("div", { class: "mono", style: "font-size:12px;overflow-wrap:anywhere" }, f)),
      k.verified ? h("div", { class: "ok" }, "✓ " + k.verified)
        : h("div", { class: "warn", style: "font-size:12.5px;line-height:1.5" }, "Not independently verified. Compare with ", h("span", { class: "mono" }, "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"), " on that machine."),
      h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
        h("button", { class: "btn primary", onclick: async () => { try { await api("TrustHost", name); toast(`${name} trusted`); } catch (e) { fail(e); } d.close(); return after(true, name); } }, "Trust these keys")));
  }).catch((e) => { if (d.open && progress.isConnected) dialog(loadError(e, () => trustDialog(name)), h("button", { class: "btn", onclick: () => d.close() }, "Close")); });
}

function addDialog() {
  const name = h("input", { class: "field", id: "add-name", placeholder: "build-box" });
  const dest = h("input", { class: "field mono", id: "add-dest", placeholder: "me@10.0.0.5 or an ssh alias" });
  const pw = h("input", { type: "checkbox" });
  const remember = h("input", { type: "checkbox", checked: true });
  const rememberRow = h("label", { class: "opt", style: "margin-left:24px", hidden: true }, remember, h("span", {}, `Remember it in ${sys.vault}`));
  const msg = h("div", { class: "err", role: "alert" });
  pw.onchange = () => { rememberRow.hidden = !pw.checked; };
  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, "Add a machine"),
    h("label", { for: "add-name", style: "font-size:12.5px" }, "Name"), name,
    h("label", { for: "add-dest", style: "font-size:12.5px" }, "SSH destination"), dest,
    h("label", { class: "opt" }, pw, h("span", {}, h("b", {}, "It logs in with a password"), h("span", { class: "muted" }, "hopsesh asks for it when it connects."))), rememberRow, msg,
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => {
        if (!name.value.trim() || !dest.value.trim()) { msg.textContent = "Give it a name and an SSH destination."; return; }
        try { await api("AddHost", name.value.trim(), dest.value.trim(), pw.checked, remember.checked); } catch (e) { msg.textContent = errText(e); return; }
        d.close(); toast(`Added ${name.value.trim()}`); return after(true, name.value.trim());
      } }, "Add")));
  name.focus();
}

// loginDialog chooses how hopsesh logs in: keys (the default) or a password, and offers to
// switch a password machine to key login for good.
function loginDialog(m) {
  const key = h("input", { type: "radio", name: "auth", checked: m.auth !== "password" });
  const pass = h("input", { type: "radio", name: "auth", checked: m.auth === "password" });
  const remember = h("input", { type: "checkbox", checked: m.auth === "password" ? m.keychain : m.canRemember });
  const rememberRow = h("label", { class: "opt", style: "margin-left:24px", hidden: m.auth !== "password" || !m.canRemember }, remember, h("span", {}, `Remember it in ${sys.vault}`));
  const sync = () => { rememberRow.hidden = !pass.checked || !m.canRemember; };
  key.onchange = sync; pass.onchange = sync;
  const msg = h("div", { class: "muted", style: "font-size:12px;line-height:1.5", role: "status" });
  const setup = async (createKey) => {
    msg.className = "muted";
    fill(msg, `Logging in to ${m.name} once with its password to add ${sys.here}'s SSH key…`);
    try {
      const r = await api("SetupKeyLogin", m.name, createKey);
      d.close();
      toast(r.created ? `Created ${r.publicKey}; ${m.name} now logs in with it` : `${m.name} now logs in with your key`);
      await after(true, m.name);
    } catch (e) {
      if (errText(e).includes("no-key")) {
        msg.className = "warn";
        fill(msg, `${sys.Here} has no SSH key that ssh would use for this machine. `, h("button", { class: "btn small", onclick: () => setup(true) }, "Create ~/.ssh/id_ed25519 and continue"));
      } else { msg.className = "err"; fill(msg, errText(e)); }
    }
  };
  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, `How hopsesh logs in to ${m.name}`),
    h("label", { class: "opt" }, key, h("span", {}, h("b", {}, "SSH key or agent"), h("span", { class: "muted" }, "Recommended."))),
    h("label", { class: "opt" }, pass, h("span", {}, h("b", {}, "Password"), h("span", { class: "muted" }, "hopsesh asks for it when it connects."))),
    rememberRow,
    m.auth === "password" ? h("div", { class: "muted", style: "font-size:12px;line-height:1.5" },
      `Set up key login adds ${sys.here}'s public SSH key to the machine (one password login), checks that it works, then stops using the password.`) : null,
    msg,
    h("div", { class: "dlg-foot" },
      m.auth === "password" && m.keychain ? h("button", { class: "btn", onclick: async () => { try { await api("ForgetPassword", m.name); toast("Forgot the remembered password"); } catch (e) { fail(e); } d.close(); return after(); } }, "Forget password") : null,
      m.auth === "password" ? h("button", { class: "btn", onclick: () => setup(false) }, "Set up key login") : null,
      h("span", { class: "spacer" }),
      h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => {
        try { await api("SetAuth", m.name, pass.checked ? "password" : "key", remember.checked); } catch (e) { fail(e); return; }
        d.close(); return after(true, m.name);
      } }, "Save")));
}

// The old Machines screen opens Settings at Machines (or Clouds).
screen("machines", (at, pick) => go("settings", at === "clouds" ? "clouds" : "machines", pick));

// ---- What hopsesh can do on each machine (MachineStatuses) ------------------------------
let statuses = null, statusBusy = false, statusErr = "", statusAt = 0;
const fixing = new Map(); // machine → "installing" while its hooks and skill are set up

// loadStatus reads every machine's checks (refresh: hooks, skill and command again, now).
// Asked while a read is running, it reads again once that one ends, so a change made
// meanwhile (an update, an install) is never left showing its old state.
let statusAgain = null;
export async function loadStatus(refreshAll = false) {
  if (refreshAll) remote.clear(); // Check again reads the Remote Control setting again too
  if (statusBusy) { statusAgain = statusAgain || refreshAll; return; }
  statusBusy = true; statusErr = "";
  if (showing()) refresh();
  try { statuses = await api("MachineStatuses", refreshAll); statusAt = Date.now(); }
  catch (e) { statusErr = errText(e); }
  finally {
    statusBusy = false;
    if (statusAgain !== null) { const again = statusAgain; statusAgain = null; loadStatus(again); return; }
    if (showing()) refresh();
  }
}
const statusOf = (name, local) => (statuses || []).find((s) => (local ? s.local : s.machine === name));
const SYM = { ok: "✓", warn: "!", error: "✕", off: "○", unknown: "?" };
// symOf is a check's symbol: an update is ↑, anything else needing attention !.
const symOf = (c) => (c.state === "warn" && c.fix === "update" ? "↑" : SYM[c.state] || "?");
const markOf = (st) => st ? { hopsesh: st.version, receiving: st.receiving?.state === "ok", newest: ["warn", "error"].includes(st.hopsesh?.state) && st.hopsesh?.fix === "update" ? st.newest : "" } : null;

// badge sums a machine up in the list: all set, what is left, or why it is gray.
function badge(st, m) {
  if (m && activeScan(m)) return h("span", { class: "mbadge off", role: "status" }, "checking…");
  if (!st) return h("span", { class: "mbadge off" }, statusBusy ? "checking…" : "not checked");
  if (st.reach.state === "error") return h("span", { class: "mbadge err" }, "not reached");
  if (!st.version) return h("span", { class: "mbadge off" }, "SSH only");
  const n = todoCount(st);
  return n ? h("span", { class: "mbadge warn" }, `${n} to do`) : h("span", { class: "mbadge ok" }, "all set");
}

// fixButton is the step that puts a check right, where hopsesh can do it from here.
function fixButton(st, c, m, label) {
  const name = st.machine, settable = st.local || st.login?.state === "ok";
  switch (c.fix) {
    case "setup": case "update":
      return m ? h("button", { class: "btn small" + (c.fix === "setup" ? " primary" : ""), onclick: () => setupDialog({ ...m, update: c.fix === "update" }) }, label || (c.fix === "update" ? `Update to ${st.newest}` : "Set up hopsesh…")) : null;
    case "align":
      return st.local ? h("button", { class: "btn small primary", onclick: () => alignDialog(st) }, "Update them…") : null;
    case "receive":
      return st.local ? h("button", { class: "btn small", onclick: async () => { try { await api("SetReceive", true); toast(`${sys.Here} now receives sessions`); } catch (e) { fail(e); } await after(false); loadStatus(); } }, "Turn on")
        : h("span", { class: "muted", style: "font-size:12px" }, "On ", name, ": ", h("span", { class: "mono" }, "hopsesh receive on"));
    case "serve":
      return st.local ? h("button", { class: "btn small primary", onclick: async (ev) => {
        ev.currentTarget.disabled = true;
        try { await api("StartAtLogin"); toast(`hopsesh now starts at login on ${sys.here} and receives sessions`); } catch (e) { fail(e); }
        await after(false); loadStatus(true);
      } }, "Start at login") : null;
    case "cli":
      return st.local ? h("button", { class: "btn small", onclick: () => view.querySelector("#here-setup")?.scrollIntoView({ behavior: "smooth", block: "start" }) }, "Install…")
        : h("span", { class: "muted", style: "font-size:12px" }, `Add its folder to your PATH on ${name}`);
    case "hooks": case "skill":
      return settable ? h("button", { class: "btn small", disabled: fixing.has(name), onclick: () => fixMachines([st]) }, fixing.has(name) ? "Installing…" : label || (c.state === "warn" ? "Update" : "Install"))
        : h("span", { class: "muted", style: "font-size:12px" }, "once hopsesh runs in the login session there");
    case "approve":
      return h("span", { class: "muted", style: "font-size:12px" }, c.reason || "Approve it in the agent");
  }
  return null;
}

// fixMachines sets up the hooks and skill on each machine hopsesh can reach for it; the
// others are reported as waiting, with why. Partial success is a normal outcome.
export async function fixMachines(list) {
  const run = { done: [], failed: [], skipped: [] };
  await Promise.all(list.map(async (st) => {
    if (!(st.local || st.login?.state === "ok")) { run.skipped.push({ machine: st.machine, why: st.reach.state === "error" ? "not reached" : "hopsesh isn't running in the login session there" }); return; }
    fixing.set(st.machine, "installing"); refresh();
    try { await api("SetUpProtection", st.local ? "" : st.machine, false); run.done.push(st.machine); }
    catch (e) { run.failed.push({ machine: st.machine, why: errText(e) }); }
    finally { fixing.delete(st.machine); }
  }));
  const total = run.done.length + run.failed.length + run.skipped.length;
  const why = [...run.failed, ...run.skipped].map((f) => `${f.machine}: ${f.why}`).join("; ");
  if (run.failed.length || run.skipped.length) toast(`Hooks and skill set up on ${run.done.length} of ${total} machine${total === 1 ? "" : "s"}. ${why}.`);
  else toast(`Hooks and skill set up on ${total === 1 ? run.done[0] : total + " machines"}`);
  await loadStatus(true);
}

// installList is every hopsesh on a machine: where, what it is, its version, and which one
// the machine runs.
const KIND = { app: "the app", command: "installed by you", own: "set up by hopsesh", package: "" };
function installList(l) {
  const tilde = (p) => (l.home && p.startsWith(l.home + (l.os === "windows" ? "\\" : "/")) ? "~" + p.slice(l.home.length) : p);
  return h("ul", { class: "installs", "aria-label": "Copies of hopsesh" }, l.installs.map((i) => h("li", {},
    h("span", { class: "mono" }, tilde(i.bundle || i.path)),
    h("span", { class: "muted" }, [i.version || "version unknown", i.kind === "package" ? `from ${i.manager}` : KIND[i.kind],
      i.main ? "the one this machine runs" : "", i.terminal ? "your terminal runs it" : "", i.service ? "starts at login" : "", i.open ? "open" : "",
      ...(i.links || []).map((x) => "also " + tilde(x))].filter(Boolean).join(" · ")))));
}

// alignDialog brings this machine's other copies of hopsesh to this version, step by step.
function alignDialog(st) {
  const steps = h("ol", { class: "setup-steps", "aria-live": "polite" });
  const foot = h("div", { class: "dlg-foot" });
  const start = async () => {
    fill(foot, h("span", { class: "muted", role: "status" }, "Updating…"));
    const off = on("hopsesh:machine-setup", (e) => { if (e.machine === st.machine) steps.append(stepItem(e.step)); });
    try {
      await api("AlignHere");
      fill(foot, h("span", { class: "ok", role: "status" }, `Every hopsesh on ${sys.here} is ${state.info?.version} now.`), h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => d.close() }, "Done"));
      loadStatus(true);
    } catch (e) {
      fill(foot, h("span", { class: "err", role: "alert" }, errText(e)), h("span", { class: "spacer" }), h("button", { class: "btn", onclick: () => d.close() }, "Close"));
      loadStatus(true);
    } finally { off?.(); }
  };
  fill(foot, h("button", { class: "btn", onclick: () => d.close() }, "Not now"), h("button", { class: "btn primary", onclick: start }, "Update them"));
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, `Bring every hopsesh on ${sys.here} to ${state.info?.version || "this version"}`),
    h("p", { class: "muted", style: "margin:0;font-size:13px" }, "They share hopsesh's settings and history here, so an older one can misread what a newer one wrote. hopsesh updates the copies it may replace (the app with its own updater, which checks the release's signature) and leaves one a package manager owns to it."),
    installList(st.installs), steps, foot);
}

const stepItem = (s) => h("li", { class: s.state }, h("b", {}, { done: "✓", skipped: "–", waiting: "…", failed: "✕" }[s.state] || "·", " ", s.name), s.detail ? h("span", { class: "muted" }, " · " + s.detail) : null);

const line = (c, label, extra) => h("div", { class: "mline", "data-check": label },
  h("span", { class: "msym " + c.state, "aria-hidden": "true" }, symOf(c)),
  h("span", { class: "mlabel" }, label),
  h("span", { class: "mtext" }, c.text === "–" ? "—" : c.text, c.reason ? h("span", { class: "muted" }, " · " + c.reason) : null, h("span", { class: "sr-only" }, ` (${c.state})`)),
  h("span", { class: "mfix" }, extra || null));

// ---- Settings › Machines ------------------------------------------------------------------
let pick = "";
export function pickMachine(name) { if (name) pick = name; }

// extras.here are this machine's own sections (its command, skill and protection hooks).
export function machinesPanel(extras = {}) {
  fresh(true);
  if (!data && !loadErr && !reading) reload();
  if (!statuses && !statusBusy && !statusErr) loadStatus();
  if (loadErr) return [loadError(loadErr, retry)];
  if (!data) return [h("div", { class: "loading", role: "status" }, "Looking for your machines (nothing is contacted)…")];
  const d = data;
  const rows = [{ name: d.here.name, title: sys.Here, local: true, sub: "this machine" }, ...d.machines.map((m) => ({ name: m.name, title: m.name, m }))];
  if (!rows.some((r) => r.name === pick || r.local && pick === "here") && !d.found.some((f) => f.name === pick)) pick = rows[0].name;
  const sel = rows.find((r) => r.name === pick || r.local && pick === "here") || null;
  const foundSel = !sel && d.found.find((f) => f.name === pick);
  const newest = (statuses || []).find((s) => s.newest)?.newest;
  const behind = (statuses || []).filter((s) => s.hopsesh?.fix === "update" && s.hopsesh.state !== "ok");
  const list = h("ul", { class: "mlist", role: "listbox", "aria-label": "Machines" },
    rows.map((r) => {
      const st = statusOf(r.name, r.local);
      return h("li", {}, h("button", { class: "mrow", role: "option", "aria-selected": String(sel === r), "data-machine": r.name, onclick: () => { pick = r.name; refresh(); } },
        h("span", { class: "mrow-mark" }, hopMark(markOf(st)) || h("span", { class: "muted", "aria-hidden": "true" }, "–")),
        h("span", { class: "mrow-name" }, r.title, r.local ? h("span", { class: "muted", style: "font-size:11.5px" }, " " + d.here.name) : null),
        badge(st, r.m),
        h("span", { class: "mrow-sub" }, st?.version ? [`${st.version}`, st.hopsesh.state !== "ok" && st.hopsesh.fix === "update" ? " ↑" : "", " · ", st.receiving.state === "ok" ? "receiving" : "not receiving"] : r.m ? (r.m.status && r.m.status !== "ok" ? machineStatus(r.m.status)[1] : "no hopsesh") : "")));
    }));
  const found = d.found.length ? h("details", { class: "mfound", open: !!foundSel },
    h("summary", {}, `Found on your network: ${d.found.length}`),
    h("ul", { class: "mlist", role: "listbox", "aria-label": "Found on your network" }, d.found.map((f) => h("li", {}, h("button", { class: "mrow found", role: "option", "aria-selected": String(foundSel === f), onclick: () => { pick = f.name; refresh(); } },
      h("span", { class: "mrow-mark muted", "aria-hidden": "true" }, "+"), h("span", { class: "mrow-name" }, f.name), h("span", { class: "mbadge off" }, f.via.join(", ")))))))
    : null;
  const master = h("div", { class: "mmaster" },
    h("div", { class: "mmaster-h" }, h("b", {}, "Machines"), newest ? h("span", { class: "muted", style: "font-size:12px" }, `newest hopsesh ${newest}`) : null),
    list,
    h("button", { class: "btn small", style: "align-self:flex-start", onclick: addDialog }, icon(ICONS.plus, 12), "Add by address…"),
    fixAll(),
    found,
    d.discoveryError ? h("p", { class: "warn", role: "status", style: "font-size:12px" }, d.discoveryError, ". SSH aliases are still shown.") : null);
  const detail = sel ? machineDetail(sel, d, extras) : foundSel ? foundDetail(foundSel) : null;
  return [
    behind.length ? h("div", { class: "mbanner", role: "status" }, h("span", { "aria-hidden": "true" }, "↑"),
      h("span", {}, `${behind.length === 1 ? "1 machine runs" : behind.length + " machines run"} an older hopsesh than ${newest}: ${behind.map((s) => `${s.machine} (${s.version})`).join(", ")}.`)) : null,
    statusErr ? h("p", { class: "err", role: "alert" }, statusErr) : null,
    h("div", { class: "msplit" }, master, h("section", { class: "mdetail", "aria-live": "polite" }, detail))];
}

// todoCount is the number of lines in a machine's Up next: one per check with a fix, and
// one for all its agent folders' hooks and skill.
function todoCount(st) {
  const n = [st.hopsesh, st.login, st.receiving, st.cli, copiesTodo(st)].filter((c) => c?.fix && c.state !== "ok").length;
  return n + (st.agents.some((a) => needsFix(a.hook) || needsFix(a.skill)) ? 1 : 0);
}

// copiesTodo is the copies check when it is a line of its own: updating hopsesh there
// brings its copies along, so it is not repeated then.
const copiesTodo = (st) => (["update", "setup"].includes(st.hopsesh?.fix) && st.hopsesh.state !== "ok" ? null : st.copies);

// needsFix: a hook or skill hopsesh can put right from here.
const needsFix = (c) => c && c.fix && c.state !== "ok" && ["hooks", "skill"].includes(c.fix);
const settable = (s) => s.local || s.login?.state === "ok";

// fixAll sets up the hooks and skill on every machine that needs them and can take them.
function fixAll() {
  const these = (statuses || []).filter((s) => settable(s) && s.agents.some((a) => needsFix(a.hook) || needsFix(a.skill)));
  if (these.length < 2) return null;
  return h("button", { class: "btn small", style: "align-self:flex-start", disabled: these.some((s) => fixing.has(s.machine)), onclick: () => fixMachines(these) },
    `Set up hooks and skill on ${these.length === 2 ? "both" : `all ${these.length}`}`);
}

function machineDetail(r, d, extras = {}) {
  const st = statusOf(r.name, r.local), m = r.m;
  const head = h("div", { class: "mdetail-h" },
    h("h2", {}, r.title), hopMark(markOf(st)),
    h("span", { class: "mono muted", style: "font-size:12px" }, r.local ? d.here.name : m.destination + (m.os ? ` · ${m.os}` : "")),
    h("span", { class: "spacer" }),
    st ? h("span", { class: "muted", style: "font-size:12px" }, `Checked ${ago(st.detailsAt || st.checkedAt) || "just now"}`) : null,
    h("button", { class: "btn small", disabled: statusBusy || (m && activeScan(m)), onclick: () => { if (m) scanMachine(m.name); else loadStatus(true); } }, statusBusy || (m && activeScan(m)) ? "Checking…" : m && failedScan(m) ? "Retry" : "Check again"));
  if (m && (failedScan(m) || activeScan(m) && !st || m.status && m.status !== "ok")) {
    return [head, h("div", { class: "mnext err-box" }, statusCell(m),
      /host-key/.test(m.status) ? h("button", { class: "btn small primary", onclick: () => trustDialog(m.name) }, "Check host key") : null), manage(m)];
  }
  if (!st) return [head, h("div", { class: "loading", role: "status" }, statusErr || "Checking…"), m ? manage(m) : null];
  const todo = [];
  const want = (c, label) => { if (c.fix && c.state !== "ok") todo.push(line(c, label, fixButton(st, c, m))); };
  want(st.hopsesh, "hopsesh");
  if (copiesTodo(st)?.fix && st.copies.state !== "ok") todo.push(line(st.copies, "Copies of hopsesh", fixButton(st, st.copies, m, st.copies.fix === "update" ? "Update them…" : undefined)));
  want(st.login, "Start at login");
  want(st.receiving, "Receive sessions");
  want(st.cli, "Command line");
  // Every agent folder's hook and skill are set up in one step: one line for them all.
  const missing = st.agents.filter((a) => needsFix(a.hook) || needsFix(a.skill));
  if (missing.length) {
    const hooks = missing.some((a) => needsFix(a.hook)), skills = missing.some((a) => needsFix(a.skill));
    const what = hooks && skills ? "the hook and skill" : hooks ? "the hook" : "the skill";
    const c = { state: missing.some((a) => [a.hook, a.skill].some((x) => needsFix(x) && x.state === "warn")) ? "warn" : "off", fix: "hooks",
      text: `${missing.length < st.agents.length ? `${missing.length} of ${st.agents.length} agent folders need` : { 1: "its agent folder needs", 2: "both agent folders need" }[missing.length] || `all ${missing.length} agent folders need`} ${what}` };
    todo.push(line(c, "Hooks and skill", fixButton(st, c, m, "Set up")));
  }
  const next = todo.length ? h("div", { class: "mnext" }, h("b", {}, `Up next · ${todo.length}`), todo)
    : st.version ? h("div", { class: "mnext ok-box" }, h("b", {}, "All set"), h("span", { class: "muted" }, " · hopsesh can do everything it does here")) : null;
  const ver = { ...st.hopsesh, text: st.version || st.hopsesh.text, reason: st.hopsesh.state === "ok" && st.newest && st.version === st.newest && !r.local ? "the newest" : st.hopsesh.reason };
  const receive = r.local ? h("button", { class: "switch", role: "switch", "aria-checked": d.here.receive ? "true" : "false", "aria-label": "Receive sessions from my other machines",
    onclick: async () => { try { await api("SetReceive", !d.here.receive); } catch (e) { fail(e); return; } toast(!d.here.receive ? `${sys.Here} now receives sessions` : `${sys.Here} no longer receives sessions`); await after(false); loadStatus(); } }) : null;
  return [head, next,
    h("section", { class: "msec" }, h("h3", {}, "hopsesh"),
      line(ver, "Version"),
      st.installs?.installs?.length > 1 || st.copies?.state && st.copies.state !== "ok" ? [line({ state: st.copies.state, text: `${st.installs.installs.length} on ${r.local ? sys.here : r.name}` }, "Copies"), installList(st.installs)] : null,
      st.version ? line(st.login, "Login session") : null,
      line(st.receiving, "Receiving sessions", receive),
      m ? line({ state: "ok", text: m.auth === "password" ? (m.keychain ? `Password, in ${sys.vault}` : "Password, asked each time") : "SSH key" }, "Login from here", h("button", { class: "btn small", onclick: () => loginDialog(m) }, "Change…")) : null,
      storageLine(r.local ? d.here.storage : m.storage)),
    r.local && extras.here ? h("section", { class: "msec", id: "here-setup" }, h("h3", {}, "Command, skill and hooks"), extras.here())
      : st.version ? h("section", { class: "msec" }, h("h3", {}, "Agent setup"),
        line(st.cli, "Command line"),
        st.agents.map((a) => h("div", { class: "mline" },
          h("span", { class: "msym " + worst(a.hook, a.skill), "aria-hidden": "true" }, symOf({ state: worst(a.hook, a.skill) })),
          h("span", { class: "mlabel", title: a.folder || "" }, agentLabel(a)),
          h("span", { class: "mtext" }, "Hook: ", a.hook.text, " · Skill: ", a.skill.text),
          h("span", { class: "mfix" })))) : null,
    st.version || r.local ? remoteSection(r.local ? "" : r.name, r.local ? sys.here : r.name) : null,
    r.local ? phoneSection() : null,
    m ? presenceBlock(m) : null,
    m ? manage(m) : null];
}

// Remote Control for every session: each agent folder's own setting there (Claude Code's
// remoteControlAtStartup), read when the page shows and changed with one switch for all.
const remote = new Map(); // machine ("" here) → { data, err, busy }
function remoteSection(machine, where) {
  const r = remote.get(machine);
  if (!r) {
    remote.set(machine, { busy: true });
    api("RemoteDefaults", machine).then((data) => remote.set(machine, { data }), (err) => remote.set(machine, { err: errText(err) })).finally(() => refresh());
  }
  const folders = (r?.data?.folders || []).filter((f) => !f.reason);
  const on = folders.length > 0 && folders.every((f) => f.on);
  const set = async () => {
    remote.set(machine, { ...r, busy: true }); refresh();
    try {
      const data = await api("SetRemoteDefault", machine, !on);
      remote.set(machine, { data });
      const failed = (data.folders || []).filter((f) => f.error);
      toast(failed.length ? cap(`${failed[0].agentName}${failed[0].profileLabel ? ` (${failed[0].profileLabel})` : ""}: ${failed[0].error}`)
        : !on ? `New Claude Code sessions on ${where} start with Remote Control` : `New Claude Code sessions on ${where} start without Remote Control`);
    } catch (err) { remote.set(machine, { ...r, busy: false }); fail(err); }
    refresh();
  };
  const sw = h("button", { class: "switch", role: "switch", "aria-checked": on ? "true" : "false", "aria-label": `Start Remote Control for every Claude Code session on ${where}`,
    disabled: !folders.length || !!r?.busy, onclick: set });
  const state = !r || r.busy ? { state: "unknown", text: "Checking…" } : r.err ? { state: "warn", text: r.err }
    : !r.data.folders.length ? { state: "off", text: "No Claude Code folder here" }
    : { state: on ? "ok" : "off", text: on ? "On: every new session connects" : folders.some((f) => f.on) ? "On in some folders" : "Off" };
  const many = (r?.data?.folders || []).length > 1 || (r?.data?.folders || []).some((f) => f.reason || f.error);
  return h("section", { class: "msec", id: "remote-control-" + (machine || "here") }, h("h3", {}, "Remote Control"),
    line(state, "Every session", sw),
    many ? r.data.folders.map((f) => line({ state: f.error ? "error" : f.reason ? "unknown" : f.on ? "ok" : "off", text: f.error || f.reason || (f.on ? "On" : "Off") }, f.agentName + (f.profileLabel ? " · " + f.profileLabel : ""))) : null,
    h("span", { class: "muted", style: "font-size:12px" }, "Reach new sessions from claude.ai or the Claude app. Sets remoteControlAtStartup in each Claude Code folder's settings; Claude desktop's own setting covers sessions started there."));
}

// The phone: MyCNC, an iPhone and iPad app for the agents on a Mac running MyCNC Host.
// hopsesh says whether MyCNC Host is here and links to it; it never installs it. The
// suggestion can be hidden, in this window only.
const PHONE_HIDDEN = "hopsesh.phone.hidden";
function phoneSection() {
  const c = state.info?.companion;
  if (!c || !c.supported) return null;
  let hidden = false;
  try { hidden = localStorage.getItem(PHONE_HIDDEN) === "1"; } catch { /* storage off: show it */ }
  if (hidden && !c.installed) return null;
  const learn = h("button", { class: "btn small", onclick: () => api("OpenCompanionPage").catch(fail) }, c.installed ? "About " + c.name : "Learn more");
  const hide = c.installed ? null : h("button", { class: "link", onclick: () => { try { localStorage.setItem(PHONE_HIDDEN, "1"); } catch { /* ignore */ } refresh(); } }, "Hide");
  const st = c.running ? { state: "ok", text: `${c.name} Host runs here${c.version ? " · " + c.version : ""}` }
    : c.installed ? { state: "warn", text: `${c.name} Host is installed but not running: open it to reach ${sys.here} from your phone` }
    : { state: "off", text: `Control this machine from your phone: ${c.name}` };
  return h("section", { class: "msec", id: "phone-companion" }, h("h3", {}, "Phone"),
    line(st, c.name, h("span", { class: "mline-actions" }, learn, hide)),
    h("span", { class: "muted", style: "font-size:12px" }, c.installed
      ? `Sessions here have “Follow on your phone”, which opens them in ${c.name}.`
      : `Approvals, terminals and the desktop of your Macs from iPhone, iPad or Mac. It needs ${c.name} Host on the Mac (macOS 14 or later) and connects over your LAN, Tailscale or ZeroTier, with no relay and no account.`));
}

const RANK = { ok: 0, off: 1, unknown: 2, warn: 3, error: 4 };
const worst = (...cs) => cs.reduce((w, c) => (RANK[c.state] > RANK[w] ? c.state : w), "ok");
// agentLabel names an agent folder by who is signed in there, else by its own name.
const agentLabel = (a) => a.name + (a.account ? " · " + a.account : a.profileLabel ? " · " + a.profileLabel : "");

// manage is what you do with an added machine itself: its host key, and removing it.
function manage(m) {
  return h("div", { class: "mmanage" },
    /host-key/.test(m.status) ? null : h("button", { class: "btn small", onclick: () => trustDialog(m.name) }, "Host key…"),
    h("span", { class: "spacer" }),
    h("button", { class: "btn small danger", onclick: async () => {
      if (!await ask({ title: `Remove ${m.name}?`, body: "hopsesh stops connecting to it and forgets its remembered password. Its sessions stay where they are.", ok: "Remove", danger: true })) return;
      try { await api("RemoveHost", m.name); toast(`Removed ${m.name}`); } catch (e) { fail(e); }
      pick = ""; await after(); loadStatus();
    } }, "Remove"));
}

function foundDetail(f) {
  return [h("div", { class: "mdetail-h" }, h("h2", {}, f.name), h("span", { class: "mono muted", style: "font-size:12px" }, f.destination + (f.os ? ` · ${f.os}` : ""))),
    h("p", { class: "muted", style: "margin:0" }, `Found via ${f.via.join(" and ")}${f.owner ? `, shared by ${f.owner}` : ""}. hopsesh hasn't contacted it: it connects only to machines you add.`),
    foundRow(f)];
}

// ---- Settings › Clouds --------------------------------------------------------------------
export function cloudsPanel() {
  fresh(false);
  if (!data && !loadErr && !reading) reload();
  if (loadErr) return [loadError(loadErr, retry)];
  if (!data) return [h("div", { class: "loading", role: "status" }, "Reading the clouds…")];
  return [h("span", { class: "muted", style: "font-size:12.5px" }, "hopsesh reaches each cloud through that agent's own command, signed in as you. Signing in happens in that command, never in hopsesh. Nothing goes to a cloud you haven't turned on."),
    data.clouds.length ? h("div", { class: "cloud-cards" }, data.clouds.map(cloudCard)) : h("p", { class: "muted" }, "No cloud is reached by the agents here yet.")];
}
