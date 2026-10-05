// hopsesh's window: boot, the menu's commands, and ssh's password questions. Plain ES
// modules, no build step.
import { api, on, h, fill, view, state, go, current, toast, fail, errText, $, sys, setSystem, ask } from "./core.js";
import "./sessions.js";
import "./plan.js";
import "./brought.js";
import "./handoff.js";
import "./hop.js";
import "./machines.js";
import "./settings.js";
import { undoLast } from "./activity.js";
import { openPalette } from "./palette.js";
import { loadTabs, onTabs, showTerminal, tabs, exits } from "./term.js";
import { render as renderSessions } from "./sessions.js";

$("#btn-search").onclick = openPalette;
$("#btn-refresh").onclick = () => go("sessions", true);
$("#btn-settings").onclick = () => go("settings");
$("#btn-terminal").onclick = () => showTerminal();
// A tab's state shows on its session's row and in the sidebar: drawn again when what they
// show changes (not on every output), keeping the focus on the selected row.
let shownTabs = "";
onTabs(() => {
  const sig = JSON.stringify([[...tabs.values()].map((t) => [t.kind, t.machine, t.key, t.attention, t.state === "exited"]), [...exits.values()].map((x) => [x.key, x.code])]);
  if (sig === shownTabs || current !== "sessions") { shownTabs = sig; return; }
  shownTabs = sig;
  const onRow = document.activeElement?.classList?.contains("row");
  renderSessions();
  if (onRow) view.querySelector('.row[aria-selected="true"]')?.focus();
});

// The app menu (and its shortcuts) sends these.
on("hopsesh:menu", menuCommand);
// A launch opened in another terminal than the chosen one (macOS denied iTerm2, say).
on("hopsesh:terminal-app", (n) => toast(n.message));
function menuCommand(cmd) {
  if (document.querySelector("#sheet[open]") && cmd !== "palette") return; // a plan is open
  switch (cmd) {
    case "palette": openPalette(); break;
    case "refresh": go("sessions", true); break;
    case "sessions": go("sessions"); break;
    case "activity": go("activity"); break;
    case "machines": go("machines"); break;
    case "settings": go("settings"); break;
    case "undo-last": undoLast(); break;
  }
}

// Windows has no menu bar: the window takes the menu's shortcuts itself.
document.addEventListener("keydown", (ev) => {
  if (sys.mac || !ev.ctrlKey || ev.metaKey) return;
  const k = ev.key.toLowerCase();
  const cmd = ev.altKey ? (k === "z" ? "undo-last" : "") : { k: "palette", r: "refresh", 1: "sessions", 2: "activity", 3: "machines", ",": "settings" }[k];
  if (!cmd) return;
  ev.preventDefault();
  menuCommand(cmd);
});

// ssh asks for a password machine's password in the middle of a scan or a hop; the
// backend raises this and waits. Questions come one at a time.
const pwQueue = [];
function askPassword(req) {
  if (pwQueue.some((r) => r.id === req.id)) return;
  pwQueue.push(req);
  if (pwQueue.length === 1) showPassword();
}
function showPassword() {
  const req = pwQueue[0];
  if (!req) return;
  const dlg = $("#pwdlg");
  const input = h("input", { class: "field", type: "password", id: "pw", autocomplete: "off" });
  const remember = h("input", { type: "checkbox", checked: req.remember });
  let answered = false;
  const done = async (ok) => {
    if (answered) return;
    answered = true;
    try {
      if (ok) await api("ProvidePassword", req.id, input.value, remember.checked);
      else await api("CancelPassword", req.id);
    } catch (e) { fail(e); }
    input.value = "";
    dlg.close();
    pwQueue.shift();
    showPassword();
  };
  fill(dlg, h("form", { class: "dlg-body", onsubmit: (e) => { e.preventDefault(); if (input.value) done(true); } },
    h("h2", { style: "margin:0;font-size:16px" }, `Password for ${req.machine}`),
    h("div", { class: "mono muted", style: "font-size:11px" }, req.destination),
    req.retry ? h("div", { class: "err", role: "alert" }, "That password was not accepted. Try again.") : null,
    h("label", { for: "pw", class: "visually-hidden" }, `Password for ${req.machine}`), input,
    req.canRemember ? h("label", { class: "opt" }, remember, h("span", {}, `Remember it in ${sys.vault}`)) : null,
    h("div", { class: "muted", style: "font-size:12px" }, "Given only to ssh for this machine. Skip leaves the machine out this time."),
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", type: "button", onclick: () => done(false) }, "Skip"), h("button", { class: "btn primary", type: "submit" }, "Log in"))));
  dlg.oncancel = (e) => { e.preventDefault(); done(false); };
  dlg.showModal();
  input.focus();
}
on("hopsesh:password", askPassword);

// configError explains settings hopsesh cannot read. An older hopsesh's: hopsesh keeps no
// code for old formats, so it offers to set the file aside and start fresh. A newer
// hopsesh's (after a downgrade): updating comes first; setting it aside is a second,
// confirmed choice.
function configError() {
  const fresh = async (newer) => {
    try { toast((newer ? "Newer settings kept at " : "Old settings kept at ") + await api("StartFresh", newer)); } catch (e) { fail(e); return; }
    state.info = await api("Info");
    go("sessions", true);
  };
  if (!state.info.configNewer) {
    fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:620px;padding-top:12vh" },
      h("h1", {}, "Your hopsesh settings are from an older version"),
      h("span", { class: "muted", style: "line-height:1.5" }, "This version stores its settings differently. Start fresh to keep the old file next to the new one and add your machines again. Your sessions are not affected."),
      h("span", { class: "mono muted", style: "font-size:11.5px" }, state.info.configError),
      h("div", {}, h("button", { class: "btn primary big", onclick: () => fresh(false) }, "Start fresh")))));
    return;
  }
  const update = async (btn) => {
    btn.disabled = true;
    try {
      const u = await api("LatestRelease");
      if (u.newer && u.canInstall) {
        fill(btn, `Downloading and checking ${u.latest}…`);
        await api("InstallUpdate");
        fill(btn, "Restarting…");
        return;
      }
      if (u.newer) await api("OpenURL", u.url);
      else toast(`hopsesh ${u.latest} is the newest release, and this is it: the settings come from a build newer than that`);
    } catch (e) { fail(e); }
    btn.disabled = false;
    fill(btn, "Update hopsesh");
  };
  const setAside = async () => {
    if (await ask({ title: "Set the newer settings aside?", ok: "Set aside and start fresh", danger: true,
      body: "hopsesh moves the file next to the new one and starts with defaults: you add your machines again, and the newer hopsesh will not see what you change here. Your sessions are not affected." })) fresh(true);
  };
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:620px;padding-top:12vh" },
    h("h1", {}, "Your hopsesh settings are from a newer version"),
    h("span", { class: "muted", style: "line-height:1.5" }, `A newer hopsesh wrote them, and this one (${state.info.version}) cannot read them. Update hopsesh to keep using them as they are. Your sessions are not affected.`),
    h("span", { class: "mono muted", style: "font-size:11.5px" }, state.info.configError),
    h("div", { style: "display:flex;gap:10px;flex-wrap:wrap" },
      h("button", { class: "btn primary big", onclick: (ev) => update(ev.currentTarget) }, "Update hopsesh"),
      h("button", { class: "btn big", onclick: setAside }, "Set them aside and start fresh…")))));
}

(async () => {
  for (const r of await api("PendingPasswords").catch(() => [])) askPassword(r);
  try { state.info = await api("Info"); } catch (e) { fill(view, h("div", { class: "loading err" }, errText(e))); return; }
  setSystem(state.info.os, state.info.terminal);
  if (state.info.configError) { configError(); return; }
  await loadTabs();
  await go("sessions", true);
  if (state.info.updateCheck === "on") {
    try { state.update = await api("CheckUpdate"); } catch { return; } // offline
    if (state.update?.newer && current === "sessions") go("sessions");
  }
})();
