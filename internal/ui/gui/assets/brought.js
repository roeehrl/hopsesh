// The done screen of bringing a session from a cloud: a quiet wait while the agent's own
// command copies it in the terminal, then one of three outcomes (complete, a partial copy
// in amber, an empty one in red), each with what can be done next.
import { api, h, fill, view, state, screen, go, current, toast, fail, keys, sys, entries } from "./core.js";
import { undo } from "./activity.js";
import { planFor } from "./plan.js";

let timer = null;

function stop() {
  clearTimeout(timer);
  timer = null;
}

// poll asks every two seconds whether the copy is there, while this screen shows.
function poll(b) {
  stop();
  timer = setTimeout(async () => {
    if (current !== "brought") return;
    let next;
    try { next = await api("AdoptStatus", b.journal); } catch (e) { next = null; }
    if (current !== "brought") return;
    if (next && next.outcome !== "waiting") {
      state.stale = true;
      render(next);
    } else poll(b);
  }, 2000);
}

const badge = (kind, text) => h("span", { class: "badge big " + kind }, text);
const linkBtn = (label, url, primary = false) => h("button", { class: "btn" + (primary ? " primary" : ""), onclick: () => api("OpenURL", url).catch(fail) }, label);

// message is an outcome's words with its known problem as a link.
function message(b) {
  if (!b.issueRef || !b.issue) return b.message;
  const [before, after] = b.message.split(b.issueRef);
  return [before, h("button", { class: "link", onclick: () => api("OpenURL", b.issue).catch(fail) }, b.issueRef), after];
}

function codeLine(b) {
  if (b.written) return h("span", { class: "muted", style: "font-size:12.5px" }, "The code is here: ", h("span", { class: "mono", style: "font-size:11.5px" }, b.worktree),
    b.branch ? [" on ", h("span", { class: "mono", style: "font-size:11.5px" }, b.branch)] : null, b.changes ? ` (${b.changes})` : null, `. The cloud ${b.noun || "session"} is untouched.`);
  if (b.noBranch) return h("span", { class: "muted", style: "font-size:12.5px" }, "The cloud session never pushed its work, so there is no code to bring. ", h("span", { class: "mono", style: "font-size:11.5px" }, b.worktree));
  return h("span", { class: "muted", style: "font-size:12.5px" }, "The code is here in full: ", h("span", { class: "mono", style: "font-size:11.5px" }, b.worktree),
    b.branch ? [" on ", h("span", { class: "mono", style: "font-size:11.5px" }, b.branch)] : null, b.renamed ? ` (renamed from ${b.renamed})` : null, ". The cloud session is untouched.");
}

async function doUndo(b) {
  if (await undo(b.journal, b.title + " from " + b.cloudTitle)) go("sessions", true);
}

// continueIn plans continuing the copy in another agent, once the list has it.
async function continueIn(b) {
  await go("sessions", true);
  const e = entries().find((x) => x.key === b.key && x.machine === state.scan.machines.find((m) => m.local)?.name);
  if (!e) { toast("The copy is not listed yet; refresh and continue it from the list"); return; }
  planFor(e, { target: b.continue });
}

function render(b) {
  const back = h("button", { class: "btn", onclick: () => go("sessions", true) }, "Back to sessions", h("span", { class: "kbd" }, "esc"));
  let body;
  switch (b.outcome) {
    case "waiting":
      body = h("section", { class: "card" }, h("div", { class: "dlg-body" },
        h("div", { class: "waiting", role: "status" }, h("span", { class: "spinner", "aria-hidden": "true" }),
          h("span", {}, b.message), h("span", { class: "muted", style: "font-size:12px" }, `It's running in ${sys.terminal}. You can close this; hopsesh picks it up on its next look.`)),
        h("div", { class: "term" }, b.command),
        h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" },
          h("button", { class: "btn", onclick: () => api("OpenBrought", b.journal).catch(fail) }, `Open in ${sys.terminal} again`),
          h("button", { class: "btn", onclick: async () => { await api("CopyText", b.command); toast("Copied"); } }, "Copy the command"),
          h("button", { class: "btn", onclick: () => doUndo(b) }, "Undo"), back)));
      poll(b);
      break;
    case "code":
      body = h("section", { class: "outcome ok" }, h("span", { class: "sec-h ok" }, "The code only"),
        h("div", { class: "out-head" }, badge("ok", "✓"), h("h2", {}, `The code of “${b.title}” is here`)),
        codeLine(b), h("div", { class: "out-acts" }, h("button", { class: "btn", onclick: () => doUndo(b) }, "Undo"), back));
      break;
    case "complete":
    case "unchecked":
      body = h("section", { class: "outcome ok", "aria-labelledby": "out-h" }, h("span", { class: "sec-h ok" }, b.outcome === "unchecked" ? "Brought" : "Complete"),
        h("div", { class: "out-head" }, badge("ok", "✓"), h("div", {}, h("h2", { id: "out-h" }, `“${b.title}” is here in ${b.agent}`),
          h("span", { class: "muted", style: "font-size:12px" }, b.written ? (b.fidelity === "code" ? `The ${b.noun || "session"}'s title and what came of it, written by hopsesh` : `${b.restored} messages, as text`)
            : b.stated ? `${b.restored} of ${b.expected} messages · checked`
            : b.check === "brief" ? `${b.restored} messages · it begins with the briefing hopsesh sent`
            : b.check === "none" ? `${b.restored} messages · ${b.agent} gives no count to check against` : `${b.restored} messages`))),
        h("div", { style: "display:flex;gap:8px;flex-wrap:wrap" },
          b.continueName && !b.written ? h("button", { class: "btn primary", onclick: () => continueIn(b) }, `Continue in ${b.continueName}`) : null,
          h("button", { class: "btn" + (b.continueName && !b.written ? "" : " primary"), id: "open", onclick: () => api("OpenBrought", b.journal).catch(fail) }, `Open in ${sys.terminal}`),
          h("button", { class: "btn", onclick: async () => { await api("CopyText", b.command); toast("Copied"); } }, "Copy the command")),
        h("div", { class: "term" }, b.command), codeLine(b),
        (b.warnings || []).map((w) => h("span", { class: "warn", style: "font-size:12px" }, w)),
        (b.loss || []).length ? h("details", { class: "sec" }, h("summary", { style: "cursor:pointer;font-size:12.5px" }, "What stays in the cloud"),
          h("ul", { style: "margin:6px 0 0;padding-left:18px;font-size:12.5px" }, b.loss.map((l) => h("li", {}, l[0].toUpperCase() + l.slice(1))))) : null,
        b.url ? h("div", {}, linkBtn(`Open the ${b.noun || "session"} in the browser`, b.url)) : null,
        b.renamed || (b.branch && !b.written) ? h("div", { class: "hint", role: "note", id: "cleanup-hint" }, "Once its work is merged, hopsesh can delete the cloud's branch for you: ",
          h("button", { class: "link", onclick: () => go("activity") }, "Activity → Look for merged branches"), ". It asks first.") : null,
        h("div", { class: "out-acts" }, h("button", { class: "btn", onclick: () => doUndo(b) }, "Undo", h("span", { class: "kbd" }, keys("mod+alt+Z"))), back));
      break;
    case "partial":
      body = h("section", { class: "outcome partial", "aria-labelledby": "out-h" }, h("span", { class: "sec-h warn" }, "Partial"),
        h("div", { class: "out-head" }, badge("warn", "!"), h("h2", { id: "out-h" }, "Only part of the conversation came back")),
        h("p", { style: "margin:0;font-size:13px" }, message(b)), codeLine(b),
        b.kept ? h("span", { class: "ok", style: "font-size:12.5px" }, "You kept the partial copy.") : null,
        h("div", { class: "out-stack" },
          b.kept ? h("button", { class: "btn primary", onclick: () => api("OpenBrought", b.journal).catch(fail) }, `Open in ${sys.terminal}`)
            : h("button", { class: "btn primary", onclick: async () => { try { await api("KeepPartial", b.journal); } catch (e) { fail(e); return; } render(Object.assign({}, b, { kept: true })); } }, "Keep the partial copy"),
          h("button", { class: "btn", onclick: () => doUndo(b) }, "Undo"),
          b.url ? linkBtn("Open the session in the browser", b.url) : null),
        back);
      break;
    case "empty": {
      const [machine] = (b.mirrorOf || "").split(":");
      const local = b.mirrorOf ? entries().find((x) => x.machine + ":" + x.key === b.mirrorOf) : null;
      body = h("section", { class: "outcome empty", "aria-labelledby": "out-h" }, h("span", { class: "sec-h err" }, b.mirrorOf ? "Empty · Remote Control" : "Empty"),
        h("div", { class: "out-head" }, badge("err", "✕"), h("h2", { id: "out-h" }, `Nothing came back from “${b.title}”`)),
        h("p", { style: "margin:0;font-size:13px" }, message(b)),
        machine ? h("span", { class: "muted", style: "font-size:12.5px" }, `The session itself runs on ${machine}. Hop it here machine to machine over SSH instead: the whole conversation comes along.`) : null,
        h("div", { class: "out-stack" },
          local && machine !== state.scan?.machines.find((m) => m.local)?.name ? h("button", { class: "btn primary", onclick: () => planFor(local, { target: "" }) }, `Hop here from ${machine}`) : null,
          h("button", { class: "btn", onclick: () => doUndo(b) }, "Undo"),
          b.url ? linkBtn("Open the session in the browser", b.url) : null),
        back);
      break;
    }
  }
  const title = b.outcome === "waiting" ? `Bringing “${b.title}” from ${b.cloudTitle}` : `Brought from ${b.cloudTitle}`;
  fill(view, h("div", { class: "page" }, h("div", { class: "page-in", style: "max-width:760px" }, h("h1", {}, title), body)));
  view.querySelector("#open")?.focus();
}

screen("brought", (b) => { stop(); render(b); });

// On this screen, Esc goes back to the sessions.
document.addEventListener("keydown", (ev) => {
  if (ev.key === "Escape" && current === "brought" && !document.querySelector("dialog[open]")) go("sessions", true);
});
