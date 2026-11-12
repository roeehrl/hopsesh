// Shared plumbing for the window: the backend bridge, DOM building (text from transcripts
// is untrusted and only ever inserted as text), app state, navigation and small formats.
import { Call, Events } from "/wails/runtime.js";

import { feedback } from "./feedback.js";
export { pending } from "./feedback.js";

const SVC = "github.com/roeehrl/hopsesh/internal/ui/gui.App.";
export const api = (method, ...args) => Call.ByName(SVC + method, ...args);
export const on = (name, fn) => Events.On(name, (ev) => fn(ev.data));

export const $ = (sel) => document.querySelector(sel);
export const view = $("#view");

// h(tag, attrs, ...children) builds DOM without innerHTML.
export function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), ["onclick", "onchange", "onsubmit", "ondblclick"].includes(k) ? feedback(el, v, fail) : v);
    else if (k === "style") el.setAttribute("style", v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// svg builds an inline stroke icon from path data.
export function icon(d, size = 14) {
  const ns = "http://www.w3.org/2000/svg";
  const s = document.createElementNS(ns, "svg");
  for (const [k, v] of Object.entries({ width: size, height: size, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "2", "stroke-linecap": "round", "stroke-linejoin": "round", "aria-hidden": "true" })) s.setAttribute(k, v);
  for (const part of [].concat(d)) {
    const p = document.createElementNS(ns, "path");
    p.setAttribute("d", part);
    s.append(p);
  }
  return s;
}
// hopMark is hopsesh's own mark beside a machine (its arc and two dots): filled and green
// when hopsesh runs there and receives sessions, hollow and gray when it is installed but
// not receiving; nothing when it is not installed. The words are its accessible name and
// tooltip, so the color is never the only signal. A newer hopsesh elsewhere adds an ↑ chip.
export function hopMark(m, size = 18) {
  const v = m?.hopsesh;
  if (!v) return null;
  const on = !!m.receiving;
  const words = `hopsesh ${v}${on ? ", receiving sessions" : ", not receiving sessions"}${m.newest ? ` · ${m.newest} is the newest` : ""}`;
  const ns = "http://www.w3.org/2000/svg";
  const s = document.createElementNS(ns, "svg");
  for (const [k, val] of Object.entries({ width: size, height: Math.round(size * 2 / 3), viewBox: "0 0 18 12", "aria-hidden": "true" })) s.setAttribute(k, val);
  const arc = document.createElementNS(ns, "path");
  for (const [k, val] of Object.entries({ d: "M3.5 9 A5.5 6.5 0 0 1 14.5 9", fill: "none", stroke: "currentColor", "stroke-width": "1.6", "stroke-linecap": "round" })) arc.setAttribute(k, val);
  s.append(arc);
  for (const cx of [3.5, 14.5]) {
    const c = document.createElementNS(ns, "circle");
    for (const [k, val] of Object.entries(on ? { cx, cy: 9.6, r: 2.1, fill: "currentColor" } : { cx, cy: 9.6, r: 1.6, fill: "var(--panel)", stroke: "currentColor", "stroke-width": "1.3" })) c.setAttribute(k, val);
    s.append(c);
  }
  return h("span", { class: "hop-mark " + (on ? "on" : "off"), role: "img", "aria-label": words, title: words }, s,
    m.newest ? h("span", { class: "hop-behind", "aria-hidden": "true" }, "↑") : null);
}

export const ICONS = {
  all: ["M4 5h16v14H4z", "M4 10h16"],
  here: ["M3 5h18v11H3z", "M8 20h8M12 16v4"],
  plus: "M12 5v14M5 12h14",
  refresh: ["M20 11a8 8 0 1 0-2.3 5.7", "M20 4v7h-7"],
  clock: ["M12 8v4l3 2", "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"],
  arrow: "M5 12h14M13 6l6 6-6 6",
  down: "M12 5v14M6 13l6 6 6-6",
  send: "M7 17 17 7M8 7h9v9",
  mark: "M6 3h12v18l-6-4-6 4z",
  chevron: "m6 9 6 6 6-6",
  cloud: "M7 18h10a4 4 0 0 0 .5-7.97A6 6 0 0 0 6.1 9.1 4.5 4.5 0 0 0 7 18z",
  external: ["M14 4h6v6", "M20 4l-9 9", "M18 14v5a1 1 0 0 1-1 1H5a1 1 0 0 1-1-1V7a1 1 0 0 1 1-1h5"],
  // A move's two ends, and how a copy left behind stands.
  movedOut: ["M3 5v14", "M21 12H7", "m15 18 6-6-6-6"],
  movedIn: ["M17 12H3", "m11 18 6-6-6-6", "M21 5v14"],
  shieldCheck: ["M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z", "m9 12 2 2 4-4"],
  shieldAlert: ["M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z", "M12 8v4", "M12 16h.01"],
  shieldUnknown: ["M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z", "M9.1 9a3 3 0 0 1 5.82 1c0 2-3 3-3 3", "M12 17h.01"],
  shieldOff: ["m2 2 20 20", "M5 5a1 1 0 0 0-1 1v7c0 5 3.5 7.5 7.67 8.94a1 1 0 0 0 .67.01c2.35-.82 4.48-1.97 5.9-3.71", "M9.31 3.65a12.25 12.25 0 0 0 1.93-1.37 1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1v7a9.78 9.78 0 0 1-.08 1.26"],
  diverged: ["M5 3a3 3 0 1 0 0 6 3 3 0 0 0 0-6z", "M12 6h5a2 2 0 0 1 2 2v7", "m15 9-3-3 3-3", "M19 15a3 3 0 1 0 0 6 3 3 0 0 0 0-6z", "M12 18H7a2 2 0 0 1-2-2V9", "m9 15 3 3-3 3"],
  usedAfter: ["M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8", "M3 3v5h5", "M12 7v5l4 2"],
  open: ["M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z", "M12 10a2 2 0 1 0 0 4 2 2 0 0 0 0-4z"],
};

export function fill(el, ...kids) {
  el.replaceChildren(...kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false));
}

// toast shows a short message; an action gets a button, and the toast stays while it has
// focus or the pointer.
let toastTimer;
export function toast(msg, action) {
  const t = $("#toast");
  fill(t, h("span", {}, rich(msg)), action ? h("button", { onclick: () => { t.classList.remove("show"); action.run(); } }, action.label) : null);
  t.classList.add("show");
  clearTimeout(toastTimer);
  const hide = () => { toastTimer = setTimeout(() => t.classList.remove("show"), action ? 12000 : 3500); };
  t.onmouseenter = () => clearTimeout(toastTimer);
  t.onmouseleave = hide;
  hide();
}
// count is a number with its noun: count(1, "tool call") is "1 tool call", count(2, …) "2 tool calls".
export const count = (n, one, many = one + "s") => `${n} ${n === 1 ? one : many}`;
export const cap = (s) => (s ? s[0].toUpperCase() + s.slice(1) : s);
export const errText = (e) => String((e && e.message) || e);
// A refusal the window answers with a question instead of a toast (guard.js: continuing
// a copy that already moved).
let refusal = () => false;
export function onRefusal(fn) { refusal = fn; }
export const fail = (e) => { if (!refusal(e)) toast(errText(e)); };

export function ago(iso) {
  if (!iso) return "";
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 45) return "just now";
  if (d < 3600) return `${Math.max(1, Math.round(d / 60))} min ago`;
  if (d < 86400) return `${Math.round(d / 3600)} h ago`;
  if (d < 7 * 86400) return `${Math.round(d / 86400)} d ago`;
  return new Date(iso).toLocaleDateString(undefined, { day: "numeric", month: "short" });
}
export function when(iso) {
  return new Date(iso).toLocaleString(undefined, { day: "numeric", month: "short", hour: "2-digit", minute: "2-digit" });
}
// bytes is a size for people; a no-break space keeps the number with its unit.
export function bytes(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + "\u00a0GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + "\u00a0MB";
  if (n >= 1 << 10) return Math.round(n / 1024) + "\u00a0KB";
  return n + "\u00a0B";
}

// rich is a module's or the backend's text with its `command` spans as code: DOM nodes
// built from the text, never markup.
export function rich(text) {
  const parts = String(text ?? "").split("`");
  if (parts.length < 3) return String(text ?? "");
  return parts.map((p, i) => (i % 2 && i < parts.length - 1 ? h("code", {}, p) : (i % 2 ? "`" + p : p)));
}

// path is a path or an address that breaks only after its slashes,
// in a mono span whose title holds the whole of it.
export function path(text, size = 12) {
  const kids = [];
  for (const part of String(text).split(/(?<=[/\\])/)) kids.push(part, h("wbr"));
  return h("span", { class: "mono path", style: `font-size:${size}px`, title: text }, kids);
}
// agentBadge pictures an agent: its icon (the installed app's, else the module's mark), or
// its initials on the agent's colour. agentChip adds the name.
const agentClass = (id) => "chip agent-" + id;
const short = (name) => { const w = (name || "").split(/\s+/).filter(Boolean); return (w.length > 1 ? w.map((x) => x[0]).join("") : w.join("")).slice(0, 2).toUpperCase(); };
// A cloud session's picture carries a small cloud at its corner (inCloud: the cloud's
// title, for the tooltip).
export function agentBadge(id, name, inCloud = "") {
  const src = agentInfo(id)?.icon;
  const pic = src ? h("img", { class: "agent-ico", src, alt: name, title: name }) : h("span", { class: agentClass(id), title: name }, short(name));
  if (!inCloud) return pic;
  pic.removeAttribute("title");
  return h("span", { class: "in-cloud", title: `${name} · ${inCloud}` }, pic, h("span", { class: "cloud-mark", "aria-hidden": "true" }, icon(ICONS.cloud, 9)));
}
export function agentChip(id, name, inCloud = "") {
  const src = agentInfo(id)?.icon;
  const pic = src ? h("img", { class: "agent-ico small", src, alt: "" }) : null;
  return h("span", { class: agentClass(id) }, inCloud && pic ? h("span", { class: "in-cloud small", title: inCloud }, pic, h("span", { class: "cloud-mark", "aria-hidden": "true" }, icon(ICONS.cloud, 7))) : pic, name);
}

// machineStatus is a scan status as [dot kind, words].
const STATUS = {
  ok: ["ok", "Connected"],
  unreachable: ["err", "Not reachable"],
  auth: ["err", "Could not log in"],
  "host-key": ["warn", "Its host key is not trusted yet"],
  "host-key-changed": ["err", "Its host key changed"],
  "tailscale-check": ["warn", "Tailscale wants you to sign in again"],
  "local-network": ["warn", "Blocked by macOS Local Network"],
  error: ["err", "Error"],
};
export const machineStatus = (status) => STATUS[status] || ["err", status];

// state is everything the screens share.
export const state = {
  info: null,
  scan: null, scanning: false,
  stale: false, // something changed the machines since the last scan
  scope: { kind: "all" }, // all | needs | here | machine (value) | cloud (value)
  presence: {}, // where this machine's sessions are open now, by machine NUL key (Presence)
  sel: null, // { machine, key }
  update: null,
  updateCheckRevision: 0, // a manual check supersedes any pending daily check
  activity: null, // the last Activity list, for the sidebar's undo count
};

// sys words things and spells shortcuts for the system the app runs on (setSystem, once
// Info has loaded): "this Mac" and ⌘K on macOS, "this PC" and Ctrl+K on Windows.
export const sys = { mac: true, win: false, os: "darwin", here: "this Mac", Here: "This Mac", vault: "the Keychain", terminal: "Terminal" };
export function setSystem(os, terminal) {
  const mac = os === "darwin", win = os === "windows";
  Object.assign(sys, { mac, win, os, here: mac ? "this Mac" : win ? "this PC" : "this computer",
    vault: mac ? "the Keychain" : win ? "Windows Credential Manager" : "the keyring", terminal: terminal || (mac ? "Terminal" : "a terminal") });
  sys.Here = cap(sys.here);
  document.documentElement.dataset.os = os;
  for (const el of document.querySelectorAll("[data-keys]")) el.textContent = keys(el.dataset.keys);
  for (const el of document.querySelectorAll("[data-keys-title]")) el.title = `${el.dataset.label} (${keys(el.dataset.keysTitle)})`;
  for (const el of document.querySelectorAll("[aria-keyshortcuts]")) el.setAttribute("aria-keyshortcuts", mac ? "Meta+K" : "Control+K");
  const term = document.querySelector("#btn-terminal");
  if (term) term.title = `Show the terminal (${mac ? "⌃`" : "Ctrl+`"})`;
}
// Until Info says, go by the browser's own report (so the first paint fits the system).
setSystem(/Windows/.test(navigator.userAgent) ? "windows" : /Mac/.test(navigator.userAgent) ? "darwin" : "linux");

// cliHow says what installing the hopsesh command does here.
export const cliHow = () => (sys.win ? "Puts the app's folder on your PATH (no admin rights)" : "Links the hopsesh command into ~/.local/bin (no password)");

// keys spells a shortcut written as "mod+K", "mod+alt+Z" or "mod+enter".
export function keys(spec) {
  const parts = spec.split("+");
  if (sys.mac) return parts.map((p) => ({ mod: "⌘", alt: "⌥", enter: "↩" })[p] || p).sort((a, b) => (a === "⌥" ? -1 : b === "⌥" ? 1 : 0)).join("");
  return parts.map((p) => ({ mod: "Ctrl", alt: "Alt", enter: "Enter" })[p] || p).join("+");
}

// Screens register themselves; go(name) shows one.
const screens = {};
export function screen(name, fn) { screens[name] = fn; }
export let current = "";
let navigation = 0;
export const navigationID = () => navigation;
export async function go(name, ...args) {
  const visit = ++navigation;
  const changed = current !== name;
  current = name;
  $("#btn-back-sessions").hidden = name === "sessions";
  state.handoffOpen = null;
  $("#where").textContent = { sessions: "", activity: "Activity", machines: "Machines", settings: "Settings", accounts: "Accounts", done: "", brought: "" }[name] ?? "";
  document.body.dataset.screen = name; // the panes' buttons work on Sessions only
  // Sessions already read stay on screen while they are read again (no blank "Reading…").
  if (changed && !(name === "sessions" && state.scan)) loading({ sessions: "Reading sessions…", machines: "Reading machines…", accounts: "Reading accounts…", settings: "Reading settings…", activity: "Reading activity…" }[name] || "Loading…");
  try { return await screens[name](...args); }
  catch (error) { if (visit === navigation) fill(view, loadError(error, () => go(name, ...args))); }
}

// Failed reads are explicitly retryable. Writes never retry automatically.
export function loadError(error, retry) {
  return h("div", { class: "load-error", role: "alert" },
    h("p", {}, "Couldn’t load this content. " + errText(error)),
    h("button", { class: "btn", onclick: retry }, "Try again"));
}

export function loading(text) {
  fill(view, h("div", { class: "loading", role: "status", "aria-live": "polite" }, text));
}

// manyAccounts: the agent is signed in with more than one account among the sessions read,
// so naming the account tells copies apart (otherwise the agent's name is enough).
export function manyAccounts(agent) {
  const ids = new Set();
  for (const e of entries()) {
    if (e.agent === agent && e.profile?.id) ids.add(e.profile.id);
    for (const r of e.returns || []) if (r.agent === agent && r.profile) ids.add(r.profile);
    for (const c of e.copies || []) if (c.agent === agent && c.profile?.id) ids.add(c.profile.id);
  }
  return ids.size > 1;
}

// entries are the list's sessions: each conversation's row, and every other copy of it the
// scan carries (profileCopies: an old copy a move left behind, another agent's copy) as a
// row of its own, which the sidebar's place and the filters judge by its own machine and
// state. A copy names the row it belongs to (home), so the list can draw it right after.
export function entries() {
  if (!state.scan) return [];
  const out = [], seen = new Set(), homes = new Map();
  for (const g of state.scan.groups) for (const e of g.entries) {
    const k = e.machine + "\u0000" + e.key;
    seen.add(k);
    out.push(Object.assign({ group: g }, e));
    for (const c of e.copies || []) if (!homes.has(c.machine + "\u0000" + c.key)) homes.set(c.machine + "\u0000" + c.key, { e, g });
  }
  for (const g of state.scan.profileCopies || []) for (const e of g.entries) {
    const k = e.machine + "\u0000" + e.key;
    if (seen.has(k)) continue;
    seen.add(k);
    const home = homes.get(k);
    // Without repository facts of its own (another machine's copy not read yet), a copy
    // stays with its conversation's repository.
    const group = home && (g.noRepo || g.repoPending) ? home.g : g;
    out.push(Object.assign({ group }, e, home ? { home: home.e.machine + "\u0000" + home.e.key } : null));
  }
  return out;
}
export function selected() {
  const s = state.sel;
  return s ? entries().find((e) => e.machine === s.machine && e.key === s.key) || copyEntry(s.machine, s.key) : null;
}
// copyEntry is another copy of a conversation (a row of its own), or a copy resolved from
// a history (not in the scan, attached to its conversation's row); null when neither.
export function copyEntry(machine, key) {
  if (scanEntry(machine, key)) {
    const e = entries().find((x) => x.machine === machine && x.key === key);
    if (e) return e;
  }
  const resolved = state.resolvedSelection;
  if (resolved?.machine === machine && resolved.key === key) return resolved;
  return null;
}
// scanEntry is a session's record in the last scan (a row or another copy), without its
// group: cheap enough for every row to look up its copies.
const scanIndex = new WeakMap();
export function scanEntry(machine, key) {
  const s = state.scan;
  if (!s) return null;
  const k = machine + "\u0000" + key;
  let idx = scanIndex.get(s);
  if (!idx || !idx.has(k)) {
    idx = new Map();
    for (const g of [...(s.groups || []), ...(s.profileCopies || [])]) for (const e of g.entries) if (!idx.has(e.machine + "\u0000" + e.key)) idx.set(e.machine + "\u0000" + e.key, e);
    scanIndex.set(s, idx);
  }
  return idx.get(k) || null;
}
export const here = () => (state.scan?.machines.find((m) => m.local) || {}).name || state.info?.host;
export const clouds = () => state.scan?.clouds || [];
export const cloudOf = (name) => clouds().find((c) => c.name === name);
// cloudState is a cloud's state in the sidebar: [dot, one word].
export function cloudState(c) {
  if (!c.allowed) return ["off", "off"];
  switch (c.status) {
    case "ready": return [c.partial ? "half" : "ok", "ready"];
    case "cli-old": return ["warn", "untested"];
    case "signed-out": return ["warn-hollow", "sign in"];
    case "cli-missing": return ["off", "not installed"];
    case "not-eligible": return ["off", "not available"];
  }
  return ["err", "error"];
}
// cloudTitle is a cloud's name for people ("Claude Code cloud" for claude-cloud).
export const cloudTitle = (name) => cloudOf(name)?.title || name;
// cloudChip names a cloud with its glyph (its id in the tooltip).
export const cloudChip = (name) => h("span", { class: "chip cloud", title: name }, icon(ICONS.cloud, 12), cloudTitle(name));
export const agentInfo = (id) => (state.info?.agents || []).find((a) => a.id === id);

// dialog fills the small dialog and shows it; close() hides it.
export function dialog(...kids) {
  const d = $("#dlg");
  fill(d, h("div", { class: "dlg-body" }, ...kids));
  if (!d.open) d.showModal();
  return d;
}

// ask is a yes/no question in the small dialog.
export function ask({ title, body, ok, danger = false }) {
  return new Promise((resolve) => {
    const d = dialog(
      h("h2", { style: "margin:0;font-size:16px" }, title),
      body ? h("div", { class: "muted", style: "line-height:1.5" }, body) : null,
      h("div", { class: "dlg-foot" },
        h("button", { class: "btn", onclick: () => d.close("no") }, "Cancel"),
        h("button", { class: "btn " + (danger ? "danger" : "primary"), onclick: () => d.close("yes") }, ok)));
    d.addEventListener("close", () => resolve(d.returnValue === "yes"), { once: true });
    d.returnValue = "";
  });
}

// Profiles identify state roots, not the historical owner of every conversation.
export const accountLabel = p => p?.account?.email || p?.name || "No account profile";
// A session's account is the one its agent recorded running it (e.owner), never just
// the sign-in its profile's root has now: a root signed in to another account before
// holds that account's sessions too.
// accountShown names it next to a session: the recorded account's email ("another
// account" for one no profile is signed in to now); unrecorded, a named profile ("Work")
// says where it is stored, and a default profile's name would only repeat the agent.
export const accountShown = e => ownerShown(e) || (e?.profile && !e.profile.default && !e?.owner?.recorded ? e.profile.name : "");
const signedInNow = p => p?.account?.loggedIn ? `${p.error ? "was last seen signed in" : "is signed in"} as ${p.account.email || p.account.label || p.account.provider} now` : "has no confirmed sign-in now";
export const accountTitle = e => {
  const p = e?.profile, o = e?.owner || {};
  if (!p) return "No registered account profile";
  const where = `Stored in ${p.name} (${p.root}), which ${signedInNow(p)}.${p.error ? " " + p.error + "." : ""}`;
  const before = o.earlier ? ` Before that it ran under ${o.earlier}.` : "";
  if (o.email && o.current) return `Run by ${o.email}, as recorded.${before} ${where}`;
  if (o.email) return `Run by ${o.email}, as recorded${o.profile ? ` (signed in to ${o.profile} now)` : ""}.${before} ${where}`;
  if (o.recorded && !o.current) return `Run by an account that no profile here is signed in to now, as recorded.${before} ${where}`;
  return `No record says which account ran this session. ${where}`;
};
export const ownerShown = e => e?.owner?.email || (e?.owner?.recorded && !e.owner.current && !e.owner.pending ? "another account" : "");
export const accountGroupLabel = e => e.profile ? `${ownerShown(e) || "Account not recorded"} · ${e.agentName} · ${e.machine === here() ? sys.Here : e.machine}${!e.profile.default || !e.owner?.current ? " · in " + e.profile.name : ""}` : "No account profile";
// accountKey files a session under its root and its recorded account's fingerprint, for
// filters and groups: the same after any sign-in change. accountKeyLegacy is 0.4.5's,
// so a saved filter keeps matching.
export const accountKey = e => (e.profile?.id || "") + "\u0000" + (e.owner?.key || "");
export const accountKeyLegacy = e => (e.profile?.id || "") + "\u0000" + (e.owner?.email || (e.owner?.recorded ? (e.owner.current ? "current" : "other") : ""));
