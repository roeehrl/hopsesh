// The Activity screen: what hopsesh did here, newest first, with Undo, and the marks still
// waiting for a copy left behind to end.
import { api, h, fill, icon, ICONS, view, state, screen, go, loading, toast, fail, errText, ago, when, ask } from "./core.js";

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

// undoLast undoes the newest operation that can be undone (⌥⌘Z).
export async function undoLast() {
  try {
    const title = await api("UndoLast");
    toast(`Undone: ${title}`);
  } catch (e) { fail(e); return; }
  go("sessions", true);
}

const KINDS = {
  move: { label: "Hopped here", ico: ICONS.down, cls: "" },
  continue: { label: "Continued", ico: ICONS.arrow, cls: "continue" },
  push: { label: "Sent", ico: ICONS.send, cls: "push" },
  mark: { label: "Marked", ico: ICONS.mark, cls: "mark" },
};

function row(x) {
  const k = KINDS[x.kind] || KINDS.move;
  const act = x.undone ? h("span", { class: "chip st-ended" }, "Undone")
    : x.canUndo ? h("button", { class: "btn", onclick: async () => { if (await undo(x.id, x.title)) render(true); } }, "Undo")
    : h("button", { class: "btn", title: "Asks before throwing work away", onclick: async () => { if (await undo(x.id, x.title)) render(true); } }, "Undo anyway…");
  return h("div", { class: "line-item" },
    h("span", { class: "ico " + k.cls }, icon(k.ico, 15)),
    h("div", { style: "flex:1 1 300px;min-width:0;display:flex;flex-direction:column;gap:3px" },
      h("div", {}, h("b", { style: "font-weight:500" }, k.label), " · ", x.title),
      h("span", { class: "muted", style: "font-size:12px" }, [`${x.changes} change${x.changes === 1 ? "" : "s"}`, x.remote.length ? `also on ${x.remote.join(", ")}` : ""].filter(Boolean).join(" · ")),
      !x.canUndo && !x.undone && x.why ? h("span", { class: "warn", style: "font-size:12px" }, "Used since: " + x.why) : null),
    h("span", { class: "muted", style: "font-size:12px;flex:0 0 auto", title: when(x.when) }, ago(x.when)),
    h("div", { style: "flex:0 0 auto" }, act));
}

async function render(reload = false) {
  if (reload || !state.activity) {
    if (!state.activity) loading("Reading the activity…");
    try { state.activity = await api("Activity"); } catch (e) { fill(view, h("div", { class: "loading err" }, errText(e))); return; }
  }
  const a = state.activity;
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in" },
    h("div", { style: "display:flex;align-items:baseline;gap:12px;flex-wrap:wrap" }, h("h1", {}, "Activity"), h("span", { class: "spacer" }),
      h("button", { class: "btn", onclick: () => go("sessions") }, "Back to sessions")),
    h("span", { class: "muted" }, "Everything hopsesh changed on this Mac. Undo puts it back, on every machine it touched; if a session was used since, hopsesh asks first."),
    a.owed.length ? h("section", { class: "card" },
      h("div", { class: "card-h" }, h("span", { class: "name" }, "Waiting to mark"), h("span", { class: "muted", style: "font-size:12px" }, "Copies left open elsewhere: hopsesh marks them on its next scan after they end.")),
      a.owed.map((o) => h("div", { class: "line-item" }, h("span", { class: "ico mark" }, icon(ICONS.clock, 15)),
        h("div", { style: "flex:1 1 300px;min-width:0" }, h("div", {}, o.title), h("span", { class: "muted", style: "font-size:12px" }, `${o.location} · will say “${o.mark}”`)),
        h("span", { class: "muted", style: "font-size:12px" }, "since " + ago(o.since))))) : null,
    h("section", { class: "card" }, a.items.length ? a.items.map(row) : h("div", { class: "empty" }, "Nothing yet. Hops, continuations and sends show up here, with Undo.")))));
}

screen("activity", () => render(true));
