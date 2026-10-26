// Change folder…: a session continues in another folder on its machine and stays the same
// session (hopsesh cd). The dialog plans the change whenever the folder or a choice
// changes, says what happens, and makes it; a session open in an agent that moves it from
// inside (Claude Code's /cd) is moved there: the dialog says what to type and waits.
import { api, h, fill, dialog, toast, errText, cap, sys, here, state } from "./core.js";
import { undo } from "./activity.js";
import { heldBack, refused, isRefusal } from "./guard.js";

let changed = async () => {};
export function onChangedDir(fn) { changed = fn; }

export function changeDirDialog(e) {
  if (heldBack(e)) return;
  const local = e.machine === here();
  const where = local ? sys.here : e.machine;
  const input = h("input", { class: "field mono", id: "cd-dir", value: "", placeholder: local ? "~/git/app-2" : `a folder on ${e.machine}`, autocomplete: "off", spellcheck: "false" });
  const other = h("input", { type: "checkbox" });
  const stop = h("input", { type: "checkbox" });
  const otherRow = h("label", { class: "opt", hidden: true }, other, h("span", {}, h("b", {}, "Allow a folder of another repository"), h("span", { class: "muted" }, "The conversation is about the files of the session's repository.")));
  const stopRow = h("label", { class: "opt", hidden: true }, stop, h("span", {}, h("b", {}, "Quit the open session first"), h("span", { class: "muted" }, `hopsesh quits it on ${where}, then changes its folder.`)));
  const suggest = h("div", { class: "cd-suggest", role: "group", "aria-label": "Other checkouts of its repository" });
  const plan = h("div", { class: "cd-plan", "aria-live": "polite" });
  const foot = h("div", { class: "dlg-foot" });
  let seq = 0, timer = 0, current = null;

  const go = h("button", { class: "btn primary", disabled: true }, "Change folder");
  const cancel = h("button", { class: "btn", onclick: () => { api("CancelChangeDir").catch(() => {}); d.close(); } }, "Cancel");
  fill(foot, cancel, go);

  async function replan() {
    const n = ++seq, dir = input.value.trim();
    current = null; go.disabled = true;
    if (!dir) { fill(plan); return; }
    fill(plan, h("span", { class: "muted", role: "status" }, "Planning…"));
    let p;
    try { p = await api("PlanChangeDir", e.machine, e.key, dir, other.checked, stop.checked); }
    catch (err) {
      // The same dialog asks about continuing a copy that already moved: this one goes first.
      if (n === seq && isRefusal(err)) { d.close(); refused(err, e); }
      else if (n === seq) fill(plan, h("p", { class: "err", role: "alert" }, cap(errText(err))));
      return;
    }
    if (n !== seq) return;
    current = p;
    const c = p.change, x = c.plan;
    otherRow.hidden = !(other.checked || (x.blockers || []).some((b) => /another|not in a git repository|not a checkout/.test(b)));
    stopRow.hidden = !(c.open && (stop.checked || !c.inSession));
    const lines = [];
    lines.push(h("div", { class: "cd-fromto" }, h("span", { class: "mono" }, x.from), h("span", { "aria-hidden": "true" }, "→"), h("span", { class: "mono" }, x.to)));
    if ((x.moves || []).length) lines.push(h("li", {}, `Moves ${x.moves.length} file${x.moves.length === 1 ? "" : "s"} to the new folder's project folder, as ${e.agentName}'s own /cd does`));
    for (const r of x.records || []) lines.push(h("li", {}, `Adds ${r}`));
    for (const t of x.notes || []) lines.push(h("li", { class: "muted" }, cap(t)));
    for (const t of x.warnings || []) lines.push(h("li", { class: "warn" }, cap(t)));
    for (const t of x.blockers || []) lines.push(h("li", { class: "err" }, cap(t)));
    const steps = h("ul", { class: "cd-steps" }, lines.slice(1));
    if (c.open && !c.stopFirst && c.inSession) {
      fill(plan, lines[0], steps, h("div", { class: "cd-insession" },
        h("b", {}, `The session is open on ${where}. In it, type:`),
        h("div", { class: "cd-cmd" }, h("code", {}, c.inSession), h("button", { class: "btn small", onclick: () => api("CopyText", c.inSession).then(() => toast("Copied")) }, "Copy")),
        h("span", { class: "muted" }, `${e.agentName} moves it itself and keeps it the same session. hopsesh waits and checks.`)));
      go.textContent = "I typed it, wait for it";
      go.disabled = (x.blockers || []).length > 0;
      return;
    }
    go.textContent = "Change folder";
    fill(plan, lines[0], steps, h("span", { class: "muted", style: "font-size:12px" }, "The same session continues there: its id and every message stay. Activity undoes it."));
    go.disabled = (x.blockers || []).length > 0;
  }
  const soon = () => { clearTimeout(timer); timer = setTimeout(replan, 300); };
  input.oninput = soon;
  other.onchange = replan;
  stop.onchange = replan;

  go.onclick = async () => {
    if (!current) return;
    const c = current.change;
    go.disabled = true;
    if (c.open && !c.stopFirst && c.inSession) {
      fill(plan, h("div", { class: "cd-insession", role: "status" }, h("b", {}, `Waiting for ${e.agentName} to move it…`), h("span", { class: "muted" }, `Type ${c.inSession} in the session on ${where}.`)));
      try { await api("WaitChangedDir", 600); }
      catch (err) { fill(plan, h("p", { class: "err", role: "alert" }, cap(errText(err)))); go.disabled = false; return; }
      d.close();
      toast(`${e.agentName} moved “${e.title}” to ${c.plan.to}. It is the same session.`);
      state.stale = true;
      await changed();
      return;
    }
    let id;
    try { id = await api("ChangeDir"); }
    catch (err) { fill(plan, h("p", { class: "err", role: "alert" }, cap(errText(err)))); go.disabled = false; return; }
    d.close();
    const title = `change the folder of “${e.title}”`;
    toast(`“${e.title}” continues in ${c.plan.to} now, the same session.`, { label: "Undo", run: () => undo(id, title) });
    state.stale = true;
    await changed();
  };

  const browse = local ? h("button", { class: "btn", onclick: async () => {
    const dir = await api("ChooseFolder", `Where should “${e.title}” continue?`).catch(() => "");
    if (dir) { input.value = dir; replan(); }
  } }, "Choose…") : null;

  const d = dialog(
    h("h2", { style: "margin:0;font-size:16px" }, `Change the folder of “${e.title}”`),
    h("span", { class: "muted", style: "font-size:12.5px" }, `On ${where}, now in `, h("span", { class: "mono" }, e.cwd || "its folder"), `. ${e.agentName} keeps the same session; it loads the new folder's instructions and settings when it resumes.`),
    h("label", { for: "cd-dir", style: "font-size:12.5px" }, local ? "Folder" : `Folder on ${e.machine}`),
    h("div", { class: "cd-pick" }, input, browse),
    suggest, otherRow, stopRow, plan, foot);
  d.addEventListener("close", () => { clearTimeout(timer); api("CancelChangeDir").catch(() => {}); }, { once: true });
  api("DirSuggestions", e.machine, e.key).then((list) => {
    if (!list?.length) return;
    fill(suggest, h("span", { class: "muted", style: "font-size:12px" }, "Other checkouts of its repository:"),
      list.map((p) => h("button", { class: "btn small mono", onclick: () => { input.value = p; replan(); } }, p)));
  }).catch(() => {});
  input.focus();
}
