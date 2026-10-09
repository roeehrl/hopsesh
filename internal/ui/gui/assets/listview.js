// The session list's display: Group by (one property, no sub-groups), Sort by within the
// groups, comfortable or compact rows, groups that collapse (each choice saved per
// grouping), the facet filters (OR within a facet, AND across; is / is not) shown as
// chips, and the text filter. It is saved in the config ([list], SaveList); the text
// filter is not. The list is one tree: group headers, then their sessions, with the
// keyboard of the WAI-ARIA tree pattern.
import { accountGroupLabel, api, h, fill, icon, ICONS, state, sys, keys, here, cloudOf, cloudTitle, agentInfo, machineStatus, count, ago, toast, dialog, fail } from "./core.js";
import { openMenu, openPopover, update, closeAll, isOpen, openEl, refill } from "./menu.js";
import { tabs as terminalTabs } from "./term.js";
import { statusKey, placesOf, key as entryKey } from "./actions.js";

const emptyFilter = () => ({ account:[],accountNot:false,tag:[],tagNot:false,status: [], statusNot: false, location: [], locationNot: false, agent: [], agentNot: false, repository: [], repositoryNot: false, lastActive: "", has: [], hasNot: false });
export const DEFAULTS = { groupBy: "repository", sortBy: "last-active", sortReverse: false, density: "comfortable", collapseInactive: false };

// list is the display as chosen; text is the text filter (not saved).
export const list = Object.assign({ decided: false, collapsed: [], expanded: [], filter: emptyFilter(), text: "" }, DEFAULTS);

state.list = list;
let loaded = false;
// load takes the saved display (Info), once.
export function load(l) {
  if (!l || loaded) return;
  loaded = true;
  Object.assign(list, {
    decided: !!l.decided, groupBy: l.groupBy || DEFAULTS.groupBy, sortBy: l.sortBy || DEFAULTS.sortBy, sortReverse: !!l.sortReverse,
    density: l.density || DEFAULTS.density, collapseInactive: !!l.collapseInactive, collapsed: [...(l.collapsed || [])], expanded: [...(l.expanded || [])],
    filter: Object.assign(emptyFilter(), l.filter || {}),
  });
}

let saveTimer;
// save stores the display (300 ms after the last change).
export function save() {
  list.decided = true;
  clearTimeout(saveTimer);
  saveTimer = setTimeout(() => {
    const { text, decided, ...rest } = list;
    api("SaveList", rest).catch(() => {});
  }, 300);
}

// changed redraws the list (sessions.js sets it): full = the whole screen.
let redraw = () => {};
export function onChange(fn) { redraw = fn; }

// decide picks the rows once, by how many sessions the first scan finds: 150 or more get
// compact rows with the inactive groups collapsed, and a toast says so.
export function decide(total, openDisplay) {
  if (list.decided || !total || state.scan?.cached || state.scan?.discovering || state.scanning) return;
  if (total >= 150) {
    list.density = "compact";
    list.collapseInactive = true;
    toast(`${total} sessions: compact rows, older repositories collapsed. Display (${keys("mod+J")}) to change.`, { label: "Display", run: openDisplay });
  }
  save();
}

// ---- What a session is, for the facets ----

export const repoKey = (e) => (e.group.noRepo ? "" : e.group.remote || e.group.local || e.group.name);
const DAY = 86400e3;
const midnight = () => { const d = new Date(); d.setHours(0, 0, 0, 0); return d.getTime(); };
const at = (e) => (e.lastActive ? new Date(e.lastActive).getTime() : 0);
const locationOf = (e) => (e.cloud ? "clouds" : e.machine === here() ? "here" : "machines");
function hasOf(e) {
  const out = [];
  if (placesOf(e).some((p) => !["claude-app", "codex-app"].includes(p.kind))) out.push("tab");
  if (e.mirror) out.push("mirror");
  if (e.unpushed || e.dirty) out.push("unpushed");
  return out;
}

export const STATUS = [["needs", "Needs you", "needs"], ["working", "Working", "working"], ["idle", "Idle", "idle"], ["moved", "Moved", "moved"], ["ended", "Ended", "ended"], ["unknown", "State not known", "unknown"]];
const LAST = [["today", "Today"], ["7d", "Previous 7 days"], ["30d", "Previous 30 days"]];
const HAS = () => [["tab", "Open in a terminal tab"], ["mirror", "Mirrored on claude.ai"], ["unpushed", "Unpushed or uncommitted work"]];
const LOCATION = () => [["here", sys.Here], ["machines", "Other machines"], ["clouds", "Clouds"]];

// FACETS are the filters: their words, values (with their names) and each session's.
const FACETS = {
 account:{name:"Account",values:()=>[...new Map(pool.map(e=>[e.profile?.id||"",accountGroupLabel(e)]))],of:e=>[e.profile?.id||""]},
 tag:{name:"Account tag",values:()=>[...new Map(pool.flatMap(e=>e.profile?.tags?.length?e.profile.tags:["Untagged"]).map(t=>[t.toLowerCase(),t]))].sort((a,b)=>a[0].localeCompare(b[0])),of:e=>(e.profile?.tags?.length?e.profile.tags:["Untagged"]).map(t=>t.toLowerCase())},
  status: { name: "Status", values: () => STATUS.map(([v, n]) => [v, n]), of: (e) => [statusKey(e)] },
  location: { name: "Location", values: LOCATION, of: (e) => [locationOf(e)] },
  agent: { name: "Agent", values: () => agentValues(), of: (e) => [e.agent] },
  repository: { name: "Repository", values: () => repoValues(), of: (e) => [repoKey(e)] },
  lastActive: { name: "Last active", values: () => LAST, single: true },
  has: { name: "Has", values: HAS, of: hasOf },
};
const ORDER = ["status", "location", "agent", "account", "tag", "repository", "lastActive", "has"];

let pool = []; // the sessions in scope, before the facets (for the menus' counts)
function agentValues() {
  const seen = new Map();
  for (const e of pool) if (!seen.has(e.agent)) seen.set(e.agent, e.agentName);
  for (const v of list.filter.agent) if (!seen.has(v)) seen.set(v, agentInfo(v)?.name || v);
  return [...seen].sort((a, b) => a[1].localeCompare(b[1]));
}
function repoValues() {
  const seen = new Map();
  for (const e of [...pool].sort((a, b) => at(b) - at(a))) if (!seen.has(repoKey(e))) seen.set(repoKey(e), e.group.noRepo ? "Outside a git checkout" : e.group.name.replace(/ \(no remote\)$/, ""));
  for (const v of list.filter.repository) if (!seen.has(v)) seen.set(v, v.split("/").pop() || v);
  return [...seen];
}

// fixed are the facets a scope already decides (not offered there).
function fixed(scope) {
  if (scope.kind === "needs") return ["status"];
  if (scope.kind === "here" || scope.kind === "machine" || scope.kind === "cloud") return ["location"];
  return [];
}
export const facetsFor = (scope) => ORDER.filter((f) => !fixed(scope).includes(f));

function selected(f) {
  const v = list.filter[f];
  return Array.isArray(v) ? v : v ? [v] : [];
}
const notOf = (f) => !!list.filter[f + "Not"];

function matches(f, e) {
  const sel = selected(f);
  if (!sel.length) return true;
  if (f === "lastActive") {
    const t = at(e);
    return sel[0] === "today" ? t >= midnight() : sel[0] === "7d" ? t >= Date.now() - 7 * DAY : t >= Date.now() - 30 * DAY;
  }
  const hit = FACETS[f].of(e).some((v) => sel.includes(v));
  return notOf(f) ? !hit : hit;
}

function textMatch(e) {
  const ws = list.text.toLowerCase().split(/\s+/).filter(Boolean);
  if (!ws.length) return true;
  const hay = [e.title,e.profile?.name,e.profile?.account?.email,...(e.profile?.tags||[]), e.group.name, e.group.remote, e.branch, e.cloud?.branch, e.cloud ? cloudTitle(e.machine) : e.machine === here() ? sys.here : e.machine, e.lastPrompt].join(" ").toLowerCase();
  return ws.every((w) => hay.includes(w));
}

export const activeFacets = (scope) => facetsFor(scope).filter((f) => selected(f).length);

// apply narrows the sessions in scope by the facets and the text: [shown, in scope].
export function apply(inScope, scope) {
  pool = inScope;
  const fs = facetsFor(scope);
  return inScope.filter((e) => fs.every((f) => matches(f, e)) && textMatch(e));
}

// countFor is how many sessions in scope a facet's value would show, the other filters
// still applied.
function countFor(f, v, scope) {
  const others = facetsFor(scope).filter((g) => g !== f);
  return pool.filter((e) => others.every((g) => matches(g, e)) && textMatch(e) && (f === "lastActive" ? withLast(v, e) : FACETS[f].of(e).includes(v))).length;
}
function withLast(v, e) {
  const t = at(e);
  return v === "today" ? t >= midnight() : v === "7d" ? t >= Date.now() - 7 * DAY : t >= Date.now() - 30 * DAY;
}

// ---- Groups ----

const STATUS_ORDER = STATUS.map(([v]) => v);
const BUCKETS = [["today", "Today"], ["yesterday", "Yesterday"], ["7d", "Previous 7 days"], ["30d", "Previous 30 days"], ["older", "Older"]];
function bucket(e) {
  const t = at(e), m = midnight();
  if (t >= m) return "today";
  if (t >= m - DAY) return "yesterday";
  if (t >= m - 7 * DAY) return "7d";
  if (t >= m - 30 * DAY) return "30d";
  return "older";
}

function sortRows(rows) {
  const by = {
    "last-active": (a, b) => at(b) - at(a),
    title: (a, b) => a.title.localeCompare(b.title, undefined, { sensitivity: "base" }),
    status: (a, b) => STATUS_ORDER.indexOf(statusKey(a)) - STATUS_ORDER.indexOf(statusKey(b)) || at(b) - at(a),
    size: (a, b) => (b.sizeKB || 0) - (a.sizeKB || 0),
  }[list.sortBy] || ((a, b) => at(b) - at(a));
  const s = [...rows].sort(by);
  return list.sortReverse ? s.reverse() : s;
}

// groupsOf puts sessions in groups for the chosen property, in an order that means
// something (by activity, or A–Z when sorting by title; fixed for status and age).
export function groupsOf(rows) {
  const g = list.groupBy;
  if (g === "none") return [{ key: "none", name: "", rows: sortRows(rows), flat: true }];
  const map = new Map();
  const add = (k, make, e) => { if (!map.has(k)) map.set(k, Object.assign({ key: k, rows: [] }, make())); map.get(k).rows.push(e); };
  for (const e of rows) {
    if (g === "family") {
 const r=e.relationship; add("family:"+(r?.family || entryKey(e)),()=>({name:r?.name||e.title, family:true, branches:r?.branches||1}),e);
 } else if (g === "repository") {
      const rk = repoKey(e);
      add("repository:" + rk, () => ({ name: e.group.noRepo ? "Outside a git checkout" : e.group.name, repo: e.group, last: !rk }), e);
    } else if (g === "location") {
      const loc = e.cloud ? "cloud:" + e.machine : e.machine;
      add("location:" + loc, () => ({ name: e.cloud ? cloudTitle(e.machine) : e.machine === here() ? sys.Here : e.machine, machine: e.cloud ? null : e.machine, cloud: e.cloud ? e.machine : null }), e);
    } else if(g==="account") add("account:"+(e.profile?.id||""),()=>({name:accountGroupLabel(e)}),e);
    else if(g==="tag") for(const t of e.profile?.tags?.length?e.profile.tags:["Untagged"]) add("tag:"+t.toLowerCase(),()=>({name:t}),e);
    else if (g === "agent") add("agent:" + e.agent, () => ({ name: e.agentName, agent: e.agent }), e);
    else if (g === "status") { const k = statusKey(e); add("status:" + k, () => ({ name: STATUS.find(([v]) => v === k)[1], status: k }), e); }
    else if (g === "last-active") { const b = bucket(e); add("last-active:" + b, () => ({ name: BUCKETS.find(([v]) => v === b)[1], bucket: b }), e); }
  }
  const out = [...map.values()];
  for (const x of out) { x.rows = sortRows(x.rows); if(g === "family") x.rows.sort((a,b)=>(a.relationship?.depth||0)-(b.relationship?.depth||0)); x.newest = Math.max(...x.rows.map(at)); }
  const byActivity = (a, b) => (a.last ? 1 : 0) - (b.last ? 1 : 0) || b.newest - a.newest;
  const az = (a, b) => (a.last ? 1 : 0) - (b.last ? 1 : 0) || a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
  if (g === "family" || g === "repository" || g === "agent" || g === "account" || g === "tag") out.sort(list.sortBy === "title" ? az : byActivity);
  else if (g === "status") out.sort((a, b) => STATUS_ORDER.indexOf(a.status) - STATUS_ORDER.indexOf(b.status));
  else if (g === "last-active") out.sort((a, b) => BUCKETS.findIndex(([v]) => v === a.bucket) - BUCKETS.findIndex(([v]) => v === b.bucket));
  else if (g === "location") {
    const rank = (x) => (x.machine === here() ? 0 : x.machine ? 1 : 2);
    out.sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  }
  return out;
}

// ---- Collapsing ----

const INACTIVE = 14 * DAY;
export function collapsed(g) {
  if (g.flat) return false;
  if (list.collapsed.includes(g.key)) return true;
  if (list.expanded.includes(g.key)) return false;
  return list.collapseInactive && g.newest < Date.now() - INACTIVE;
}
const remember = (arr, k) => { const i = arr.indexOf(k); if (i >= 0) arr.splice(i, 1); arr.push(k); while (arr.length > 300) arr.shift(); };
const forget = (arr, k) => { const i = arr.indexOf(k); if (i >= 0) arr.splice(i, 1); };

// setCollapsed records the user's choice for a group.
export function setCollapsed(g, closed) {
  if (closed) { forget(list.expanded, g.key); remember(list.collapsed, g.key); } else {
    forget(list.collapsed, g.key);
    if (list.collapseInactive && g.newest < Date.now() - INACTIVE) remember(list.expanded, g.key);
  }
  save();
}

// open opens the group a session is in (a session picked from the palette, a notification
// or Reveal), whatever the user chose before: the only time the app opens a group itself.
export function openGroupOf(e, rows) {
  const g = groupsOf(rows).find((x) => x.rows.some((r) => entryKey(r) === entryKey(e)));
  if (g && collapsed(g)) setCollapsed(g, false);
}

let lastGroups = [];
// setAll collapses or expands every group shown.
export function setAll(closed) {
  const gk = document.activeElement?.closest?.(".grp")?.dataset.gkey;
  for (const g of lastGroups) if (!g.flat) setCollapsed(g, closed);
  redraw();
  if (gk) focusGroup(gk);
}

// ---- The toolbar: title, count, text filter, Filter, Display, and the chips ----

let scopeNow = { kind: "all" };
let inputEl = null;
// toolbar is the list's toolbar; the text field is the same element across redraws, so
// typing in it keeps its place.
export function toolbar(title, scope) {
  scopeNow = scope;
  if (!inputEl) {
    inputEl = h("input", { type: "text", id: "list-filter", placeholder: "Filter sessions", autocomplete: "off", spellcheck: "false", "aria-label": "Filter sessions",
      oninput: () => { list.text = inputEl.value; redraw(false); },
      onkeydown: (ev) => {
        if (ev.key === "Escape") {
          ev.preventDefault();
          if (inputEl.value) { inputEl.value = ""; list.text = ""; redraw(false); } else focusTree();
        } else if (ev.key === "ArrowDown") { ev.preventDefault(); focusTree(); }
      } });
  }
  inputEl.value = list.text;
  return h("div", { class: "toolbar", id: "list-toolbar" },
    h("div", { class: "tb-row" },
      h("h1", {}, title),
      h("span", { class: "muted tb-count", id: "list-count", "aria-live": "polite" }),
      h("span", { class: "spacer" }),
      h("label", { class: "tb-search", for: "list-filter" }, icon(["M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z", "m20 20-3.5-3.5"], 13), inputEl, h("span", { class: "kbd" }, keys("mod+F"))),
      h("button", { class: "btn tb-btn", id: "btn-filter", "aria-label": "Filter", "aria-haspopup": "menu", "aria-expanded": "false", "aria-keyshortcuts": sys.mac ? "Shift+Meta+F" : "Control+Shift+F",
        title: `Filter (${sys.mac ? "⇧⌘F" : "Ctrl+Shift+F"})`, onclick: (ev) => toggleFilter(ev.currentTarget) },
        icon("M4 5h16l-6 7.5V19l-4 1.5v-8z", 13), h("span", { class: "tb-l" }, "Filter"), h("span", { class: "chip tb-n", id: "filter-n", hidden: true })),
      h("button", { class: "btn tb-btn", id: "btn-display", "aria-label": "Display", "aria-haspopup": "dialog", "aria-expanded": "false", "aria-controls": "display-pop",
        title: `Display options (${keys("mod+J")})`, onclick: (ev) => toggleDisplay(ev.currentTarget) },
        icon(["M4 7h10M18 7h2M4 17h4M12 17h8", "M16 5a2 2 0 1 0 0 4 2 2 0 0 0 0-4zM10 15a2 2 0 1 0 0 4 2 2 0 0 0 0-4z"], 13), h("span", { class: "tb-l" }, "Display"), icon(ICONS.chevron, 10))),
    h("div", { class: "chips-row", id: "list-chips", hidden: true }));
}

// counts sets the toolbar's count, the Filter button's number and the chips.
export function counts(shown, total, scope) {
  const c = document.querySelector("#list-count");
  if (c) c.textContent = shown === total ? count(total, "session") : `${shown} of ${total}`;
  const act = activeFacets(scope);
  const n = document.querySelector("#filter-n");
  if (n) { n.hidden = !act.length; n.textContent = String(act.length); }
  const row = document.querySelector("#list-chips");
  if (!row) return;
  row.hidden = !act.length;
  fill(row, act.map((f) => chip(f)), act.length ? h("button", { class: "link chips-clear", onclick: clearFilters }, "Clear") : null);
  document.documentElement.style.setProperty("--tb-h", (document.querySelector("#list-toolbar")?.offsetHeight || 50) + "px");
}

function valueName(f, v) {
  return (FACETS[f].values().find(([x]) => x === v) || [v, v])[1];
}

function chip(f) {
  const vals = selected(f).map((v) => valueName(f, v));
  const not = notOf(f);
  const words = `${FACETS[f].name} ${not ? "is not" : "is"} ${vals.join(" or ")}`;
  return h("span", { class: "fchip" + (not ? " not" : "") },
    FACETS[f].single ? h("span", { class: "fchip-f" }, FACETS[f].name) : h("button", { class: "fchip-f", title: not ? "Is not: click for is" : "Is: click for is not", "aria-label": `${FACETS[f].name}: ${not ? "is not" : "is"}, switch`,
      onclick: () => { list.filter[f + "Not"] = !not; save(); redraw(false); } }, FACETS[f].name, not ? " is not" : ""),
    h("button", { class: "fchip-v", "aria-label": `${words}, edit`, onclick: (ev) => {
      const menu = openMenu(ev.currentTarget, facetItems(f), { label: FACETS[f].name });
      menu.dataset.facet = f;
    } }, vals.join(", ")),
    h("button", { class: "fchip-x", "aria-label": `Remove ${FACETS[f].name} filter`, onclick: () => { clearFacet(f); save(); redraw(false); } }, "✕"));
}

function clearFacet(f) {
  if (f === "lastActive") list.filter.lastActive = "";
  else { list.filter[f] = []; list.filter[f + "Not"] = false; }
}

export function clearFilters() {
  for (const f of ORDER) clearFacet(f);
  save();
  redraw(false);
  if (isOpen() && openEl()?.getAttribute("aria-label") === "Filter") update(openEl(), filterItems());
}

// ---- The Filter menu ----

function toggleValue(f, v) {
  if (FACETS[f].single) list.filter[f] = list.filter[f] === v ? "" : v;
  else {
    const arr = list.filter[f];
    const i = arr.indexOf(v);
    if (i >= 0) arr.splice(i, 1); else arr.push(v);
  }
  save();
  redraw(false);
  const el = openEl();
  if (el?.getAttribute("aria-label") === "Filter") update(el, filterItems());
  else if (el) update(el, facetItems(el.dataset.facet));
}

const DOT = { needs: "needs", working: "working", idle: "idle", moved: "moved", ended: "ended", unknown: "unknown" };

// facetItems are one facet's values, checked when chosen, with their counts.
function facetItems(f) {
  const vals = FACETS[f].values();
  const items = [];
  if (f === "repository") items.push({ node: repoSearch() });
  const q = f === "repository" ? repoQuery.toLowerCase() : "";
  for (const [v, name] of vals) {
    if (q && !name.toLowerCase().includes(q) && !v.toLowerCase().includes(q)) continue;
    const on = selected(f).includes(v);
    const it = { id: f + ":" + v, label: name, count: countFor(f, v, scopeNow), keep: true, run: () => toggleValue(f, v) };
    if (FACETS[f].single) it.radio = on; else it.check = on;
    if (f === "status") it.chip = null, it.icon = h("span", { class: "dot " + DOT[v] });
    items.push(it);
  }
  if (items.length === (f === "repository" ? 1 : 0)) items.push({ heading: "Nothing matches" });
  return items;
}

let repoQuery = "", repoInput = null;
function repoSearch() {
  if (!repoInput) repoInput = h("input", { class: "pop-search", type: "text", placeholder: "Find a repository", "aria-label": "Find a repository", autocomplete: "off", spellcheck: "false",
    oninput: () => { repoQuery = repoInput.value; const el = repoInput.closest(".pop"); if (el) refill(el, facetItems("repository")); } });
  return repoInput;
}

// filterItems is the Filter menu: Status first (the facet people use most), then the
// other facets as submenus, then Clear filters.
function filterItems() {
  const fs = facetsFor(scopeNow);
  const items = [];
  if (fs.includes("status")) {
    const not = notOf("status");
    items.push({ heading: "Status", headExtra: h("button", { class: "btn is-not", type: "button", "aria-label": `Status ${not ? "is not" : "is"}, switch to ${not ? "is" : "is not"}`,
      onclick: (ev) => { ev.stopPropagation(); list.filter.statusNot = !list.filter.statusNot; save(); redraw(false); fill(ev.currentTarget, list.filter.statusNot ? "is not" : "is", icon(ICONS.chevron, 9)); } },
      not ? "is not" : "is", icon(ICONS.chevron, 9)) });
    for (const it of facetItems("status")) if (it.label !== "State not known" || it.count || it.check) items.push(it);
    items.push({ sep: true });
  }
  for (const f of fs.filter((x) => x !== "status")) {
    const sel = selected(f);
    items.push({ id: "facet:" + f, label: FACETS[f].name, chip: sel.length ? h("span", { class: "chip pop-chip" }, sel.length === 1 ? valueName(f, sel[0]) : `${sel.length}`) : null,
      submenu: () => facetItems(f) });
  }
  items.push({ sep: true });
  items.push({ node: h("div", { class: "pop-foot" }, h("button", { class: "link", type: "button", onclick: () => clearFilters() }, "Clear filters"), h("span", { class: "spacer" }),
    h("span", { class: "kbd" }, sys.mac ? "⇧⌘F" : "Ctrl+Shift+F")) });
  return items;
}

export function toggleFilter(anchor = document.querySelector("#btn-filter")) {
  if (!anchor) return;
  if (isOpen() && openEl()?.getAttribute("aria-label") === "Filter") { closeAll(true); return; }
  openMenu(anchor, filterItems(), { label: "Filter", width: 270 });
}

// ---- The Display popover ----

const SORT_DIR = { "last-active": ["Newest first", "Oldest first"], title: ["A–Z", "Z–A"], status: ["Needs you first", "Ended first"], size: ["Largest first", "Smallest first"] };

function setDisplay(patch) {
  Object.assign(list, patch);
  save();
  redraw(false);
  const pop = document.querySelector("#display-pop");
  if (!pop) return;
  // Drawn again: the focus goes back to the same control.
  const ae = document.activeElement, id = ae?.id, words = ae?.textContent;
  fill(pop, displayContent());
  const again = (id && pop.querySelector("#" + CSS.escape(id))) || [...pop.querySelectorAll("button")].find((b) => b.textContent === words) || pop.querySelector("select");
  again?.focus();
}

function displayContent() {
  const sel = (id, label, value, opts, onchange) => [h("label", { class: "dp-l", for: id }, label),
    h("select", { id, onchange: (ev) => onchange(ev.target.value) }, opts.map(([v, n]) => h("option", { value: v, selected: v === value }, n)))];
  const [fwd, back] = SORT_DIR[list.sortBy] || SORT_DIR["last-active"];
  return [
    h("div", { class: "dp-head" }, h("span", { class: "dp-title" }, "Display"), h("span", { class: "spacer" }), h("span", { class: "kbd" }, keys("mod+J"))),
    h("div", { class: "dp-grid" },
      sel("dp-group", "Group by", list.groupBy, [["family", "Conversation family"], ["repository", "Repository"], ["location", "Location"], ["agent", "Agent"], ["account", "Account"], ["tag", "Account tag"], ["status", "Status"], ["last-active", "Last active"], ["none", "None"]], (v) => setDisplay({ groupBy: v })),
      h("label", { class: "dp-l", for: "dp-sort" }, "Sort by"),
      h("div", { class: "dp-sort" },
        h("select", { id: "dp-sort", onchange: (ev) => setDisplay({ sortBy: ev.target.value, sortReverse: false }) },
          [["last-active", "Last active"], ["title", "Title"], ["status", "Status"], ["size", "Size"]].map(([v, n]) => h("option", { value: v, selected: v === list.sortBy }, n))),
        h("button", { class: "btn dp-dir", type: "button", "aria-label": `${list.sortReverse ? back : fwd}, switch to ${list.sortReverse ? fwd : back}`, onclick: () => setDisplay({ sortReverse: !list.sortReverse }) },
          list.sortReverse ? back : fwd, icon(["M8 4v16M4 8l4-4 4 4", "M16 20V4M12 16l4 4 4-4"], 12))),
      h("span", { class: "dp-l", id: "dp-rows" }, "Rows"),
      h("div", { class: "seg", role: "radiogroup", "aria-labelledby": "dp-rows", style: "justify-self:start" },
        [["comfortable", "Comfortable"], ["compact", "Compact"]].map(([v, n]) => h("button", { type: "button", role: "radio", "aria-checked": list.density === v ? "true" : "false",
          onclick: () => setDisplay({ density: v }), onkeydown: (ev) => { if (ev.key === "ArrowLeft" || ev.key === "ArrowRight") { ev.preventDefault(); setDisplay({ density: list.density === "compact" ? "comfortable" : "compact" }); document.querySelector('#display-pop .seg [aria-checked="true"]')?.focus(); } } }, n)))),
    h("label", { class: "opt dp-opt" }, h("input", { type: "checkbox", checked: list.collapseInactive, onchange: (ev) => setDisplay({ collapseInactive: ev.target.checked }) }),
      h("span", {}, h("b", {}, "Collapse inactive groups"), h("span", { class: "muted" }, "Groups with nothing new in 14 days start closed"))),
    h("div", { class: "dp-foot" },
      h("button", { class: "link", type: "button", onclick: () => setAll(true) }, "Collapse all"), h("span", { class: "muted" }, "·"),
      h("button", { class: "link", type: "button", onclick: () => setAll(false) }, "Expand all"),
      h("span", { class: "spacer" }),
      h("button", { class: "link", type: "button", onclick: () => setDisplay(Object.assign({}, DEFAULTS, { collapsed: [], expanded: [] })) }, "Reset to defaults")),
  ];
}

export function toggleDisplay(anchor = document.querySelector("#btn-display")) {
  if (!anchor) return;
  if (document.querySelector("#display-pop")) { closeAll(true); return; }
  openPopover(anchor, displayContent(), { label: "Display options", id: "display-pop", width: 340 });
}

// ---- The tree ----

let treeEl = null;
const focusTree = () => {
  const t = treeEl;
  if (!t) return;
  (t.querySelector('[role="treeitem"][aria-selected="true"]') || t.querySelector('[role="treeitem"][tabindex="0"]') || t.querySelector('[role="treeitem"]'))?.focus();
};
export { focusTree };

// tree draws the groups: a header (chevron, name, count, what needs you and what works)
// and, open, its sessions (row draws one). Collapsed groups have no rows built.
let chunkObserver=null, chunksByKey=new Map();
function chunkedRows(body,rows,makeRow){
 if(rows.length<250){for(const e of rows)body.append(makeRow(e));return}
 for(let start=0;start<rows.length;start+=50){
  const entries=rows.slice(start,start+50),height=entries.length*(list.density==="compact"?36:72);
  const chunk=h("div",{class:"session-chunk",role:"none",style:`min-height:${height}px`});
  const mount=()=>{if(chunk.childElementCount)return;chunk.style.minHeight="";chunk.append(...entries.map(makeRow));};
  chunk.__mount=mount;
  for(const e of entries)chunksByKey.set(entryKey(e),mount);
  body.append(chunk);if(start===0)mount();chunkObserver.observe(chunk);
 }
}
export function tree(groups, row, selKey) {
 chunkObserver?.disconnect();chunksByKey=new Map();
 chunkObserver=new IntersectionObserver(changes=>{
  for(const c of changes){const chunk=c.target;if(c.isIntersecting)chunk.__mount();else if(chunk.childElementCount&&!chunk.contains(document.activeElement)&&!chunk.querySelector('[aria-selected="true"]')){chunk.style.minHeight=chunk.getBoundingClientRect().height+"px";chunk.replaceChildren()}}
 },{rootMargin:"500px"});
  lastGroups = groups;
  const flat = groups.length === 1 && groups[0].flat;
  const t = h("div", { class: "tree" + (list.density === "compact" ? " compact" : ""), role: "tree", "aria-label": "Sessions", onkeydown: treeKeys });
  let first = true;
  for (const g of groups) {
    if (flat) {
      // Group by None: one level, the sessions in one card.
      const card = h("div", { class: "grp flat", role: "none" });
      chunkedRows(card,g.rows,e=>row(e,1));
      const firstRow = card.querySelector(".row");
      if (firstRow) firstRow.tabIndex = 0;
      t.append(card);
      first = false;
      continue;
    }
    const closed = collapsed(g);
    const needs = g.rows.filter((e) => statusKey(e) === "needs").length;
    const working = g.rows.filter((e) => statusKey(e) === "working").length;
    const label = [g.name, count(g.rows.length, "session"), needs ? `${needs} need${needs === 1 ? "s" : ""} you` : "", working ? `${working} working` : ""].filter(Boolean).join(", ");
    const head = h("div", { class: "gh" + (g.cloud ? " cloud" : ""), onclick: (ev) => toggleGroup(g, ev) },
      h("span", { class: "chev" + (closed ? " closed" : ""), "aria-hidden": "true" }, icon(ICONS.chevron, 12)),
      groupMark(g),
      h("span", { class: "gname" }, g.name),
      h("span", { class: "gcount" }, g.family ? `${g.rows.length} of ${g.branches} branches` : String(g.rows.length)),
      needs ? h("span", { class: "gsum needs" }, h("span", { class: "dot needs" }), `${needs} need${needs === 1 ? "s" : ""} you`) : null,
      working ? h("span", { class: "gsum ok" }, h("span", { class: "dot working" }), `${working} working`) : null,
      h("span", { class: "spacer" }),
      g.family ? h("button", {class:"btn icon", "aria-label":"Rename conversation family", title:"Rename conversation family", onclick:ev=>{ev.stopPropagation();renameFamily(g);}}, "✎") : null,
      g.family ? h("span",{class:"gmeta"}, (()=>{const n=[...terminalTabs.values()].filter(t=>"family:"+t.relationship?.family===g.key).length;return n?`${n} terminal${n===1?"":"s"}`:"";})()) : null,
      groupRight(g, closed));
    const gi = h("div", { class: "grp" + (closed ? " closed" : ""), role: "treeitem", "aria-level": "1", "aria-expanded": closed ? "false" : "true", "aria-label": label,
      tabindex: "-1", "data-gkey": g.key }, head);
    if (!closed) {
      const body = h("div", { class: "gbody", role: "group", style: `contain-intrinsic-size: auto ${g.rows.length * (list.density === "compact" ? 32 : 60)}px` });
      chunkedRows(body,g.rows,e=>{
       const item=row(e,2); if(g.family && e.relationship?.parent){
         item.style.marginLeft=Math.min(e.relationship.depth||1,3)*18+"px";
         item.prepend(h("span",{class:"family-ancestry muted",title:e.relationship.ancestors?.join(" → ")},"↳ Fork · "+(e.relationship.ancestors?.at(-1)||"Parent unavailable")));
       }
       return item;
     });
      gi.append(body);
    }
    if (first) { gi.tabIndex = 0; first = false; }
    t.append(gi);
  }
  // Roving tabindex: the selected row is the tree's tab stop (else the first item).
  const sel = selKey && rowByKey(t, selKey);
  if (sel) { for (const x of t.querySelectorAll('[tabindex="0"]')) x.tabIndex = -1; sel.tabIndex = 0; }
  treeEl = t;
  return t;
}

// rowByKey is the row of a session (its data-key holds machine NUL key, which a CSS
// selector can't name).
export function rowByKey(root, k) {
 chunksByKey.get(k)?.();
  for (const r of root.querySelectorAll(".row")) if (r.dataset.key === k) return r;
  return null;
}

function groupMark(g) {
  if (g.cloud) return icon(ICONS.cloud, 14);
  if (g.machine) {
    const m = state.scan?.machines.find((x) => x.name === g.machine);
    return h("span", { class: "dot " + (m ? machineStatus(m.status)[0] : "ok"), title: m ? machineStatus(m.status)[1] : "" });
  }
  if (g.status) return h("span", { class: "dot " + DOT[g.status] });
  return null;
}

function groupRight(g, closed) {
  const out = [];
  if (closed && g.newest && Date.now() - g.newest > INACTIVE) out.push(h("span", { class: "gmeta" }, `last active ${ago(new Date(g.newest).toISOString())}`));
  if (g.repo && !g.repo.noRepo) {
    if (g.repo.remote) out.push(h("span", { class: "mono gremote", title: g.repo.remote }, g.repo.remote));
    out.push(h("span", { class: "gmeta " + (g.repo.local ? "ok" : g.repo.noRemote ? "" : "warn") }, g.repo.noRemote ? "A checkout without a remote" : g.repo.local ? "Cloned here" : `Not on ${sys.here}`));
  }
  return out;
}

// toggleGroup opens or closes a group; with ⌥ (Alt on Windows) every group.
function toggleGroup(g, ev) {
  const closed = !collapsed(g);
  if (ev?.altKey) { setAll(closed); focusGroup(g.key); return; }
  setCollapsed(g, closed);
  redraw();
  focusGroup(g.key);
}
function focusGroup(k) { document.querySelector(`.grp[data-gkey="${CSS.escape(k)}"]`)?.focus({ preventScroll: true }); }

// The row a key goes to: selecting sessions is sessions.js's (selectRow), running their
// actions too (runRow).
let selectRow = () => {}, runRow = () => {};
export function onRows(select, run) { selectRow = select; runRow = run; }

let typed = "", typedAt = 0;
// treeKeys: ↑↓ Home End move through headers and rows; ← closes a group (or goes to its
// header from a row), → opens it (or goes to its first row); ⌥←/⌥→ (Ctrl on Windows) and *
// do every group; Space and ↩ on a header toggle it; ↩ and ⌘↩ on a row are its actions;
// letters go to a title.
function treeKeys(ev) {
  if (ev.target.closest(".act")) return;
  const logical=lastGroups.flatMap(g=>g.flat?g.rows.map(e=>({key:entryKey(e),title:e.title})): [{group:g.key},...collapsed(g)?[]:g.rows.map(e=>({key:entryKey(e),title:e.title}))]);
  const element=x=>x?.key?rowByKey(treeEl,x.key):x?.group?[...treeEl.querySelectorAll(".grp")].find(g=>g.dataset.gkey===x.group):null;
  const cur = ev.target.closest('[role="treeitem"]');
  const i=logical.findIndex(x=>x.key?x.key===cur?.dataset.key:x.group===cur?.dataset.gkey);
  const isGroup = cur?.classList.contains("grp");
  const g = isGroup ? lastGroups.find((x) => x.key === cur.dataset.gkey) : null;
  const all = sys.mac ? ev.altKey : ev.ctrlKey;
  const to = (el) => {
    if (!el) return;
    ev.preventDefault();
    if (el.classList.contains("grp")) { for (const x of treeEl.querySelectorAll('[tabindex="0"]')) x.tabIndex = -1; el.tabIndex = 0; el.focus(); el.scrollIntoView({ block: "nearest" }); }
    else selectRow(el);
  };
  switch (ev.key) {
    case "ArrowDown": to(element(logical[i+1])); return;
    case "ArrowUp": to(element(logical[Math.max(0,i-1)])); return;
    case "Home": to(element(logical[0])); return;
    case "End": to(element(logical.at(-1))); return;
    case "ArrowLeft":
      if (all && (ev.altKey || ev.ctrlKey)) { ev.preventDefault(); setAll(true); return; }
      if (isGroup && g && !collapsed(g)) { ev.preventDefault(); toggleGroup(g); }
      else if (!isGroup) to(cur?.closest(".grp"));
      return;
    case "ArrowRight":
      if (all && (ev.altKey || ev.ctrlKey)) { ev.preventDefault(); setAll(false); return; }
      if (isGroup && g && collapsed(g)) { ev.preventDefault(); toggleGroup(g); }
      else if (isGroup) to(element(logical[i+1]));
      return;
    case "*": ev.preventDefault(); setAll(false); return;
    case " ": case "Enter":
      if (isGroup && g) { ev.preventDefault(); toggleGroup(g); return; }
      if (ev.key === "Enter" && cur) { ev.preventDefault(); runRow(cur, ev.metaKey || ev.ctrlKey); }
      return;
  }
  if (ev.key.length === 1 && /\S/.test(ev.key) && !ev.metaKey && !ev.ctrlKey && !ev.altKey) {
    typed = Date.now() - typedAt > 700 ? ev.key.toLowerCase() : typed + ev.key.toLowerCase();
    typedAt = Date.now();
    const rows=logical.filter(x=>x.key);
    const start = Math.max(0, rows.findIndex(x=>x.key===cur?.dataset.key) + (typed.length === 1 ? 1 : 0));
    const hit = rows.slice(start).concat(rows.slice(0, start)).find((x) => (x.title || "").toLowerCase().startsWith(typed));
    if (hit) to(element(hit));
  }
}

// The window's shortcuts for the list: ⌘F (Ctrl+F) the text filter, ⇧⌘F the Filter menu,
// ⌘J the Display popover. Windows' own find bar never opens.
let lastDisplay = 0;
export function displayCommand() {
  if (Date.now() - lastDisplay < 250) return; // the menu's accelerator and the key both came
  lastDisplay = Date.now();
  toggleDisplay();
}
document.addEventListener("keydown", (ev) => {
  if (document.body.dataset.screen !== "sessions" || document.querySelector("dialog[open]")) return;
  const mod = sys.mac ? ev.metaKey : ev.ctrlKey;
  if (!mod || ev.altKey) return;
  const k = ev.key.toLowerCase();
  if (k === "f" && ev.shiftKey) { ev.preventDefault(); toggleFilter(); }
  else if (k === "f") { ev.preventDefault(); inputEl?.focus(); inputEl?.select(); }
  else if (k === "j" && !ev.shiftKey) { ev.preventDefault(); displayCommand(); }
});

function renameFamily(g){
 const input=h("input",{class:"field",value:g.name,"aria-label":"Family name",maxlength:200});
 const d=dialog(h("h2",{},"Rename conversation family"),input,h("p",{class:"muted"},"This changes the group label in Hopsesh. Session titles and history stay unchanged."),h("div",{class:"dlg-foot"},h("button",{class:"btn",onclick:()=>d.close()},"Cancel"),h("button",{class:"btn primary",onclick:async()=>{try{await api("RenameFamily",g.key.slice(7),input.value.trim());for(const e of pool)if(e.relationship?.family===g.key.slice(7))e.relationship.name=input.value.trim()||e.title;d.close();redraw();}catch(e){fail(e);}}},"Save name")));
 input.focus();input.select();
}
