// The Machines screen: this machine (and whether it receives sessions), the machines you
// added, and the ones discovery found. hopsesh connects only to machines you added.
import { on, api, h, fill, icon, ICONS, view, state, screen, go, loading, toast, fail, errText, cap, dialog, ask, machineStatus, sys, when, current, rich, navigationID, loadError } from "./core.js";
import { signIn, onSignedIn } from "./term.js";

// A sign-in tab ended well: the card shows the new check.
onSignedIn(() => { if (current === "machines") reload(); });

let data = null;
on('hopsesh:discovery',d=>{if(current==='machines'&&!d.discovering&&!document.querySelector('dialog[open]'))reload().catch(fail)});
const scanning = new Set();
const scanErrors = new Map();
let pointerHeld = false, renderTimer = 0;
document.addEventListener("pointerdown", () => { pointerHeld = true; }, true);
window.addEventListener("pointerup", () => { pointerHeld = false; }, true);
window.addEventListener("pointercancel", () => { pointerHeld = false; }, true);


let read = 0;
async function reload() {
  const n = ++read, visit = navigationID();
  try {
    const result = await api("Machines");
    if (n !== read || visit !== navigationID()) return;
    data = result;
    if (current === "machines") render();
  } catch (e) {
    if (n === read && visit === navigationID() && current === "machines") fill(view, loadError(e, reload));
  }
}

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
  if (current === "machines") render();
  try { await api("ScanMachine", name); state.scan=await api("ScanSnapshot"); state.stale = false; }
  catch (e) { scanErrors.set(name, errText(e)); }
  finally { scanning.delete(name); await reload(); }
}

// Backend phase notifications keep all views in sync without a client poll.
export async function machineScanChanged() {
 if (!data) return;
 const states = await api("MachineScans");
 let finished=false;
 for(const m of data.machines){const next=states?.[m.name];finished ||= next?.phase === "done" && JSON.stringify(m.scan)!==JSON.stringify(next);m.scan=next;}
 if(current!=="machines")return;
 if(finished)await reload();else if(!document.querySelector("dialog[open]"))render();
}

// The shared runtime schedules remote observations even when this view is closed.
// Phase notifications update rows; Scan is an explicit retry after authentication.

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
    m.status === "ok" ? h("span", { class: m.hopsesh ? "muted" : "warn", style: "font-size:12px" }, m.hopsesh ? `hopsesh ${m.hopsesh}: ${m.receive === false ? "receiving is not approved for this connection" : "you can send sessions there"}` : "No hopsesh there: you can bring sessions from it, not send to it") : null,
    m.status !== "ok" && m.hint ? h("span", { class: "muted", style: "font-size:12px" }, rich(cap(m.hint))) : null,
    error && error !== m.error ? h("span", { class: "err" }, error) : null,
    m.scan?.finished ? h("span", { class: "muted", style: "font-size:11px" }, "Last checked " + when(m.scan.finished)) : null,
    m.status !== "ok" && m.error && !url ? h("span", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, m.error) : null,
    url ? h("button", { class: "btn small", style: "align-self:flex-start", onclick: () => api("OpenURL", url).catch(fail) }, "Open the Tailscale sign-in") : null);
}

function machineRow(m) {
  const login = m.relay ? "Encrypted relay" : m.auth === "password" ? (m.keychain ? `Password, in ${sys.vault}` : "Password, asked each time") : "SSH key";
  return h("div", { class: "mgrid", "data-machine": m.name, "aria-busy": activeScan(m) ? "true" : "false" },
    h("div", { style: "min-width:0" }, h("div", { style: "font-weight:500" }, m.name), h("div", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, m.destination + (m.os ? ` · ${m.os}` : ""))),
    h("div", {}, h("button", { class: "btn small", title: "How hopsesh logs in to this machine", onclick: () => m.relay ? go("settings","relay") : loginDialog(m) }, login)),
    statusCell(m),
    h("div", { style: "display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end" },
      h("button", { class: "btn small", disabled: activeScan(m), "aria-label": `${activeScan(m) ? "Scanning" : failedScan(m) ? "Retry scan" : "Scan"} ${m.name}`, onclick: () => scanMachine(m.name) }, activeScan(m) ? "Scanning…" : failedScan(m) ? "Retry scan" : "Scan"),
      /host-key/.test(m.status) ? h("button", { class: "btn small" + (m.status === "host-key" ? " primary" : ""), onclick: () => trustDialog(m.name) }, "Check host key") : null,
      h("button", { class: "btn small danger", onclick: async () => {
        if (!await ask({ title: `Remove ${m.name}?`, body: "hopsesh stops connecting to it and forgets its remembered password. Its sessions stay where they are.", ok: "Remove", danger: true })) return;
        try { await api("RemoveHost", m.name); toast(`Removed ${m.name}`); } catch (e) { fail(e); }
        await after();
      } }, "Remove")));
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

function render() {
  if (current !== "machines") return;
  // A scan can finish between pointerdown and click. Keep its result, but do
  // not replace the button the user is pressing or a dialog they are reading.
  if (pointerHeld || document.querySelector("dialog[open], button:active")) {
    if (!renderTimer) renderTimer = setTimeout(() => { renderTimer = 0; render(); }, 100);
    return;
  }
  const d = data;
  if (!d) return;
  const receive = h("button", { class: "switch", role: "switch", "aria-checked": d.here.receive ? "true" : "false", "aria-label": "Receive sessions from my other machines",
    onclick: async () => {
      try { await api("SetReceive", !d.here.receive); } catch (e) { fail(e); return; }
      toast(!d.here.receive ? `${sys.Here} now receives sessions` : `${sys.Here} no longer receives sessions`);
      await after(false);
    } });
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in" },
    h("h1", {}, "Machines"),
    h("span", { class: "muted" }, "hopsesh connects only to the machines you add, with your own ssh and keys, and only reads your coding agents' session folders until you hop a session."),
    h("section", { class: "card" }, h("div", { class: "line-item" },
      h("span", { class: "ico push" }, icon(ICONS.here, 15)),
      h("div", { style: "flex:1 1 300px;min-width:0;display:flex;flex-direction:column;gap:3px" },
        h("b", { style: "font-weight:500" }, `${sys.Here} · ${d.here.name}`),
        h("span", { class: "muted", style: "font-size:12px" }, [`hopsesh ${d.here.hopsesh}`, ...d.here.agents].join(" · "))),
      h("label", { style: "display:flex;gap:10px;align-items:center;max-width:380px" },
        h("span", { style: "display:flex;flex-direction:column;gap:2px" }, h("span", {}, "Receive sessions from my other machines"),
          h("span", { class: "muted", style: "font-size:12px" }, d.here.receive ? "On: “Send to…” on your other machines can deliver sessions here." : `Off: ${sys.here} refuses sessions sent from other machines.`)),
        receive))),
    h("section", { class: "card" },
      h("div", { class: "card-h" }, h("div", {}, h("h2", { class: "name" }, "Your machines"), h("div", { class: "muted", style: "font-size:12px" }, "Scanned when added, then every 5 minutes while this page is active. Failed scans wait for Retry.")), h("span", { class: "spacer" }), h("button", { class: "btn small", onclick: addDialog }, icon(ICONS.plus, 12), "Add by address…")),
      d.machines.length ? [h("div", { class: "mgrid h" }, h("span", {}, "Machine"), h("span", {}, "Login"), h("span", {}, "Last scan"), h("span", {})), d.machines.map(machineRow)]
        : h("div", { class: "empty" }, "No machines yet. Add one found below, or by its address.")),
    h("section", { class: "card" },
      h("div", { class: "card-h stacked" }, h("h2", { class: "name" }, "Found on your network"), h("span", { class: "muted", style: "font-size:12px" }, "From Tailscale and ~/.ssh/config. Nothing is contacted until you add it.")),
      d.discoveryError ? h("p",{class:"warn",role:"status"},d.discoveryError,". SSH aliases are still shown.") : null,
      d.found.length ? d.found.map(foundRow) : h("div", { class: "empty" }, "No other machines found. Add one by its address.")),
    d.clouds.length ? [h("div", { id: "clouds", style: "display:flex;flex-direction:column;gap:4px;margin-top:8px;scroll-margin-top:16px" }, h("h2", { style: "margin:0;font-size:17px" }, "Clouds"),
      h("span", { class: "muted", style: "font-size:12.5px" }, "hopsesh reaches each cloud through that agent's own command, signed in as you. Signing in happens in that command, never in hopsesh. Nothing goes to a cloud you haven't turned on.")),
      h("div", { class: "cloud-cards" }, d.clouds.map(cloudCard))] : null)));
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

// The Machines screen; "clouds" scrolls to the clouds.
screen("machines", async (at) => {
  if (!data) loading("Looking for your machines (nothing is contacted)…");
  const visit = navigationID();
  await reload();
  if (current !== "machines" || visit !== navigationID()) return;
  const to = at === "clouds" && view.querySelector("#clouds"), pg = view.querySelector(".page");
  if (to && pg) pg.scrollTop += to.getBoundingClientRect().top - pg.getBoundingClientRect().top - 12; // the page scrolls, never the window
});
