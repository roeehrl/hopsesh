// The inspector: the selected session's header (its title, a status line that says where
// it runs, one line of facts), its action row ([Primary ▾] [Move ▾] [⋯]), where it is
// open, the end of its conversation (safe, locally rendered Markdown), and its
// repository, copies and details, which open and close app-wide.
import { role, chip as roleChip } from "./guard.js";
import { accountLabel, accountTitle, api, h, fill, icon, ICONS, state, sys, here, ago, when, bytes, agentBadge, cloudOf, cloudTitle, count, path, rich, fail, toast, dialog, errText, cap, $ } from "./core.js";
import { selectDestination, model, statusLine, placeName, showPlace, placeCount, onInspector, liveOf, appWord } from "./actions.js";
import { openMenu, isOpen, openEl, closeAll } from "./menu.js";
import { sectionOpen, setSection } from "./layout.js";
import { markdown } from "./markdown.js";
import { movementNotice, resolveDestination, returnCard } from "./returns.js";
import { tabs } from "./term.js";

const AGENT_SHORT = { claude: "Claude", codex: "Codex" };
const PLACE_ICON = {
  hopsesh: ["M3 4h18v16H3z", "m7 10 3 2.5L7 15M12 15h5"], iterm2: ["M3 4h18v16H3z", "M3 9h18"], terminal: ["M3 4h18v16H3z", "M3 9h18"], wt: ["M3 4h18v16H3z", "M3 9h18"],
  ide: ["M8 6 3 12l5 6M16 6l5 6-5 6"], tmux: ["M3 4h18v16H3z", "M12 4v16"], ssh: ["M4 12h16M14 6l6 6-6 6"], unknown: ["M3 4h18v16H3z"],
};
export const placeIcon = (kind, size = 12) => (kind === "claude-app" || kind === "codex-app" ? null : icon(PLACE_ICON[kind] || PLACE_ICON.unknown, size));

// splitButton is the primary action and, when there are other places, the chevron that
// lists them (picking one runs it, and for Resume makes it the agent's default).
function splitButton(m) {
  const p = m.primary;
  if (!p) return null;
  const main = h("button", { class: "btn primary split-main", id: "act-primary", disabled: !!p.disabled, title: p.why || p.label, "aria-describedby": p.why ? "act-caption" : null, onclick: () => p.run() }, h("span", { class: "btn-t" }, p.label));
  if (!m.chevron.length) return h("span", { class: "split solo" }, main);
  const chev = h("button", { class: "btn primary split-chev", id: "act-chevron", "aria-label": m.chevronLabel, "aria-haspopup": "menu", "aria-expanded": "false", title: m.chevronLabel,
    onclick: (ev) => toggle(ev.currentTarget, m.chevron, m.chevronLabel, "start") }, icon(ICONS.chevron, 10));
  return h("span", { class: "split" }, main, chev);
}

// toggle opens a button's menu, or closes it when it is the one open.
function toggle(btn, items, label, align) {
  if (isOpen() && btn.getAttribute("aria-expanded") === "true") { closeAll(true); return; }
  openMenu(btn, items, { label, align, width: 300 });
}

// moveItems flattens Move ▾'s groups into a menu with headings.
function moveItems(groups) {
  const out = [];
  groups.filter(g=>g.items.length).forEach((g, i) => { if (i) out.push({ sep: true }); out.push({ heading: g.heading }, ...g.items); });
  return out;
}

// actionRow is the inspector's buttons and the caption under them.
export function actionRow(e, m = model(e)) {
  const hasMove = m.move.some((g) => g.items.length);
  return h("div", { class: "act-row" },
    h("div", { class: "act-btns" },
      splitButton(m),
      m.fix ? h("button", { class: "btn", onclick: m.fix.run }, m.fix.label) : null,
      hasMove ? h("button", { class: "btn act-move", id: "act-move", "aria-haspopup": "menu", "aria-expanded": "false", onclick: (ev) => toggle(ev.currentTarget, moveItems(m.move), "Move", "end") },
        "Move", icon(ICONS.chevron, 10)) : null,
      h("button", { class: "btn icon act-more", id: "act-more", "aria-label": "More actions", "aria-haspopup": "menu", "aria-expanded": "false", title: "More actions",
        onclick: (ev) => toggle(ev.currentTarget, m.more, "More actions", "end") }, h("span", { "aria-hidden": "true", class: "dots" }, "⋯"))),
    m.caption ? h("span", { class: "act-caption" + (m.twice ? " warn" : ""), id: "act-caption" }, m.twice ? icon(["M12 4 2.5 20h19z", "M12 10v4M12 17v.5"], 13) : null, m.caption) : null);
}

// openChevron opens the primary's menu (⌘↩), or runs the primary when it has none.
export function openChevron(e) {
  const btn = $("#act-chevron");
  if (btn) { btn.click(); return; }
  const m = model(e);
  if (m.primary && !m.primary.disabled) m.primary.run();
}

// section is a part of the inspector that opens and closes (its state is app-wide).
function section(name, title, def, badge, ...kids) {
  const open = sectionOpen(name, def);
  const id = "sec-" + name;
  return h("div", { class: "sec disc" + (open ? " open" : "") },
    h("button", { class: "disc-h", "aria-expanded": open ? "true" : "false", "aria-controls": id, onclick: (ev) => {
      const now = ev.currentTarget.getAttribute("aria-expanded") !== "true";
      setSection(name, now);
      ev.currentTarget.setAttribute("aria-expanded", now ? "true" : "false");
      ev.currentTarget.parentElement.classList.toggle("open", now);
    } }, h("span", { class: "chev", "aria-hidden": "true" }, icon(ICONS.chevron, 10)), h("span", { class: "sec-h" }, title), badge),
    h("div", { class: "disc-body", id }, kids));
}

// header is the title, the status line and the facts.
// lineageLine says what this copy is in its journey, under the title (never in it).
function lineageLine(e) {
  const r = role(e);
  if (!r) return null;
  return h("span", { class: "ins-lineage", "aria-label": `${r.chip}. ${r.line}` }, roleChip(e), " ", h("span", { class: "muted" }, r.line));
}

function header(e) {
  const [k, lead, rest, cl, twice] = statusLine(e);
  const g = e.group;
  const facts = [];
  if(e.profile) facts.push(h("span",{title:accountTitle(e.profile)},`${accountLabel(e.profile)}${e.profile.tags?.length?" · "+e.profile.tags.join(", "):""}`));
  if (e.cloud) {
    if (e.cloud.repo || !g.noRepo) facts.push(h("span", {}, (e.cloud.repo || g.name).split("/").pop()));
    if (e.cloud.branch) facts.push(h("span", { class: "mono" }, e.cloud.branch));
    if (e.cloud.changes) facts.push(h("span", {}, e.cloud.changes));
    if (e.cloud.pr) facts.push(h("span", {}, "PR " + e.cloud.pr));
  } else {
    facts.push(h("span", {}, g.noRepo ? "Outside a git checkout" : g.name.replace(/ \(no remote\)$/, "")));
    if (e.branch) facts.push(h("span", { class: "mono" }, e.branch));
    if (e.unpushed) facts.push(h("span", { class: "warn" }, `+${e.unpushed} unpushed`));
    if (e.dirty) facts.push(h("span", { class: "warn" }, count(e.dirty, "uncommitted file")));
    if (e.machine !== here() && !(e.copies || []).some((c) => c.local)) facts.push(h("span", { class: "muted" }, `not on ${sys.here} yet`));
  }
  const host = e.cloud ? (e.cloud.url.match(/^https:\/\/([^/]+)/) || [])[1] : "";
  const derived = ["prompt", "reply", "none"].includes(e.titleSource);
  return h("div", { class: "ins-head" },
    h("div", { class: "ins-title" }, agentBadge(e.agent, e.agentName, e.cloud ? cloudTitle(e.machine) : ""),
      h("h2", { title: e.title + (e.canRename ? " (double-click to rename)" : ""), ondblclick: e.canRename ? () => renameDialog(e) : null }, e.title)),
    lineageLine(e),
    derived && e.canRename ? h("span", { class: "ins-derived" }, e.titleSource === "none" ? "No title yet · " : `Title from ${e.titleSource === "reply" ? "first reply" : "first prompt"} · `,
      h("button", { class: "link", onclick: () => renameDialog(e) }, "Rename…")) : null,
    h("span", { class: "ins-status" }, h("span", { class: "dot " + (twice ? "needs" : k) }),
      h("span", {}, h("b", { class: twice ? "st-twice" : "st-" + k }, lead), rest ? ` · ${rest}` : "",
        host ? [" · ", h("button", { class: "link", onclick: () => api("OpenURL", e.cloud.url).catch(fail) }, `Status on ${host} ↗`)] : null)),
    facts.length ? h("span", { class: "ins-facts" }, facts.map((f, i) => [i ? h("span", { class: "sep", "aria-hidden": "true" }, " · ") : null, f])) : null);
}

// openIn is where a running session is open, one line per place, each with Show.
function openIn(e, m) {
  if (!m.places.length) return null;
  const lines = [];
  for (const p of m.places) {
    if (p.kind === "hopsesh") {
      const order = [...tabs.keys()];
      for (const t of p.tabs) {
        const n = order.indexOf(t.id) + 1;
        lines.push(h("div", { class: "open-line" }, h("span", { class: "open-ic accent" }, placeIcon("hopsesh")),
          h("span", { class: "open-t" }, "hopsesh Terminal ", h("span", { class: "muted" }, `— ${n ? `tab ${n} ` : ""}“${t.title}”`),
            t.attention ? h("span", { class: "needs-word" }, " · ", h("span", { class: "dot needs" }), "waiting for you") : null),
          h("button", { class: "btn small", onclick: () => showPlace(e, { kind: "hopsesh", tabs: [t] }) }, "Show")));
      }
      continue;
    }
    const can = p.kind !== "tmux" && p.kind !== "ssh" && !(p.kind === "ide" && !sys.mac);
    lines.push(h("div", { class: "open-line" }, h("span", { class: "open-ic" }, placeIcon(p.kind) || agentBadge(e.agent, e.agentName)),
      h("span", { class: "open-t" }, placeName(p, e), p.count > 1 ? h("span", { class: "muted" }, ` — ${p.count} processes`) : null,
        p.waiting ? h("span", { class: "needs-word" }, " · ", h("span", { class: "dot needs" }), "waiting for you") : null),
      can ? h("button", { class: "btn small", onclick: () => showPlace(e, p) }, "Show") : null));
  }
  return h("div", { class: "sec" }, h("span", { class: "sec-h" }, "Open in"), lines);
}

// ---- The conversation ----

let previewTimer = 0;
const wantN = new Map(); // how many messages the user asked to see, by session

// conversation is the Recent conversation section, filled once the preview is read (after
// 120 ms on the same selection; a skeleton meanwhile).
function conversation(e) {
  if (e.cloud || !e.canPreview || !state.info?.previews) return null;
  const body = h("div", { class: "conv", "aria-live": "polite", "aria-busy": "true" }, skeleton());
  // The backend validates the file's current size/mtime before reusing a preview.
  // Scan metadata can be a minute old, so it cannot safely key a second cache here.
  clearTimeout(previewTimer);
  previewTimer = setTimeout(async () => {
    let p;
    try { p = await api("Preview", e.machine, e.key, wantN.get(e.machine + e.key) || 4); } catch (err) { p = { items: [], failed: true, note: "Preview not available: " + errText(err) }; }
    if (body.isConnected && state.info?.previews) fillConversation(body, e, p);
  }, 120);
  return section("conversation", "Recent conversation", true, null, body);
}

const skeleton = () => h("div", { class: "skel", role: "status" }, h("span", { class: "visually-hidden" }, "Reading conversation…"), h("span", { style: "width:30%" }), h("span", { style: "width:92%" }), h("span", { style: "width:70%" }));

const who = (e) => AGENT_SHORT[e.agent] || e.agentName;
function stamp(iso) { return iso ? h("span", { class: "msg-t", title: when(iso) }, ago(iso)) : null; }

// A separate expansion button leaves formatted text selectable and links usable.
let messageID = 0;
const previews = new Map();
const measurePreviews = new ResizeObserver((entries) => {
  for (const [node] of previews) if (!node.isConnected) { measurePreviews.unobserve(node); previews.delete(node); }
  for (const { target } of entries) previews.get(target)?.();
});
function messageBody(text, clampLines) {
  const full = clampLines >= 1000;
  const t = h("div", { class: "msg-x" + (full ? " open" : ""), id: "message-" + (++messageID), style: `--clamp:${clampLines}` }, markdown(text));
  if (full) return [t];
  const button = h("button", { class: "link msg-expand", hidden: true, "aria-expanded": "false", "aria-controls": t.id, onclick: () => setOpen(!t.classList.contains("open")) }, "Show more");
  function setOpen(open) {
    t.classList.toggle("open", open);
    button.setAttribute("aria-expanded", String(open));
    button.textContent = open ? "Show less" : "Show more";
  }
  // Keyboard focus on a link also reveals it instead of landing outside the clip.
  t.addEventListener("focusin", () => { if (!button.hidden) setOpen(true); });
  previews.set(t, () => { button.hidden = !t.classList.contains("open") && t.scrollHeight <= t.clientHeight + 1; });
  measurePreviews.observe(t);
  return [t, button];
}
function message(role, text, iso, e, clampLines) {
  return h("div", { class: "msg " + role }, h("span", { class: "msg-l" + (role === "agent" ? " agent-" + e.agent : "") }, role === "user" ? "You" : who(e), stamp(iso)), messageBody(text, clampLines));
}

export function previewItems(e, p, clampUser = 3, clampAgent = 5) {
  const out = [];
  for (const it of p.items || []) {
    if (it.role === "user") out.push(message("user", it.text, it.time, e, clampUser));
    else if (it.role === "agent") out.push(message("agent", it.text, it.time, e, clampAgent));
    else if (it.role === "tools") { if (it.text) out.push(h("span", { class: "msg-tools" }, it.text)); }
    else if (it.role === "compacted") out.push(h("span", { class: "msg-tools" }, "‹conversation compacted›"));
  }
  return out;
}

function fillConversation(body, e, p) {
  body.removeAttribute("aria-busy");
  const kids = [];
  const derived = ["prompt", "reply", "none"].includes(e.titleSource);
  if (derived && p.first && !(p.items || []).some((x) => x.role === "user" && x.text === p.first.text)) {
    const t = messageBody(p.first.text, 2);
    kids.push(h("div", { class: "started" }, h("span", { class: "msg-l" }, icon(["M12 17v5", "M8 3h8l-1 6 3 4H6l3-4z"], 11), "Started with", stamp(p.first.time)), t));
  }
  kids.push(previewItems(e, p));
  if (p.note) kids.push(h("span", { class: "muted", style: "font-size:12px" }, p.note));
  else if (!(p.items || []).length) kids.push(h("span", { class: "muted", style: "font-size:12px" }, "No messages yet."));
  if (p.failed) kids.push(h("button", { class: "link", onclick: () => refresh(e) }, "Try again"));
  kids.push(h("span", { class: "conv-foot" },
    p.more ? h("button", { class: "link", onclick: () => { const k = e.machine + e.key; wantN.set(k, (wantN.get(k) || 4) + 10); refresh(e); } }, "Load earlier") : null,
    h("button", { class: "link", onclick: () => transcript(e) }, "Open transcript")));
  fill(body, kids);
}

// refresh draws the inspector again for the same session.
function refresh(e) {
  const old = $("#inspector");
  if (old && old.dataset.key === e.machine + "\u0000" + e.key) old.replaceWith(inspector(e));
}

// transcript is a read-only sheet with the end of the conversation, longer, unclamped.
export async function transcript(e, n = 40) {
  const sheet = $("#sheet");
  const body = h("div", { class: "sheet-body transcript", "aria-busy": "true" }, skeleton());
  fill(sheet, h("div", { class: "sheet-in" },
    h("header", { class: "sheet-head" }, h("span", { class: "sec-h" }, "Transcript · read-only"), h("h2", { id: "sheet-title" }, e.title),
      h("span", { class: "muted", style: "font-size:12px" }, `${e.agentName} · ${e.machine === here() ? sys.here : e.machine} · the last ${n} messages`)),
    body,
    h("footer", { class: "sheet-foot" }, h("span", { class: "spacer" }), h("button", { class: "btn primary", onclick: () => sheet.close() }, "Close"))));
  if (!sheet.open) sheet.showModal();
  let p;
  try { p = await api("Preview", e.machine, e.key, n); } catch (err) { p = { items: [], failed: true, note: errText(err) }; }
  body.removeAttribute("aria-busy");
  fill(body, p.failed ? h("button", { class: "btn", onclick: () => transcript(e, n) }, "Try again") : null, p.note ? h("span", { class: "muted" }, p.note) : null, previewItems(e, p, 1000, 1000),
    p.more ? h("button", { class: "link", style: "align-self:flex-start", onclick: () => transcript(e, n + 40) }, "Load earlier") : null);
  for (const x of body.querySelectorAll(".msg-x")) x.classList.add("open");
}

// renameDialog asks for a session's new title; its agent's own data gets it (Activity
// undoes it).
export function renameDialog(e) {
  const input = h("input", { class: "field", id: "rename-title", value: e.title, maxlength: "200", autocomplete: "off", spellcheck: "false" });
  const msg = h("div", { class: "err", role: "alert" });
  const go = async () => {
    const t = input.value.trim();
    if (!t) { msg.textContent = "A title can't be empty."; return; }
    try { await api("Rename", e.machine, e.key, t); } catch (err) { msg.textContent = cap(errText(err)); return; }
    d.close();
    toast(`Renamed to “${t}”. ${e.agentName} shows it too; Activity undoes it.`);
    await renamed();
  };
  input.onkeydown = (ev) => { if (ev.key === "Enter") { ev.preventDefault(); d.querySelector(".btn.primary").click(); } };
  const d = dialog(h("h2", { style: "margin:0;font-size:16px" }, "Rename session"),
    h("label", { for: "rename-title", style: "font-size:12.5px" }, "Title"), input,
    h("span", { class: "muted", style: "font-size:12px" }, `${e.agentName} keeps it in its own data, as its own rename does, so its session list shows it too.`), msg,
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", onclick: () => d.close() }, "Cancel"), h("button", { class: "btn primary", onclick: go }, "Rename")));
  input.focus();
  input.select();
}
let renamed = async () => {};
export function onRenamed(fn) { renamed = fn; }

onInspector(renameDialog, (e) => transcript(e));

// ---- The sections below ----

function repository(e) {
  const g = e.group;
  if (e.cloud) {
    const c = e.cloud, cl = cloudOf(e.machine);
    const kv = (label, value) => [h("dt", {}, label), h("dd", {}, value)];
    const diffDown = (cl?.codeDown || [])[0] === "diff";
    return section("repository", "Repository", true, null, h("dl", { class: "kv" },
      kv("Repository", c.repo ? path(c.repo) : h("span", { class: "muted" }, "Not known yet")),
      diffDown ? kv("Started from", c.branch ? h("span", { class: "mono", style: "font-size:12px" }, c.branch) : h("span", { class: "muted" }, "Not known: its patch goes on your checkout's HEAD"))
        : kv("Branch", c.branch ? h("span", { class: "mono", style: "font-size:12px" }, c.branch) : h("span", { class: "muted" }, cl?.codeOnly ? "None yet" : `${e.agentName} fetches it when it brings the session`)),
      c.envLabel ? kv("Environment", h("span", { class: "mono", style: "font-size:12px" }, c.envLabel)) : null,
      c.changes ? kv("Changes", c.changes) : null,
      c.base ? kv("Base", h("span", { class: "mono", style: "font-size:12px" }, c.base.slice(0, 7))) : null,
      c.pr ? kv("PR", c.pr) : null,
      c.checkout ? kv("Here", path(c.checkout, 11.5)) : null));
  }
  return section("repository", "Repository", true, g.remote && !sectionOpen("repository", true) ? h("span", { class: "mono disc-aside" }, g.remote) : null,
    g.noRepo ? h("span", { class: "muted" }, "Started outside a git checkout") : [
      g.remote ? path(g.remote) : h("span", { class: "mono", style: "font-size:12px" }, g.name),
      e.branch ? h("span", {}, h("span", { class: "mono", style: "font-size:12px" }, e.branch), e.worktree ? h("span", { class: "muted" }, ` in a ${e.worktree}` + (e.mainBranch ? ` · main folder on ${e.mainBranch}` : "")) : null) : null,
      e.unpushed || e.dirty ? h("span", { class: "warn" }, [e.unpushed ? count(e.unpushed, "unpushed commit") : "", e.dirty ? count(e.dirty, "uncommitted file") : ""].filter(Boolean).join(" · ")) : null,
      g.local ? h("span", { class: "ok" }, "Cloned here at ", path(g.local, 12.5)) : g.remote ? h("span", { class: "muted" }, `Not on ${sys.here}: hopsesh can clone it`) : null],
    !g.noRepo && e.cwd && e.cwd !== g.local ? h("span", { class: "muted" }, path(e.cwd, 11)) : null);
}

function copyPlace(c) {
  if (cloudOf(c.machine)) return cloudTitle(c.machine);
  return `${c.agentName} ${c.local ? "on " + sys.here : "on " + c.machine}`;
}

function history(e) {
  const others = (e.copies || []).filter((c) => !(c.machine === e.machine && c.key === e.key));
  const n = others.length + e.history.length + (e.mirror ? 1 : 0);
  if (!n && !e.journey && !e.lineageError && !e.movement && !e.relationship?.parent && !e.relationship?.issue) return null;
  return section("copies", "Copies & history", false, h("span", { class: "chip disc-n" }, String(n)),
    e.relationship?.parent ? h("div",{class:"item"},h("strong",{},"Conversation family: "+e.relationship.name),h("span",{class:"muted"},(e.relationship.ancestors||[]).join(" → ")+" → "+e.relationship.branchName),h("span",{class:"muted"},e.relationship.evidence)) : null,
    e.relationship?.issue && e.relationship.issue !== e.lineageError ? h("div",{class:"item warn"},e.relationship.issue) : null,
    e.lineageError ? h("div", { class: "item warn" }, `Lineage unavailable: ${e.lineageError}`, e.canArchiveLineage ? h("p", {}, "Archive this metadata to start a new family. The native conversation is preserved; Activity can undo this.") : h("p",{},"Its parent conversation is archived, unreadable or depended on for history. If the parent is archived, unarchive it in its agent and refresh."),
 e.canArchiveLineage ? h("button", {class:"btn small",onclick:async()=>{try{await api("ArchiveLineage",e.machine,e.key);toast("Lineage metadata archived. Activity can undo it.");await renamed();}catch(err){fail(err);}}},"Archive unsupported lineage") : null) : null,
 e.journey ? h("div", { class: "journey-counts" },
 h("span", { class: "chip" }, `${e.journey.transfers} transfers`),
 h("span", { class: "chip" }, `${e.journey.roundTrips} round trips to origin${e.journey.originProfile?" profile":""}`),
 h("span", { class: "chip" }, `${e.journey.returns} returns to visited locations`),
 h("span", {class:"chip"}, `${e.journey.machineTransfers||0} machine transfers · ${e.journey.machineRoundTrips||0} machine round trips`),
 e.journey.fork ? h("span", { class: "chip",title:`Parent branch: ${e.journey.parentBranch}` }, "Separate fork") : null,
 h("span",{class:"muted",style:"font-size:12px"},`Origin: ${e.journey.origin&&e.journey.origin!=="/"?e.journey.origin:"not recorded"}; branch ${e.journey.branch.slice(0,8)}`)) : null,
 others.length ? h("div", { class: "sub-h" }, "Other copies") : null,
    others.map((c) => h("span", {}, copyPlace(c), c.profile ? h("span",{title:accountTitle(c.profile)}," · "+accountLabel(c.profile)) : null, h("span", { class: "muted" }, c.newest ? " · newest" : c.leftBehind ? " · moved on" : " · older"), " ", h("button",{class:"link",onclick:()=>resolveDestination(c).then(selectDestination).catch(fail)},"Show copy"))),
    e.mirror ? [h("div", { class: "sub-h" }, "Mirrored"), h("span", {}, `Remote Control keeps a copy on ${e.mirror.host} while it runs. `,
      h("button", { class: "link", onclick: () => api("OpenURL", e.mirror.url).catch(fail) }, "Open it"))] : null,
    e.history.length ? [h("div", { class: "sub-h" }, e.cloud ? "Lineage" : "Where it has been"),
      e.history.map((x, i) => h("div", { class: "hop" }, h("span", { class: "dot" + (i === e.history.length - 1 ? " ok" : "") }),
        h("div", {}, h("div", {}, x.what), x.loss?.length ? h("div", {class:"muted",style:"font-size:12px"}, `Fidelity: ${x.loss.join("; ")}`) : null, h("div", { class: "muted", style: "font-size:11px" }, when(x.when)))))] : null);
}

function details(e) {
  const kv = (label, value) => value ? [h("dt", {}, label), h("dd", {}, value)] : null;
  const id = e.cloud ? e.cloud.id : e.session || e.key.split("/").pop();
  return section("details", "Details", false, null, h("dl", { class: "kv wide" },
    kv("Session ID", h("span", { class: "id-line" }, h("span", { class: "mono", style: "font-size:11.5px" }, id),
      h("button", { class: "btn small", "aria-label": "Copy session ID", onclick: async () => { await api("CopyText", id); toast("Copied the session ID"); } }, "Copy"))),
    kv("File", e.path ? path(e.path, 11) : null),
    kv("Size", e.sizeKB ? bytes(e.sizeKB * 1024) : null),
    kv("Agent", [e.agentName, e.agentVersion ? " " + e.agentVersion : ""].join("")),
    kv("Machine", e.cloud ? cloudTitle(e.machine) : e.machine === here() ? `${e.machine} (${sys.here})` : e.machine),
    kv("Last active", e.lastActive ? when(e.lastActive) : null)));
}

// cloudNotes say what bringing a cloud session here does, and the cloud's limits.
function cloudNotes(e) {
  const c = e.cloud, cl = cloudOf(e.machine);
  const noun = c.noun || "session";
  if (cl && !cl.listable) return (cl?.limits || []).map((l) => h("span", { class: "muted ins-note" }, rich(l)));
  return [h("span", { class: "muted ins-note" }, cl?.codeOnly
    ? `hopsesh brings its code into a new worktree; the conversation stays in ${cl.title} for now. The cloud ${noun} is not changed.`
    : cl?.fidelity === "native" ? `Bringing it here makes a new worktree; ${e.agentName} copies the conversation in ${sys.terminal}. The cloud session is not changed.`
    : !(cl?.codeDown || []).length ? `Bringing it here makes a new worktree of its repository; hopsesh writes its messages, as text, as a new session of the agent you pick. Its code stays in ${cl?.title}. The cloud ${noun} is not changed.`
    : `Bringing it here makes a new worktree with its code; hopsesh writes ${cl?.fidelity === "code" ? `the ${noun}'s title and what came of it` : "its messages"} as a new session${e.bringIn ? " of the agent you pick" : ""}. The cloud ${noun} is not changed.`),
  (cl?.limits || []).map((l) => h("span", { class: "muted ins-note" }, rich(l)))];
}

// inspector is the selected session's pane (or what selecting one does).
export function inspector(e, previous=null) {
  if (!e) return h("aside", { class: "inspector", id: "inspector", "aria-label": "Session details" }, h("div", { class: "empty" }, "Select a session to see what you can do with it."));
  const m = model(e);
  const {group,observedAt,...item}=e;
  const signature=JSON.stringify([item,{...group,entries:undefined},statusLine(e),m.primary?.label,m.primary?.disabled,state.info?.previews,state.info?.agents,[...tabs.values()].filter(t=>t.machine===e.machine&&t.key===e.key).map(t=>[t.id,t.state,t.attention])]);
  const same=previous?.dataset.key===e.machine+"\u0000"+e.key;
  if(same && previous.dataset.signature===signature) return previous;
  const conversationKey=JSON.stringify([e.machine,e.key,e.lastActive,e.sizeKB,e.canPreview,state.info?.previews]);
  const retained=same && previous.dataset.conversationKey===conversationKey ? previous.querySelector('#sec-conversation')?.parentElement : null;
  return h("aside", { class: "inspector", id: "inspector", "aria-label": e.cloud ? `Cloud ${e.cloud.noun || "session"} details` : "Session details", "data-key": e.machine + "\u0000" + e.key, "data-signature":signature, "data-conversation-key":conversationKey },
    header(e), actionRow(e, m), movementNotice(e, selectDestination, () => {
      setSection("copies", true); refresh(e); $("#sec-copies")?.scrollIntoView({block:"nearest"});
    }), returnSection(m), openIn(e, m), retained || conversation(e), repository(e), history(e), details(e), e.cloud ? cloudNotes(e) : null);
}

function returnSection(m) {
  if (!m.returns?.length) return null;
  return h("section", {class:"sec", "aria-label":"Return destinations"}, h("span", {class:"sec-h"}, "Move back to an existing session"),
    m.returns.map(returnCard));
}
