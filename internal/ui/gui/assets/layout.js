// The Sessions screen's panes: the sidebar and the inspector are resizable (a divider each,
// by pointer or keyboard, as the WAI-ARIA window splitter) and can be hidden (the title
// bar's buttons, the View menu, Enter on a divider, or dragging past half the minimum).
// The window lays out on a grid whose columns are --sb-w and --in-w. What the user chose
// is saved in the config (SaveLayout); a sidebar hidden because the window is narrow is
// not.
import { api, h, state, sys, $ } from "./core.js";

export const SIDEBAR = { def: 220, min: 180, max: 320, snap: 90 };
export const INSPECTOR = { def: 360, min: 280, max: 560, snap: 140 };
const LIST_MIN = 380, NARROW = 1000, STEP = 16, BIG_STEP = 64;

// lay is what the user chose; preview a pane's width while a drag passes its collapse
// point (0) or moves it.
const lay = { sidebarWidth: SIDEBAR.def, sidebarHidden: false, inspectorWidth: INSPECTOR.def, inspectorHidden: false };
const preview = { sidebar: null, inspector: null };
let loaded = false;

// load takes the saved layout (Info), before the first paint.
export function load(l) {
  if (!l || loaded) return;
  loaded = true;
  Object.assign(lay, {
    sidebarWidth: clamp(l.sidebarWidth || SIDEBAR.def, SIDEBAR.min, SIDEBAR.max), sidebarHidden: !!l.sidebarHidden,
    inspectorWidth: clamp(l.inspectorWidth || INSPECTOR.def, INSPECTOR.min, INSPECTOR.max), inspectorHidden: !!l.inspectorHidden,
  });
  apply();
}

const clamp = (v, lo, hi) => Math.max(lo, Math.min(hi, Math.round(v)));
// narrow: the window hides the sidebar for now (below 1000px), unsaved.
const narrow = () => window.innerWidth < NARROW;
// override: the user showed the sidebar in a narrow window (until it is wide again).
let override = false;
const sidebarBase = () => (lay.sidebarHidden || (narrow() && !override) ? 0 : lay.sidebarWidth);
const sidebarShown = () => (preview.sidebar ?? sidebarBase()) > 0;
const inspectorShown = () => (preview.inspector ?? (lay.inspectorHidden ? 0 : 1)) > 0;
// inspectorMax keeps the list at its minimum width at least.
const inspectorMax = (sb) => Math.max(INSPECTOR.min, Math.min(INSPECTOR.max, window.innerWidth - sb - LIST_MIN));

// widths are the columns now: [sidebar, inspector].
function widths() {
  const sb = preview.sidebar ?? sidebarBase();
  const insp = preview.inspector ?? (lay.inspectorHidden ? 0 : clamp(lay.inspectorWidth, INSPECTOR.min, inspectorMax(sb)));
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
  else Object.assign(lay, { inspectorWidth: INSPECTOR.def, inspectorHidden: false });
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

// The window's width decides whether the sidebar shows (below 1000px it hides, for now).
window.addEventListener("resize", () => {
  if (!narrow()) override = false;
  apply();
});
