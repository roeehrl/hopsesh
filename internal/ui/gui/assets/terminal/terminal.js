import { setAppearance, darkAppearance, onAppearanceChange } from "./appearance.js";
// The hopsesh Terminal window: one xterm.js terminal per tab, fed by the tab's stream. The
// page reaches hopsesh only through its two streams (stream.js): no bindings, no events,
// no clipboard reads. What a tab runs was decided in Go; the window only shows tabs, sends
// the user's keys and the emulator's answers, sizes, and a few typed requests about a tab
// (show it, end it and open it in the user's terminal app, run it again, a new shell).
//
// Rules this page keeps (docs/design.md §15):
//   - Output is written to xterm.js as bytes (Uint8Array); each write's callback acks it.
//   - Nothing here types into a program. The only input is the user's keys and pastes,
//     Shift+Return's ESC CR, and xterm.js's answers to the program's queries.
//   - Copying goes from the selection to the clipboard; nothing reads the clipboard
//     (pasting is the browser's own paste event, which the user starts).
//   - Links open only as http(s), and only after hopsesh's own dialog; other schemes are
//     refused here.
//   - The hand-off's trust-question banner is a display-only match on what the tab shows;
//     it never drives input or any step.
import { SerializeAddon } from "./vendor/addon-serialize.mjs";
import { Terminal } from "./vendor/xterm.mjs";
import { FitAddon } from "./vendor/addon-fit.mjs";
import { WebglAddon } from "./vendor/addon-webgl.mjs";
import { Unicode11Addon } from "./vendor/addon-unicode11.mjs";
import { WebLinksAddon } from "./vendor/addon-web-links.mjs";
import { SearchAddon } from "./vendor/addon-search.mjs";
import { watchTabs, connectTab } from "./stream.js";

const $ = (s) => document.querySelector(s);
const params = new URLSearchParams(location.search);
const forceDOM = params.get("renderer") === "dom"; // the browser tests read the rows

// h builds DOM from parts, never by parsing markup: a program's or a session's text is only ever text.
function h(tag, attrs = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid === null || kid === undefined || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}
function svg(paths, size = 12, cls = "") {
  const ns = "http://www.w3.org/2000/svg";
  const s = document.createElementNS(ns, "svg");
  for (const [k, v] of Object.entries({ width: size, height: size, viewBox: "0 0 24 24", fill: "none", stroke: "currentColor", "stroke-width": "2.2", "aria-hidden": "true" })) s.setAttribute(k, v);
  if (cls) s.setAttribute("class", cls);
  for (const d of paths) {
    const p = document.createElementNS(ns, d.startsWith("rect") ? "rect" : "path");
    if (d.startsWith("rect")) for (const [k, v] of Object.entries({ x: 5, y: 11, width: 14, height: 9, rx: 2 })) p.setAttribute(k, v);
    else p.setAttribute("d", d);
    s.append(p);
  }
  return s;
}
const padlock = () => svg(["rect", "M8 11V8a4 4 0 0 1 8 0v3"], 12, "padlock");
const ICON_INFO = ["M12 11v6M12 7.5v.5", "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"];

let prefs = { fontSize: 13, scrollback: 5000, screenReader: false, os: /Mac/.test(navigator.userAgent) ? "darwin" : /Windows/.test(navigator.userAgent) ? "windows" : "linux" };
const mac = () => prefs.os === "darwin";
document.documentElement.dataset.os = prefs.os;

const tabs = new Map(); // id → { info, term, fit, search, conn, el, order, output, tail, trust, started, closeTimer }
let order = [];
let active = "";
let scope = "";
const collapsedGroups = new Set();
let request = () => {};
let tipWanted = false; // the first-run tip, until "Got it"

// ---- the tab's words -------------------------------------------------------------------

const program = (t) => (t.info.program || "").replace(/\.exe$/i, "");
const who = (t) => t.info.agent || program(t);

// chipOf is a tab's status chip: [class, words].
function chipOf(t) {
  const i = t.info;
  if (i.state === "exited") return i.code < 0 ? ["stopped", "stopped"] : i.code === 0 ? ["ok", "exited 0"] : ["bad", `exited ${i.code}`];
  if (i.attention) return ["wait", "waiting for you"];
  if (i.state === "waiting" && i.reason === "idle") return ["idle", "idle"];
  if (!t.output && Date.now() - t.started < 2000) return ["run", "starting"];
  return ["run", "running"];
}

const KIND_NAME = { session: "session", step: "hand-off step", bring: "bring-back", signin: "sign-in", shell: "shell" };

function home(p) {
  const hm = prefs.home;
  if (hm && (p === hm || p.startsWith(hm + "/") || p.startsWith(hm + "\\"))) return "~" + p.slice(hm.length);
  return p;
}

function ago(iso) {
  const d = (Date.now() - new Date(iso).getTime()) / 1000;
  if (d < 45) return "just now";
  if (d < 3600) return `${Math.max(1, Math.round(d / 60))} min ago`;
  return `${Math.round(d / 3600)} h ago`;
}

// ---- xterm.js ---------------------------------------------------------------------------

function theme() {
  const dark = darkAppearance();
  return dark ? {
    background: "#1f1f1c", foreground: "#ecebe5", cursor: "#ecebe5", cursorAccent: "#1f1f1c", selectionBackground: "#3cbcac55",
    black: "#2c2b28", red: "#f0948c", green: "#8fd19e", yellow: "#e3c27a", blue: "#8fb8e6", magenta: "#c9a4e8", cyan: "#7fd4c8", white: "#d4d2c9",
    brightBlack: "#7d7a71", brightRed: "#ffb0a8", brightGreen: "#b2e6bd", brightYellow: "#f0d9a0", brightBlue: "#b0cff0", brightMagenta: "#dcc2f2", brightCyan: "#a6e6dd", brightWhite: "#ffffff",
  } : {
    background: "#ffffff", foreground: "#1d1c19", cursor: "#1d1c19", cursorAccent: "#ffffff", selectionBackground: "#0b6b6233",
    black: "#1d1c19", red: "#a6322a", green: "#1f6b3d", yellow: "#7a5a0e", blue: "#1d4f8c", magenta: "#7a3b8c", cyan: "#0b6b62", white: "#8a877e",
    brightBlack: "#5f5c55", brightRed: "#c0432f", brightGreen: "#2f8a55", brightYellow: "#9a6f12", brightBlue: "#2c6a99", brightMagenta: "#9a52ae", brightCyan: "#12867b", brightWhite: "#33312c",
  };
}

function fontFamily() {
  const sys = mac() ? '"SF Mono", Menlo, monospace' : prefs.os === "windows" ? '"Cascadia Mono", Consolas, monospace' : '"DejaVu Sans Mono", monospace';
  return prefs.font ? `${prefs.font}, ${sys}` : sys;
}

// makeTerm is a tab's emulator.
function makeTerm(t) {
  const term = new Terminal({
    allowProposedApi: true, // the Unicode 11 widths and the search's match count
    fontFamily: fontFamily(), fontSize: prefs.fontSize, scrollback: prefs.scrollback, screenReaderMode: !!prefs.screenReader,
    theme: theme(), cursorBlink: true, macOptionIsMeta: false, drawBoldTextInBrightColors: false, rescaleOverlappingGlyphs: true,
    // Rule 8: links (OSC 8 too) open only as http(s) and after hopsesh's dialog.
    linkHandler: { activate: (_ev, uri) => openLink(t, uri), allowNonHttpProtocols: true },
  });
  const fit = new FitAddon();
  const search = new SearchAddon();
 const serializer = new SerializeAddon(); term.loadAddon(serializer); t.serializer = serializer;
  term.loadAddon(fit);
  term.loadAddon(search);
  term.loadAddon(new Unicode11Addon());
  term.unicode.activeVersion = "11";
  term.loadAddon(new WebLinksAddon((_ev, uri) => openLink(t, uri)));
  term.open(t.el);
  if (!forceDOM) {
    try {
      const gl = new WebglAddon();
      gl.onContextLoss(() => gl.dispose()); // back to the DOM renderer
      term.loadAddon(gl);
    } catch { /* no WebGL here: the DOM renderer draws it */ }
  }
  term.onData((d) => { // keys, pastes and the emulator's answers
    t.conn?.input(d);
    if (t.savedTimer && !t.savedSeen) { t.savedSeen = true; if (t.id === active) paint(); } // the "Saved here" banner goes on the next key
  });
  term.onBinary((d) => t.conn?.input(Uint8Array.from(d, (c) => c.charCodeAt(0) & 255)));
  term.onResize(({ cols, rows }) => { if (t.restored) t.conn?.resize(cols, rows); });
  term.textarea?.addEventListener("compositionstart", () => { t.composing=true; });
  term.textarea?.addEventListener("compositionend", () => { t.composing=false; });
  term.attachCustomKeyEventHandler((ev) => keys(ev, t));
  search.onDidChangeResults?.((r) => {
    if (t.id !== active) return;
    $("#find-n").textContent = !$("#find-q").value ? "" : r.resultCount ? `${r.resultIndex + 1} of ${r.resultCount}` : "No matches";
  });
  t.term = term;
  t.fit = fit;
  t.search = search;
}

// ---- tabs ---------------------------------------------------------------------------------

function addTab(info) {
  const el = h("div", { class: "term", id: "term-" + info.id, hidden: true });
  $("#terms").append(el);
  const t = { id: info.id, info, el, output: false, tail: "", trust: false, started: Date.now() };
  tabs.set(info.id, t);
  makeTerm(t);
  const dec = new TextDecoder();
  t.conn = connectTab(info.id, {
    output: (bytes, done) => {
      if (!t.output) { t.output = true; strip(); }
      if (info.capture || t.info.kind === "step") watchTrust(t, dec.decode(bytes, { stream: true }));
      t.term.write(bytes, done);
    },
    restored: () => new Promise(resolve => t.term.write("", () => { t.restored=true; fitTab(t); t.conn.resize(t.term.cols,t.term.rows); resolve(); })),
    checkpoint: () => new Promise(resolve => t.term.write("", () => {
       const b = t.term.buffer.active;
       // Explicit CUP fixes serialize 0.14's final-column cursor restoration bug
       // (xtermjs/xterm.js#6165). Leave pending-wrap cursors untouched.
       const cursor = b.cursorX < t.term.cols ? `\x1b[${b.cursorY+1};${b.cursorX+1}H` : "";
       resolve("\x1bc" + t.serializer.serialize() + cursor);
    })),
    state: (i) => { if (!t.initialSize) { t.initialSize=true; t.term.resize(i.cols,i.rows); } update(t, i); },
    error: (e) => toast(e),
    closed: () => {},
  });

}

// watchTrust is the trust question's display-only match: the step's banner says what the
// question is when Claude Code's wording shows, and stays generic otherwise. It looks at a
// few kilobytes of what the tab shows and nothing else, and it never answers anything.
const TRUST = /Do you trust the files in this folder|Is this a project you created or one you trust|Yes, I trust this folder/;
function watchTrust(t, text) {
  t.tail = (t.tail + text.replace(/\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)|\x1b./g, "")).slice(-4096);
  const trust = TRUST.test(t.tail);
  if (trust !== t.trust) {
    t.trust = trust;
    if (t.id === active) paint();
  }
}

// update takes a tab's new state (from its stream or the list).
function update(t, i) {
  const before = t.info;
  // List messages are complete metadata snapshots; omitted optional fields clear
  // earlier associations. Per-PTY state frames contain only Info and are merged.
  t.info = i.kind ? {...i} : Object.assign({}, t.info, i);
  if (before.state !== "exited" && t.info.state === "exited") exited(t);
  if (before.attention !== t.info.attention || before.state !== t.info.state || before.code !== t.info.code) announce(t);
  if (before.link !== t.info.link && t.info.link) say("Session link captured");
  strip();
  if (t.id === active) paint();
}

// exited resets what the program left switched on (rule 11), and says how it ended.
function exited(t) {
  const i = t.info;
  t.term.write("\x1b[0m\x1b[?2004l\x1b[?1004l\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1049l\x1b[<u\x1b[?25h");
  if (i.code === 0 || i.code < 0) {
    const what = i.code < 0 ? `${program(t)} was stopped` : `program exited with code 0`;
    t.term.write(`\r\n\x1b[2m[${what}${i.kind === "signin" && i.code === 0 ? " · this tab closes in 10 s" : ""}]\x1b[0m\r\n`);
  }
  if (i.kind === "signin" && i.code === 0) closeSoon(t);
}

// closeSoon closes a sign-in tab 10 s after it ended well, unless the pointer is on it (the
// user is reading it).
function closeSoon(t) {
  clearTimeout(t.closeTimer);
  t.closeTimer = setTimeout(() => {
    if (!tabs.has(t.id)) return;
    if (t.id === active && $("#stage").matches(":hover")) { closeSoon(t); return; }
    t.conn.close();
  }, 10_000);
}

function removeTab(id) {
  const t = tabs.get(id);
  if (!t) return;
  clearTimeout(t.closeTimer);
  t.conn.detach();
  t.term.dispose();
  t.el.remove();
  tabs.delete(id);
  if (active === id) activate(order.filter((x) => x !== id && tabs.has(x)).at(-1) || "");
}

function activate(id, focus = true) {
  if (!tabs.has(id)) id = "";
  active = id;
  for (const t of tabs.values()) t.el.hidden = t.id !== id;
  if (id) {
    const t = tabs.get(id);
    request({ op: "active", id });
    requestAnimationFrame(() => { fitTab(t); if (focus) t.term.focus(); });
  }
  closeFind();
  strip();
  paint();
}

function fitTab(t) {
  if (!t.restored || t.el.hidden || !t.el.getClientRects().length || t.el.clientWidth<1 || t.el.clientHeight<1) return;
  try { t.fit.fit(); } catch { /* not laid out yet */ }
}

// ---- the list from hopsesh -----------------------------------------------------------------

function onMessage(m) {
  if (m.rehost) { rehost(m.rehost); return; }
  if (m.notice) { toast(m.notice); return; }
  if (m.select) { if (tabs.has(m.select)) { scope=groupKey(tabs.get(m.select));collapsedGroups.delete(scope); activate(m.select); } else pendingSelect = m.select; return; }
  if (m.prefs) setPrefs(m.prefs);
  if (!m.tabs) return;
  const seen = new Set();
  for (const info of m.tabs) {
    seen.add(info.id);
    const t = tabs.get(info.id);
    if (t) update(t, info); else addTab(info);
  }
  for (const id of [...tabs.keys()]) if (!seen.has(id)) removeTab(id);
  order = m.tabs.map((x) => x.id);
  if (pendingSelect && tabs.has(pendingSelect)) { activate(pendingSelect); pendingSelect = ""; }
  else if (!active || !tabs.has(active)) activate(order.at(-1) || "", false);
  strip();
  paint();
}
let pendingSelect = "";

function setPrefs(p) {
 $("#terminal-grouping").value=p.grouping||"family";
 collapsedGroups.clear();for(const k of p.collapsed||[])collapsedGroups.add(k);
 $("#placement").value = p.placement || "separate";
 document.documentElement.classList.toggle("embedded", p.placement === "bottom" || p.placement === "right");
  const changed = JSON.stringify(p) !== JSON.stringify(prefs);
  prefs = Object.assign({}, prefs, p);
  setAppearance(prefs.appearance);
  document.documentElement.dataset.os = prefs.os;
  shortcuts();
  if (!changed) return;
  for (const t of tabs.values()) {
    Object.assign(t.term.options, { fontFamily: fontFamily(), fontSize: prefs.fontSize, scrollback: prefs.scrollback, screenReaderMode: !!prefs.screenReader });
    fitTab(t);
  }
  $("#t-reader").setAttribute("aria-pressed", prefs.screenReader ? "true" : "false");
}

// shortcuts spells the way back for the system: ⌃` on macOS, Ctrl+` elsewhere.
function shortcuts() {
  const k = mac() ? "⌃`" : "Ctrl+`";
  $("#back").lastChild.textContent = k;
  $("#back").title = `Back to sessions (${k})`;
  $("#tip").firstChild.textContent = `Press ${k} to move between the terminal and your sessions. `;
}

// ---- drawing the window ------------------------------------------------------------------

function groupKey(t){return prefs.grouping==="none"?"":prefs.grouping==="session"?(t.info.machine+"/"+(t.info.key||t.id)):(t.info.relationship?.family||"other");}
function groupName(t){return prefs.grouping==="session"?(t.info.relationship?.branchName||t.info.title):(t.info.relationship?.name||"Other terminals");}
function drawGroups(){
 const el=$("#terminal-groups");
 if(prefs.grouping==="none" || tabs.size<2){scope="";el.replaceChildren();return;}
 const groups=new Map();
 for(const t of tabs.values()){const k=groupKey(t);if(!groups.has(k))groups.set(k,{name:groupName(t),tabs:[]});groups.get(k).tabs.push(t);}
 if(scope && !groups.has(scope))scope="";
 el.replaceChildren(h("button",{class:!scope?"ib selected":"ib",onclick:()=>{scope="";strip();}},"All terminals"),
 ...[...groups].map(([key,g])=>h("button",{class:scope===key?"ib selected":"ib","aria-expanded":!collapsedGroups.has(key),onclick:()=>{
 if(scope===key){const on=!collapsedGroups.has(key);on?collapsedGroups.add(key):collapsedGroups.delete(key);request({op:"collapse",id:key,on});}else{scope=key;collapsedGroups.delete(key);if(!g.tabs.some(t=>t.id===active))activate(g.tabs[0].id);}strip();
 }},(collapsedGroups.has(key)?"▸ ":"▾ ")+g.name+" · "+g.tabs.length+(g.tabs.some(t=>t.info.attention)?" · needs you":""))));
}
function strip() {
  drawGroups();
  const list = $("#tabs");
  // With no group controls, every tab must remain reachable. A late session
  // association or closing a group's last sibling can change its group key.
  const grouped = prefs.grouping !== "none" && tabs.size > 1;
  const ids = order.filter((id) => tabs.has(id) && (!grouped || ((!scope || groupKey(tabs.get(id))===scope) && !collapsedGroups.has(groupKey(tabs.get(id))))));
  list.replaceChildren(...ids.map((id) => {
    const t = tabs.get(id);
    const [cls, words] = chipOf(t);
    const sel = id === active;
    const priv = t.info.private;
    const name = `${t.info.title}, ${t.info.agent || KIND_NAME[t.info.kind] || "program"}, ${words}${priv ? ", not recorded" : ""}`;
    return h("div", { class: "tt", role: "tab", id: "tab-" + id, "aria-selected": sel ? "true" : "false", "aria-label": name, tabindex: sel ? "0" : "-1", title: t.info.title,
      "data-tab": id, onclick: () => activate(id), onauxclick: (ev) => { if (ev.button === 1) closeTab(t); },
      onkeydown: (ev) => tabKeys(ev, id) },
    t.info.attention ? h("span", { class: "wdot", "aria-hidden": "true" }) : null,
    priv ? padlock() : null,
    h("span", { class: "name" }, t.info.title),
    h("span", { class: "chip " + cls, title: words }, h("span", { class: "cd" }), h("span", { class: "cw" }, words)),
    h("button", { class: "x", tabindex: "-1", "aria-label": "Close tab " + t.info.title, onclick: (ev) => { ev.stopPropagation(); closeTab(t); } }, svg(["M6 6l12 12M18 6 6 18"], 12)));
  }));
  $("#empty").hidden = tabs.size > 0;
  $("#info").hidden = !active;
  tighten();
  revealActiveTab();
}

function revealActiveTab() {
  const strip = $("#tabs"), tab = $("#tab-" + active);
  if (!tab) return;
  const box = strip.getBoundingClientRect(), item = tab.getBoundingClientRect();
  if (item.left < box.left) strip.scrollLeft -= box.left - item.left;
  else if (item.right > box.right) strip.scrollLeft += item.right - box.right;
}

// tighten marks the tabs squeezed to (near) their minimum width: they show their status
// as a dot, which leaves their name some room.
function tighten() {
  const els = [...$("#tabs").children];
  for (const el of els) el.classList.remove("tight"); // measured with their words
  const narrow = els.filter((el) => { const n = el.querySelector(".name"); return el.getBoundingClientRect().width < 150 && n && n.scrollWidth > n.clientWidth; });
  for (const el of narrow) el.classList.add("tight");
}

function tabKeys(ev, id) {
  const ids = order.filter((x) => tabs.has(x));
  const i = ids.indexOf(id);
  const n = ev.key === "ArrowRight" ? i + 1 : ev.key === "ArrowLeft" ? i - 1 : ev.key === "Home" ? 0 : ev.key === "End" ? ids.length - 1 : -1;
  if (n >= 0 && n < ids.length) {
    ev.preventDefault();
    activate(ids[n], false);
    $("#tab-" + ids[n])?.focus();
  }
}

// paint draws the active tab's banner, info row, note and exit bar.
function paint() {
  const t = tabs.get(active);
  const banner = $("#banner"), note = $("#note"), bar = $("#exitbar");
  banner.hidden = note.hidden = bar.hidden = true;
  $("#tip").hidden = !tipWanted;
  if (!t) return;
  const i = t.info;
  const done = i.state === "exited";
  const runs = $("#runs");
  $("#lock").hidden = !(i.kind === "signin" || i.kind === "shell" || i.private);
  $("#reads").hidden = i.kind !== "step";
  if (i.kind === "signin") runs.replaceChildren("Runs ", h("span", { class: "mono" }, i.command), " · your browser opens the sign-in page; hopsesh never sees it");
  else if (i.kind === "shell") runs.replaceChildren("Your login shell ", h("span", { class: "mono" }, (i.command || "").replace(/^your login shell /, "")), " in ", h("span", { class: "mono" }, home(i.dir)));
  else runs.replaceChildren(t.info.machine ? `${t.info.machine}${t.info.account ? " · "+t.info.account : ""} · ` : "", "Runs ", h("span", { class: "mono" }, i.command || i.program), " in ", h("span", { class: "mono" }, home(i.dir)), " · started " + ago(i.started));
  if(i.relationship?.branchName) runs.append(" · "+i.relationship.branchName);
 if (i.association === "Session association not confirmed") runs.append(" · Session association not confirmed");
 runs.title = `${i.command || i.program}\n${i.dir}`;
  $("#t-external").hidden = !i.external || (done && i.kind === "step");
  $("#t-reader").setAttribute("aria-pressed", prefs.screenReader ? "true" : "false");

  const show = (cls, ...kids) => { banner.className = cls; banner.replaceChildren(...kids); banner.hidden = false; };
  const agent = i.agent || "The program";
  if (i.kind === "step") {
    if (i.link) show("ok", h("span", {}, "✓"), h("span", {}, "Session link captured: ", h("span", { class: "mono" }, i.link.replace(/^https?:\/\//, "").replace(/(session_[A-Za-z0-9]{10})[A-Za-z0-9]+.*/, "$1…"))),
      h("span", { class: "spacer" }), h("button", { class: "ib", onclick: () => copyText(i.link, "Copied the link") }, "Copy link"));
    else if (!done && t.trust) show("warn", svg(ICON_INFO, 16), h("span", {}, `${agent} is asking whether it trusts hopsesh's hand-off folder. Answer it here; hopsesh never answers for you.`));
    else if (!done) show("warn", svg(ICON_INFO, 16), h("span", {}, `If ${agent} asks you something, answer it here. hopsesh reads this tab only for the session link; it never types or answers here.`));
  } else if (i.kind === "bring" && !done && i.saved) {
    if (!t.savedSeen) {
      // Said once, then out of the way: after a few seconds or the next key.
      show("ok", h("span", {}, "✓"), h("span", {}, h("b", {}, `Saved here as “${i.saved}”.`), " Keep working in this tab."));
      if (!t.savedTimer) t.savedTimer = setTimeout(() => { t.savedSeen = true; if (t.id === active) paint(); }, 6000);
    }
  } else if (i.kind === "bring" && !done) {
    show("warn", svg(ICON_INFO, 16), h("span", {}, `Send a message to keep this session here. ${agent} saves its copy once you do.`));
  } else if (i.kind === "signin") {
    show("lock", padlock(), h("span", {}, "Nothing in a sign-in tab is recorded. hopsesh doesn't watch, match or keep what appears here, and it's gone when you close the tab."));
  }
  if (!banner.hidden) $("#tip").hidden = true; // one thing at a time above the terminal
  if (done && i.code > 0) {
    const at = new Date().toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
    bar.replaceChildren(...[h("span", {}, h("b", {}, `${program(t)} exited with code ${i.code}`), ` · ${at}. The output stays until you close the tab.`), h("span", { class: "spacer" }),
      i.rerun ? h("button", { class: "btn", onclick: () => request({ op: "rerun", id: t.id }) }, "Run again") : null,
      i.external && i.kind !== "step" ? h("button", { class: "btn", onclick: () => moveOut(t) }, "Open in my terminal") : null,
      h("button", { class: "btn", onclick: () => t.conn.close() }, "Close tab")].filter(Boolean));
    bar.hidden = false;
  }
}

// ---- actions ---------------------------------------------------------------------------

function ask({ title, body, ok, danger = false, extra }) {
  return new Promise((resolve) => {
    const d = $("#dlg");
    d.replaceChildren(h("div", { class: "dlg-body" }, h("h2", {}, title), body ? h("div", { class: "muted" }, body) : null, extra || null,
      h("div", { class: "dlg-foot" },
        h("button", { class: "btn", onclick: () => d.close("no") }, "Cancel"),
        h("button", { class: "btn " + (danger ? "danger" : "primary"), onclick: () => d.close("yes") }, ok))));
    d.returnValue = "";
    d.addEventListener("close", () => { resolve(d.returnValue === "yes"); tabs.get(active)?.term.focus(); }, { once: true });
    d.showModal();
  });
}

async function closeTab(t) {
  if (!t) return;
  const i = t.info;
  if (i.state !== "exited" && i.kind !== "shell") {
    const body = i.kind === "session" ? "The conversation is kept up to its last finished turn." : i.kind === "step" ? "The hand-off stops waiting for it." : "";
    if (!await ask({ title: `End ${program(t)} in “${i.title}”?`, body, ok: "End and close", danger: true })) return;
  }
  t.conn.close();
}

async function moveOut(t) {
  const i = t.info;
  const where = prefs.terminalName || "your terminal app";
  const title = i.kind === "step" ? `Run this step in ${where}?` : `Move “${i.title}” to ${where}?`;
  const body = i.kind === "step"
    ? `hopsesh ends this tab and starts ${i.command} again in ${where}. Nothing is in the cloud until you answer its question there.`
    : i.state === "exited" ? `hopsesh runs ${i.command} again in ${where}.`
    : `hopsesh ends it here and runs the same ${i.command} in ${where}.${i.kind === "session" ? " A turn in progress stops." : ""}`;
  if (await ask({ title, body, ok: i.state === "exited" ? `Open in ${where}` : `End here and open in ${where}` })) request({ op: "external", id: t.id });
}

// openLink sends a clicked link to hopsesh, which asks before it opens anything; other
// schemes stop here (rule 8).
function openLink(t, uri) {
  let u;
  try { u = new URL(uri); } catch { u = null; }
  if (!u || (u.protocol !== "http:" && u.protocol !== "https:")) {
    const d = $("#dlg");
    d.replaceChildren(h("div", { class: "dlg-body" }, h("h2", {}, "This link can't open from a terminal"), h("div", { class: "url" }, String(uri).slice(0, 500)),
      h("div", { class: "muted" }, "Only web links (http and https) open from hopsesh's tabs. Links to apps and files are blocked, whatever printed them."),
      h("div", { class: "dlg-foot" }, h("button", { class: "btn primary", onclick: () => d.close() }, "OK"))));
    d.showModal();
    return;
  }
  t.conn.link(u.href);
}

// copyText puts text on the clipboard (writing only; nothing here reads it).
async function copyText(text, done = "Copied") {
  if (!text) { toast("Select some text first"); return; }
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const ta = h("textarea", { style: "position:fixed;opacity:0" });
    ta.value = text;
    document.body.append(ta);
    ta.select();
    document.execCommand("copy");
    ta.remove();
  }
  toast(done);
}

function openFind() {
  if (!active) return;
  $("#find").hidden = false;
  $("#find-q").focus();
  $("#find-q").select();
}
function closeFind() {
  if ($("#find").hidden) return;
  $("#find").hidden = true;
  tabs.get(active)?.search.clearDecorations();
  tabs.get(active)?.term.focus();
}
const FIND = { decorations: { matchOverviewRuler: "#b8641a", activeMatchColorOverviewRuler: "#0b6b62", matchBackground: "#f7e1c8", activeMatchBackground: "#b8641a" } };
function find(next = true) {
  const t = tabs.get(active);
  const q = $("#find-q").value;
  if (!t || !q) { $("#find-n").textContent = ""; return; }
  const found = next ? t.search.findNext(q, FIND) : t.search.findPrevious(q, FIND);
  if (!found) $("#find-n").textContent = "No matches";
}

function fontBy(d) {
  const size = d === 0 ? 13 : Math.min(24, Math.max(9, prefs.fontSize + d));
  request({ op: "font", size });
}

function cycle(d) {
  const ids = order.filter((x) => tabs.has(x));
  if (ids.length < 2) return;
  activate(ids[(ids.indexOf(active) + d + ids.length) % ids.length]);
}

// keys is the terminal's keyboard rule: the program gets every key except the window's
// own shortcuts. On macOS those are ⌘ keys (programs never get ⌘); elsewhere Ctrl+Shift+
// letter. Ctrl+` goes back to the sessions everywhere. It returns false for a key the
// program must not get.
function keys(ev, t) {
  if (ev.type !== "keydown") return true;
  const k = ev.key.toLowerCase();
  const stop = () => { ev.preventDefault(); ev.stopPropagation(); return false; };
  if (ev.ctrlKey && !ev.metaKey && !ev.altKey && ev.code === "Backquote") { request({ op: "main" }); return stop(); }
  if (ev.key === "Enter" && ev.shiftKey && !ev.ctrlKey && !ev.metaKey && !ev.altKey) { t.conn.input("\x1b\r"); return stop(); } // a new line in Claude Code
  if (ev.ctrlKey && ev.key === "Tab") { cycle(ev.shiftKey ? -1 : 1); return stop(); }
  if (mac()) {
    if (!ev.metaKey) return true;
    if (ev.shiftKey && ev.key === "Enter") { request({ op: "maximize" }); return stop(); }
    if (ev.shiftKey && (ev.key === "]" || ev.key === "}")) { cycle(1); return stop(); }
    if (ev.shiftKey && (ev.key === "[" || ev.key === "{")) { cycle(-1); return stop(); }
    if (ev.shiftKey && k === "k") { t.term.clear(); return stop(); }
    switch (k) {
      case "c": case "v": case "a": case "q": case "h": case "m": case ",": case "k": return false; // the app's menu (copy and paste through the Edit menu)
      case "f": openFind(); return stop();
      case "t": request({ op: "shell", id: active }); return stop();
      case "w": closeTab(t); return stop();
      case "=": case "+": fontBy(1); return stop();
      case "-": fontBy(-1); return stop();
      case "0": fontBy(0); return stop();
    }
    return false; // ⌘ keys never reach programs
  }
  if (ev.ctrlKey && ev.shiftKey && !ev.altKey) {
    switch (k) {
      case "c": copyText(t.term.getSelection()); return stop();
      case "v": return false; // the browser's paste, which xterm.js takes
      case "t": request({ op: "shell", id: active }); return stop();
      case "w": closeTab(t); return stop();
      case "f": openFind(); return stop();
      case "k": t.term.clear(); return stop();
      case "p": request({ op: "main" }); return stop();
      case "enter": request({ op: "maximize" }); return stop();
    }
  }
  if (ev.ctrlKey && !ev.shiftKey && !ev.altKey) {
    if (k === "c" && t.term.hasSelection()) { copyText(t.term.getSelection()); t.term.clearSelection(); return stop(); }
    if (k === "v") return false; // paste
    if (k === "=" || k === "+") { fontBy(1); return stop(); }
    if (k === "-") { fontBy(-1); return stop(); }
    if (k === "0") { fontBy(0); return stop(); }
  }
  return true;
}

// ---- small things --------------------------------------------------------------------------

let toastTimer;
function toast(msg) {
  const el = $("#toast");
  el.textContent = msg;
  el.classList.add("show");
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove("show"), 3500);
}

function say(text) { $("#live").textContent = text; }

function announce(t) {
  const i = t.info;
  const [, words] = chipOf(t);
  if (i.state === "exited") say(`${i.title} tab: ${program(t)} ${i.code < 0 ? "was stopped" : `exited with code ${i.code}`}`);
  else if (i.attention) say(`${i.title} tab: waiting for you`);
  else say(`${i.title} tab: ${words}`);
}

// ---- wiring -------------------------------------------------------------------------------

request = watchTabs(onMessage, connected => {
  const el = $("#connection");
  el.hidden = connected;
  el.textContent = connected ? "" : "Connecting to Hopsesh… Terminal controls will be ready when the connection returns.";
  $("#new-shell").disabled = !connected;
  $("#empty-shell").disabled = !connected;
});
$("#new-shell").onclick = () => request({ op: "shell", id: active });
$("#empty-shell").onclick = () => request({ op: "shell", id: "" });
$("#back").onclick = () => request({ op: "main" });
$("#t-external").onclick = () => { const t = tabs.get(active); if (t) moveOut(t); };
$("#t-copy").onclick = () => { const t = tabs.get(active); if (t) copyText(t.term.getSelection()); };
$("#t-find").onclick = openFind;
$("#t-clear").onclick = () => tabs.get(active)?.term.clear();
$("#t-close").onclick = () => closeTab(tabs.get(active));
$("#t-max").onclick = () => request({ op: "maximize" });
$("#t-reader").onclick = () => request({ op: "reader", on: !prefs.screenReader });
$("#tabs").addEventListener("wheel", (ev) => { // a mouse's wheel scrolls the tabs sideways
  if (Math.abs(ev.deltaY) > Math.abs(ev.deltaX)) { $("#tabs").scrollLeft += ev.deltaY; ev.preventDefault(); }
}, { passive: false });
$("#find-q").addEventListener("input", () => find(true));
$("#find-q").addEventListener("keydown", (ev) => {
  if (ev.key === "Enter") { ev.preventDefault(); find(!ev.shiftKey); }
  if (ev.key === "Escape") { ev.preventDefault(); closeFind(); }
});
$("#find-next").onclick = () => find(true);
$("#find-prev").onclick = () => find(false);
$("#find-close").onclick = closeFind;
document.addEventListener("keydown", (ev) => {
  // Outside a terminal (the strip, the find field): the same way back.
  if (ev.ctrlKey && ev.code === "Backquote") { ev.preventDefault(); request({ op: "main" }); }
});
new ResizeObserver(() => { const t = tabs.get(active); if (t) fitTab(t); }).observe($("#stage"));
new ResizeObserver(() => { tighten(); revealActiveTab(); }).observe($("#strip"));
onAppearanceChange(() => { for (const t of tabs.values()) t.term.options.theme = theme(); });
setInterval(() => { strip(); if (active) paint(); }, 30_000); // "started 3 min ago", and "starting" ending

// The first-run tip: how to leave the terminal, until "Got it" (kept), and never under a
// banner.
try {
  tipWanted = !localStorage.getItem("hopsesh.terminal.tip");
} catch { /* no storage here: no tip */ }
$("#tip-ok").onclick = () => { tipWanted = false; $("#tip").hidden = true; try { localStorage.setItem("hopsesh.terminal.tip", "1"); } catch { /* fine */ } };
shortcuts();
paint();

// For the browser tests: what a tab shows, as text (the page's own copy of its screen).
window.hopseshTerminal = {
  text: (id) => {
    const t = tabs.get(id || active);
    if (!t) return "";
    const b = t.term.buffer.active;
    const out = [];
    for (let y = 0; y < b.length; y++) out.push(b.getLine(y)?.translateToString(true) ?? "");
    return out.join("\n");
  },
  size: (id) => { const t = tabs.get(id || active); return t ? { cols: t.term.cols, rows: t.term.rows } : null; },
  active: () => active,
  buffer: () => { const t=tabs.get(active);const b=t?.term.buffer.active;return b?{type:b.type,x:b.cursorX,y:b.cursorY}:null; },
};

let moving = false;
async function rehost(placement) {
 if (moving) return;
 if ([...tabs.values()].some(t => t.composing)) { toast("Finish composing your text before moving the terminal."); $("#placement").value=prefs.placement||"separate"; return; }
 moving = true; $("#placement").disabled = true;
 try {
   await Promise.all([...tabs.values()].map(t => t.conn.prepare()));
   request({op:"placement", id:placement});
 } catch { toast("Couldn’t move the terminal. Your programs are still running here."); }
 finally { moving = false; $("#placement").disabled = false; }
}
$("#placement").value = prefs.placement || "separate";
$("#placement").onchange = e => {
 const p=e.target.value;
 if ((p==="separate") !== ((prefs.placement||"separate")==="separate")) rehost(p);
 else request({op:"placement",id:p});
};

$("#terminal-grouping").onchange=e=>{scope="";request({op:"grouping",id:e.target.value});};
