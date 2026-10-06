// What can be done with a session, in one vocabulary where the preposition says what
// changes: Resume in ‹place› (the program it runs in), Show in ‹place› (nothing), Bring to
// this Mac… and Send to ‹machine›… (the machine), Continue with ‹agent›… (the agent), Hand
// off to ‹cloud›… (to a cloud). model(e) is a session's whole action row: its status line,
// the primary (a split button whose menu lists the other places), Move ▾ and ⋯; the row's
// button, ↩, ⌘↩ and the palette all come from it.
import { api, state, sys, here, agentInfo, cloudOf, cloudTitle, toast, fail, errText, cap, entries, count, ago, h, icon, ICONS, agentBadge } from "./core.js";
import { planFor } from "./plan.js";
import { tabs, resume, showTerminal, moveToTerminal, openShell, signIn } from "./term.js";
import { planHandoff } from "./handoff.js";
import { planHop } from "./hop.js";

export const key = (e) => e.machine + "\u0000" + e.key;

// liveOf is how a session runs now: what presence last read (every few seconds), else
// the scan's.
export function liveOf(e) {
  return state.presence?.[key(e)] || { live: e.live, status: e.status, needs: e.needs, app: e.app, places: e.places || [] };
}

// appWord names an agent's desktop app: "Claude" for Claude Code.
const APPS = { claude: "Claude", codex: "Codex" };
export const appWord = (e) => liveOf(e).app || APPS[e.agent] || e.agentName;

// placeName is a place for people: "hopsesh Terminal", "iTerm2", "VS Code", "Claude app".
export function placeName(p, e) {
  switch (p.kind) {
    case "hopsesh": return "hopsesh Terminal";
    case "iterm2": return "iTerm2";
    case "terminal": return "Terminal";
    case "wt": return "Windows Terminal";
    case "claude-app": return "Claude app";
    case "codex-app": return "Codex app";
    case "ide": return p.app || "an editor";
    case "tmux": return "tmux";
    case "ssh": return "an ssh login";
  }
  return e?.app ? `${e.app} app` : "a terminal";
}

// sessionTabs are the live hopsesh Terminal tabs that run a session (its own, and a
// bring-back's that saved it), waiting ones first.
export function sessionTabs(e) {
  return [...tabs.values()].filter((t) => (t.kind === "session" || t.kind === "bring") && t.key && t.machine === e.machine && t.key === e.key && t.state !== "exited")
    .sort((a, b) => (b.attention ? 1 : 0) - (a.attention ? 1 : 0));
}

// placesOf is where a session on this machine is open: hopsesh's own tabs (the window
// knows them), then what presence found (terminal apps, the agent's app, editors, tmux,
// ssh). A session that runs where nothing tells is "a terminal".
export function placesOf(e) {
  if (e.cloud || e.machine !== here()) return [];
  const out = [];
  const ts = sessionTabs(e);
  if (ts.length) out.push({ kind: "hopsesh", count: ts.length, waiting: ts.some((t) => t.attention), tabs: ts });
  const lv = liveOf(e);
  if (lv.live) for (const p of lv.places || []) out.push(Object.assign({}, p, { waiting: p.waiting || (lv.needs && !ts.length && (lv.places || []).length === 1) }));
  if (lv.live && !ts.length && !(lv.places || []).length) out.push({ kind: lv.app ? "claude-app" : "unknown", count: 1, waiting: !!lv.needs });
  return out;
}
export const placeCount = (ps) => ps.reduce((n, p) => n + (p.count || 1), 0);

// canShow: hopsesh can bring that place forward (tmux and ssh: it can't).
const canShow = (p) => p.kind !== "tmux" && p.kind !== "ssh" && !(p.kind === "ide" && !sys.mac);

// show brings a place forward: a tab of the hopsesh Terminal window, the terminal app's
// tab (found only now, by FindTab), the agent's app, or the editor.
export function showPlace(e, p) {
  switch (p.kind) {
    case "hopsesh": return showTerminal(p.tabs[0].id);
    case "claude-app": case "codex-app": return api("ShowApp", e.machine, e.key).catch((err) => toast(cap(errText(err))));
    case "ide": return api("ShowPlace", p.app).catch((err) => toast(cap(errText(err))));
  }
  return api("ShowEntry", e.machine, e.key).catch((err) => toast(`Couldn't show “${e.title}”: ${errText(err)}`));
}

// resumePlaces are where a session on this machine can resume, with what each means.
export function resumePlaces(e) {
  const out = [{ id: "here", name: "hopsesh Terminal", note: "Tab ends when hopsesh quits" },
    { id: "terminal", name: sys.terminal, note: "Keeps running after hopsesh quits" }];
  if (agentInfo(e.agent)?.capabilities?.includes("app")) out.push({ id: "app", disabled:e.canApp===false, why:e.appWhy, name: `${APPS[e.agent] || e.agentName} app`, note: e.appWhy || `Opens this conversation in the ${APPS[e.agent] || e.agentName} app` });
  return out;
}

// defaultPlace is where an agent's sessions resume: as last chosen from a Resume menu,
// else Settings → Terminal's choice.
export function defaultPlace(e) {
  const ids = resumePlaces(e).filter(p=>!p.disabled).map((p) => p.id);
  const p = state.info?.places?.[e.agent];
  return p && ids.includes(p) ? p : state.info?.where === "terminal" ? "terminal" : "here";
}

// resumeIn resumes a session in a place; picked from the menu, the place becomes the
// agent's default.
export async function resumeIn(e, place, remember = false) {
  if (remember && place !== defaultPlace(e)) {
    try {
      await api("SetPlace", e.agent, place);
      state.info.places = Object.assign({}, state.info.places, { [e.agent]: place });
    } catch (err) { fail(err); }
  }
  if (place === "app") return api("ResumeEntry", e.machine, e.key, true).catch(fail);
  return resume(e, place);
}

// cloudBlock says why a cloud's sessions cannot be brought here now ("" when they can).
export function cloudBlock(c) {
  if (!c) return "This cloud is not in use here.";
  if (!c.allowed) return `hopsesh leaves ${c.title} alone until you allow it.`;
  if (!c.fetchable) return `hopsesh cannot bring ${c.title} sessions here yet.`;
  if (c.status === "ready" || c.status === "cli-old") return "";
  return cap(c.error && c.status === "signed-out" ? c.error.replace(/^.*?: /, "") : c.hint || c.error || c.status);
}

// noCode says why a code-only cloud's session has no code to bring yet ("" when it has).
function noCode(e, cl) {
  if (!(cl.codeDown || []).length) return `hopsesh can't bring the code of ${cl.title} sessions here yet.`;
  if (e.cloud.branch || cl.codeDown.includes("diff")) return "";
  return `${cl.title} has not pushed a branch for this session yet.`;
}

// newestCopy is the entry of a session's newest copy elsewhere, when it is listed.
function newestCopy(e) {
  const c = (e.copies || []).find((x) => x.newest && !(x.machine === e.machine && x.key === e.key));
  if (!c) return [null, null];
  return [c, entries().find((x) => x.machine === c.machine && x.key === c.key) || null];
}

const whereWord = (machine) => (machine === here() ? sys.here : machine);
const IN_TAB = "End it in hopsesh Terminal first";

// select is the list's selection (sessions.js sets it), for "Go to the newer copy".
let selectFn = () => {};
export function onSelect(fn) { selectFn = fn; }

// model is a session's actions and the words around them.
export function model(e) {
  return e.cloud ? cloudModel(e) : localModel(e);
}

function localModel(e) {
  const local = e.machine === here();
  const lv = liveOf(e);
  const m = { primary: null, chevron: [], fix: null, caption: "", move: [], more: more(e, local), places: [], twice: false, chevronLabel: "Other places" };
  const resumeItem = (p, older = false) => ({ id: "resume:" + p.id, label: `Resume ${older ? "this older copy " : ""}in ${p.name}`, sub: p.note, disabled:!!p.disabled, why:p.why, run: () => resumeIn(e, p.id, true),
    icon: p.id === "app" ? agentBadge(e.agent, e.agentName) : icon(p.id === "here" ? ["M3 4h18v16H3z", "m7 10 3 2.5L7 15M12 15h5"] : ["M3 4h18v16H3z", "M3 9h18"], 16) });
  if (local) {
    const places = placesOf(e);
    m.places = places;
    const n = placeCount(places);
    const tab = places.find((p) => p.kind === "hopsesh");
    if (places.length) {
      const first = tab || places.find(canShow) || places[0];
      const show = (p) => ({ id: "show:" + p.kind, label: canShow(p) ? (p.kind === "ide" ? `Show ${placeName(p, e)}` : `Show in ${placeName(p, e)}`) : `Running in ${placeName(p, e)}`, short: "Show",
        disabled: !canShow(p), why: canShow(p) ? "" : `hopsesh can't show a session in ${placeName(p, e)}`, run: () => showPlace(e, p) });
      m.primary = show(first);
      if (n >= 2) {
        m.twice = true;
        m.chevron = places.filter((p) => p !== first).map(show);
        m.caption = "Two copies writing one session can interleave; close one.";
      } else if (tab) {
        m.chevron = [{ id: "move-terminal", label: `Move to ${sys.terminal}…`, sub: "Ends it here and runs it there", run: () => moveToTerminal(tab.tabs[0]) }];
        m.chevronLabel = "Move it";
      } else if (first.kind === "ide") m.caption = sys.mac ? `hopsesh can't pick the tab inside ${placeName(first, e)}` : `hopsesh can't show a session in ${placeName(first, e)}`;
      else if (first.kind === "tmux" || first.kind === "ssh") { m.primary = null; m.caption = `Running in ${placeName(first, e)}; hopsesh can't show it`; }
      else if (first.kind === "claude-app" || first.kind === "codex-app") m.caption = "The app opens on its last view";
    } else if (/^(moved|continued)/.test(lv.status || "")) {
      const [c, newer] = newestCopy(e);
      const other = !c ? "" : c.machine === e.machine ? `the copy in ${c.agentName} is newer` : `the copy on ${c.machine} is newer`;
      m.primary = { id: "newer", label: "Go to the newer copy", short: "Go to newer", disabled: !newer, why: newer ? "" : "The newer copy isn't in the list", run: () => newer && selectFn(newer) };
      const p = resumePlaces(e).find((x) => x.id === defaultPlace(e));
      m.chevron = [{ id: "resume-anyway", label: "Resume this copy anyway…", sub: `In ${p.name}${other ? " · " + other : ""}`, run: () => resumeIn(e, p.id) }];
      m.chevronLabel = "Other choices";
    } else if (!e.hereNewest) {
      const [c, newer] = newestCopy(e);
      if (c && newer) m.primary = { id: "bring-newest", label: `Bring newest from ${whereWord(c.machine)}…`, short: "Bring…", run: () => planFor(newer, { target: "" }) };
      else m.primary = { id: "bring-newest", label: "Bring the newest copy…", short: "Bring…", disabled: true, why: "The newest copy is on a machine that isn't reached now" };
      m.chevron = resumePlaces(e).map((p) => resumeItem(p, true));
      m.chevronLabel = "Resume this older copy";
    } else {
      const ps = resumePlaces(e);
      const d = ps.find((p) => p.id === defaultPlace(e)) || ps[0];
      m.primary = { id: "resume:" + d.id, label: `Resume in ${d.name}`, short: "Resume", run: () => resumeIn(e, d.id) };
      m.chevron = ps.filter((p) => p !== d).map((p) => resumeItem(p));
      m.chevronLabel = "Resume elsewhere";
    }
    m.move = moveGroups(e, true, !!tab);
  } else {
    m.primary = { id: "bring", label: `Bring to ${sys.here}…`, short: "Bring…", run: () => planFor(e, { target: "" }) };
    if (lv.live) m.caption = `Still open on ${e.machine}; hopsesh marks that copy when it ends`;
    m.move = moveGroups(e, false, false);
  }
  return m;
}

function cloudModel(e) {
  const cl = cloudOf(e.machine);
  const why = cloudBlock(cl);
  const m = { primary: null, chevron: [], fix: null, caption: "", move: [], more: more(e, false), places: [], twice: false, chevronLabel: "Other ways to bring it" };
  const bring = (target, label) => ({ id: "bring" + (target ? ":" + target : ""), label, short: "Bring…", run: () => planFor(e, { target }) });
  if (cl?.codeOnly) {
    const no = why || noCode(e, cl);
    m.primary = { id: "code", label: "Get the code…", short: "Get the code…", disabled: !!no, why: no, run: () => planFor(e, { target: "", codeOnly: true }) };
    if (no) m.caption = no;
  } else if (e.bringIn) {
    m.primary = bring(e.bringIn.id, `Bring to ${sys.here}…`);
    m.chevron = e.continueIn.map((t) => bring(t.id, `Bring to ${sys.here} into ${t.name}…`));
  } else if (cl && !cl.codeOnly && !agentInfo(e.agent)?.capabilities?.includes("write")) {
    const no = "No agent here takes its messages: install Claude Code or Codex, or get the code only.";
    m.primary = { id: "bring", label: `Bring to ${sys.here}…`, short: "Bring…", disabled: true, why: why || no, run: () => {} };
    m.caption = why || no;
  } else {
    m.primary = bring("", `Bring to ${sys.here}…`);
    m.chevron = e.continueIn.map((t) => bring(t.id, `Bring to ${sys.here} into ${t.name}…`));
  }
  if (!cl?.codeOnly && (cl?.codeDown || []).length) {
    const diffDown = cl.codeDown[0] === "diff";
    m.chevron.push({ id: "code", label: "Get the code only…", disabled: !(e.cloud.branch || diffDown), why: e.cloud.branch || diffDown ? "" : "hopsesh doesn't know its branch yet: bring it with its conversation once",
      run: () => planFor(e, { target: "", codeOnly: true }) });
  }
  if (why) {
    // Blocked: the primary waits, and a button beside it fixes what blocks it.
    m.primary = Object.assign({}, m.primary, { disabled: true, why });
    m.chevron = m.chevron.map((x) => Object.assign({}, x, { disabled: true, why }));
    m.caption = why;
    if (cl && !cl.allowed) m.fix = { label: "Turn on", run: () => turnOnCloud(cl) };
    else if (cl && cl.status === "signed-out") m.fix = { label: "Sign in", run: () => signIn(cl) };
  }
  const hops = (e.hop || []).filter((t) => cloudOf(t.cloud)?.allowed).map((t) => handItem(t, () => planHop(e, t.cloud)));
  if (hops.length) m.move.push({ heading: "Cloud", items: [...hops, note(`It comes to ${sys.here} first; the next cloud gets a briefing.`)] });
  return m;
}

let turnOnCloud = () => {};
export function onTurnOn(fn) { turnOnCloud = fn; }

// handItem is "Hand off to ‹cloud›…", disabled with its reason when the cloud can't take
// it now.
function handItem(t, run) {
  const ok = t.ok;
  const plain = (x) => String(x || "").replace(/`/g, "");
  return { id: "handoff:" + t.cloud, label: `Hand off to ${t.title}…`, disabled: !ok, why: ok ? "" : cap(plain(t.why)), sub: ok ? [t.note, ...(t.limits || []).slice(0, 1)].filter(Boolean).map(plain).join(" · ") : "", title: t.cloud, icon: icon(ICONS.cloud, 16),
    chip: cloudOf(t.cloud)?.experimental ? h("span", { class: "chip st-warn mini" }, "experimental") : null, run };
}

// note is a line of words at the end of a menu's group.
const note = (text) => ({ node: h("div", { class: "pop-note", role: "presentation" }, text) });

// moveGroups is Move ▾: Machine, Agent and Cloud, each item visible, a disabled one with
// its reason.
function moveGroups(e, local, inTab) {
  const out = [];
  const block = (it) => (inTab ? Object.assign(it, { disabled: true, why: IN_TAB }) : it);
  const machine = [];
  if (local) {
    const peers = state.scan?.peers || [];
    for (const p of peers) machine.push(block({ id: "send:" + p, label: `Send to ${p}…`, icon: icon(ICONS.here, 16), run: () => planFor(e, { target: "", sendTo: p }) }));
    if (!peers.length) machine.push({ id: "send", label: "Send to another machine…", icon: icon(ICONS.here, 16), disabled: true, why: "No other machine with hopsesh is reached" });
  } else machine.push({ id: "bring", label: `Bring to ${sys.here}…`, icon: icon(ICONS.here, 16), run: () => planFor(e, { target: "" }) });
  out.push({ heading: "Machine", items: machine });
  if (["claude","codex"].includes(e.agent)) out.push({heading:"Account",items:[block({id:"account",label:"Move to another account…",sub:"Choose a profile and review the portable conversation",run:()=>planFor(e,{target:e.agent})})]});
  const agents = (e.continueIn || []).map((t) => block({ id: "continue:" + t.id, label: `Continue with ${t.name}${local ? "" : " on " + sys.here}…`, icon: agentBadge(t.id, t.name), run: () => planFor(e, { target: t.id }),
    chip: t.experimental ? h("span", { class: "chip st-warn mini" }, "experimental") : null }));
  if (agents.length) out.push({ heading: "Agent", items: agents });
  const clouds = (e.handoff || []).filter((t) => cloudOf(t.cloud)?.allowed).map((t) => block(handItem(t, () => planHandoff(e, t.cloud, t.bundle))));
  if (clouds.length) out.push({ heading: "Cloud", items: [...clouds, note("A cloud gets a briefing, not this conversation.")] });
  return out;
}

// more is ⋯: the session's folder, its file, its command and id, a new title, the
// transcript.
function more(e, local) {
  const onlyHere = local ? "" : `Only for sessions on ${sys.here}`;
  const out = [];
  if (!e.cloud) {
    out.push({ id: "shell", label: "Open a shell in its folder", disabled: !local, why: onlyHere, run: () => openShell(e) });
    out.push({ id: "reveal", label: sys.mac ? "Reveal in Finder" : sys.win ? "Show in Explorer" : "Show in Files", disabled: !local, why: onlyHere,
      run: () => api("RevealEntry", e.machine, e.key).catch(fail) });
    out.push({ id: "copy-command", label: "Copy resume command", disabled: !local, why: onlyHere, run: async () => {
      try { await api("CopyText", await api("ResumeCommand", e.machine, e.key)); toast("Copied the resume command"); } catch (err) { fail(err); }
    } });
  }
  out.push({ id: "copy-id", label: "Copy session ID", run: async () => { await api("CopyText", e.cloud ? e.cloud.id : e.session || e.key.split("/").pop()).catch(fail); toast("Copied the session ID"); } });
  if (!e.cloud) {
    out.push({ id: "rename", label: "Rename…", disabled: !e.canRename, why: e.canRename ? "" : `hopsesh can't rename ${e.agentName} sessions`, run: () => renameFn(e) });
    out.push({ id: "transcript", label: "Open transcript", disabled: !e.canPreview || !state.info?.previews,
      why: !e.canPreview ? `hopsesh can't read ${e.agentName} transcripts` : !state.info?.previews ? "Conversation previews are off (Settings → General)" : "", run: () => transcriptFn(e) });
  } else {
    const host = (e.cloud.url.match(/^https:\/\/([^/]+)/) || [])[1] || "its site";
    out.push({ id: "open-site", label: `Open on ${host} ↗`, run: () => api("OpenURL", e.cloud.url).catch(fail) });
    out.push({ id: "archive", label: "Archive", disabled: true, why: `${e.agentName} archives only on ${host}` });
  }
  return out;
}

let renameFn = () => {}, transcriptFn = () => {};
export function onInspector(rename, transcript) { renameFn = rename; transcriptFn = transcript; }

// actionsFor lists a session's actions flat, the primary first (the palette, ↩).
export function actionsFor(e) {
  const m = model(e);
  const out = [];
  if (m.primary && !m.primary.disabled) out.push(m.primary);
  for (const it of m.chevron) if (!it.disabled) out.push(it);
  for (const g of m.move) for (const it of g.items) if (it.run && !it.disabled && !out.some((x) => x.id === it.id)) out.push(it);
  for (const it of m.more) if (!it.disabled) out.push(it);
  return out;
}

// statusOf is a session's state for its row: [kind, words].
const CLOUD_STATES = { running: ["running", "Running"], idle: ["idle", "Idle"], done: ["done", "Done"], failed: ["error", "Failed"],
  archived: ["ended", "Archived"], unknown: ["unknown", "State not known"] };
export function statusOf(e) {
  if (e.cloud) {
    const st = CLOUD_STATES[e.cloud.state];
    if (st && e.cloud.state !== "unknown") return st;
    const host = (e.cloud.url.match(/^https:\/\/([^/]+)/) || [])[1];
    return ["unknown", host ? `Status on ${host}` : CLOUD_STATES.unknown[1]];
  }
  const lv = liveOf(e);
  const ts = e.machine === here() ? sessionTabs(e) : [];
  if (ts.length) return ts.some((t) => t.attention) ? ["needs", "Waiting for you"] : ["working", "Working"];
  if (lv.needs) return ["needs", "Needs you"];
  if (lv.live) return /idle/i.test(lv.status) ? ["idle", "Idle"] : ["working", "Working"];
  if (/^(moved|continued)/.test(lv.status || "")) return ["moved", cap(lv.status)];
  return ["ended", "Ended"];
}

// statusKey is a session's state as the Status filter and grouping know it: needs,
// working, idle, moved, ended or unknown (a cloud whose state its vendor doesn't tell).
export function statusKey(e) {
  if (e.cloud) {
    switch (e.cloud.state) {
      case "running": return "working";
      case "idle": return "idle";
      case "done": case "failed": case "archived": return "ended";
    }
    return "unknown";
  }
  const k = statusOf(e)[0];
  return k === "needs" || k === "working" || k === "idle" || k === "moved" ? k : "ended";
}

// statusLine is the inspector's status line: [dot kind, the state and where it runs,
// the rest]. "Working in iTerm2", "this Mac · 3 min ago".
export function statusLine(e) {
  const [k, words] = statusOf(e);
  if (e.cloud) {
    const cl = cloudOf(e.machine);
    const t = cloudTitle(e.machine);
    const first = e.history?.[0];
    const lead = { running: `Running in ${t}`, idle: `Idle in ${t}`, done: `Done in ${t}`, error: `Failed in ${t}`, ended: `Archived in ${t}` }[k] || `In ${t}`;
    return [k, lead, first ? `started ${ago(first.when)}` : e.lastActive ? ago(e.lastActive) : "", cl];
  }
  const ps = placesOf(e);
  const n = placeCount(ps);
  const where = e.machine === here() ? sys.here : e.machine;
  if (n >= 2) return ["needs", `${k === "needs" ? "Waiting for you" : k === "idle" ? "Idle" : "Working"} in ${count(ps.length > 1 ? ps.length : n, "place")}`, `${where} · ${ago(e.lastActive)}`, null, true];
  if (ps.length) {
    const p = ps[0];
    const lead = k === "needs" ? "Waiting for you" : k === "idle" ? "Idle" : "Working";
    return [k, `${lead} in ${placeName(p, e)}`, `${where} · ${ago(e.lastActive)}`];
  }
  const lv = liveOf(e);
  if (lv.live && e.machine !== here()) return [k, `${k === "needs" ? "Waiting for you" : k === "idle" ? "Idle" : "Working"} on ${e.machine}`, ago(e.lastActive)];
  if (k === "moved") return [k, words, ago(e.lastActive)];
  if (!e.hereNewest && e.machine === here()) {
    const [c] = newestCopy(e);
    if (c) return ["ended", "Older copy", `newest on ${whereWord(c.machine)}`];
  }
  return [k, words, `${where} · ${ago(e.lastActive)}`];
}
