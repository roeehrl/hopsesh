// The command palette (⌘K, Ctrl+K): find a session and act on it, or run any command, from the keyboard.
import { api, h, fill, icon, ICONS, state, go, current, toast, fail, agentBadge, entries, here, $, sys, keys, clouds, selected, cloudTitle } from "./core.js";
import { pickHandoff } from "./handoff.js";
import { actionsFor, statusOf, render as renderSessions, reveal, pasteDialog, showEntry, listCommand } from "./sessions.js";
import { list } from "./listview.js";
import { undoLast } from "./activity.js";
import { showTerminal, openShell } from "./term.js";

const pal = $("#palette");
let items = [], sel = 0;

// show selects a session in the list (its place, no filter that hides it, its group
// open), its row in view.
async function show(e) {
  if (current !== "sessions") await go("sessions");
  showEntry(e);
  document.querySelector('#view .row[aria-selected="true"]')?.focus({ preventScroll: true });
  reveal();
}

function commands() {
  const i = state.info || {};
  return [
    { label: "Sessions", hint: keys("mod+1"), run: () => go("sessions") },
    { label: "Activity and undo", hint: keys("mod+2"), run: () => go("activity") },
    { label: "Machines", hint: keys("mod+3"), run: () => go("machines") },
    { label: "Settings", hint: keys("mod+,"), run: () => go("settings", "general") },
    { label: "Refresh: read every machine again", hint: keys("mod+R"), run: () => go("sessions", true) },
    { label: "Undo the last hop", hint: keys("mod+alt+Z"), run: undoLast },
    { label: "Add a machine", run: () => go("machines") },
    { label: "Terminal: show the hopsesh Terminal window", hint: sys.mac ? "⌃`" : "Ctrl+`", run: () => showTerminal() },
    { label: "Terminal: open a shell", sub: selected() && !selected().cloud && selected().machine === here() ? `in “${selected().title}”'s folder` : "in your home folder",
      run: () => openShell(selected() && !selected().cloud && selected().machine === here() ? selected() : null) },
    { label: `Terminal: turn screen reader mode ${state.terminalReader ? "off" : "on"}`, run: toggleReader },
    { label: "Settings: Terminal", run: () => go("settings", "terminal") },
    { label: "Display options", hint: keys("mod+J"), run: () => listCommand("display") },
    { label: "Filter sessions…", hint: sys.mac ? "⇧⌘F" : "Ctrl+Shift+F", run: () => listCommand("filter") },
    { label: "Clear filters", run: () => listCommand("clear-filters") },
    ...[["repository", "Repository"], ["location", "Location"], ["agent", "Agent"], ["status", "Status"], ["last-active", "Last active"], ["none", "None"]]
      .map(([v, n]) => ({ label: `Group by: ${n}`, sub: list.groupBy === v ? "now" : "", run: () => listCommand("group:" + v) })),
    ...[["last-active", "Last active"], ["title", "Title"], ["status", "Status"], ["size", "Size"]]
      .map(([v, n]) => ({ label: `Sort by: ${n}`, sub: list.sortBy === v ? "now" : "", run: () => listCommand("sort:" + v) })),
    { label: list.density === "compact" ? "Rows: Comfortable" : "Rows: Compact", run: () => listCommand(list.density === "compact" ? "rows:comfortable" : "rows:compact") },
    { label: "Collapse all groups", run: () => listCommand("collapse-all") },
    { label: "Expand all groups", run: () => listCommand("expand-all") },
    ...handOff(),
    ...(clouds().some((c) => c.fetchable) ? [
      { label: "Bring from cloud…", sub: clouds().filter((c) => c.fetchable).map((c) => c.title).join(", "), run: bringFromCloud },
      { label: "Paste a cloud link…", sub: "claude.ai/code/…, session_…, cse_…", run: () => pasteDialog() },
    ] : []),
    { label: "Machines: Clouds", sub: "turn on, sign in, test", run: () => go("machines", "clouds") },
    { label: i.receive ? "Stop receiving sessions from my other machines" : "Receive sessions from my other machines",
      run: async () => { try { await api("SetReceive", !i.receive); state.info = await api("Info"); toast(state.info.receive ? `${sys.Here} now receives sessions` : `${sys.Here} no longer receives sessions`); } catch (e) { fail(e); } if (current === "sessions") renderSessions(); } },
    { label: "Settings: agents", run: () => go("settings", "agents") },
    { label: "Settings: the hopsesh skill", run: () => go("settings", "skill") },
    { label: "Settings: the command-line tool", run: () => go("settings", "cli") },
    { label: "Settings: updates", run: () => go("settings", "updates") },
  ];
}

// toggleReader turns the terminal's screen reader mode on or off.
async function toggleReader() {
  try {
    const t = await api("TerminalSettings");
    const on = !(t.screenReader === "on");
    await api("SetTerminalSettings", { app: t.app, where: t.where, font: t.font, fontSize: t.fontSize, scrollback: t.scrollback, keepTabs: t.keepTabs,
      notify: t.notify, closeEnded: t.closeEnded, screenReader: on ? "on" : "off", systemConsole: t.systemConsole });
    state.terminalReader = on;
    toast(`The terminal's screen reader mode is ${on ? "on" : "off"}`);
  } catch (e) { fail(e); }
}

// handOff is "Hand off to…" for the selected session (or the newest one), when it can go
// to a cloud.
function handOff() {
  const sel = selected();
  const e = sel && !sel.cloud ? sel : entries().filter((x) => !x.cloud).sort((a, b) => (b.lastActive > a.lastActive ? 1 : -1))[0];
  if (!e || !(e.handoff || []).length) return [];
  return [{ label: "Hand off to…", sub: `“${e.title}”`, hint: "Pick a cloud", run: () => pickHandoff(e) }];
}

// bringFromCloud shows the first cloud sessions can come from.
function bringFromCloud() {
  const c = clouds().find((x) => x.fetchable && x.allowed) || clouds().find((x) => x.fetchable);
  state.scope = c ? { kind: "cloud", value: c.name } : { kind: "all" };
  if (current === "sessions") renderSessions(); else go("sessions");
}

const words = (q) => q.toLowerCase().split(/\s+/).filter(Boolean);
const matches = (text, ws) => { const t = text.toLowerCase(); return ws.every((w) => t.includes(w)); };

// sessionItem is a session found: ↩ shows it in the list, ⌘↩ (Ctrl+Enter) runs its main
// action. Its line says where it is: the repository, the machine, its state.
function sessionItem(e, group) {
  const acts = actionsFor(e);
  const [, st] = statusOf(e);
  const repo = e.group.noRepo ? "" : e.group.name.replace(/ \(no remote\)$/, "");
  const where = e.cloud ? cloudTitle(e.machine) : e.machine === here() ? sys.here : e.machine;
  return { group, session: e, label: e.title, sub: [repo, where, st[0].toLowerCase() + st.slice(1), e.cloud?.pr ? "PR " + e.cloud.pr : ""].filter(Boolean).join(" · "),
    hint: acts[0] ? `${keys("mod+enter")} ${acts[0].label}` : "", run: () => show(e), second: acts[0]?.run };
}

function build(q) {
  const ws = words(q);
  const all = entries();
  let found = ws.length
    ? all.filter((e) => matches([e.title, e.lastPrompt, e.group.name, e.machine, e.agentName, e.branch, e.cwd].join(" "), ws))
      .sort((a, b) => (matches(b.title, ws) - matches(a.title, ws)) || (b.lastActive > a.lastActive ? 1 : -1))
    : all.filter((e) => e.needs).concat(all.filter((e) => !e.needs).sort((a, b) => (b.lastActive > a.lastActive ? 1 : -1))).slice(0, 6);
  const out = [];
  if (found.length) {
    const top = found[0];
    out.push(sessionItem(top, "Session"));
    for (const a of actionsFor(top)) out.push({ group: "Actions for this session", label: a.label, run: a.run });
    out.push({ group: "Actions for this session", label: "Show it in the list", run: () => show(top) });
    found = found.slice(1, 9);
  }
  for (const e of found) out.push(sessionItem(e, ws.length ? "Other matches" : "Recent"));
  for (const c of commands().filter((c) => !ws.length || matches(c.label, ws))) out.push(Object.assign({ group: "Commands" }, c));
  return out;
}

function paint() {
  const list = pal.querySelector(".pal-list");
  let group = "";
  const kids = [];
  items.forEach((it, i) => {
    if (it.group !== group) { group = it.group; kids.push(h("div", { class: "pal-grp", role: "presentation" }, group)); }
    kids.push(h("button", { class: "pal-item", role: "option", id: "pal-" + i, "aria-selected": i === sel ? "true" : "false", tabindex: "-1",
        onmousemove: () => { if (sel !== i) { sel = i; paint(); } }, onclick: () => runAt(i, false) },
      it.session ? agentBadge(it.session.agent, it.session.agentName, it.session.cloud ? cloudTitle(it.session.machine) : "") : icon(ICONS.arrow, 13),
      h("span", { style: "flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" }, it.label, it.sub ? h("span", { class: "muted" }, " · " + it.sub) : null),
      it.hint ? h("span", { class: "muted", style: "font-size:12px" }, it.hint) : null,
      i === sel && it.run ? h("span", { class: "kbd", title: it.session ? "Show it in the list" : "" }, "↩") : null));
  });
  fill(list, kids.length ? kids : h("div", { class: "empty" }, "Nothing matches."));
  list.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  pal.querySelector("input").setAttribute("aria-activedescendant", items.length ? "pal-" + sel : "");
}

function runAt(i, second) {
  const it = items[i];
  if (!it) return;
  const fn = second ? it.second : it.run;
  if (!fn) return;
  pal.close();
  fn();
}

export function openPalette() {
  if (pal.open) return;
  const input = h("input", { type: "text", role: "combobox", "aria-expanded": "true", "aria-controls": "pal-list", "aria-label": "Search sessions or run a command",
    placeholder: "Search sessions or run a command", autocomplete: "off", spellcheck: "false" });
  fill(pal,
    h("div", { class: "pal-in" }, icon(["M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z", "m20 20-3.5-3.5"], 16), input),
    h("div", { class: "pal-list", id: "pal-list", role: "listbox" }),
    h("div", { class: "pal-foot" }, h("span", {}, "↑↓ move"), h("span", {}, "↩ show the session, or run"), h("span", {}, `${keys("mod+enter")} the session's main action`), h("span", {}, "esc close"), h("span", { class: "spacer" }), h("span", {}, `${keys("mod+alt+Z")} undo the last hop`)));
  input.addEventListener("input", () => { items = build(input.value); sel = 0; paint(); });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      if (items.length) sel = (sel + (e.key === "ArrowDown" ? 1 : items.length - 1)) % items.length;
      paint();
    } else if (e.key === "Enter") {
      e.preventDefault();
      runAt(sel, e.metaKey || e.ctrlKey);
    }
  });
  items = build("");
  sel = 0;
  paint();
  pal.showModal();
  input.focus();
}

pal.addEventListener("click", (e) => { if (e.target === pal) pal.close(); });
