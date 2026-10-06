// Pop-up menus and popovers: the Resume split button's menu, Move ▾, ⋯, the Filter menu
// (with its facets' submenus) and the Display popover. One is open at a time (a submenu
// beside its menu); it hangs below its button inside the window, and Escape, a click
// elsewhere, Tab or a choice closes it, focus going back to the button.
import { h, fill } from "./core.js";

const stack = []; // the open popups, outermost first: { el, anchor, onclose }

// item is one entry of a menu: { label, sub (a second line), icon (a node), chip, hint,
// disabled, why (the reason, as its second line), run, check (true/false: a checkbox
// item), radio, keep (stays open after it runs), submenu (() => items), count }. A
// heading is { heading }, a separator { sep: true }.
function item(it, level) {
  if (it.sep) return h("div", { class: "pop-sep", role: "separator" });
  if (it.heading) return h("div", { class: "pop-h", role: "presentation" }, it.heading, it.headExtra || null);
  if (it.node) return it.node;
  const role = it.check !== undefined ? "menuitemcheckbox" : it.radio !== undefined ? "menuitemradio" : "menuitem";
  const checked = it.check !== undefined ? it.check : it.radio;
  const second = it.why || it.sub;
  const el = h("button", {
    class: "pop-item" + (it.submenu ? " has-sub" : "") + (second ? " two" : ""), role, tabindex: "-1", type: "button",
    "aria-checked": checked === undefined ? null : checked ? "true" : "false",
    "aria-disabled": it.disabled ? "true" : null, "aria-haspopup": it.submenu ? "menu" : null, "aria-expanded": it.submenu ? "false" : null,
    title: it.title || null,
    onclick: (ev) => {
      ev.stopPropagation();
      if (it.disabled) return;
      if (it.submenu) { openSub(el, it, level); return; }
      if (!it.keep) closeAll(true);
      it.run?.(ev);
    },
    onpointerenter: () => { if (it.submenu && !it.disabled) openSub(el, it, level); else closeFrom(level + 1); },
  },
  checked !== undefined ? h("span", { class: "pop-check" + (it.radio !== undefined ? " radio" : ""), "aria-hidden": "true" }, checked ? "✓" : "") : null,
  it.icon ? h("span", { class: "pop-ic", "aria-hidden": "true" }, it.icon) : null,
  h("span", { class: "pop-txt" },
    h("span", { class: "pop-t" }, it.label, it.chip || null),
    second ? h("span", { class: "pop-s" + (it.why ? " why" : "") }, second) : null),
  it.count !== undefined ? h("span", { class: "pop-n" }, String(it.count)) : null,
  it.hint ? h("span", { class: "pop-n" }, it.hint) : null,
  it.submenu ? h("span", { class: "pop-arrow", "aria-hidden": "true" }, "›") : null);
  if (it.id) el.dataset.id = it.id;
  return el;
}

// openMenu opens a menu of items below anchor (its right edge on the button's with
// align "end"). It returns the menu.
export function openMenu(anchor, items, { label = "", align = "start", width = 0, onclose = null } = {}) {
  closeAll(false);
  const el = h("div", { class: "pop menu-list", role: "menu", "aria-label": label }, items.filter(Boolean).map((it) => item(it, 0)));
  show(el, anchor, { align, width, onclose });
  focusFirst(el);
  return el;
}

// openPopover opens a popover (a dialog that does not block the window) with any content.
export function openPopover(anchor, content, { label = "", align = "end", width = 0, onclose = null, id = "" } = {}) {
  closeAll(false);
  const el = h("div", { class: "pop popover", role: "dialog", "aria-label": label, id: id || null }, content);
  show(el, anchor, { align, width, onclose });
  (el.querySelector("select, button, input, [tabindex='0']"))?.focus();
  return el;
}

// refill replaces an open menu's items (a checkbox changed the counts), keeping focus on
// the same item.
export function refill(el, items) {
  const id = document.activeElement?.closest?.(".pop-item")?.dataset?.id;
  const idx = [...el.querySelectorAll(".pop-item")].indexOf(document.activeElement);
  const level = stack.findIndex((s) => s.el === el);
  const ae = document.activeElement;
  const typing = ae?.tagName === "INPUT" && el.contains(ae);
  fill(el, items.filter(Boolean).map((it) => item(it, Math.max(0, level))));
  if (typing && ae.isConnected) { ae.focus(); return place(stack[level]); }
  const again = (id && el.querySelector(`.pop-item[data-id="${CSS.escape(id)}"]`)) || el.querySelectorAll(".pop-item")[idx];
  again?.focus();
  place(stack[level]);
}

function openSub(btn, it, level) {
  if (stack[level + 1]?.anchor === btn) return;
  closeFrom(level + 1);
  const el = h("div", { class: "pop menu-list", role: "menu", "aria-label": it.label }, it.submenu().filter(Boolean).map((x) => item(x, level + 1)));
  btn.setAttribute("aria-expanded", "true");
  const entry = { el, anchor: btn, side: true, onclose: () => btn.setAttribute("aria-expanded", "false"), build: it.submenu };
  stack.push(entry);
  document.body.append(el);
  place(entry);
  el.addEventListener("keydown", (ev) => keys(ev, el));
  return el;
}

// update changes an open menu's items in place (checks, counts, labels), by their ids, so
// focus and any open submenu stay where they are; then every open submenu from its
// builder.
export function update(el, items) {
  patch(el, items);
  refreshSubs();
}

function patch(el, items) {
  for (const it of items.filter(Boolean)) {
    const b = it.id && el.querySelector(`.pop-item[data-id="${CSS.escape(it.id)}"]`);
    if (!b) continue;
    const checked = it.check !== undefined ? it.check : it.radio;
    if (checked !== undefined) {
      b.setAttribute("aria-checked", checked ? "true" : "false");
      const c = b.querySelector(".pop-check");
      if (c) c.textContent = checked ? "✓" : "";
    }
    const n = b.querySelector(".pop-n");
    if (n && it.count !== undefined) n.textContent = String(it.count);
    fill(b.querySelector(".pop-t"), it.label, it.chip || null);
  }
}

// refreshSubs brings every open submenu up to date (one with a search field is built
// again, keeping the field).
export function refreshSubs() {
  for (const s of stack.slice(1)) {
    if (!s.build) continue;
    const fresh = s.build();
    if (fresh.some((x) => x?.node)) refill(s.el, fresh); else patch(s.el, fresh);
  }
}

function show(el, anchor, opts) {
  const entry = { el, anchor, align: opts.align, width: opts.width, onclose: opts.onclose };
  stack.push(entry);
  anchor?.setAttribute?.("aria-expanded", "true");
  document.body.append(el);
  place(entry);
  el.addEventListener("keydown", (ev) => keys(ev, el));
}

// place hangs a popup below its button (or beside it, for a submenu), inside the window.
function place(entry) {
  if (!entry) return;
  const { el, anchor } = entry;
  const r = anchor.getBoundingClientRect();
  const vw = window.innerWidth, vh = window.innerHeight;
  if (entry.width) el.style.width = Math.min(entry.width, vw - 16) + "px";
  el.style.maxHeight = "";
  const w = el.offsetWidth, ht = el.scrollHeight;
  let left, top;
  if (entry.side) {
    left = r.right + 2;
    if (left + w > vw - 8) left = Math.max(8, r.left - w - 2);
    top = Math.max(8, Math.min(r.top - 6, vh - ht - 8));
  } else {
    left = entry.align === "end" ? r.right - w : r.left;
    left = Math.max(8, Math.min(left, vw - w - 8));
    const below = vh - r.bottom - 12, above = r.top - 12;
    const up = below < Math.min(ht, 260) && above > below;
    top = up ? Math.max(8, r.top - 4 - Math.min(ht, above)) : r.bottom + 4;
    el.style.maxHeight = (up ? above : below) + "px";
  }
  el.style.left = Math.round(left) + "px";
  el.style.top = Math.round(top) + "px";
}

function focusFirst(el) {
  const items = [...el.querySelectorAll(".pop-item")];
  (items.find((b) => b.getAttribute("aria-disabled") !== "true") || items[0])?.focus();
}

// keys: ↑↓ Home End move, → opens a submenu (← closes it), Enter and Space choose,
// Escape closes, Tab leaves. A popover's own controls keep their keys.
function keys(ev, el) {
  const isMenu = el.getAttribute("role") === "menu";
  const level = stack.findIndex((s) => s.el === el);
  if (ev.key === "Escape") { ev.preventDefault(); ev.stopPropagation(); if (level > 0) { const a = stack[level].anchor; closeFrom(level); a.focus(); } else closeAll(true); return; }
  if (ev.key === "Tab") { closeAll(false); return; }
  if (!isMenu) return;
  const items = [...el.querySelectorAll(".pop-item")];
  if (ev.target.tagName === "INPUT") {
    // A menu's search field: the arrows move into the items, other keys type.
    if (ev.key === "ArrowDown") { ev.preventDefault(); focusFirst(el); }
    return;
  }
  const i = items.indexOf(document.activeElement);
  const go = (j) => { ev.preventDefault(); items[(j + items.length) % items.length]?.focus(); };
  switch (ev.key) {
    case "ArrowDown": go(i + 1); break;
    case "ArrowUp": go(i - 1); break;
    case "Home": go(0); break;
    case "End": go(items.length - 1); break;
    case "ArrowRight": if (document.activeElement?.classList.contains("has-sub")) { ev.preventDefault(); document.activeElement.click(); focusFirst(stack[level + 1]?.el || el); } break;
    case "ArrowLeft": if (level > 0) { ev.preventDefault(); const a = stack[level].anchor; closeFrom(level); a.focus(); } break;
    case " ": ev.preventDefault(); document.activeElement?.click(); if (document.activeElement?.classList.contains("has-sub")) focusFirst(stack[level + 1]?.el); break;
    default:
      if (ev.key.length === 1 && /\S/.test(ev.key)) {
        const k = ev.key.toLowerCase();
        const order = items.slice(i + 1).concat(items.slice(0, i + 1));
        order.find((b) => b.textContent.trim().toLowerCase().startsWith(k))?.focus();
      }
  }
}

function closeFrom(level) {
  while (stack.length > level) {
    const s = stack.pop();
    s.el.remove();
    s.onclose?.();
  }
}

// closeAll closes every open popup (focus back to the first one's button).
export function closeAll(focus) {
  if (!stack.length) return;
  const first = stack[0];
  closeFrom(0);
  first.anchor?.setAttribute?.("aria-expanded", "false");
  if (focus && first.anchor?.isConnected) first.anchor.focus();
}

export const isOpen = () => stack.length > 0;
export const openEl = () => stack[0]?.el || null;

document.addEventListener("pointerdown", (ev) => {
  if (stack.length && !stack.some((s) => s.el.contains(ev.target) || s.anchor?.contains?.(ev.target))) closeAll(false);
}, true);
window.addEventListener("resize", () => closeAll(false));
// Escape closes a popover even when what had focus in it was drawn again.
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && stack.length && !document.querySelector("dialog[open]")) { ev.preventDefault(); closeAll(true); }
});
document.addEventListener("scroll", (ev) => { if (stack.length && !stack.some((s) => s.el.contains(ev.target))) for (const s of stack) place(s); }, true);
