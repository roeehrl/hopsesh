// hopsesh's window: boot, the menu's commands, and ssh's password questions. Plain ES
// modules, no build step.
import { api, on, h, fill, view, state, go, current, toast, fail, errText, $ } from "./core.js";
import "./sessions.js";
import "./plan.js";
import "./machines.js";
import "./settings.js";
import { undoLast } from "./activity.js";
import { openPalette } from "./palette.js";

$("#btn-search").onclick = openPalette;
$("#btn-refresh").onclick = () => go("sessions", true);
$("#btn-settings").onclick = () => go("settings");

// The app menu (and its shortcuts) sends these.
on("hopsesh:menu", (cmd) => {
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
    req.canRemember ? h("label", { class: "opt" }, remember, h("span", {}, "Remember it in the Keychain")) : null,
    h("div", { class: "muted", style: "font-size:12px" }, "Given only to ssh for this machine. Skip leaves the machine out this time."),
    h("div", { class: "dlg-foot" }, h("button", { class: "btn", type: "button", onclick: () => done(false) }, "Skip"), h("button", { class: "btn primary", type: "submit" }, "Log in"))));
  dlg.oncancel = (e) => { e.preventDefault(); done(false); };
  dlg.showModal();
  input.focus();
}
on("hopsesh:password", askPassword);

// configError explains settings an older hopsesh wrote; hopsesh keeps no code for old
// formats, so it offers to set the file aside and start fresh.
function configError() {
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:620px;padding-top:12vh" },
    h("h1", {}, "Your hopsesh settings are from an older version"),
    h("span", { class: "muted", style: "line-height:1.5" }, "This version works with every coding agent and stores its settings differently. Start fresh to keep the old file next to the new one and add your machines again. Your sessions are not affected."),
    h("span", { class: "mono muted", style: "font-size:11.5px" }, state.info.configError),
    h("div", {}, h("button", { class: "btn primary big", onclick: async () => {
      try { toast("Old settings kept at " + await api("StartFresh")); } catch (e) { fail(e); return; }
      state.info = await api("Info");
      go("sessions", true);
    } }, "Start fresh")))));
}

(async () => {
  for (const r of await api("PendingPasswords").catch(() => [])) askPassword(r);
  try { state.info = await api("Info"); } catch (e) { fill(view, h("div", { class: "loading err" }, errText(e))); return; }
  if (state.info.configError) { configError(); return; }
  await go("sessions", true);
  if (state.info.updateCheck === "on") {
    try { state.update = await api("CheckUpdate"); } catch { return; } // offline
    if (state.update?.newer && current === "sessions") go("sessions");
  }
})();
