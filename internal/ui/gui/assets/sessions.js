// The Sessions screen: scopes on the left, sessions by repository in the middle, the
// selected session on the right. Every action on a session comes from actionsFor(), which
// the palette uses too.
import { api, h, fill, icon, ICONS, view, state, screen, go, loading, toast, fail, cap, ago, when, bytes, agentBadge, agentChip, machineStatus, sys, keys, cliHow,
  entries, selected, here, agentInfo, $, count, clouds, cloudOf, cloudState, cloudChip, dialog, errText } from "./core.js";
import { handoffMenu } from "./handoff.js";
import { planFor, planPicked } from "./plan.js";

// scan reads every machine again. The list stays while it runs.
export async function scan() {
  if (state.scanning) return;
  state.scanning = true;
  state.stale = false;
  freshness();
  if (!state.scan) {
    loading(`Reading ${sys.here} and your machines…`);
    if (state.info?.localNetwork?.gated && state.info.localNetwork.firstRun) {
      view.firstChild.append(h("div", { class: "muted", style: "font-size:12px;max-width:520px;line-height:1.5" },
        "macOS may ask whether hopsesh can find devices on your local network. Choose Allow to reach machines on this network; machines over Tailscale work either way."));
    }
  }
  try {
    state.scan = await api("Scan");
    state.info = await api("Info");
    state.activity = await api("Activity").catch(() => state.activity);
  } catch (e) {
    state.scanning = false;
    if (!state.scan) fill(view, h("div", { class: "loading err" }, String(e.message || e)));
    else fail(e);
    freshness();
    return;
  }
  state.scanning = false;
  freshness();
  if (state.sel && !selected()) state.sel = null;
}

function freshness() {
  const el = $("#fresh");
  el.textContent = state.scanning ? "Refreshing…" : state.scan ? "updated " + ago(state.scan.updated) : "";
}
setInterval(freshness, 30000);

// CLOUD_STATES are a cloud session's states: [kind, words].
const CLOUD_STATES = { running: ["running", "Running"], idle: ["idle", "Idle"], done: ["done", "Done"], failed: ["error", "Failed"],
  archived: ["ended", "Archived"], unknown: ["unknown", "State unknown"] };

// statusOf is a session's state: [kind, words].
export function statusOf(e) {
  if (e.cloud) return CLOUD_STATES[e.cloud.state] || CLOUD_STATES.unknown;
  if (e.needs) return ["needs", "Needs you"];
  if (e.live) return /idle/i.test(e.status) ? ["idle", "Idle"] : ["working", "Working"];
  if (/^(moved|continued)/.test(e.status)) return ["moved", e.status[0].toUpperCase() + e.status.slice(1)];
  return ["ended", "Ended"];
}

// cloudBlock says why a cloud's sessions cannot be brought here now ("" when they can).
export function cloudBlock(c) {
  if (!c) return "This cloud is not in use here.";
  if (!c.allowed) return `hopsesh leaves ${c.title} alone until you allow it.`;
  if (!c.fetchable) return `hopsesh cannot bring ${c.title} sessions here yet.`;
  if (c.status === "ready" || c.status === "cli-old") return "";
  return cap(c.error && c.status === "signed-out" ? c.error.replace(/^.*?: /, "") : c.hint || c.error || c.status);
}

// actionsFor lists what can be done with a session: the first is its main action.
export function actionsFor(e) {
  const out = [];
  if (e.cloud) {
    const why = cloudBlock(cloudOf(e.machine));
    out.push({ id: "bring", label: `Bring here (${e.agentName})`, short: "Bring here", why, run: () => planFor(e, { target: "" }) });
    for (const t of e.continueIn) out.push({ id: "bring:" + t.id, label: `Bring here and continue in ${t.name}`, why, run: () => planFor(e, { target: t.id }) });
    return why ? out.map((a) => Object.assign(a, { run: () => toast(why) })) : out;
  }
  const local = e.machine === here();
  const a = agentInfo(e.agent);
  if (local) {
    if (!e.live && e.hereNewest) {
      out.push({ id: "resume", label: "Resume", run: () => api("ResumeEntry", e.machine, e.key, false).catch(fail) });
      if (a?.capabilities?.includes("app")) out.push({ id: "app", label: `Open in the ${e.agentName} app`, run: () => api("ResumeEntry", e.machine, e.key, true).catch(fail) });
    }
  } else {
    out.push({ id: "hop", label: e.staleHere ? "Hop back" : "Hop here", run: () => planFor(e, { target: "" }) });
  }
  for (const t of e.continueIn) out.push({ id: "continue:" + t.id, label: `Continue in ${t.name}${local ? "" : " here"}`, run: () => planFor(e, { target: t.id }) });
  if (local) for (const m of state.scan.peers) out.push({ id: "send:" + m, label: `Send to ${m}`, run: () => planFor(e, { target: "", sendTo: m }) });
  return out;
}

function scoped() {
  const sc = state.scope, f = state.filter;
  return entries().filter((e) =>
    (sc.kind !== "needs" || e.needs) &&
    (sc.kind !== "here" || e.machine === here()) &&
    (sc.kind !== "incloud" || e.cloud || e.mirror) &&
    (sc.kind !== "cloud" || (e.cloud ? e.machine === sc.value : e.mirror?.cloud === sc.value)) &&
    (sc.kind !== "machine" || e.machine === sc.value) &&
    (sc.kind !== "agent" || e.agent === sc.value) &&
    (!f.agent || e.agent === f.agent) &&
    (!f.live || e.live));
}

function scopeTitle() {
  const sc = state.scope;
  return { all: "All sessions", needs: "Needs you", here: `On ${sys.here}`, incloud: "In the cloud", machine: sc.value, cloud: cloudOf(sc.value)?.title || sc.value,
    agent: agentInfo(sc.value)?.name || sc.value }[sc.kind];
}

function setScope(sc) { state.scope = sc; render(); }

function sidebar() {
  const s = state.scan, all = entries();
  const cur = (k, v) => state.scope.kind === k && (v === undefined || state.scope.value === v) ? "true" : "false";
  const needs = all.filter((e) => e.needs).length;
  const machines = s.machines.filter((m) => !m.local);
  const dotFor = (m) => machineStatus(m.status)[0];
  const act = state.activity;
  return h("nav", { class: "sidebar", "aria-label": "Scopes" },
    h("button", { class: "side-btn", "aria-current": cur("needs"), onclick: () => setScope({ kind: "needs" }) },
      h("span", { class: "dot" + (needs ? " needs" : "") }), h("span", { style: needs ? "font-weight:600" : "" }, "Needs you"),
      needs ? h("span", { class: "chip st-needs", style: "margin-left:auto" }, needs) : h("span", { class: "count" }, "0")),
    h("button", { class: "side-btn", "aria-current": cur("all"), onclick: () => setScope({ kind: "all" }) }, icon(ICONS.all), "All sessions", h("span", { class: "count" }, all.length)),
    h("button", { class: "side-btn", "aria-current": cur("here"), onclick: () => setScope({ kind: "here" }) }, icon(ICONS.here), `On ${sys.here}`,
      h("span", { class: "count" }, all.filter((e) => e.machine === here()).length)),
    clouds().length ? h("button", { class: "side-btn", "aria-current": cur("incloud"), onclick: () => setScope({ kind: "incloud" }) }, icon(ICONS.cloud), "In the cloud",
      h("span", { class: "count" }, all.filter((e) => e.cloud || e.mirror).length)) : null,
    h("div", { class: "side-h" }, "Machines"),
    machines.map((m) => h("button", { class: "side-btn", "aria-current": cur("machine", m.name), onclick: () => setScope({ kind: "machine", value: m.name }) },
      h("span", { class: "dot " + dotFor(m) }),
      h("span", { style: "min-width:0" }, h("span", {}, m.name), h("small", { class: dotFor(m) === "ok" ? "" : "warn" }, dotFor(m) === "ok" ? [m.os, m.hopsesh ? "hopsesh " + m.hopsesh : ""].filter(Boolean).join(" · ") : machineStatus(m.status)[1])),
      h("span", { class: "count" }, m.status === "ok" ? m.sessions : ""))),
    h("button", { class: "side-btn", style: "color:var(--accent)", onclick: () => go("machines") }, icon(ICONS.plus), machines.length ? "Add a machine" : "Add your other machines"),
    clouds().length ? h("div", { class: "side-h" }, "Clouds") : null,
    clouds().map(cloudSide),
    h("div", { class: "side-h" }, "Agents"),
    (state.info.agents || []).filter((a) => a.enabled).map((a) => h("button", { class: "side-btn", "aria-current": cur("agent", a.id), onclick: () => setScope({ kind: "agent", value: a.id }) },
      agentBadge(a.id, a.name), a.name, h("span", { class: "count" }, all.filter((e) => e.agent === a.id).length))),
    h("div", { class: "side-foot" },
      h("button", { class: "side-btn", style: "padding:6px 0", onclick: () => go("activity") }, icon(ICONS.clock), "Activity",
        act ? h("span", { class: "count" }, act.items.filter((x) => x.canUndo).length + " can undo") : null),
      h("button", { class: "link", style: "text-align:left;text-decoration:none;color:var(--muted)", onclick: () => go("machines") },
        h("span", { class: "dot " + (state.info.receive ? "ok" : ""), style: "display:inline-block;margin-right:6px" }), "Receiving sessions: " + (state.info.receive ? "on" : "off")),
      updateNote()));
}

// cloudSide is a cloud in the sidebar: a dot, its name and one word, and its count, or a
// Turn on link while it is not allowed.
function cloudSide(c) {
  const [dot, word] = cloudState(c);
  const cur = state.scope.kind === "cloud" && state.scope.value === c.name ? "true" : "false";
  if (!c.allowed) return h("div", { class: "side-off" }, h("span", { class: "dot off" }), h("span", {}, c.title),
    h("button", { class: "link", title: `Allow hopsesh to use ${c.title}`, onclick: () => turnOn(c) }, "Turn on"));
  return h("button", { class: "side-btn", "aria-current": cur, onclick: () => setScope({ kind: "cloud", value: c.name }) },
    h("span", { class: "dot " + dot }),
    h("span", { style: "min-width:0" }, h("span", {}, c.title), h("small", { class: dot === "ok" || dot === "half" ? "ok" : dot === "off" ? "" : "warn" }, word)),
    h("span", { class: "count" }, c.listable && (c.status === "ready" || c.status === "cli-old") ? c.sessions : "–"));
}

// turnOn allows a cloud and reads it.
async function turnOn(c) {
  try { await api("SetCloudAllowed", c.name, true); } catch (e) { fail(e); return; }
  toast(`${c.title} is on: hopsesh reaches it through ${c.driver}, signed in as you`);
  state.scope = { kind: "cloud", value: c.name };
  await scan();
  render();
}

// pasteDialog asks for a cloud session's link or id, and the checkout here it works on.
export async function pasteDialog(cloud) {
  const c = cloud ? cloudOf(cloud) : clouds().find((x) => x.fetchable);
  if (!c) { toast("No cloud can bring sessions here"); return; }
  const checkouts = await api("Checkouts").catch(() => []);
  const input = h("input", { class: "field mono", id: "paste-link", placeholder: "claude.ai/code/…, session_… or cse_…", autocomplete: "off", spellcheck: "false" });
  const repo = repoSelect(checkouts);
  const msg = h("div", { class: "err", role: "alert" });
  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, "Paste a cloud link"),
    h("label", { for: "paste-link", style: "font-size:12.5px" }, "The session's link or id"), input,
    h("label", { for: "paste-repo", style: "font-size:12.5px" }, "Its repository here"), repo,
    h("span", { class: "muted", style: "font-size:12px" }, `hopsesh lists it under ${c.title}; bringing it here makes a new worktree, so your checkout stays as it is.`), msg,
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", onclick: async () => {
        if (!input.value.trim()) { msg.textContent = "Paste a link or an id."; return; }
        let p;
        try { p = await api("PasteCloud", input.value.trim(), repo.value); } catch (e) { msg.textContent = errText(e); return; }
        d.close();
        state.scope = { kind: "cloud", value: p.cloud };
        state.sel = { machine: p.cloud, key: p.key };
        await scan();
        render();
      } }, "Add")));
  input.focus();
}

// findDialog opens the vendor's own picker (claude --teleport) in a new worktree of a
// repository chosen here, and brings what is picked like any other session.
export async function findDialog(cloud) {
  const c = cloud ? cloudOf(cloud) : clouds().find((x) => x.fetchable && x.partial);
  if (!c) { toast("No cloud has a picker of its own"); return; }
  const checkouts = await api("Checkouts").catch(() => []);
  const repo = repoSelect(checkouts);
  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, `Find in ${c.agentName}`),
    h("span", { class: "muted", style: "font-size:12.5px;line-height:1.5" }, `${c.agentName}'s own picker lists your ${c.title} sessions. hopsesh opens it in ${sys.terminal}, in a new worktree of the repository you choose, so your checkout stays as it is; the session you pick there is brought here like any other.`),
    h("label", { for: "paste-repo", style: "font-size:12.5px" }, "Repository"), repo,
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"),
      h("button", { class: "btn primary", disabled: !checkouts.length, onclick: () => { d.close(); planPicked(c.name, "", repo.value); } }, "Plan it")));
}

function repoSelect(checkouts) {
  const sel = h("select", { id: "paste-repo", style: "width:100%" }, h("option", { value: "" }, checkouts.length ? "Choose later" : "No checkout found here"),
    checkouts.map((k) => h("option", { value: k.path }, `${k.name} · ${k.identity}`)));
  const g = selected()?.group;
  const pick = checkouts.find((k) => g && k.path === g.local) || checkouts[0];
  if (pick) sel.value = pick.path;
  return sel;
}

// updateNote asks once whether hopsesh may check for releases, then links a newer one.
function updateNote() {
  if (state.info.updateCheck === "") {
    const answer = async (yes) => { await api("SetUpdateCheck", yes).catch(fail); state.info = await api("Info"); if (yes) state.update = await api("CheckUpdate").catch(() => null); render(); };
    return h("span", {}, "Check GitHub daily for new versions? ", h("button", { class: "link", onclick: () => answer(true) }, "Yes"), " · ", h("button", { class: "link", onclick: () => answer(false) }, "No"));
  }
  if (state.update?.newer) return h("button", { class: "link", style: "text-align:left", onclick: () => go("settings", "updates") }, `hopsesh ${state.update.latest} is available`);
  return null;
}

// notices are things to set up, shown above the list until done or dismissed.
function notices() {
  const out = [], i = state.info, s = state.scan;
  const picked = state.scope.kind === "machine" && s.machines.find((m) => m.name === state.scope.value);
  if (picked && picked.status !== "ok") out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `${picked.name}: ${machineStatus(picked.status)[1]}`),
      h("div", { style: "font-size:12.5px" }, cap(picked.hint || picked.error))),
    h("button", { class: "btn", onclick: () => go("machines") }, "Open Machines")));
  const blocked = s.machines.filter((m) => m.status === "local-network");
  if (blocked.length) out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `macOS is blocking hopsesh from ${blocked.map((m) => m.name).join(", ")}`),
      h("div", { style: "font-size:12.5px" }, "If a macOS prompt is open, choose Allow. Otherwise turn hopsesh on under Privacy & Security › Local Network. Machines over Tailscale are not affected.")),
    h("button", { class: "btn", onclick: () => api("OpenLocalNetworkSettings").catch(fail) }, "Open Privacy & Security"),
    h("button", { class: "btn", onclick: async () => { await api("RetryLocalNetwork"); scan().then(render); } }, "Try again")));
  const sc = state.scope.kind === "cloud" && cloudOf(state.scope.value);
  if (sc) out.push(...cloudNotices(sc));
  if (!i.hasHosts && !sc) out.push(h("div", { class: "card notice" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `These are ${sys.here}'s sessions`), h("div", { class: "muted", style: "font-size:12.5px" }, "Add your other machines to see and bring over their sessions too.")),
    h("button", { class: "btn primary", onclick: () => go("machines") }, "Add machines")));
  if (i.skillState === "absent" && i.skillPrompt !== "declined") out.push(h("div", { class: "card notice" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, "Let your agents use hopsesh"), h("div", { class: "muted", style: "font-size:12.5px" }, "Ask an agent “bring my laptop session here” or “continue this in Codex”. It shows you the plan and acts only after you say yes.")),
    h("button", { class: "btn primary", onclick: async () => { try { await api("InstallSkill", false, false); toast("Installed the hopsesh skill in every agent"); } catch (e) { fail(e); } state.info = await api("Info"); render(); } }, "Install the skill"),
    h("button", { class: "btn", onclick: async () => { await api("DismissSkillOffer"); state.info = await api("Info"); render(); } }, "Not now")));
  else if (i.skillState === "stale" || i.skillState === "broken") out.push(h("div", { class: "card notice" },
    h("b", { style: "flex:1 1 360px" }, i.skillState === "stale" ? "The hopsesh skill is out of date" : "The hopsesh skill is damaged"),
    h("button", { class: "btn primary", onclick: async () => { try { await api("InstallSkill", false, false); toast("Updated the hopsesh skill"); } catch (e) { fail(e); } state.info = await api("Info"); render(); } }, i.skillState === "stale" ? "Update it" : "Repair it")));
  if (i.cliOffer) out.push(h("div", { class: "card notice" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `Use hopsesh in ${sys.terminal} too`), h("div", { class: "muted", style: "font-size:12.5px" }, `${cliHow()}. It always runs this app's version.`)),
    h("button", { class: "btn primary", onclick: async () => { try { await api("InstallCLI", false); toast(sys.win ? "hopsesh is ready in new terminal windows" : "hopsesh is ready in Terminal"); } catch (e) { fail(e); } state.info = await api("Info"); render(); } }, "Install command"),
    h("button", { class: "btn", onclick: async () => { await api("DismissCLIOffer"); state.info = await api("Info"); render(); } }, "Not now")));
  return out;
}

// cloudNotices are the head of a cloud's scope: honest about what a partial listing shows,
// with the vendor's picker and a pasted link for the rest, or why the cloud cannot be used.
function cloudNotices(c) {
  const out = [];
  const why = cloudBlock(c);
  if (why) out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `${c.title}: ${cloudState(c)[1]}`), h("div", { style: "font-size:12.5px" }, why)),
    c.status === "signed-out" ? h("button", { class: "btn", onclick: () => scan().then(render) }, "Refresh") : null,
    !c.allowed ? h("button", { class: "btn primary", onclick: () => turnOn(c) }, "Turn on") : null));
  else if (c.partial) out.push(h("div", { class: "cloud-head", role: "note" },
    h("span", { style: "flex:1 1 380px" }, `Showing the cloud sessions hopsesh started or brought here, and Remote Control mirrors. ${c.agentName} can show you the rest.`),
    h("button", { class: "btn", onclick: () => findDialog(c.name) }, `Find in ${c.agentName}…`),
    h("button", { class: "btn", onclick: () => pasteDialog(c.name) }, "Paste a link…")));
  return out;
}

function row(e) {
  const [k, words] = statusOf(e);
  const acts = actionsFor(e);
  const sel = state.sel && state.sel.machine === e.machine && state.sel.key === e.key;
  const bits = [];
  if (e.branch) bits.push(e.branch + (e.worktree ? ` (${e.worktree})` : ""));
  if (e.unpushed) bits.push(`+${e.unpushed} unpushed`);
  if (e.needs) bits.push("waiting for your answer in its window");
  else if (e.lastPrompt) bits.push(`“${e.lastPrompt}”`);
  const others = (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key));
  if (e.cloud) {
    bits.length = 0;
    if (e.cloud.branch) bits.push(e.cloud.branch);
    const last = e.history[e.history.length - 1];
    bits.push(last ? `${last.what.toLowerCase()} ${ago(last.when)}` : "from a pasted link");
  }
  const chips = [];
  if (e.cloud) chips.push(cloudChip(e.machine));
  if (e.cloud?.pr) chips.push(h("span", { class: "chip st-moved" }, "PR " + e.cloud.pr));
  if (e.mirror) chips.push(mirrorChip(e.mirror));
  for (const c of others) chips.push(h("span", { class: "chip" + (c.newest ? " st-warn" : "") }, `${c.newest ? "newest: " : "also "}${c.agentName} ${c.local ? "here" : "on " + c.machine}`));
  const where = e.cloud ? "cloud" : e.machine === here() ? "here" : e.machine;
  return h("div", { class: "row", role: "option", "aria-selected": sel ? "true" : "false", tabindex: sel ? "0" : "-1", "data-key": e.machine + "\u0000" + e.key,
      onclick: () => select(e), ondblclick: () => acts[0]?.run() },
    agentBadge(e.agent, e.agentName),
    h("div", { class: "main" }, h("span", { class: "t", title: e.title }, e.title), h("span", { class: "s" }, bits.join(" · ") || " "),
      chips.length ? h("div", { class: "copies" }, chips) : null),
    h("div", { class: "state" }, h("span", { class: "chip st-" + k }, h("span", { class: "dot " + k }), words), `${where} · ${ago(e.lastActive)}`),
    h("div", { class: "act" }, acts[0] ? h("button", { class: "btn small outline", style: "width:100%", title: acts[0].why || "", onclick: (ev) => { ev.stopPropagation(); acts[0].run(); } }, acts[0].short || acts[0].label) : null));
}

// mirrorChip links a session's copy on the vendor's site (Remote Control).
function mirrorChip(m) {
  return h("button", { class: "chip claude-link", title: m.url, onclick: (ev) => { ev.stopPropagation(); api("OpenURL", m.url).catch(fail); } }, icon(ICONS.external, 11), "mirrored on " + m.host);
}

function select(e) {
  state.sel = { machine: e.machine, key: e.key };
  state.handoffOpen = null;
  render();
  view.querySelector('.row[aria-selected="true"]')?.focus();
}

// cloudInspector is a cloud session's details: where it is, what can be done with it here,
// its repository and where it has been.
function cloudInspector(e) {
  const [k, words] = statusOf(e);
  const c = e.cloud, cl = cloudOf(e.machine);
  const acts = actionsFor(e);
  const why = acts[0]?.why;
  const host = (c.url.match(/^https:\/\/([^/]+)/) || [])[1] || "its site";
  const kv = (label, value) => [h("dt", {}, label), h("dd", {}, value)];
  return h("aside", { class: "inspector", "aria-label": "Cloud session details" },
    h("div", { style: "display:flex;flex-direction:column;gap:6px" },
      h("div", { style: "display:flex;gap:6px;flex-wrap:wrap;align-items:center" }, agentChip(e.agent, e.agentName), cloudChip(e.machine),
        h("span", { class: "chip st-" + k }, h("span", { class: "dot " + k }), words)),
      h("h2", {}, e.title),
      h("span", { class: "mono muted", style: "font-size:11.5px" }, c.id),
      h("button", { class: "btn", style: "align-self:flex-start", onclick: () => api("OpenURL", c.url).catch(fail) }, icon(ICONS.external, 13), "Open in browser")),
    h("div", { style: "display:flex;flex-direction:column;gap:8px" },
      h("button", { class: "btn primary big", disabled: !!why, onclick: acts[0].run }, acts[0].label, h("span", { class: "kbd" }, "↩")),
      acts.slice(1).map((a) => h("button", { class: "btn wrap", disabled: !!why, onclick: a.run }, "Bring here and continue in ▸ ", agentChip(a.id.slice(6), a.label.replace(/^.* in /, "")))),
      h("button", { class: "btn", disabled: !!why || !c.branch, title: c.branch ? "" : "hopsesh doesn't know its branch yet: bring it with its conversation once", onclick: () => planFor(e, { target: "", codeOnly: true }) }, "Get the code only"),
      h("div", { style: "display:flex;gap:8px;align-items:center;flex-wrap:wrap" }, h("button", { class: "btn", disabled: true, "aria-describedby": "arch-why" }, "Archive"),
        h("span", { id: "arch-why", class: "muted", style: "font-size:11.5px" }, `${e.agentName} archives only on ${host}`)),
      why ? h("span", { class: "warn", style: "font-size:12px" }, why) : null),
    h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Repository"),
      h("dl", { class: "kv" },
        kv("Repository", c.repo ? h("span", { class: "mono", style: "font-size:12px" }, c.repo) : h("span", { class: "muted" }, "Not known yet")),
        kv("Branch", c.branch ? h("span", { class: "mono", style: "font-size:12px" }, c.branch) : h("span", { class: "muted" }, `${e.agentName} fetches it when it brings the session`)),
        c.base ? kv("Base", h("span", { class: "mono", style: "font-size:12px" }, c.base.slice(0, 7))) : null,
        c.pr ? kv("PR", c.pr) : null,
        kv("State", [words, e.lastActive ? h("span", { class: "muted" }, ` · ${ago(e.lastActive)}`) : null]),
        c.checkout ? kv("Here", h("span", { class: "mono", style: "font-size:11.5px" }, c.checkout)) : null)),
    e.history.length ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Lineage"),
      e.history.map((x, i) => h("div", { class: "hop" }, h("span", { class: "dot" + (i === e.history.length - 1 ? " ok" : "") }),
        h("div", {}, h("div", {}, x.what), h("div", { class: "muted", style: "font-size:11px" }, when(x.when)))))) : null,
    cl && !cl.listable ? null : h("span", { class: "muted", style: "font-size:11.5px" }, `Bringing it here makes a new worktree; ${e.agentName} copies the conversation in ${sys.terminal}. The cloud session is not changed.`));
}

function inspector() {
  const e = selected();
  if (!e) return h("aside", { class: "inspector", "aria-label": "Session details" }, h("div", { class: "empty" }, "Select a session to see what you can do with it."));
  if (e.cloud) return cloudInspector(e);
  const [k, words] = statusOf(e);
  const acts = actionsFor(e);
  const local = e.machine === here();
  const others = (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key));
  let note = "";
  if (!local && e.live) note = `It is still open on ${e.machine}: hopsesh hands it off, and marks that copy once it ends.`;
  if (local && e.live) note = e.needs ? "It is waiting for your answer in its own window." : "It is running here, in its own window.";
  if (local && !e.live && !e.hereNewest) note = "A newer copy is elsewhere; bring that one here instead.";
  return h("aside", { class: "inspector", "aria-label": "Session details" },
    h("div", { style: "display:flex;flex-direction:column;gap:6px" },
      h("div", { style: "display:flex;gap:6px;flex-wrap:wrap" }, agentChip(e.agent, e.agentName), h("span", { class: "chip st-" + k }, h("span", { class: "dot " + k }), words)),
      h("h2", {}, e.title),
      h("span", { class: "muted", style: "font-size:12px" }, `${local ? "On " + sys.here : "On " + e.machine} · last active ${ago(e.lastActive)} · ${bytes(e.sizeKB * 1024)}`)),
    acts.length ? h("div", { style: "display:flex;flex-direction:column;gap:8px" },
      h("button", { class: "btn primary big", onclick: acts[0].run }, acts[0].label, h("span", { class: "kbd" }, "↩")),
      acts.slice(1).map((a, i) => h("button", { class: "btn", onclick: a.run }, a.label, i === 0 ? h("span", { class: "kbd" }, keys("mod+enter")) : null)),
      note ? h("span", { class: "muted", style: "font-size:11.5px" }, note) : null)
      : h("span", { class: "muted", style: "font-size:12px" }, note),
    handoffMenu(e, state.handoffOpen === e.machine + "\u0000" + e.key, () => {
      const k = e.machine + "\u0000" + e.key;
      state.handoffOpen = state.handoffOpen === k ? null : k;
      render();
      view.querySelector('[role="menu"] [role="menuitem"]:not([disabled])')?.focus();
    }),
    e.lastPrompt ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Last prompt"), h("span", { style: "font-style:italic;color:var(--ink2)" }, `“${e.lastPrompt}”`)) : null,
    h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Repository"),
      e.group.noRepo ? h("span", { class: "muted" }, "Started outside a git checkout") : [
        h("span", { class: "mono", style: "font-size:12px" }, e.group.remote || e.group.name + " (no remote)"),
        e.branch ? h("span", {}, e.branch, e.worktree ? h("span", { class: "muted" }, ` in a ${e.worktree}` + (e.mainBranch ? ` · main folder on ${e.mainBranch}` : "")) : null) : null,
        e.unpushed || e.dirty ? h("span", { class: "warn" }, [e.unpushed ? count(e.unpushed, "unpushed commit") : "", e.dirty ? count(e.dirty, "uncommitted file") : ""].filter(Boolean).join(" · ")) : null,
        e.group.local ? h("span", { class: "ok" }, "Cloned here at " + e.group.local) : e.group.remote ? h("span", { class: "muted" }, `Not on ${sys.here}: hopsesh can clone it`) : null],
      e.cwd !== e.group.local ? h("span", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, e.cwd) : null),
    e.mirror ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Mirrored"),
      h("span", {}, `Remote Control keeps a copy on ${e.mirror.host} while it runs. `, h("button", { class: "link", onclick: () => api("OpenURL", e.mirror.url).catch(fail) }, "Open it"))) : null,
    others.length ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Other copies"),
      others.map((c) => h("span", {}, `${c.agentName} on ${c.local ? sys.here : c.machine}`, h("span", { class: "muted" }, c.newest ? " · newest" : c.mark ? " · marked" : " · older")))) : null,
    e.history.length ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Where it has been"),
      e.history.map((x, i) => h("div", { class: "hop" }, h("span", { class: "dot" + (i === e.history.length - 1 ? " ok" : "") }),
        h("div", {}, h("div", {}, x.what), h("div", { class: "muted", style: "font-size:11px" }, when(x.when)))))) : null);
}

export function render() {
  if (!state.scan) return;
  const s = state.scan;
  const list = scoped();
  const groups = s.groups.map((g) => ({ g, rows: g.entries.filter((e) => list.some((x) => x.machine === e.machine && x.key === e.key)) })).filter((x) => x.rows.length);
  const agents = (state.info.agents || []).filter((a) => a.enabled);
  const content = h("section", { class: "content" },
    h("div", { class: "toolbar" },
      h("h1", {}, scopeTitle()),
      h("span", { class: "muted", style: "font-size:12px" }, `${list.length} session${list.length === 1 ? "" : "s"}`),
      h("span", { class: "spacer" }),
      agents.length > 1 && state.scope.kind !== "agent" ? h("label", { class: "visually-hidden", for: "f-agent" }, "Agent") : null,
      agents.length > 1 && state.scope.kind !== "agent" ? h("select", { id: "f-agent", onchange: (ev) => { state.filter.agent = ev.target.value; render(); } },
        h("option", { value: "" }, "All agents"), agents.map((a) => h("option", { value: a.id, selected: state.filter.agent === a.id }, a.name))) : null,
      h("button", { class: "btn small", "aria-pressed": state.filter.live ? "true" : "false", style: state.filter.live ? "border-color:var(--accent);color:var(--accent)" : "",
        onclick: () => { state.filter.live = !state.filter.live; render(); } }, "Live only")),
    h("div", { class: "list" }, notices(),
      groups.length ? groups.map(({ g, rows }) => h("div", { class: "card", role: "listbox", "aria-label": g.name },
        h("div", { class: "card-h" }, h("span", { class: "name" }, g.name), g.remote ? h("span", { class: "mono muted", style: "font-size:11.5px" }, g.remote) : null, h("span", { class: "spacer" }),
          h("span", { style: "font-size:11.5px", class: g.local ? "ok" : g.noRepo || g.noRemote ? "muted" : "warn" },
            g.noRepo ? "Outside a git checkout" : g.noRemote ? "A checkout without a remote" : g.local ? "Cloned here" : `Not on ${sys.here}`)),
        rows.map(row)))
      : h("div", { class: "empty" }, state.scope.kind === "needs" ? "Nothing needs you right now." : "No sessions here.")));
  fill(view, h("div", { class: "three" }, sidebar(), content, inspector()));
}

// Keyboard: ↑/↓ move through the list, ↩ runs the main action, ⌘↩ (Ctrl+Enter) the second.
document.addEventListener("keydown", (ev) => {
  if (!state.scan || document.querySelector("dialog[open]") || /INPUT|TEXTAREA|SELECT/.test(ev.target.tagName)) return;
  if (!view.querySelector(".three")) return;
  const rows = [...view.querySelectorAll(".row")];
  const i = rows.findIndex((r) => r.getAttribute("aria-selected") === "true");
  if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    ev.preventDefault();
    const next = rows[Math.max(0, Math.min(rows.length - 1, i + (ev.key === "ArrowDown" ? 1 : -1)))];
    if (next) { const [machine, key] = next.dataset.key.split("\u0000"); state.sel = { machine, key }; render(); view.querySelector('.row[aria-selected="true"]')?.focus(); }
  } else if (ev.key === "Enter") {
    const e = selected();
    if (!e) return;
    const acts = actionsFor(e);
    const a = ev.metaKey || ev.ctrlKey ? acts[1] : acts[0];
    if (a) { ev.preventDefault(); a.run(); }
  }
});

screen("sessions", async (rescan = false) => {
  if (!state.scan || rescan || state.stale) await scan();
  render();
});
