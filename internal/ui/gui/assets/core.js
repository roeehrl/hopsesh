// Shared plumbing for the window: the backend bridge, DOM building (text from transcripts
// is untrusted and only ever inserted as text), app state, navigation and small formats.
import { Call, Events } from "/wails/runtime.js";

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
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
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
export const ICONS = {
  all: ["M4 5h16v14H4z", "M4 10h16"],
  here: ["M3 5h18v11H3z", "M8 20h8M12 16v4"],
  plus: "M12 5v14M5 12h14",
  clock: ["M12 8v4l3 2", "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"],
  arrow: "M5 12h14M13 6l6 6-6 6",
  down: "M12 5v14M6 13l6 6 6-6",
  send: "M7 17 17 7M8 7h9v9",
  mark: "M6 3h12v18l-6-4-6 4z",
  chevron: "m6 9 6 6 6-6",
};

export function fill(el, ...kids) {
  el.replaceChildren(...kids.flat(Infinity).filter((k) => k !== null && k !== undefined && k !== false));
}

// toast shows a short message; an action gets a button, and the toast stays while it has
// focus or the pointer.
let toastTimer;
export function toast(msg, action) {
  const t = $("#toast");
  fill(t, h("span", {}, msg), action ? h("button", { onclick: () => { t.classList.remove("show"); action.run(); } }, action.label) : null);
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
export const fail = (e) => toast(errText(e));

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
export function bytes(n) {
  if (n >= 1 << 30) return (n / (1 << 30)).toFixed(1) + " GB";
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1) + " MB";
  if (n >= 1 << 10) return Math.round(n / 1024) + " KB";
  return n + " B";
}
// agentBadge pictures an agent: its icon (the installed app's, else the module's mark), or
// its initials on the agent's colour. agentChip adds the name.
const agentClass = (id) => "chip agent-" + id;
const short = (name) => { const w = (name || "").split(/\s+/).filter(Boolean); return (w.length > 1 ? w.map((x) => x[0]).join("") : w.join("")).slice(0, 2).toUpperCase(); };
export function agentBadge(id, name) {
  const src = agentInfo(id)?.icon;
  return src ? h("img", { class: "agent-ico", src, alt: name, title: name }) : h("span", { class: agentClass(id), title: name }, short(name));
}
export function agentChip(id, name) {
  const src = agentInfo(id)?.icon;
  return h("span", { class: agentClass(id) }, src ? h("img", { class: "agent-ico small", src, alt: "" }) : null, name);
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
  scope: { kind: "all" }, // all | needs | here | machine (value) | agent (value)
  filter: { agent: "", live: false },
  sel: null, // { machine, key }
  update: null,
  activity: null, // the last Activity list, for the sidebar's undo count
};

// sys words things and spells shortcuts for the system the app runs on (setSystem, once
// Info has loaded): "this Mac" and ⌘K on macOS, "this PC" and Ctrl+K on Windows.
export const sys = { mac: true, win: false, os: "darwin", here: "this Mac", Here: "This Mac", vault: "the Keychain", terminal: "Terminal" };
export function setSystem(os) {
  const mac = os === "darwin", win = os === "windows";
  Object.assign(sys, { mac, win, os, here: mac ? "this Mac" : win ? "this PC" : "this computer",
    vault: mac ? "the Keychain" : win ? "Windows Credential Manager" : "the keyring", terminal: mac ? "Terminal" : "a terminal" });
  sys.Here = cap(sys.here);
  document.documentElement.dataset.os = os;
  for (const el of document.querySelectorAll("[data-keys]")) el.textContent = keys(el.dataset.keys);
  for (const el of document.querySelectorAll("[data-keys-title]")) el.title = `${el.dataset.label} (${keys(el.dataset.keysTitle)})`;
  for (const el of document.querySelectorAll("[aria-keyshortcuts]")) el.setAttribute("aria-keyshortcuts", mac ? "Meta+K" : "Control+K");
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
export function go(name, ...args) {
  current = name;
  $("#where").textContent = { sessions: "", activity: "Activity", machines: "Machines", settings: "Settings", done: "" }[name] ?? "";
  return screens[name](...args);
}

export function loading(text) {
  fill(view, h("div", { class: "loading", role: "status" }, text));
}

// The entry for the current selection, from the last scan.
export function entries() {
  return state.scan ? state.scan.groups.flatMap((g) => g.entries.map((e) => Object.assign({ group: g }, e))) : [];
}
export function selected() {
  const s = state.sel;
  return s ? entries().find((e) => e.machine === s.machine && e.key === s.key) || null : null;
}
export const here = () => (state.scan?.machines.find((m) => m.local) || {}).name || state.info?.host;
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
