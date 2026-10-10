// The Sessions screen: places on the left (Needs you, All sessions, the machines, the
// clouds), the sessions in the middle as one tree (grouped, sorted and filtered by
// listview.js), the selected session on the right (inspector.js). Every action on a
// session comes from actions.js, which the palette uses too.
import { accountLabel, accountTitle, loadError, api, on, h, fill, icon, ICONS, view, state, screen, go, current, loading, toast, fail, cap, ago, agentBadge, machineStatus, sys, keys,
  entries, selected, here, agentInfo, $, count, clouds, cloudOf, cloudState, cloudChip, dialog, errText, rich, cloudTitle } from "./core.js";
import { planPicked } from "./plan.js";
import { healthNotice, loadHookHealth, withCLI, chip as roleChip } from "./guard.js";
import { dividers, apply as applyLayout } from "./layout.js";
import { exitOf, exitWords, waiting, strayWaiting, showTerminal, tabs, onTabs } from "./term.js";
import { model, statusOf, statusKey, placesOf, placeCount, placeName, showPlace, cloudBlock, onSelect, onTurnOn, key as entryKey } from "./actions.js";
import { list, load as loadList, toolbar, counts, apply as applyFilters, groupsOf, tree, onChange, onRows, decide, toggleDisplay, clearFilters, activeFacets,
  openGroupOf, focusTree, rowByKey, setAll, displayCommand, toggleFilter, save as saveList } from "./listview.js";
import { inspector, openChevron, onRenamed, placeIcon } from "./inspector.js";
import { openMenu, isOpen } from "./menu.js";

export { statusOf, cloudBlock };
export { actionsFor } from "./actions.js";

// scan reads every machine again. The list stays while it runs.
let pendingScan=null;
export async function scan(method = "Scan", background = false) {
  if (state.scanning) {
    // A newly pasted cloud link or explicit refresh must not disappear merely
    // because an earlier progressive scan has already queried that source.
    if(!pendingScan) pendingScan=(async()=>{while(state.scanning)await new Promise(resolve=>setTimeout(resolve,50));pendingScan=null;return scan(method,background)})();
    return pendingScan;
  }
  state.scanning = true;
  state.scanError = "";
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
    const result=await api(method);
    if(background)queueScan(result);else acceptScan(result);
    state.info = await api("Info");
    state.activity = await api("Activity").catch(() => state.activity);
  } catch (e) {
    state.scanError = errText(e);
    state.stale = true;
    state.scanning = false;
    if (!state.scan && current === "sessions") fill(view, loadError(e, () => go("sessions", true)));
    else fail(e);
    freshness();
    return;
  }
  state.scanning = false;
  state.presence = {}; // the scan's own is newer
  freshness();
  if (!background && !pendingScan && state.sel && !selected()) state.sel = null;
}

// refreshHere reads this machine again (RefreshHere), keeping what the last scan found
// elsewhere: quiet, with the list kept as it is until the answer comes.
async function refreshHere(background = false) {
  if (state.scanning || !state.scan) return;
  state.scanning = true;
  try {
    const result=await api("RefreshHere");
    if(background)queueScan(result);else acceptScan(result);
    state.presence = {};
  } catch { /* the next refresh tries again; the list stays */ }
  state.scanning = false;
  freshness();
  if (!background && state.sel && !selected()) state.sel = null;
}

function freshness() {
  const el = $("#fresh");
  el.textContent = state.scanning || state.scan?.discovering ? (entries().length ? `${entries().length} sessions found · Checking for changes…` : "Finding sessions…") : state.scan?.cached ? "Showing saved sessions · Checking for changes…" : state.scan?.updated ? "updated " + ago(state.scan.updated) : "Not checked yet";
  const other = state.scan && state.scan.elsewhere !== state.scan.updated;
  el.title = other ? `${sys.Here}: ${ago(state.scan.updated)}. Your other machines and the clouds: ${ago(state.scan.elsewhere)}.` : "";
}
setInterval(freshness, 30000);

// The list keeps itself current while the window is in front: this machine every
// minute (a local read), and everything (SSH to every machine, the clouds' commands) when
// the window comes back after five minutes or more. Never while a dialog, a plan or a
// menu is open, or another screen is shown.
const HERE_EVERY = 60_000, ALL_AFTER = 5 * 60_000;
const since = (iso) => (iso ? Date.now() - new Date(iso).getTime() : Infinity);
// A presence response between pointer-down and pointer-up must not replace the
// button under the pointer: WebKit would then swallow the user's click.
const busy = () => pointerHeld || !state.scan || state.scanning || current !== "sessions" || document.hidden || !!document.querySelector("dialog[open], button:active") || isOpen();
async function autoRefresh(all) {
  if (busy()) return;
  if (all) await scan("Scan",true); else if (!state.info?.desktopManaged) await refreshHere(true);
  backgroundRender();
}
setInterval(() => { if (document.hasFocus() && (since(state.scan?.elsewhere) >= ALL_AFTER * 2 || since(state.scan?.updated) >= HERE_EVERY)) autoRefresh(since(state.scan?.elsewhere) >= ALL_AFTER * 2); }, 15_000);
const comeBack = () => { if (!document.hidden && (since(state.scan?.elsewhere) >= ALL_AFTER || since(state.scan?.updated) >= HERE_EVERY / 2)) autoRefresh(since(state.scan?.elsewhere) >= ALL_AFTER); };
document.addEventListener("visibilitychange", comeBack);
window.addEventListener("focus", comeBack);

// Presence: where this machine's sessions are open (the agents' registries and the
// process table; never a terminal app's), every 5 s while the window is in front on
// Sessions, every 30 s while it is visible, not at all while hidden; at once when the
// window comes back or a tab changes. Other machines' come with their scans.
let presTimer = 0, presSig = "", presBusy = false;
function presenceSoon(ms) {
  clearTimeout(presTimer);
  if (state.info?.desktopManaged || document.hidden) return; // backend owns desktop refresh; paused until visible again
  presTimer = setTimeout(pollPresence, ms);
}
const presenceEvery = () => (document.hasFocus() && current === "sessions" ? 5_000 : 30_000);
async function pollPresence() {
  if (presBusy || !state.scan || state.scanning || document.hidden) { presenceSoon(presenceEvery()); return; }
  presBusy = true;
  const p = await api("Presence").catch(() => null);
  presBusy = false;
  if (p) {
    const sig = JSON.stringify(p.entries);
    if (sig !== presSig) {
      presSig = sig;
      state.presence = p.entries;
      if (current === "sessions") backgroundRender();
    }
  }
  presenceSoon(presenceEvery());
}
document.addEventListener("visibilitychange", () => { if (!document.hidden) presenceSoon(0); });
window.addEventListener("focus", () => presenceSoon(0));
onTabs(() => presenceSoon(300));

// needsYou: the session waits for its person (its agent says so, or its tab here does).
const needsYou = (e) => statusKey(e) === "needs";

// scoped are the sessions of the place chosen in the sidebar.
function scoped() {
  const sc = state.scope;
  return entries().filter((e) =>
    (sc.kind !== "needs" || needsYou(e)) &&
    (sc.kind !== "here" || (e.machine === here() && !e.cloud)) &&
    (sc.kind !== "cloud" || (e.cloud && e.machine === sc.value)) &&
    (sc.kind !== "machine" || e.machine === sc.value));
}

function scopeTitle() {
  const sc = state.scope;
  return { all: "All sessions", needs: "Needs you", here: sys.Here, machine: sc.value, cloud: cloudOf(sc.value)?.title || sc.value }[sc.kind] || "All sessions";
}

// setScope shows another place; the menus close, and a selection not in it goes.
function setScope(sc) { state.scope = sc; state.handoffOpen = null; render(); }

// sidebar is the places: Needs you, All sessions, the machines (this one first) and the
// clouds that are on, with what each needs; Activity and receiving below.
function sidebar() {
  const s = state.scan, all = entries();
  const cur = (k, v) => state.scope.kind === k && (v === undefined || state.scope.value === v) ? "true" : "false";
  const needs = all.filter(needsYou).length + strayWaiting().length;
  const nTabs = tabs.size, nWait = waiting().length;
  const machines = s.machines.filter((m) => !m.local);
  const dotFor = (m) => machineStatus(m.status)[0];
  const act = state.activity;
  const mine = all.filter((e) => e.machine === here() && !e.cloud);
  const local = s.machines.find((m) => m.local);
  const agentSubtitle = (m) => (m?.agentNames || []).join(", ") || (m?.phase === "reading" ? "Reading sessions…" : "No agents detected");
  const scanSubtitle=m=>m?.phase==="reading"?"Reading sessions…":m?.phase==="saved"?`Saved · last checked ${ago(m.checkedAt)||"previously"}`:"";
  const on = clouds().filter((c) => c.allowed);
  return h("nav", { class: "sidebar", id: "sidebar", "aria-label": "Places" },
    h("button", { class: "side-btn", "aria-current": cur("needs"), onclick: () => setScope({ kind: "needs" }) },
      h("span", { class: "dot" + (needs ? " needs" : "") }), h("span", { style: needs ? "font-weight:600" : "" }, "Needs you"),
      needs ? h("span", { class: "chip st-needs", style: "margin-left:auto" }, needs) : h("span", { class: "count" }, "0")),
    h("button", { class: "side-btn", "aria-current": cur("all"), onclick: () => setScope({ kind: "all" }) }, icon(ICONS.all), "All sessions", h("span", { class: "count" }, all.length)),
    h("div", { class: "side-h" }, "Machines"),
    h("button", { class: "side-btn", "aria-current": cur("here"), onclick: () => setScope({ kind: "here" }) },
      h("span", { class: "dot ok" }), h("span", { class: "label" }, h("span", {}, sys.Here), h("small", { title: (local?.agents || []).join(", ") }, agentSubtitle(local)), scanSubtitle(local) ? h("small",{},scanSubtitle(local)) : null),
      h("span", { class: "count" }, mine.length)),
    machines.map((m) => h("button", { class: "side-btn", "aria-current": cur("machine", m.name), onclick: () => setScope({ kind: "machine", value: m.name }) },
      h("span", { class: "dot " + dotFor(m) }),
      h("span", { class: "label" }, h("span", {}, m.name), h("small", { class: dotFor(m) === "ok" ? "" : "warn", title: dotFor(m) === "ok" ? (m.agents || []).join(", ") : m.hint || m.error }, dotFor(m) === "ok" ? agentSubtitle(m) : machineStatus(m.status)[1]),scanSubtitle(m) ? h("small",{},scanSubtitle(m)) : null),
      h("span", { class: "count" }, m.status === "ok" ? m.sessions : ""))),
    h("button", { class: "side-btn", style: "color:var(--accent)", onclick: () => go("machines") }, icon(ICONS.plus), machines.length ? "Add a machine" : "Add your other machines"),
    clouds().length ? h("div", { class: "side-h" }, "Clouds") : null,
    on.map(cloudSide),
    on.length < clouds().length ? h("button", { class: "side-btn", style: "color:var(--accent)", onclick: () => go("machines", "clouds") }, icon(ICONS.plus), "Turn on a cloud…") : null,
    h("div", { class: "side-foot" },
      nTabs ? h("button", { class: "side-btn", style: "padding:6px 0", onclick: () => showTerminal() }, icon(["M3 4h18v16H3z", "m7 9 3 3-3 3M12 15h5"]), "Terminal",
        h("span", { class: "count" + (nWait ? " warn" : "") }, nWait ? `${nWait} waiting` : `${count(nTabs, "tab")}`)) : null,
      h("button", { class: "side-btn", style: "padding:6px 0", onclick: () => go("activity") }, icon(ICONS.clock), "Activity",
        act ? h("span", { class: "count" }, act.items.filter((x) => x.canUndo).length + " can undo") : null),
      h("button", { class: "link", style: "text-align:left;text-decoration:none;color:var(--muted)", onclick: () => go("machines") },
        h("span", { class: "dot " + (state.info.receive ? "ok" : ""), style: "display:inline-block;margin-right:6px" }), "Receiving sessions: " + (state.info.receive ? "on" : "off")),
      updateNote()));
}

// cloudSide is a cloud that is turned on, in the sidebar: a dot, its name and one word,
// and its count (without Remote Control mirrors: those run on this machine).
function cloudSide(c) {
  const [dot, word] = cloudState(c);
  const cur = state.scope.kind === "cloud" && state.scope.value === c.name ? "true" : "false";
  return h("button", { class: "side-btn", "aria-current": cur, title: c.title, onclick: () => setScope({ kind: "cloud", value: c.name }) },
    h("span", { class: "dot " + dot }),
    h("span", { class: "label" }, h("span", {}, c.title), h("small", { class: dot === "ok" || dot === "half" ? "ok" : dot === "off" ? "" : "warn" }, word)),
    h("span", { class: "count" }, c.listable && (c.status === "ready" || c.status === "cli-old") ? c.sessions - (c.mirrors || 0) : "–"));
}

// turnOn turns a cloud on (allows it) and reads it.
async function turnOn(c) {
  try { await api("SetCloudAllowed", c.name, true); } catch (e) { fail(e); return; }
  toast(`${c.title} is on: hopsesh reaches it through ${c.driver}, signed in as you`);
  state.scope = { kind: "cloud", value: c.name };
  await scan();
  render();
}
onTurnOn(turnOn);

// pasteDialog asks for a cloud session's link or id, and the checkout here it works on.
export async function pasteDialog(cloud) {
  const c = cloud ? cloudOf(cloud) : clouds().find((x) => x.fetchable);
  if (!c) { toast("No cloud can bring sessions here"); return; }
  const checkouts = await api("Checkouts");
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
  const checkouts = await api("Checkouts");
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
  if (state.scanning || s.discovering || s.cached) out.push(h("div",{class:"discovery-note",role:"status","aria-live":"polite"},s.cached ? "Showing saved sessions. Checking for changes…" : `${entries().length} sessions found. Other sources may still be loading.`,h("button",{class:"link",onclick:()=>api("CancelScan")},"Stop checking")));
  if(state.scope.kind==="all") for(const m of s.machines.filter(m=>m.status!=="ok" || m.phase==="error")) out.push(h("div",{class:"card notice warn-card",role:"status"},h("span",{},`${m.name===here()?sys.Here:m.name}: ${m.error||machineStatus(m.status)[1]}. Saved sessions remain available.`),h("button",{class:"btn",onclick:async()=>{if(m.local)await refreshHere();else {await api("ScanMachine",m.name);acceptScan(await api("ScanSnapshot"))}render()}},"Retry")));
  if (state.scanError) out.push(h("div", { class: "card notice warn-card", role: "alert" }, h("span", {}, "Refresh failed. Showing the previous results. " + state.scanError), h("button", { class: "btn", onclick: () => go("sessions", true) }, "Retry refresh")));
  const picked = state.scope.kind === "machine" && s.machines.find((m) => m.name === state.scope.value);
  if (picked && picked.status !== "ok") out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `${picked.name}: ${machineStatus(picked.status)[1]}`),
      h("div", { style: "font-size:12.5px" }, rich(cap(picked.hint || picked.error)))),
    h("button", { class: "btn", onclick: () => go("machines") }, "Open Machines")));
  const blocked = s.machines.filter((m) => m.status === "local-network");
  if (blocked.length) out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `macOS is blocking hopsesh from ${blocked.map((m) => m.name).join(", ")}`),
      h("div", { style: "font-size:12.5px" }, "If a macOS prompt is open, choose Allow. Otherwise turn hopsesh on under Privacy & Security › Local Network. Machines over Tailscale are not affected.")),
    h("button", { class: "btn", onclick: () => api("OpenLocalNetworkSettings").catch(fail) }, "Open Privacy & Security"),
    h("button", { class: "btn", onclick: async () => { await api("RetryLocalNetwork"); await scan(); render(); } }, "Try again")));
  const stray = strayWaiting();
  if (stray.length) out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, stray.length === 1 ? `“${stray[0].title}” is waiting for you` : `${stray.length} programs are waiting for you`),
      h("div", { style: "font-size:12.5px" }, "In the hopsesh Terminal window.")),
    h("button", { class: "btn", onclick: () => showTerminal(stray[0].id) }, "Show the terminal")));
  const sc = state.scope.kind === "cloud" && cloudOf(state.scope.value);
  if (sc) out.push(...cloudNotices(sc));
  if (state.scope.kind !== "all") return out;
  if (i.skillState === "stale" || i.skillState === "broken") out.push(h("div", { class: "card notice" },
    h("b", { style: "flex:1 1 360px" }, i.skillState === "stale" ? "The hopsesh skill is out of date" : "The hopsesh skill is damaged"),
    h("button", { class: "btn primary", onclick: async () => { try { await withCLI(() => api("InstallSkill", false, false)); toast("Updated the hopsesh skill"); } catch (e) { fail(e); } state.info = await api("Info"); render(); } }, i.skillState === "stale" ? "Update it" : "Repair it")));
  out.push(healthNotice(render));
  out.push(setupLine());
  return out;
}

// setupLine is the one line of what is left to set up, on All sessions only: each step
// opens its place in Machines or Settings, and drops out once done (or declined there);
// ✕ closes the line for good. Every step stays in Machines and Settings.
function setupLine() {
  const i = state.info;
  if (i.setupDismissed) return null;
  const steps = [
    !i.hasHosts && ["Add machines", "Your other machines' sessions, here too", () => go("machines")],
    i.skillState === "absent" && i.skillPrompt !== "declined" && ["Install the skill", "Your agents can find and hop sessions, after asking you", () => go("settings", "skill")],
    i.cliOffer && ["Install the command", `${cliHowText()}`, () => go("settings", "cli")],
  ].filter(Boolean);
  if (!steps.length) return null;
  const close = async () => { try { await api("DismissSetup"); state.info = await api("Info"); } catch (e) { fail(e); } render(); };
  return h("div", { class: "setup", role: "note", "aria-label": "Finish setting up" },
    h("b", {}, `Finish setting up (${3 - steps.length} of 3 done):`),
    steps.map(([label, why, run], n) => [n ? h("span", { class: "muted", "aria-hidden": "true" }, "·") : null, h("button", { class: "link", title: why, onclick: run }, label)]),
    h("span", { class: "spacer" }),
    h("button", { class: "x", "aria-label": "Close: don't show this again", title: "Don't show this again (each step stays in Machines and Settings)", onclick: close }, "✕"));
}
const cliHowText = () => (sys.win ? "Puts the app's folder on your PATH (no admin rights)" : "Links the hopsesh command into ~/.local/bin (no password)");

// cloudNotices are the head of a cloud's scope: honest about what a partial listing shows,
// with the vendor's picker and a pasted link for the rest, or why the cloud cannot be used.
function cloudNotices(c) {
  const out = [];
  const why = cloudBlock(c);
  if (why) out.push(h("div", { class: "card notice warn-card", role: "status" },
    h("div", { style: "flex:1 1 360px" }, h("b", {}, `${c.title}: ${cloudState(c)[1]}`), h("div", { style: "font-size:12.5px" }, rich(why))),
    c.status === "signed-out" ? h("button", { class: "btn", onclick: () => scan().then(render) }, "Refresh") : null,
    !c.allowed ? h("button", { class: "btn primary", onclick: () => turnOn(c) }, "Turn on") : null));
  else if (c.partial) out.push(h("div", { class: "cloud-head", role: "note" },
    h("span", { style: "flex:1 1 380px" }, `Showing the cloud sessions hopsesh started or brought here. ${c.agentName} can show you the rest; sessions mirrored by Remote Control run on their machine and are listed there.`),
    h("button", { class: "btn", onclick: () => findDialog(c.name) }, `Find in ${c.agentName}…`),
    h("button", { class: "btn", onclick: () => pasteDialog(c.name) }, "Paste a link…")));
  return out;
}

// ---- Rows ----

// chipFor is a presence chip: the place's picture and words, an amber dot when it waits.
function chipFor(e, p) {
  const words = p.kind === "hopsesh" ? count(p.count, "tab") : placeName(p, e) + (p.count > 1 ? ` ×${p.count}` : "");
  const tip = p.kind === "hopsesh" ? p.tabs.map((t) => `hopsesh Terminal — tab “${t.title}”${t.attention ? " · waiting" : ""}`).join("\n")
    : `${placeName(p, e)}${p.count > 1 ? ` — ${p.count} processes` : ""}${p.waiting ? " · waiting" : ""}`;
  return h("button", { class: "pchip" + (p.kind === "hopsesh" ? " hop" : ""), type: "button", tabindex: "-1", title: tip, "aria-label": `${words}${p.waiting ? ", waiting for you" : ""}: show`,
    onclick: (ev) => { ev.stopPropagation(); return showPlace(e, p); } },
    p.kind === "claude-app" || p.kind === "codex-app" ? agentBadge(e.agent, e.agentName) : placeIcon(p.kind, 11),
    h("span", { class: "pc-w" }, words), h("span", { class: "pc-n", "aria-hidden": "true" }, String(p.count || 1)),
    p.waiting ? h("span", { class: "dot needs", "aria-hidden": "true" }) : null);
}

// presenceChips are at most two places, then "+N" (a popover lists them all), and
// "Open twice" when two processes write one session.
function presenceChips(e) {
  const ps = placesOf(e);
  if (!ps.length) return [];
  const out = ps.slice(0, 2).map((p, i) => { const c = chipFor(e, p); if (!i) c.classList.add("keep"); return c; });
  if (ps.length > 2) out.push(h("button", { class: "pchip more", type: "button", tabindex: "-1", title: ps.slice(2).map((p) => placeName(p, e)).join(", "),
    onclick: (ev) => { ev.stopPropagation(); openMenu(ev.currentTarget, ps.map((p) => ({ label: `Show in ${placeName(p, e)}`, sub: p.count > 1 ? `${p.count} processes` : "", run: () => showPlace(e, p) })), { label: "Open in" }); } },
    `+${ps.length - 2}`));
  if (placeCount(ps) >= 2) out.push(h("span", { class: "chip st-warn twice keep" }, icon(["M12 4 2.5 20h19z", "M12 10v4M12 17v.5"], 11), "Open twice"));
  return out;
}

// mirrorChip links a session's copy on the vendor's site (Remote Control).
function mirrorChip(m) {
  return h("button", { class: "chip claude-link", tabindex: "-1", title: m.url, onclick: (ev) => { ev.stopPropagation(); api("OpenURL", m.url).catch(fail); } }, icon(ICONS.external, 11), "mirrored on " + m.host);
}

// row is one session in the tree: its agent, title and branch, the last prompt and where
// it is open (comfortable rows), its state with where and when, and its main action.
function row(e, level) {
  const [k, words] = statusOf(e);
  const m = model(e);
  const p = m.primary;
  const sel = state.sel && state.sel.machine === e.machine && state.sel.key === e.key;
  const bits = [];
  let branch = "";
  if (e.cloud) {
    branch = e.cloud.branch || "";
    if (e.cloud.branch) bits.push(e.cloud.branch);
    if (e.cloud.changes) bits.push(e.cloud.changes);
    const first = e.history[0], last = e.history[e.history.length - 1];
    if (e.cloud.state === "unknown" && first) bits.push(`started ${ago(first.when)}`);
    else bits.push(last ? `${last.what[0].toLowerCase()}${last.what.slice(1)} ${ago(last.when)}` : "from a pasted link");
  } else {
    branch = e.branch ? e.branch + (e.worktree ? ` (${e.worktree})` : "") : "";
    if (branch) bits.push(branch);
    if (e.unpushed) bits.push(`+${e.unpushed} unpushed`);
    if (statusKey(e) === "needs" && !placesOf(e).length) bits.push(e.app ? `waiting for your answer in the ${e.app} app` : "waiting for your answer");
    else if (e.lastPrompt) bits.push(`“${e.lastPrompt}”`);
  }
  const repoWord = list.groupBy === "repository" || e.group.noRepo ? "" : e.group.name.replace(/ \(no remote\)$/, "");
  if(e.journey?.fork) bits.push("separate fork");
 if(e.journey?.roundTrips) bits.push(`${e.journey.roundTrips} round trips`);
 const chips = presenceChips(e);
if(e.profile) chips.push(h("span", {class:"chip account-chip",title:accountTitle(e.profile)},accountLabel(e.profile)));
  if (e.cloud) chips.push(cloudChip(e.machine));
  const rc = roleChip(e);
  if (rc) chips.unshift(rc);
  if (e.cloud?.pr) chips.push(h("span", { class: "chip st-moved" }, "PR " + e.cloud.pr));
  if (e.mirror) chips.push(mirrorChip(e.mirror));
  const ext = !e.live && e.machine === here() && !placesOf(e).length ? exitOf(e) : null;
  if (ext) chips.push(h("span", { class: "chip " + (ext.code === 0 ? "st-idle" : ext.closed ? "st-ended" : "st-error"), title: `How its last run in ${ext.terminal} ended` }, `${exitWords(ext)} in ${ext.terminal}`));
  for (const c of (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key))) chips.push(h("span", { class: "chip" + (c.newest ? " st-warn" : "") }, `${c.newest ? "newest: " : "also "}${copyWhere(c)}`));
  const where = e.cloud ? "cloud" : e.machine === here() ? sys.here : e.machine;
  return h("div", { class: "row", role: "treeitem", "aria-level": String(level), "aria-selected": sel ? "true" : "false", tabindex: sel ? "0" : "-1", "data-key": entryKey(e),
      onclick: () => select(e), ondblclick: () => { if (p && !p.disabled) return p.run(); } },
    h("span", { class: "r-ic" }, agentBadge(e.agent, e.agentName, e.cloud ? cloudTitle(e.machine) : "")),
    h("div", { class: "r-main" },
      h("span", { class: "r-line" }, h("span", { class: "t", title: e.title }, e.title),
        h("span", { class: "b" }, [repoWord, branch].filter(Boolean).join(" · "))),
      h("span", { class: "s" }, bits.join(" · ") || " "),
      chips.length ? h("div", { class: "copies" }, chips) : null),
    h("div", { class: "r-state" }, h("span", { class: "chip st-" + k }, h("span", { class: "dot " + k }), words), h("span", { class: "w" }, `${where} · ${ago(e.lastActive)}`)),
    h("div", { class: "act" }, p ? h("button", { class: "btn small outline", tabindex: "-1", disabled: !!p.disabled, title: p.why || p.label, "aria-label": p.label,
      onclick: (ev) => { ev.stopPropagation(); return p.run(); } }, h("span", { class: "btn-t" }, p.short || p.label)) : null));
}

function copyWhere(c) {
  if (cloudOf(c.machine)) return cloudTitle(c.machine);
  return `${c.agentName} ${c.local ? "here" : "on " + c.machine}`;
}

// ---- Selection: attributes change, the inspector is drawn again; the list stays ----

export function select(e, focus = true) {
  state.sel = { machine: e.machine, key: e.key };
  state.handoffOpen = null;
  const t = view.querySelector(".tree");
  const k = entryKey(e);
  if (t) {
    for (const r of t.querySelectorAll('[aria-selected="true"]')) r.setAttribute("aria-selected", "false");
    for (const r of t.querySelectorAll('[tabindex="0"]')) r.tabIndex = -1;
    const r = rowByKey(t, k);
    if (r) { r.setAttribute("aria-selected", "true"); r.tabIndex = 0; if (focus) { r.focus({ preventScroll: true }); inView(r, false); } }
  }
  const old = $("#inspector");
  if (old) { old.replaceWith(inspector(e, old)); applyLayout(); }
}
onSelect((e) => showEntry(e));

// The tree's keys select rows and run their actions.
onRows((el) => {
  const [machine, key] = el.dataset.key.split("\u0000");
  const e = entries().find((x) => x.machine === machine && x.key === key);
  if (e) select(e);
}, (el, mod) => {
  const e = selected();
  if (!e || el.dataset.key !== entryKey(e)) return;
  if (mod) { openChevron(e); return; }
  const p = model(e).primary;
  if (p && !p.disabled) return p.run();
});

// ---- Drawing ----

let lastShown = [], lastScope = [];
function body(shown, inScope) {
  if (!shown.length) {
    if(state.scanning || state.scan.discovering) return h("div",{class:"empty",role:"status"},"Looking for sessions in this view… Results appear as they are found.");
    const hidden = inScope.length - shown.length;
    if (hidden > 0) return h("div", { class: "empty" }, h("span", {}, "No sessions match · ", count(hidden, "session"), " hidden by filters · ",
      h("button", { class: "link", onclick: () => { clearFilters(); if (list.text) { list.text = ""; $("#list-filter").value = ""; refreshList(); } } }, "Clear filters")));
    return h("div", { class: "empty" }, state.scope.kind === "needs" ? "Nothing needs you right now." : "No sessions here.");
  }
  return tree(groupsOf(shown), row, state.sel ? state.sel.machine + "\u0000" + state.sel.key : "");
}

// refreshList draws the list again (a filter, the display, a group) and keeps the
// toolbar, so typing in the text filter goes on; the inspector follows the selection.
export function refreshList() {
  const l = view.querySelector(".list");
  if (!l) return render();
  const inScope = scoped();
  const shown = applyFilters(inScope, state.scope);
  const dropped = state.sel && !shown.some((x) => x.machine === state.sel.machine && x.key === state.sel.key);
  if (dropped) state.sel = null;
  const focusIn = l.contains(document.activeElement) ? document.activeElement.closest("[data-key],[data-gkey]") : null;
  const fk = focusIn?.dataset.key, gk = focusIn?.dataset.gkey;
  fill(l, notices(), body(shown, inScope));
  counts(shown.length, inScope.length, state.scope);
  if (dropped) $("#inspector")?.replaceWith(inspector(null));
  if (fk) rowByKey(l, fk)?.focus({ preventScroll: true });
  else if (gk) l.querySelector(`.grp[data-gkey="${CSS.escape(gk)}"]`)?.focus({ preventScroll: true });
  lastShown = shown; lastScope = inScope;
  applyLayout();
}
onChange((full = true) => (full ? render() : refreshList()));

export function render() {
  if (!state.scan || current !== "sessions") return;
  decide(entries().length, () => toggleDisplay());
  const active=document.activeElement;
  const inputID=active?.matches?.("input") ? active.id : null;
  const inputSelection=inputID ? [active.selectionStart,active.selectionEnd] : null;
  const inspectorTop=view.querySelector(".inspector")?.scrollTop||0;
  const old = view.querySelector(".content");
  const top = old ? old.scrollTop : 0;
  const focusKey = document.activeElement?.closest?.(".tree [data-key]")?.dataset.key;
  const focusGroup = document.activeElement?.closest?.(".tree [data-gkey]")?.dataset.gkey;
  const inScope = scoped();
  const shown = applyFilters(inScope, state.scope);
  // A selection the list doesn't show goes (another place, a filter, a refresh).
  if (!state.scanning && !pendingScan && !state.scan.discovering && state.sel && !shown.some((x) => x.machine === state.sel.machine && x.key === state.sel.key)) { state.sel = null; state.handoffOpen = null; }
  const content = h("section", { class: "content" }, toolbar(scopeTitle(), state.scope), h("div", { class: "list" }, notices(), body(shown, inScope)));
  const previous=view.querySelector("#inspector"),nextInspector=inspector(selected(),previous),layout=view.querySelector(".three.layout");
  if(layout&&previous&&old){
    layout.querySelector(".sidebar").replaceWith(sidebar());
    old.replaceWith(content);
    if(nextInspector!==previous)previous.replaceWith(nextInspector);
  }else fill(view,h("div",{class:"three layout"},sidebar(),content,nextInspector,dividers()));
  counts(shown.length, inScope.length, state.scope);
  lastShown = shown; lastScope = inScope;
  applyLayout();
  content.scrollTop = top;
  const ins=view.querySelector(".inspector");if(ins)ins.scrollTop=inspectorTop;
  if(inputID){const input=document.getElementById(inputID);input?.focus({preventScroll:true});if(inputSelection)input?.setSelectionRange(...inputSelection)}
  else if(active?.isConnected && active!==document.body)active.focus({preventScroll:true});
  if (focusKey) rowByKey(content, focusKey)?.focus({ preventScroll: true });
  else if (focusGroup) content.querySelector(`.grp[data-gkey="${CSS.escape(focusGroup)}"]`)?.focus({ preventScroll: true });
}

// showEntry selects a session from elsewhere (the palette, Go to the newer copy, a
// notification): its place if it isn't in this one, no filter that hides it, its group
// open; then it is shown landing.
export function showEntry(e) {
  const k = entryKey(e);
  if (!entries().some(x=>entryKey(x)===k)) {
    const group=e.group || state.scan?.groups.find(g=>g.entries.some(x=>(x.copies || []).some(c=>c.machine===e.machine && c.key===e.key)));
    if (!group) { toast("This copy is not in the current scan; refresh its machine."); return; }
    const i=group.entries.findIndex(x=>(x.copies || []).some(c=>c.machine===e.machine && c.key===e.key));
    if (i<0) return;
    const {group:ignored,...copy}=e;
    group.entries[i]=copy;
    e=Object.assign({group},copy);
    toast(`Showing ${e.agentName} on ${e.machine === here() ? sys.here : e.machine}; other copies remain in Copies & history.`);
  }
  const inScope = () => scoped().some((x) => entryKey(x) === k);
  if (!inScope()) state.scope = { kind: "all" };
  if (!applyFilters(scoped(), state.scope).some((x) => entryKey(x) === k)) {
    if (activeFacets(state.scope).length) toast("Filters cleared to show it");
    list.text = "";
    const f = $("#list-filter");
    if (f) f.value = "";
    clearFilters();
  }
  openGroupOf(e, applyFilters(scoped(), state.scope));
  state.sel = { machine: e.machine, key: e.key };
  if (current === "sessions") render();
}

// reveal scrolls the selected row to the middle of the list and flashes it, so a session
// picked elsewhere (the palette) is seen to land.
export function reveal() {
  const r = view.querySelector('.row[aria-selected="true"]');
  if (!r) return;
  inView(r, true);
  r.classList.remove("flash");
  void r.offsetWidth; // restart the animation
  r.classList.add("flash");
}

// Keyboard, when the focus is not in the list: ↑/↓ move the selection, ↩ runs the main
// action, ⌘↩ (Ctrl+Enter) opens its menu. In the list, the tree's own keys work
// (listview.js).
document.addEventListener("keydown", (ev) => {
  if (!state.scan || document.querySelector("dialog[open]") || isOpen() || /INPUT|TEXTAREA|SELECT/.test(ev.target.tagName)) return;
  if (!view.querySelector(".three") || ev.target.closest?.(".tree") || ev.target.closest?.("button, a, [role=separator]")) return;
  const rows = [...view.querySelectorAll(".tree .row")];
  const i = rows.findIndex((r) => r.getAttribute("aria-selected") === "true");
  if (ev.key === "ArrowDown" || ev.key === "ArrowUp") {
    ev.preventDefault();
    const next = rows[Math.max(0, Math.min(rows.length - 1, i + (ev.key === "ArrowDown" ? 1 : -1)))];
    if (next) next.click();
  } else if (ev.key === "Enter") {
    const e = selected();
    if (!e) return;
    ev.preventDefault();
    if (ev.metaKey || ev.ctrlKey) { openChevron(e); return; }
    const p = model(e).primary;
    if (p && !p.disabled) return p.run();
  }
});

// The View menu's and the palette's list commands.
export function listCommand(cmd) {
  const [what, value] = cmd.split(":");
  if (what === "group") Object.assign(list, { groupBy: value });
  else if (what === "sort") Object.assign(list, { sortBy: value, sortReverse: false });
  else if (what === "compact") list.density = list.density === "compact" ? "comfortable" : "compact";
  else if (what === "rows") list.density = value;
  else if (what === "collapse-all" || what === "expand-all") { if (current !== "sessions") go("sessions"); setAll(what === "collapse-all"); return; }
  else if (what === "display") { if (current !== "sessions") go("sessions").then(() => displayCommand()); else displayCommand(); return; }
  else if (what === "filter") { if (current !== "sessions") go("sessions").then(() => toggleFilter()); else toggleFilter(); return; }
  else if (what === "clear-filters") { clearFilters(); return; }
  else return;
  saveList();
  if (current === "sessions") refreshList(); else go("sessions");
}

screen("sessions", async (rescan = false) => {
  loadList(state.info?.list);
  if (!state.scan || rescan || state.stale) await scan();
  if (!state.scan || current !== "sessions") return;
  decide(entries().length, () => toggleDisplay());
  render();
  // Asking Codex whether it trusts the hooks starts its app-server: off the first paint.
  loadHookHealth().then(() => { if (current === "sessions") render(); });
  const r = view.querySelector('.row[aria-selected="true"]');
  if (r) inView(r, false);
  presenceSoon(presenceEvery());
});

onRenamed(async () => { await refreshHere(); render(); });

// inView scrolls the list (never the window) to a row: to its middle, or just enough
// (below the toolbar and its group's sticky header).
function inView(r, center) {
  const list = r.closest(".content");
  if (!list) return;
  const top = (list.querySelector(".toolbar")?.offsetHeight || 0) + (r.closest(".grp")?.querySelector(".gh")?.offsetHeight || 0);
  const lb = list.getBoundingClientRect(), rb = r.getBoundingClientRect();
  if (center) list.scrollTop += rb.top - lb.top - (lb.height + top - rb.height) / 2;
  else if (rb.top < lb.top + top) list.scrollTop -= lb.top + top - rb.top + 8;
  else if (rb.bottom > lb.bottom) list.scrollTop += rb.bottom - lb.bottom + 8;
}

// One latest pending publication. Never replace a control between pointer-down
// and pointer-up, nor close an open menu, dialog or transfer review.
let discoveryPending=null,discoveryPresence=null,discoveryTimer=0,pointerHeld=false,backgroundDirty=false;
document.addEventListener("pointerdown",()=>{pointerHeld=true},true);
window.addEventListener("pointerup",()=>{pointerHeld=false},true);
window.addEventListener("pointercancel",()=>{pointerHeld=false},true);
export function acceptScan(scan){
 if(scan?.revision && state.scan?.revision && scan.revision<=state.scan.revision)return false;
 state.scan=scan;freshness();return true;
}
function scheduleDiscovery(){if(!discoveryTimer)discoveryTimer=setTimeout(drawDiscovery,150)}
export function backgroundRender(){backgroundDirty=true;scheduleDiscovery()}
export function queueScan(d,presence=null){
 if(!d || (discoveryPending?.revision && d.revision<discoveryPending.revision))return;
 discoveryPending=d;discoveryPresence=presence;scheduleDiscovery();
}
function drawDiscovery(){
 discoveryTimer=0;
 if(!discoveryPending&&!backgroundDirty)return;
 if(pointerHeld || document.querySelector("dialog[open],button:active") || isOpen()) {scheduleDiscovery();return}
 let changed=backgroundDirty;backgroundDirty=false;
 if(discoveryPending){
  const d=discoveryPending,presence=discoveryPresence;discoveryPending=null;discoveryPresence=null;
  if(acceptScan(d)){state.presence=presence||{};changed=true}
 }
 if(changed&&current==="sessions")render();
}
on("hopsesh:discovery",d=>queueScan(d));
on("hopsesh:accounts",async()=>{try{queueScan(await api("ScanSnapshot"))}catch{}});
