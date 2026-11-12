// The Sessions screen's panes: the sidebar and the inspector are resizable (a divider each,
// by pointer or keyboard, as the WAI-ARIA window splitter) and can be hidden (the title
// bar's buttons, the View menu, Enter on a divider, or dragging past half the minimum).
// The window lays out on a grid whose columns are --sb-w and --in-w. What the user chose
// is saved in the config (SaveLayout); a sidebar hidden because the window is narrow is
// not. The inspector's width follows the window until the user sets one: by default 30%
// of the room beside the sidebar (360 to 480px), at most 960px, 60% of that room, and
// whatever leaves the list 440px. Its sections' open or closed state is saved too.
import { api, h, state, sys, $ } from "./core.js";

export const SIDEBAR = { def: 220, min: 180, max: 320, snap: 90 };
export const INSPECTOR = { min: 300, max: 960, snap: 150 };
// The session list's status column (status, account, mirror and when): resizable from its
// header, the WAI-ARIA window splitter again; double-click fits it to what the rows show.
export const STATUS = { def: 220, min: 140, max: 560 };
const TITLE_MIN = 220; // what the title column keeps when the status column widens
const LIST_MIN = 440, NARROW = 1000, STEP = 16, BIG_STEP = 64;

// lay is what the user chose (an inspector width of 0 is its default); preview a pane's
// width while a drag passes its collapse point (0) or moves it.
const lay = { sidebarWidth: SIDEBAR.def, sidebarHidden: false, inspectorWidth: 0, inspectorHidden: false, statusWidth: 0, sectionsOpen: [], sectionsClosed: [] };
const preview = { sidebar: null, inspector: null, status: null };
let loaded = false;

// load takes the saved layout (Info), before the first paint.
export function load(l) {
  if (!l || loaded) return;
  loaded = true;
  Object.assign(lay, {
    sidebarWidth: clamp(l.sidebarWidth || SIDEBAR.def, SIDEBAR.min, SIDEBAR.max), sidebarHidden: !!l.sidebarHidden,
    inspectorWidth: l.inspectorWidth ? clamp(l.inspectorWidth, INSPECTOR.min, INSPECTOR.max) : 0, inspectorHidden: !!l.inspectorHidden,
    statusWidth: l.statusWidth ? clamp(l.statusWidth, STATUS.min, STATUS.max) : 0,
    sectionsOpen: [...(l.sectionsOpen || [])], sectionsClosed: [...(l.sectionsClosed || [])],
  });
  apply();
}

// sectionOpen is whether an inspector section is open (def until the user chose).
export function sectionOpen(name, def) {
  if (lay.sectionsOpen.includes(name)) return true;
  if (lay.sectionsClosed.includes(name)) return false;
  return def;
}
// setSection remembers that the user opened or closed an inspector section.
export function setSection(name, open) {
  lay.sectionsOpen = lay.sectionsOpen.filter((x) => x !== name);
  lay.sectionsClosed = lay.sectionsClosed.filter((x) => x !== name);
  (open ? lay.sectionsOpen : lay.sectionsClosed).push(name);
  save();
}

const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, Math.round(v)));
// narrow: the window hides the sidebar for now (below 1000px), unsaved.
const narrow = () => window.innerWidth < NARROW;
// override: the user showed the sidebar in a narrow window (until it is wide again).
let override = false;
const sidebarBase = () => (lay.sidebarHidden || (narrow() && !override) ? 0 : lay.sidebarWidth);
const sidebarShown = () => (preview.sidebar ?? sidebarBase()) > 0;
const inspectorShown = () => (preview.inspector ?? (lay.inspectorHidden ? 0 : 1)) > 0;
// The room beside the sidebar decides the inspector's default and its maximum (which keeps
// the list at its minimum width at least).
const room = (sb) => window.innerWidth - sb;
const inspectorDef = (sb) => clamp(room(sb) * 0.3, 360, 480);
const inspectorMax = (sb) => Math.max(INSPECTOR.min, Math.min(INSPECTOR.max, room(sb) - LIST_MIN, Math.round(room(sb) * 0.6)));

// widths are the columns now: [sidebar, inspector].
function widths() {
  const sb = preview.sidebar ?? sidebarBase();
  const want = lay.inspectorWidth || inspectorDef(sb);
  const insp = preview.inspector ?? (lay.inspectorHidden ? 0 : clamp(want, INSPECTOR.min, inspectorMax(sb)));
  return [sb, insp];
}

let told = "";
// apply sets the columns, the panes' inert state, the dividers' values and the buttons.
export function apply() {
  if (!narrow()) override = false; // shown in a narrow window: until it is wide again
  const [sb, insp] = widths();
  const root = document.documentElement.style;
  root.setProperty("--sb-w", sb + "px");
  root.setProperty("--in-w", insp + "px");
  const side = $("#sidebar"), ins = $("#inspector");
  if (side) side.inert = sb === 0;
  if (ins) ins.inert = insp === 0;
  applyStatus();
  divider("sidebar", sb, SIDEBAR.min, SIDEBAR.max);
  divider("inspector", insp, INSPECTOR.min, inspectorMax(sb));
  button("#btn-sidebar", sidebarShown(), "Sidebar", sys.mac ? "⌃⌘S" : "Ctrl+B");
  button("#btn-inspector", inspectorShown(), "Inspector", sys.mac ? "⌥⌘I" : "Ctrl+I");
  const now = `${sidebarShown()} ${inspectorShown()}`;
  if (now !== told) { told = now; api("ViewState", sidebarShown(), inspectorShown()).catch(() => {}); }
}

function divider(which, w, lo, hi) {
  const d = document.querySelector(`.divider[data-pane="${which}"]`);
  if (!d) return;
  d.setAttribute("aria-valuenow", String(w));
  d.setAttribute("aria-valuemin", String(lo));
  d.setAttribute("aria-valuemax", String(hi));
  d.classList.toggle("collapsed", w === 0);
}

function button(sel, shown, noun, key) {
  const b = $(sel);
  if (!b) return;
  b.setAttribute("aria-expanded", shown ? "true" : "false");
  b.title = `${shown ? "Hide" : "Show"} ${noun} (${key})`;
  b.setAttribute("aria-label", `${shown ? "Hide" : "Show"} ${noun.toLowerCase()}`);
}

// animate lets the next change of the columns slide (a toggle; never a drag or a window
// resize; the reduced-motion rule makes it instant).
function animate() {
  const g = document.querySelector(".layout");
  if (!g) return;
  g.classList.add("animate");
  clearTimeout(animate.t);
  animate.t = setTimeout(() => g.classList.remove("animate"), 220);
}

let saveTimer;
function save() {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => api("SaveLayout", Object.assign({}, lay)).catch(() => {}), 300);
}

// toggle shows or hides a pane. In a narrow window the sidebar's showing is for now (not
// saved): it hides again when the window is narrow next time.
export function toggle(which) {
  if (which === "inspector" && document.body.classList.contains("terminal-right")) { dispatchEvent(new Event("terminal-session-details")); return; }
  if (which === "sidebar") {
    if (lay.sidebarHidden) { lay.sidebarHidden = false; override = narrow(); }
    else if (narrow()) override = !override;
    else lay.sidebarHidden = true;
  } else lay.inspectorHidden = !lay.inspectorHidden;
  animate();
  apply();
  save();
}

// reset puts a pane back to its default width, shown.
function reset(which) {
  if (which === "sidebar") Object.assign(lay, { sidebarWidth: SIDEBAR.def, sidebarHidden: false });
  else Object.assign(lay, { inspectorWidth: 0, inspectorHidden: false });
  animate();
  apply();
  save();
}

// dividers are the two splitters, drawn into the grid (render calls it).
export function dividers() {
  return ["sidebar", "inspector"].map((which) => h("div", {
    class: "divider", "data-pane": which, role: "separator", tabindex: "0", "aria-orientation": "vertical", "aria-controls": which,
    "aria-label": which === "sidebar" ? "Resize sidebar" : "Resize inspector",
    onpointerdown: (ev) => drag(ev, which), ondblclick: () => reset(which), onkeydown: (ev) => keys(ev, which),
    onpointerenter: (ev) => { const d = ev.currentTarget; d.hoverTimer = setTimeout(() => d.classList.add("hot"), 300); },
    onpointerleave: (ev) => { const d = ev.currentTarget; clearTimeout(d.hoverTimer); if (!d.hasPointerCapture?.(ev.pointerId)) d.classList.remove("hot"); },
  }));
}

// drag follows the pointer: the pane's width clamped, or collapsed past its snap point
// (shown as it would be), committed on release.
function drag(ev, which) {
  if (ev.button !== 0) return;
  ev.preventDefault();
  const d = ev.currentTarget, grid = d.parentElement;
  d.setPointerCapture(ev.pointerId);
  document.body.classList.add("resizing");
  d.classList.add("hot");
  const spec = which === "sidebar" ? SIDEBAR : INSPECTOR;
  let frame = 0, last = null;
  const at = (x) => {
    const r = grid.getBoundingClientRect();
    const want = which === "sidebar" ? x - r.left : r.right - x;
    if (want < spec.snap) return 0;
    const hi = which === "sidebar" ? SIDEBAR.max : inspectorMax(widths()[0]);
    return clamp(want, spec.min, hi);
  };
  const move = (e) => {
    last = e.clientX;
    if (frame) return;
    frame = requestAnimationFrame(() => { frame = 0; preview[which] = at(last); apply(); });
  };
  const end = () => {
    d.removeEventListener("pointermove", move);
    d.removeEventListener("pointerup", end);
    d.removeEventListener("lostpointercapture", end);
    cancelAnimationFrame(frame);
    document.body.classList.remove("resizing");
    d.classList.remove("hot");
    const w = last === null ? null : at(last);
    preview[which] = null;
    if (w !== null) {
      if (which === "sidebar") {
        if (w === 0) { lay.sidebarHidden = !narrow(); override = false; } else Object.assign(lay, { sidebarWidth: w, sidebarHidden: false });
      } else if (w === 0) lay.inspectorHidden = true;
      else Object.assign(lay, { inspectorWidth: w, inspectorHidden: false });
      save();
    }
    apply();
  };
  d.addEventListener("pointermove", move);
  d.addEventListener("pointerup", end);
  d.addEventListener("lostpointercapture", end);
}

// keys: Left and Right move the divider (Shift: further), Home and End take the pane to
// its minimum and maximum, Enter hides it or brings it back.
function keys(ev, which) {
  const side = which === "sidebar";
  const [sb, insp] = widths();
  const cur = side ? sb : insp;
  const hi = side ? SIDEBAR.max : inspectorMax(sb), lo = side ? SIDEBAR.min : INSPECTOR.min;
  const step = ev.shiftKey ? BIG_STEP : STEP;
  let w = null;
  switch (ev.key) {
    case "ArrowLeft": w = side ? cur - step : cur + step; break;
    case "ArrowRight": w = side ? cur + step : cur - step; break;
    case "Home": w = lo; break;
    case "End": w = hi; break;
    case "Enter": ev.preventDefault(); toggle(which); return;
    default: return;
  }
  ev.preventDefault();
  w = clamp(w, lo, hi);
  if (side) Object.assign(lay, { sidebarWidth: w, sidebarHidden: false });
  else Object.assign(lay, { inspectorWidth: w, inspectorHidden: false });
  apply();
  save();
}

// ---- The status column ----

// statusMax keeps the title column at least TITLE_MIN wide in the list as it is now.
function statusMax() {
  const list = document.querySelector(".content .list");
  if (!list) return STATUS.max;
  return Math.max(STATUS.min, Math.min(STATUS.max, list.clientWidth - 2 * 18 - 2 - 2 * 14 - 22 - 2 * 12 - TITLE_MIN));
}
// statusWidth is the column's width now: the user's (or the default), within what the
// list has room for (a narrower window shrinks it for now; the saved width stays).
const statusWidth = () => clamp(preview.status ?? (lay.statusWidth || STATUS.def), STATUS.min, statusMax());

function applyStatus() {
  const w = statusWidth();
  document.documentElement.style.setProperty("--state-w", w + "px");
  for (const d of document.querySelectorAll('.col-resize')) {
    d.setAttribute("aria-valuenow", String(w));
    d.setAttribute("aria-valuetext", `${w} pixels`);
    d.setAttribute("aria-valuemin", String(STATUS.min));
    d.setAttribute("aria-valuemax", String(statusMax()));
  }
}

function setStatus(w) {
  lay.statusWidth = w === STATUS.def ? 0 : w;
  applyStatus();
  save();
}

// fitStatus is the width that shows every visible row's status column whole.
function fitStatus() {
  const t = document.querySelector(".tree");
  if (!t) return STATUS.def;
  t.classList.add("measuring");
  let need = STATUS.min;
  for (const part of t.querySelectorAll(".row .status-top, .row .r-indicators, .row .r-state > .w")) need = Math.max(need, Math.ceil(part.getBoundingClientRect().width));
  t.classList.remove("measuring");
  return clamp(need + 2, STATUS.min, statusMax());
}

// statusHeader is the list's column header: the columns' names and, between them, the
// status column's resize handle (pointer, keyboard, double-click to fit).
export function statusHeader(compact) {
  const handle = h("div", {
    class: "col-resize", role: "separator", tabindex: "0", "aria-orientation": "vertical", "aria-label": "Resize the status column",
    title: "Drag to resize · double-click to fit", onpointerdown: statusDrag, ondblclick: () => setStatus(fitStatus()), onkeydown: statusKeys,
    onpointerenter: (ev) => { const d = ev.currentTarget; d.hoverTimer = setTimeout(() => d.classList.add("hot"), 300); },
    onpointerleave: (ev) => { const d = ev.currentTarget; clearTimeout(d.hoverTimer); if (!d.hasPointerCapture?.(ev.pointerId)) d.classList.remove("hot"); },
  });
  const w = statusWidth();
  for (const [k, v] of [["aria-valuenow", w], ["aria-valuetext", `${w} pixels`], ["aria-valuemin", STATUS.min], ["aria-valuemax", STATUS.max]]) handle.setAttribute(k, String(v));
  return h("div", { class: "col-head" + (compact ? " compact" : ""), role: "presentation" },
    h("span", {}), h("span", { class: "col-name" }, "Session"), h("span", { class: "col-name col-state" }, handle, "Status"));
}

function statusDrag(ev) {
  if (ev.button !== 0) return;
  ev.preventDefault();
  const d = ev.currentTarget, start = ev.clientX, from = statusWidth();
  d.setPointerCapture(ev.pointerId);
  document.body.classList.add("resizing");
  d.classList.add("hot");
  let frame = 0, last = start, cancelled = false;
  const at = (x) => clamp(from + start - x, STATUS.min, statusMax());
  const move = (e) => {
    last = e.clientX;
    if (frame) return;
    frame = requestAnimationFrame(() => { frame = 0; preview.status = at(last); applyStatus(); });
  };
  const esc = (e) => { if (e.key === "Escape") { cancelled = true; d.releasePointerCapture(ev.pointerId); } };
  const end = () => {
    d.removeEventListener("pointermove", move);
    d.removeEventListener("pointerup", end);
    d.removeEventListener("lostpointercapture", end);
    window.removeEventListener("keydown", esc, true);
    cancelAnimationFrame(frame);
    document.body.classList.remove("resizing");
    d.classList.remove("hot");
    preview.status = null;
    if (cancelled || last === start) applyStatus();
    else setStatus(at(last));
  };
  d.addEventListener("pointermove", move);
  d.addEventListener("pointerup", end);
  d.addEventListener("lostpointercapture", end);
  window.addEventListener("keydown", esc, true);
}

// Left widens the status column (its handle moves left), Right narrows it; Shift: further.
// Home and End: its minimum and maximum; Enter: back to the default.
function statusKeys(ev) {
  const cur = statusWidth(), step = ev.shiftKey ? BIG_STEP : STEP;
  let w;
  switch (ev.key) {
    case "ArrowLeft": w = cur + step; break;
    case "ArrowRight": w = cur - step; break;
    case "Home": w = STATUS.min; break;
    case "End": w = statusMax(); break;
    case "Enter": w = STATUS.def; break;
    default: return;
  }
  ev.preventDefault();
  setStatus(clamp(w, STATUS.min, statusMax()));
}

// The window's width decides whether the sidebar shows (below 1000px it hides, for now).
window.addEventListener("resize", () => {
  if (!narrow()) override = false;
  apply();
});
