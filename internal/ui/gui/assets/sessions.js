// The Sessions screen: scopes on the left, sessions by repository in the middle, the
// selected session on the right. Every action on a session comes from actionsFor(), which
// the palette uses too.
import { api, h, fill, icon, ICONS, view, state, screen, go, loading, toast, fail, cap, ago, when, bytes, agentBadge, agentChip, machineStatus, sys, keys, cliHow,
  entries, selected, here, agentInfo, $, count } from "./core.js";
import { planFor } from "./plan.js";

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

// statusOf is a session's state: [kind, words].
export function statusOf(e) {
  if (e.needs) return ["needs", "Needs you"];
  if (e.live) return /idle/i.test(e.status) ? ["idle", "Idle"] : ["working", "Working"];
  if (/^(moved|continued)/.test(e.status)) return ["moved", e.status[0].toUpperCase() + e.status.slice(1)];
  return ["ended", "Ended"];
}

// actionsFor lists what can be done with a session: the first is its main action.
export function actionsFor(e) {
  const out = [];
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
    (sc.kind !== "machine" || e.machine === sc.value) &&
    (sc.kind !== "agent" || e.agent === sc.value) &&
    (!f.agent || e.agent === f.agent) &&
    (!f.live || e.live));
}

function scopeTitle() {
  const sc = state.scope;
  return { all: "All sessions", needs: "Needs you", here: `On ${sys.here}`, machine: sc.value, agent: agentInfo(sc.value)?.name || sc.value }[sc.kind];
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
    h("div", { class: "side-h" }, "Machines"),
    machines.map((m) => h("button", { class: "side-btn", "aria-current": cur("machine", m.name), onclick: () => setScope({ kind: "machine", value: m.name }) },
      h("span", { class: "dot " + dotFor(m) }),
      h("span", { style: "min-width:0" }, h("span", {}, m.name), h("small", { class: dotFor(m) === "ok" ? "" : "warn" }, dotFor(m) === "ok" ? [m.os, m.hopsesh ? "hopsesh " + m.hopsesh : ""].filter(Boolean).join(" · ") : machineStatus(m.status)[1])),
      h("span", { class: "count" }, m.status === "ok" ? m.sessions : ""))),
    h("button", { class: "side-btn", style: "color:var(--accent)", onclick: () => go("machines") }, icon(ICONS.plus), machines.length ? "Add a machine" : "Add your other machines"),
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
  if (!i.hasHosts) out.push(h("div", { class: "card notice" },
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
  return h("div", { class: "row", role: "option", "aria-selected": sel ? "true" : "false", tabindex: sel ? "0" : "-1", "data-key": e.machine + "\u0000" + e.key,
      onclick: () => select(e), ondblclick: () => acts[0]?.run() },
    agentBadge(e.agent, e.agentName),
    h("div", { class: "main" }, h("span", { class: "t", title: e.title }, e.title), h("span", { class: "s" }, bits.join(" · ") || " "),
      others.length ? h("div", { class: "copies" }, others.map((c) => h("span", { class: "chip" + (c.newest ? " st-warn" : "") },
        `${c.newest ? "newest: " : "also "}${c.agentName} ${c.local ? "here" : "on " + c.machine}`))) : null),
    h("div", { class: "state" }, h("span", { class: "chip st-" + k }, h("span", { class: "dot " + k }), words), `${e.machine === here() ? "here" : e.machine} · ${ago(e.lastActive)}`),
    h("div", { class: "act" }, acts[0] ? h("button", { class: "btn small outline", style: "width:100%", onclick: (ev) => { ev.stopPropagation(); acts[0].run(); } }, acts[0].label) : null));
}

function select(e) {
  state.sel = { machine: e.machine, key: e.key };
  render();
  view.querySelector('.row[aria-selected="true"]')?.focus();
}

function inspector() {
  const e = selected();
  if (!e) return h("aside", { class: "inspector", "aria-label": "Session details" }, h("div", { class: "empty" }, "Select a session to see what you can do with it."));
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
    e.lastPrompt ? h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Last prompt"), h("span", { style: "font-style:italic;color:var(--ink2)" }, `“${e.lastPrompt}”`)) : null,
    h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Repository"),
      e.group.noRepo ? h("span", { class: "muted" }, "Started outside a git checkout") : [
        h("span", { class: "mono", style: "font-size:12px" }, e.group.remote || e.group.name + " (no remote)"),
        e.branch ? h("span", {}, e.branch, e.worktree ? h("span", { class: "muted" }, ` in a ${e.worktree}` + (e.mainBranch ? ` · main folder on ${e.mainBranch}` : "")) : null) : null,
        e.unpushed || e.dirty ? h("span", { class: "warn" }, [e.unpushed ? count(e.unpushed, "unpushed commit") : "", e.dirty ? count(e.dirty, "uncommitted file") : ""].filter(Boolean).join(" · ")) : null,
        e.group.local ? h("span", { class: "ok" }, "Cloned here at " + e.group.local) : e.group.remote ? h("span", { class: "muted" }, `Not on ${sys.here}: hopsesh can clone it`) : null],
      e.cwd !== e.group.local ? h("span", { class: "mono muted", style: "font-size:11px;overflow-wrap:anywhere" }, e.cwd) : null),
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
