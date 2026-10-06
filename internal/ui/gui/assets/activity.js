// The Activity screen: what hopsesh did here, newest first, with Undo, and the marks still
// waiting for a copy left behind to end.
import { api, h, fill, icon, ICONS, view, state, screen, go, current, loading, toast, fail, errText, ago, when, ask, sys, cloudTitle } from "./core.js";

// undo reverses an operation. When the session was used since, it says what changed and
// asks before throwing that work away.
export async function undo(id, title) {
  try {
    await api("Undo", id, false);
  } catch (e) {
    const m = errText(e);
    if (!/changed since/.test(m)) { fail(e); return false; }
    const why = m.replace(/^.*?changed since:?\s*/, "");
    const yes = await ask({ title: `“${title}” was used since`, danger: true, ok: "Undo anyway",
      body: `${why ? `What changed: ${why}. ` : ""}Undoing it anyway puts everything back as it was before, and what was done since is lost.` });
    if (!yes) return false;
    try { await api("Undo", id, true); } catch (e2) { fail(e2); return false; }
  }
  toast(`Undone: ${title}`);
  state.stale = true;
  return true;
}

// undoLast undoes the newest operation that can be undone (⌥⌘Z, Ctrl+Alt+Z).
export async function undoLast() {
  try {
    const title = await api("UndoLast");
    toast(`Undone: ${title}`);
  } catch (e) { fail(e); return; }
  go("sessions", true);
}

const KINDS = {
  "lineage-sync": {label:"Receipts synchronized",ico:ICONS.mark,cls:""},
  "archive-lineage": {label:"Metadata archived",ico:ICONS.mark,cls:""},
  move: { label: "Hopped here", ico: ICONS.down, cls: "" },
  continue: { label: "Continued", ico: ICONS.arrow, cls: "continue" },
  push: { label: "Sent", ico: ICONS.send, cls: "push" },
  mark: { label: "Marked", ico: ICONS.mark, cls: "mark" },
  fetch: { label: "Brought", ico: ICONS.cloud, cls: "cloud" },
  handoff: { label: "Handed off", ico: ICONS.send, cls: "cloud" },
  hop: { label: "Handed on", ico: ICONS.cloud, cls: "cloud" },
  cleanup: { label: "Cleaned up", ico: ICONS.mark, cls: "" },
  rename: { label: "Renamed", ico: ICONS.mark, cls: "mark" },
};

// hopText words a hop from one cloud to another: both legs, and what undo does.
function hopText(x) {
  const o = x.hop;
  if (!o) return { title: x.title, detail: "", note: "" };
  return {
    title: x.title,
    detail: [`${cloudTitle(o.from)} → ${sys.here} → ${cloudTitle(o.to)}`, o.key, o.state === "done" ? "" : o.state].filter(Boolean).join(" · "),
    note: o.state === "waiting" ? o.message : o.state === "failed" ? o.message
      : `Undo takes both legs back: the hand-off, then the copy here. Both cloud sessions stay where they are.`,
  };
}

// handoffText words a hand-off to a cloud: where it went, and what undo does and doesn't.
function handoffText(x) {
  const o = x.handoff;
  if (!o) return { title: x.title, detail: "", note: "" };
  const tail = " to " + o.cloudTitle;
  return {
    title: x.title.endsWith(tail) ? `“${x.title.slice(0, -tail.length)}”${tail}` : x.title,
    detail: [`${o.machine} → ${o.cloudTitle || cloudTitle(o.cloud)}`, o.session, o.branch ? "branch " + o.branch : ""].filter(Boolean).join(" · "),
    note: `Undo ${o.pushed ? "deletes the branch and " : "removes "}the mark. The ${o.noun || "session"} stays in ${o.cloudTitle}; archive it there if you want it gone.`,
  };
}

// fetchText words a fetch from a cloud: its title, what came back, and what undo does.
function fetchText(x) {
  const f = x.fetch;
  if (!f) return { title: x.title, detail: "", note: "" };
  const outcome = { partial: ": partial", empty: ": empty", waiting: ": waiting" }[f.outcome] || "";
  const partial = f.outcome === "partial" ? " restored" + (f.kept ? ", partial copy kept" : "") : "";
  const counts = f.outcome === "code" ? "the code only" : f.stated ? `${f.restored} of ${f.expected} messages${partial}`
    : f.restored ? `${f.restored} message${f.restored === 1 ? "" : "s"}${partial}${f.outcome === "unchecked" ? ", nothing to check them against" : ""}` : "";
  return {
    title: `“${f.title}” from ${f.cloudTitle}${outcome}`,
    detail: [`${cloudTitle(f.cloud)} → ${f.agent} on ${sys.here}`, counts, "worktree " + f.worktree].filter(Boolean).join(" · "),
    note: f.outcome === "waiting" ? "" : `Undo removes the copy here and the worktree. The cloud session stays on ${(f.url.match(/^https:\/\/([^/]+)/) || [])[1] || "the cloud"}.`,
  };
}

function row(x) {
  const k = KINDS[x.kind] || KINDS.move;
  const ft = x.kind === "fetch" ? fetchText(x) : x.kind === "handoff" ? handoffText(x) : x.kind === "hop" ? hopText(x) : null;
  const act = x.undone ? h("span", { class: "chip st-ended" }, "Undone")
    : x.canUndo ? h("button", { class: "btn", onclick: async () => { if (await undo(x.id, x.title)) render(true); } }, "Undo")
    : h("button", { class: "btn", title: "Asks before throwing work away", onclick: async () => { if (await undo(x.id, x.title)) render(true); } }, "Undo anyway…");
  return h("div", { class: "line-item" },
    h("span", { class: "ico " + k.cls }, icon(k.ico, 15)),
    h("div", { style: "flex:1 1 300px;min-width:0;display:flex;flex-direction:column;gap:3px" },
      h("div", {}, h("b", { style: "font-weight:500" }, k.label), " · ", ft ? ft.title : x.title),
      h("span", { class: "muted", style: "font-size:12px" }, ft?.detail || [`${x.changes} change${x.changes === 1 ? "" : "s"}`, x.remote.length ? `also on ${x.remote.join(", ")}` : ""].filter(Boolean).join(" · ")),
      x.pendingReceipt && !x.undone ? h("span",{class:"warn",style:"font-size:12px"},"Receipt acknowledgement pending.") : null,
      x.pendingReceipt && !x.undone ? h("button",{class:"btn small",onclick:async(ev)=>{const b=ev.currentTarget;b.disabled=true;b.textContent="Retrying acknowledgement…";try{await api("RetryReceipts",x.id);toast("Lineage acknowledgement recovered");state.stale=true;await render(true);}catch(err){fail(err);b.disabled=false;b.textContent="Retry acknowledgement";}}},"Retry acknowledgement") : null,
      ft?.note && !x.undone ? h("span", { class: x.fetch?.outcome === "partial" || x.kind === "handoff" || x.hop?.state === "failed" ? "warn" : "muted", style: "font-size:12px" }, ft.note) : null,
      x.hop?.state === "waiting" && !x.undone ? h("div", { style: "display:flex;gap:8px" },
        h("button", { class: "btn small", onclick: async () => { try { const d = await api("ContinueHop", x.id); toast(d.hop?.state === "done" ? "Handed on" : d.hop?.message || "Still waiting for the copy"); render(true); } catch (e) { fail(e); } } }, "Go on"),
        h("button", { class: "btn small", onclick: () => api("OpenHop", x.id).catch(fail) }, `Open in ${sys.terminal} again`)) : null,
      !x.canUndo && !x.undone && x.why ? h("span", { class: "warn", style: "font-size:12px" }, "Used since: " + x.why) : null),
    h("span", { class: "muted", style: "font-size:12px;flex:0 0 auto", title: when(x.when) }, ago(x.when)),
    h("div", { style: "flex:0 0 auto" }, act));
}

async function render(reload = false) {
  if (current !== "activity") return;
  if (reload || !state.activity) {
    loading("Reading the activity…");
    try { state.activity = await api("Activity"); } catch (e) { if (current === "activity") fill(view, h("div", { class: "loading err" }, errText(e))); return; }
  }
  if (current !== "activity") return;
  const a = state.activity;
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in" },
    h("h1", {}, "Activity"),
    h("span", { class: "muted" }, `Everything hopsesh changed on ${sys.here}. Undo puts it back, on every machine it touched; if a session was used since, hopsesh asks first. What a cloud made stays there.`),
    a.waiting.length ? h("section", { class: "card" },
      h("div", { class: "card-h" }, h("h2", { class: "name" }, "Waiting to be adopted")),
      a.waiting.map((w) => h("div", { class: "line-item" }, h("span", { class: "ico cloud" }, icon(ICONS.cloud, 15)),
        h("div", { style: "flex:1 1 300px;min-width:0;font-size:12.5px" }, `“${w.title}” is being copied from ${w.cloudTitle} in ${sys.terminal}. hopsesh adds it here when the copy appears, or on its next scan.`),
        h("button", { class: "btn small", onclick: () => go("brought", w) }, "Show")))) : null,
    a.owed.length ? h("section", { class: "card" },
      h("div", { class: "card-h stacked" }, h("h2", { class: "name" }, "Waiting to mark"), h("span", { class: "muted", style: "font-size:12px" }, "Copies left open elsewhere: hopsesh marks them on its next scan after they end.")),
      a.owed.map((o) => h("div", { class: "line-item" }, h("span", { class: "ico mark" }, icon(ICONS.clock, 15)),
        h("div", { style: "flex:1 1 300px;min-width:0" }, h("div", {}, o.title), h("span", { class: "muted", style: "font-size:12px" }, `${o.location} · will say “${o.mark}”`)),
        h("span", { class: "muted", style: "font-size:12px" }, "since " + ago(o.since))))) : null,
    branchesCard(),
    h("section", { class: "card" }, a.items.length ? a.items.filter((x) => !x.part).map(row) : h("div", { class: "empty" }, "Nothing yet. Hops, continuations and sends show up here, with Undo.")))));
}

// branchesCard is the clean-up of the branches cloud hand-offs left on the remotes: hopsesh
// asks the remotes (read-only) only when the user says so, and deletes only what the user
// picks, among the branches whose work is merged.
function branchesCard() {
  const b = state.branches;
  const look = async () => {
    state.branches = { busy: true, list: [] };
    render();
    try { state.branches = { list: await api("CleanupBranches") }; } catch (e) { state.branches = null; fail(e); }
    render();
  };
  const del = async (list) => {
    const yes = await ask({ title: list.length === 1 ? `Delete ${list[0].branch}?` : `Delete ${list.length} merged branches?`, ok: "Delete", danger: true,
      body: `Their work is merged into the default branch. hopsesh deletes ${list.length === 1 ? "it" : "them"} on the remote only while ${list.length === 1 ? "it is" : "they are"} where hopsesh saw ${list.length === 1 ? "it" : "them"}; Undo in Activity pushes ${list.length === 1 ? "it" : "them"} back.` });
    if (!yes) return;
    try {
      const r = await api("DeleteBranches", list.map((c) => c.id));
      toast(`Deleted ${r.deleted.length} branch${r.deleted.length === 1 ? "" : "es"}`);
    } catch (e) { fail(e); }
    state.branches = null;
    render(true);
  };
  const offered = (b?.list || []).filter((c) => c.offer);
  return h("section", { class: "card", "aria-label": "Branches on your remotes" },
    h("div", { class: "card-h" }, h("div", { class: "card-h-copy" }, h("h2", { class: "name" }, "Branches cloud hand-offs left"),
      h("span", { class: "muted", style: "font-size:12px" }, "Hand-off branches and the clouds' own branches, offered for deletion once their work is merged.")),
      offered.length > 1 ? h("button", { class: "btn small", onclick: () => del(offered) }, `Delete ${offered.length} merged`) : null,
      h("button", { class: "btn small", id: "look-branches", disabled: !!b?.busy, onclick: look }, b ? "Look again" : "Look for merged branches")),
    b?.busy ? h("div", { class: "loading", role: "status" }, "Asking the remotes…")
      : b && !b.list.length ? h("div", { class: "empty" }, "No branches from cloud hand-offs on your remotes.")
      : (b?.list || []).map((c) => h("div", { class: "line-item branch-item", "data-branch": c.branch },
        h("span", { class: "ico cloud" }, icon(ICONS.cloud, 15)),
        h("div", { style: "flex:1 1 300px;min-width:0;display:flex;flex-direction:column;gap:3px" },
          h("span", { class: "mono", style: "font-size:12.5px" }, c.branch),
          h("span", { class: "muted", style: "font-size:12px" }, [c.repo || c.checkout, c.cloudTitle, c.kind === "handoff" ? "hand-off branch" : "the cloud's own branch"].filter(Boolean).join(" · ")),
          h("span", { class: c.offer ? "ok" : "muted", style: "font-size:12px" }, c.why)),
        c.offer ? h("button", { class: "btn", onclick: () => del([c]) }, "Delete") : null)));
}

screen("activity", () => render(true));
