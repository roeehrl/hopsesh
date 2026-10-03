// The ⌘K palette: find a session and act on it, or run any command, from the keyboard.
import { api, h, fill, icon, ICONS, state, go, current, toast, fail, agentBadge, entries, here, $ } from "./core.js";
import { actionsFor, statusOf, render as renderSessions } from "./sessions.js";
import { undoLast } from "./activity.js";

const pal = $("#palette");
let items = [], sel = 0;

function show(e) {
  state.scope = { kind: "all" };
  state.filter = { agent: "", live: false };
  state.sel = { machine: e.machine, key: e.key };
  if (current === "sessions") renderSessions(); else go("sessions");
}

function commands() {
  const i = state.info || {};
  return [
    { label: "Sessions", hint: "⌘1", run: () => go("sessions") },
    { label: "Activity and undo", hint: "⌘2", run: () => go("activity") },
    { label: "Machines", hint: "⌘3", run: () => go("machines") },
    { label: "Settings", hint: "⌘,", run: () => go("settings", "general") },
    { label: "Refresh: read every machine again", hint: "⌘R", run: () => go("sessions", true) },
    { label: "Undo the last hop", hint: "⌥⌘Z", run: undoLast },
    { label: "Add a machine", run: () => go("machines") },
    { label: i.receive ? "Stop receiving sessions from my other machines" : "Receive sessions from my other machines",
      run: async () => { try { await api("SetReceive", !i.receive); state.info = await api("Info"); toast(state.info.receive ? "This Mac now receives sessions" : "This Mac no longer receives sessions"); } catch (e) { fail(e); } if (current === "sessions") renderSessions(); } },
    { label: "Settings: agents", run: () => go("settings", "agents") },
    { label: "Settings: the hopsesh skill", run: () => go("settings", "skill") },
    { label: "Settings: the command-line tool", run: () => go("settings", "cli") },
    { label: "Settings: updates", run: () => go("settings", "updates") },
  ];
}

const words = (q) => q.toLowerCase().split(/\s+/).filter(Boolean);
const matches = (text, ws) => { const t = text.toLowerCase(); return ws.every((w) => t.includes(w)); };

function sessionItem(e, group) {
  const acts = actionsFor(e);
  const [, st] = statusOf(e);
  return { group, session: e, label: e.title, sub: `${e.machine === here() ? "this Mac" : e.machine} · ${st.toLowerCase()}`, hint: acts[0]?.label || "Show",
    run: acts[0] ? acts[0].run : () => show(e), second: acts[1]?.run };
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
    for (const a of actionsFor(top).slice(1)) out.push({ group: "Actions for this session", label: a.label, run: a.run });
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
      it.session ? agentBadge(it.session.agent, it.session.agentName) : icon(it.group === "Commands" ? ICONS.arrow : ICONS.chevron, 13),
      h("span", { style: "flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" }, it.label, it.sub ? h("span", { class: "muted" }, " · " + it.sub) : null),
      it.hint ? h("span", { class: "muted", style: "font-size:12px" }, it.hint) : null,
      i === sel && (it.run || it.second) ? h("span", { class: "kbd" }, "↩") : null));
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
    h("div", { class: "pal-foot" }, h("span", {}, "↑↓ move"), h("span", {}, "↩ run"), h("span", {}, "⌘↩ second action"), h("span", {}, "esc close"), h("span", { class: "spacer" }), h("span", {}, "⌥⌘Z undo the last hop")));
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
