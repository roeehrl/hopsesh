// The Machines screen: this machine (and whether it receives sessions), the machines you
// added, and the ones discovery found. hopsesh connects only to machines you added.
import { api, h, fill, icon, ICONS, view, state, screen, go, loading, toast, fail, errText, cap, dialog, ask, machineStatus, sys, when } from "./core.js";

let data = null;
let changed = false; // machines changed since the last scan


async function reload() {
  try { data = await api("Machines"); } catch (e) { fail(e); }
  render();
}

// after reloads what a change affects; machine changes also need a new scan.
function after(rescan = true) {
  if (rescan) changed = state.stale = true;
  return Promise.all([api("Info").then((i) => { state.info = i; }).catch(fail), reload()]);
}

function statusCell(m) {
  if (!m.scanned) return h("span", { class: "muted" }, "Not scanned yet");
  const [k, words] = machineStatus(m.status);
  const url = (m.error.match(/https:\/\/login\.tailscale\.com\/\S+/) || [])[0];
  return h("div", { style: "display:flex;flex-direction:column;gap:3px;min-width:0" },
    h("span", { style: "display:flex;gap:7px;align-items:center" }, h("span", { class: "dot " + k }), words),
    m.status === "ok" ? h("span", { class: "muted", style: "font-size:12px" }, [`${m.sessions} session${m.sessions === 1 ? "" : "s"}`, m.agents.join(", ")].filter(Boolean).join(" · ")) : null,
    m.status === "ok" ? h("span", { class: m.hopsesh ? "muted" : "warn", style: "font-size:12px" }, m.hopsesh ? `hopsesh ${m.hopsesh}: you can send sessions there` : "No hopsesh there: you can bring sessions from it, not send to it") : null,
    m.status !== "ok" && m.hint ? h("span", { class: "muted", style: "font-size:12px" }, cap(m.hint)) : null,
    m.status !== "ok" && m.error && !url ? h("span", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, m.error) : null,
    url ? h("button", { class: "btn small", style: "align-self:flex-start", onclick: () => api("OpenURL", url).catch(fail) }, "Open the Tailscale sign-in") : null);
}

function machineRow(m) {
  const login = m.auth === "password" ? (m.keychain ? `Password, in ${sys.vault}` : "Password, asked each time") : "SSH key";
  return h("div", { class: "mgrid" },
    h("div", { style: "min-width:0" }, h("div", { style: "font-weight:500" }, m.name), h("div", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, m.destination + (m.os ? ` · ${m.os}` : ""))),
    h("div", {}, h("button", { class: "btn small", title: "How hopsesh logs in to this machine", onclick: () => loginDialog(m) }, login)),
    statusCell(m),
    h("div", { style: "display:flex;gap:6px;flex-wrap:wrap;justify-content:flex-end" },
      /host-key/.test(m.status) || !m.scanned ? h("button", { class: "btn small" + (m.status === "host-key" ? " primary" : ""), onclick: () => trustDialog(m.name) }, "Check host key") : null,
      h("button", { class: "btn small danger", onclick: async () => {
        if (!await ask({ title: `Remove ${m.name}?`, body: "hopsesh stops connecting to it and forgets its remembered password. Its sessions stay where they are.", ok: "Remove", danger: true })) return;
        try { await api("RemoveHost", m.name); toast(`Removed ${m.name}`); } catch (e) { fail(e); }
        after();
      } }, "Remove")));
}

function foundRow(f) {
  const online = f.online === null || f.online === undefined ? "" : f.online ? "online" : "offline";
  return h("div", { class: "mgrid" },
    h("div", { style: "min-width:0" }, h("div", { style: "font-weight:500" }, f.name), h("div", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, f.destination + (f.os ? ` · ${f.os}` : ""))),
    h("span", { class: "muted", style: "font-size:12px" }, "Found via " + f.via.join(" and ")),
    h("span", { class: "muted", style: "font-size:12px" }, [online, f.owner ? `shared by ${f.owner}` : ""].filter(Boolean).join(" · ")),
    h("div", { style: "display:flex;justify-content:flex-end" }, h("button", { class: "btn small outline", onclick: async () => {
      if (f.owner && !await ask({ title: `Add ${f.name}?`, body: `It belongs to ${f.owner}. hopsesh would log in to it with your SSH setup and read its coding agents' session folders.`, ok: "Add it" })) return;
      try { await api("SetAllowed", f.name, f.destination, true); toast(`Added ${f.name}`); } catch (e) { fail(e); }
      after();
    } }, "Add")));
}

// cloudCard is one of the agents' clouds: consent, the driver, the login, what comes back,
// and a read-only test.
function cloudCard(c) {
  const allow = h("button", { class: "switch", role: "switch", "aria-checked": c.allowed ? "true" : "false", "aria-label": `Allow ${c.title}`,
    onclick: async () => {
      try { await api("SetCloudAllowed", c.name, !c.allowed); } catch (e) { fail(e); return; }
      toast(!c.allowed ? `${c.title} is allowed` : `hopsesh leaves ${c.title} alone`);
      after();
    } });
  const kv = (label, ...value) => [h("dt", {}, label), h("dd", {}, ...value)];
  const t = c.test;
  const signed = t ? (t.ok || t.account ? t.account : h("span", { class: "warn" }, t.error || "Not signed in"))
    : c.status === "signed-out" ? h("span", { class: "warn" }, cap(c.hint)) : c.allowed ? h("span", { class: "muted" }, "Test it to see") : h("span", { class: "muted" }, "Not checked while it is off");
  const result = h("span", { style: "font-size:12px", role: "status" });
  const show = (r) => fill(result, h("b", { class: r.ok ? "ok" : "err", style: "font-weight:600" }, r.ok ? "✓ " : "✕ "),
    h("span", { class: r.ok ? "ok" : "err" }, (r.checks.length ? r.checks.map((x) => x.text).join(" · ") : r.error) + " · " + when(r.at)));
  if (t) show(t);
  const brings = c.fidelity === "native" ? `The whole conversation, copied by ${c.agentName}; hopsesh checks the message count` : c.fidelity === "code" ? "The code, title and summary" : "The messages, as text";
  return h("section", { class: "card cloud-card", "aria-labelledby": "cc-" + c.name },
    h("div", { class: "set-row" }, h("span", { style: "color:var(--cloud)" }, icon(ICONS.cloud, 16)), h("h3", { id: "cc-" + c.name, style: "margin:0;font-size:15px" }, c.title),
      h("span", { class: "spacer" }), h("span", { style: "font-size:12.5px;font-weight:500" }, "Allow"), allow),
    h("dl", { class: "kv wide" },
      kv("Driver", c.version ? h("span", { class: "mono", style: "font-size:12px" }, `${c.driver} ${c.version}`) : h("span", { class: "warn" }, `${c.title} is reached through the `, h("span", { class: "mono" }, c.driver), " command, which isn't installed here"),
        c.version ? [" ", h("span", { class: "chip " + (c.tested ? "st-idle" : "st-warn") }, c.tested ? "tested" : "untested"), c.tested ? null : h("span", { class: "muted", style: "font-size:12px" }, ` hopsesh tested ${c.testedOn}`)] : null),
      kv("Signed in", signed),
      kv("Brings back", brings),
      c.vendorPrefix ? kv("Branches", c.rename ? [h("span", { class: "mono", style: "font-size:12px" }, c.vendorPrefix + "…"), " kept here as ", h("span", { class: "mono", style: "font-size:12px" }, `hopsesh/from/${c.name}/…`)] : "Kept as the cloud names them") : null,
      kv("Listing", c.partial ? `Only what hopsesh started or brought here, and Remote Control mirrors: ${c.agentName} has no list command` : "Every session the cloud lists")),
    h("div", { class: "set-row", style: "padding-top:10px;border-top:1px solid var(--line2)" },
      h("button", { class: "btn", disabled: !c.allowed, title: c.allowed ? "A read-only look: the login and the flags hopsesh uses" : "Allow it first", onclick: async (ev) => {
        const btn = ev.currentTarget;
        btn.disabled = true;
        fill(result, h("span", { class: "muted" }, "Checking…"));
        try { await api("TestCloud", c.name); } catch (e) { fill(result, h("span", { class: "err" }, errText(e))); btn.disabled = false; return; }
        reload();
      } }, "Test"), result));
}

function render() {
  const d = data;
  if (!d) return;
  const receive = h("button", { class: "switch", role: "switch", "aria-checked": d.here.receive ? "true" : "false", "aria-label": "Receive sessions from my other machines",
    onclick: async () => {
      try { await api("SetReceive", !d.here.receive); } catch (e) { fail(e); return; }
      toast(!d.here.receive ? `${sys.Here} now receives sessions` : `${sys.Here} no longer receives sessions`);
      after(false);
    } });
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in" },
    h("div", { style: "display:flex;align-items:baseline;gap:12px;flex-wrap:wrap" }, h("h1", {}, "Machines"), h("span", { class: "spacer" }),
      changed ? h("button", { class: "btn primary", onclick: () => { changed = false; go("sessions"); } }, "Scan them now") : h("button", { class: "btn", onclick: () => go("sessions") }, "Back to sessions")),
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
      h("div", { class: "card-h" }, h("span", { class: "name" }, "Your machines"), h("span", { class: "spacer" }), h("button", { class: "btn small", onclick: addDialog }, icon(ICONS.plus, 12), "Add by address…")),
      d.machines.length ? [h("div", { class: "mgrid h" }, h("span", {}, "Machine"), h("span", {}, "Login"), h("span", {}, "Last scan"), h("span", {})), d.machines.map(machineRow)]
        : h("div", { class: "empty" }, "No machines yet. Add one found below, or by its address.")),
    h("section", { class: "card" },
      h("div", { class: "card-h" }, h("span", { class: "name" }, "Found on your network"), h("span", { class: "muted", style: "font-size:12px" }, "From Tailscale and ~/.ssh/config. Nothing is contacted until you add it.")),
      d.found.length ? d.found.map(foundRow) : h("div", { class: "empty" }, "No other machines found. Add one by its address.")),
    d.clouds.length ? [h("div", { style: "display:flex;flex-direction:column;gap:4px;margin-top:8px" }, h("h2", { style: "margin:0;font-size:17px" }, "Clouds"),
      h("span", { class: "muted", style: "font-size:12.5px" }, "hopsesh reaches each cloud through that agent's own command, signed in as you. Nothing goes to a cloud you haven't allowed.")),
      h("div", { class: "cloud-cards" }, d.clouds.map(cloudCard))] : null)));
}

function trustDialog(name) {
  const d = dialog(h("div", { role: "status" }, `Fetching ${name}'s host keys…`));
  api("ScanKeys", name).then((k) => {
    dialog(
      h("h2", { style: "margin:0;font-size:16px" }, `Host keys of ${name}`),
      h("div", { class: "mono muted", style: "font-size:11px" }, k.address),
      k.fingerprints.map((f) => h("div", { class: "mono", style: "font-size:12px;overflow-wrap:anywhere" }, f)),
      k.verified ? h("div", { class: "ok" }, "✓ " + k.verified)
        : h("div", { class: "warn", style: "font-size:12.5px;line-height:1.5" }, "Not independently verified. Compare with ", h("span", { class: "mono" }, "ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub"), " on that machine."),
      h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
        h("button", { class: "btn primary", onclick: async () => { try { await api("TrustHost", name); toast(`${name} trusted`); } catch (e) { fail(e); } d.close(); after(); } }, "Trust these keys")));
  }).catch((e) => dialog(h("div", { class: "err" }, errText(e)), h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Close"))));
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
        d.close(); toast(`Added ${name.value.trim()}`); after();
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
      after();
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
      m.auth === "password" && m.keychain ? h("button", { class: "btn", onclick: async () => { try { await api("ForgetPassword", m.name); toast("Forgot the remembered password"); } catch (e) { fail(e); } d.close(); after(); } }, "Forget password") : null,
      m.auth === "password" ? h("button", { class: "btn", onclick: () => setup(false) }, "Set up key login") : null,
      h("span", { class: "spacer" }),
      h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => {
        try { await api("SetAuth", m.name, pass.checked ? "password" : "key", remember.checked); } catch (e) { fail(e); return; }
        d.close(); after();
      } }, "Save")));
}

screen("machines", async () => {
  if (!data) loading("Looking for your machines (nothing is contacted)…");
  await reload();
});
