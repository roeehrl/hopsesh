// The Accounts screen: the accounts you use, who each tool is signed in as on every
// machine (the grid), and the folders tools keep sessions in. A session keeps the account
// that ran it, and continues as whoever is signed in to the tool and machine it opens in.
import { api, h, fill, view, state, screen, go, toast, fail, ask, errText, current, navigationID, sys, on, ago, icon, ICONS, dialog, loading, loadError, agentInfo, agentShort, localAgents, RESTART } from './core.js';

let data = null, busy = false, problem = '';
const checking = new Set();
const openFolders = new Map();

let read = 0;
async function reload() {
  const n = ++read, visit = navigationID();
  try {
    const v = await api('AccountsView');
    if (n !== read || visit !== navigationID()) return;
    data = v;
    if (current === 'accounts') render();
  } catch (e) {
    if (n === read && visit === navigationID() && current === 'accounts') fill(view, loadError(e, reload));
  }
}
on('hopsesh:accounts', () => { if (current === 'accounts') reload().catch(fail); });
on('hopsesh:signed-in', () => { if (current === 'accounts') scan(); });

async function scan() {
  if (busy) return;
  busy = true; problem = ''; render();
  try { state.scan = await api('ScanAccounts'); state.stale = false; await reload(); }
  catch (e) { problem = errText(e); }
  finally { busy = false; if (current === 'accounts') render(); }
}

const person = (id) => data?.people.find((p) => p.id === id);
const machineWord = (name) => (data?.machines.find((m) => m.machine === name)?.local ? sys.Here : name);

// avatar is an account's initials in its color (its email is beside it, never color alone).
function avatar(p, size = 22) {
  const id = typeof p === 'string' ? p : p?.id;
  const x = typeof p === 'string' ? person(p) : p;
  if (!x) return h('span', { class: 'avatar unknown', style: `width:${size}px;height:${size}px`, 'aria-hidden': 'true' }, '?');
  return h('span', { class: 'avatar' + (x.unknown ? ' unknown' : ''), style: `width:${size}px;height:${size}px;--hue:${x.hue}`, 'aria-hidden': 'true', 'data-person': id }, x.initials);
}

// pathText is a path that wraps after its slashes, not inside a folder's name.
const pathText = (p) => p.split(/(?<=\/)/).flatMap((part, i) => (i ? [h('wbr'), part] : [part]));

// ---- Your accounts ----------------------------------------------------------------
const agentWord = (a) => agentShort(a);
// accountAgents are the agents that take account folders, in display order.
const accountAgents = () => localAgents().filter((a) => a.accounts);
const sessionsWord = (n) => `${n} session${n === 1 ? '' : 's'}`;
// unknownName tells an account seen only in records apart from the others: "Claude account 1".
const unknownName = (p) => `${agentWord(p.agent)} account ${p.number || ''}`.trim();

function personCard(p) {
  return h('article', { class: 'person-card', 'data-person': p.id },
    h('div', { class: 'person-head' }, avatar(p, 32),
      h('div', { class: 'person-id' },
        h('b', { class: 'person-name' }, p.email),
        h('span', { class: 'muted' }, p.labels.join(' · ') || 'Signed in'))),
    p.where.length ? h('ul', { class: 'person-where' }, p.where.map((w) => {
      const [machine, tools] = w.split(/: (.*)/s);
      return h('li', {}, h('b', {}, machine), h('span', {}, tools));
    })) : null,
    h('span', { class: 'person-foot muted' }, p.sessions ? `Ran ${sessionsWord(p.sessions)} on ${p.machines} machine${p.machines === 1 ? '' : 's'}` : 'No sessions recorded yet'));
}

// unknownCard is every account seen only in session records, in one card: each ran
// sessions, but no tool on any machine is signed in to it now, so its email isn't known.
function unknownCard(ps) {
  return h('article', { class: 'person-card unknown-card', 'data-person': 'unknown' },
    h('div', { class: 'person-id' },
      h('b', { class: 'person-name' }, ps.length === 1 ? 'An account not signed in anywhere' : `${ps.length} accounts not signed in anywhere`),
      h('span', { class: 'muted' }, 'They ran sessions, but no tool on any machine is signed in to them now. Sign one in to see its email.')),
    h('ul', { class: 'unknown-list' }, ps.map((p) => h('li', { 'data-person': p.id }, avatar(p, 22),
      h('span', {}, h('b', {}, unknownName(p)), h('span', { class: 'muted' }, ` · ${sessionsWord(p.sessions)}${p.ranOn?.length ? ' on ' + p.ranOn.join(', ') : ''}`))))),
    h('div', { class: 'person-foot' }, h('button', { class: 'btn small', onclick: () => addDialog({ agent: ps[0].agent || '' }) }, 'Sign one in on a machine…')));
}

// ---- Where you're signed in ---------------------------------------------------------
// tools are the grid's columns, [id, name]: each agent's terminal, then its desktop app.
const tools = () => localAgents().flatMap((a) => (a.tools || []).map((t) => [t.id, t.name]));

function signInLine(s) {
  const p = person(s.person);
  const who = s.status === 'signed-in' ? (s.email || (p?.unknown ? unknownName(p) : 'Signed in'))
    : s.status === 'signed-out' ? 'Signed out' : s.status === 'failed' ? 'Check failed' : 'Not known';
  return h('div', { class: 'signin ' + s.status },
    s.status === 'signed-in' ? avatar(s.person, 20) : h('span', { class: 'avatar none', 'aria-hidden': 'true' }, s.status === 'signed-out' ? '–' : '?'),
    h('div', { class: 'signin-text' },
      h('span', { class: s.status === 'signed-in' ? 'si-who' : 'si-who warn' }, who),
      h('span', { class: 'si-where muted', title: s.path }, h('span', {}, s.folder === 'default' ? 'default folder' : s.folder), ' ', h('span', { class: 'mono si-path' }, pathText(s.path))),
      s.detail && s.status !== 'signed-in' ? h('span', { class: 'si-detail muted' }, s.detail) : null));
}

function cell(c) {
  if (!c.installed) return h('div', { class: 'tcell none', role: 'cell' }, h('span', { class: 'muted' }, 'Not installed'));
  return h('div', { class: 'tcell', role: 'cell' },
    c.version ? h('span', { class: 'tver' }, c.version) : null,
    c.note && !c.version ? h('span', { class: 'tver' }, c.note) : null,
    c.signIns.map(signInLine),
    c.note && c.version ? h('span', { class: 'tnote muted' }, c.note) : null);
}

function machineHead(m) {
  const presence = m.local ? '' : { login: `hopsesh there · checked ${ago(m.checked)}`, installed: 'hopsesh there, not in a login session', older: `hopsesh ${m.hopsesh} there · update needed`, ssh: 'SSH only' }[m.presence] || '';
  return h('div', { class: 'tmach', role: 'rowheader' }, h('b', {}, m.local ? sys.Here : m.machine),
    h('span', { class: m.presence === 'ssh' || m.presence === 'older' ? 'warn' : 'muted', style: 'font-size:11.5px' }, m.local ? `checked ${ago(m.checked)}` : presence));
}

function grid(d) {
  const cols = tools();
  const rows = d.machines.map((m) => {
    // A Mac read over SSH alone can't show its sign-ins (the Keychain is locked to SSH).
    if (!m.local && m.presence === 'ssh' && m.os === 'darwin') {
      return h('div', { class: 'trow', role: 'row' }, machineHead(m),
        h('div', { class: 'tcell wide', role: 'cell' },
          h('span', {}, `hopsesh reads ${m.machine}'s sessions over SSH, but can't see who is signed in there: macOS keeps sign-ins in the Keychain, which is locked to SSH.`),
          h('button', { class: 'btn small', style: 'align-self:flex-start', onclick: () => go('machines') }, `Set up hopsesh on ${m.machine}…`)));
    }
    return h('div', { class: 'trow', role: 'row' }, machineHead(m), cols.map(([t]) => cell(m.cells.find((c) => c.tool === t) || { installed: false })));
  });
  return h('section', { class: 'card', 'aria-labelledby': 'where-h' },
    h('div', { class: 'card-h stacked' }, h('h2', { class: 'name', id: 'where-h' }, "Where you're signed in"),
      h('span', { class: 'muted card-sub' }, "Who each tool is signed in as, in each folder it keeps sessions in.")),
    h('div', { class: 'tgrid-wrap' }, h('div', { class: 'tgrid', role: 'table', 'aria-label': "Where you're signed in", style: `--tools:${cols.length}` },
      h('div', { class: 'trow head', role: 'row' }, h('div', { role: 'columnheader' }, 'Machine'), cols.map(([, name]) => h('div', { role: 'columnheader' }, name))),
      rows.length ? rows : h('div', { class: 'empty' }, 'Check all machines to see their tools.'))));
}

// ---- Folders ------------------------------------------------------------------------------
function counts(f) {
  const total = f.counts.reduce((n, c) => n + c.n, 0) + f.unrecorded;
  if (!total) return h('span', { class: 'counts muted' }, 'No sessions yet');
  const parts = f.counts.map((c) => {
    const p = person(c.person), who = p?.unknown ? unknownName(p) : p?.email || 'an unknown account';
    return h('span', { class: 'count', title: `${sessionsWord(c.n)} by ${who}` }, avatar(c.person, 16), `${c.n}`, h('span', { class: 'sr-only' }, ` by ${who}`));
  });
  if (f.unrecorded) parts.push(h('span', { class: 'count muted', title: "Sessions that don't record which account ran them" }, `${f.unrecorded} with no account recorded`));
  return h('div', { class: 'counts', 'aria-label': 'Sessions by the account that ran them' }, h('span', { class: 'muted' }, sessionsWord(total)), parts);
}

function folderCard(f) {
  const remote = !f.local;
  const signIn = async (atScreen = false) => {
    try {
      if (remote) await api('SignInOn', f.machine, f.agent, f.profile, atScreen);
      else await api('LoginAccount', f.profile);
      toast('Finish signing in in the tab. The folder updates once you do.');
    } catch (e) { fail(e); }
  };
  return h('article', { class: 'folder-card', 'data-profile': f.profile },
    h('div', { class: 'folder-head' },
      h('span', { class: 'chip agent-' + f.agent }, f.agentName),
      h('b', { class: 'folder-name' }, f.name)),
    h('span', { class: 'folder-path mono muted', title: f.path }, pathText(f.path)),
    h('span', { class: 'folder-tools' }, f.tools),
    f.command ? h('span', { class: 'muted folder-line' }, 'Run it yourself with ', h('span', { class: 'mono' }, f.command)) : null,
    f.port ? h('span', { class: 'muted folder-line' }, `Its ${f.agentName} service listens on port `, h('span', { class: 'mono' }, String(f.port))) : null,
    f.error ? h('span', { class: 'err folder-line' }, f.error) : null,
    counts(f),
    h('div', { class: 'account-actions' },
      f.canRun ? h('button', { class: 'btn small', onclick: () => newSessionDialog(f) }, 'New session…') : null,
      !f.signedIn ? h('button', { class: 'btn small primary', disabled: !f.local && !f.canRun, title: !f.local && !f.canRun ? `Needs hopsesh running on ${f.machine}` : '', onclick: () => signIn() }, f.error ? 'Sign in again' : 'Sign in') : null,
      h('button', { class: 'btn icon small', disabled: checking.has(f.profile), title: 'Check the sign-in again', 'aria-label': `Check sign-in of ${f.name} on ${f.machine} again`, onclick: () => check(f) }, icon(ICONS.refresh, 13)),
      !f.default ? h('button', { class: 'btn small', onclick: () => editDialog(f) }, 'Edit') : null,
      !f.default ? h('button', { class: 'btn small quiet forget', onclick: async () => {
        if (!await ask({ title: `Forget “${f.name}”?`, body: `hopsesh stops listing it${f.command ? ' and removes the ' + f.command + ' command' : ''}. The folder ${f.path} and its sign-in stay on ${machineWord(f.machine)}.`, ok: 'Forget' })) return;
        try { await api('ForgetAccount', f.profile, f.generation); await scan(); } catch (e) { fail(e); }
      } }, 'Forget') : null));
}

async function check(f) {
  if (checking.has(f.profile)) return;
  checking.add(f.profile); render();
  try { await api('RefreshAccount', f.profile); state.scan = await api('ScanSnapshot'); } catch (e) { problem = errText(e); }
  finally { checking.delete(f.profile); await reload(); }
}

function folders(d) {
  const byMachine = new Map();
  for (const f of d.folders) { if (!byMachine.has(f.machine)) byMachine.set(f.machine, []); byMachine.get(f.machine).push(f); }
  return h('section', { class: 'card', 'aria-labelledby': 'folders-h' },
    h('div', { class: 'card-h' }, h('h2', { class: 'name', id: 'folders-h' }, 'Folders')),
    [...byMachine].map(([machine, fs]) => {
      const k = machine, local = fs[0]?.local;
      return h('details', { class: 'account-group', 'data-group': 'machine:' + machine, open: openFolders.get(k) ?? local, ontoggle: (ev) => { if (ev.target.isConnected) openFolders.set(k, ev.target.open); } },
        h('summary', {}, h('span', {}, `${local ? sys.Here : machine} · ${fs.length} folder${fs.length === 1 ? '' : 's'}`)),
        h('div', { class: 'folder-grid' }, fs.map(folderCard)));
    }));
}

// ---- Dialogs -------------------------------------------------------------------------------
function machineOptions() {
  return (data?.machines || []).map((m) => {
    const why = m.local ? '' : { ssh: 'set up hopsesh there first', older: 'update hopsesh there first', installed: 'nobody is logged in there' }[m.presence] ?? (m.presence === 'login' ? '' : 'not reached');
    return { name: m.machine, local: m.local, label: m.local ? sys.Here : m.machine, why };
  });
}

export function addDialog({ agent = '', machine = '' } = {}) {
  const ms = machineOptions();
  const pick = ms.find((m) => m.name === machine && !m.why) || ms.find((m) => m.local) || ms[0];
  const choices = accountAgents();
  if (!choices.some((a) => a.id === agent)) agent = choices[0]?.id || '';
  const agentSel = h('div', { role: 'radiogroup', 'aria-label': 'Agent', class: 'radios' },
    choices.map((a) => h('label', {}, h('input', { type: 'radio', name: 'add-agent', value: a.id, checked: a.id === agent }), a.name)));
  const machineSel = h('select', { class: 'field', 'aria-label': 'On which machine' }, ms.map((m) => h('option', { value: m.name, disabled: !!m.why, selected: m === pick }, m.label + (m.why ? ' · ' + m.why : ''))));
  const name = h('input', { class: 'field', maxlength: 120, placeholder: 'work', 'aria-label': 'Name' });
  const cmd = h('input', { type: 'checkbox', checked: !sys.win });
  const atScreen = h('input', { type: 'radio', name: 'add-where' });
  const fromHere = h('input', { type: 'radio', name: 'add-where', checked: true });
  const where = h('div', { class: 'add-where' });
  const what = h('p', { class: 'muted', style: 'font-size:12.5px;line-height:1.5;margin:0' });
  const cmdLabel = h('span', {});
  const error = h('p', { class: 'err', role: 'alert', style: 'margin:0' });
  const slug = () => name.value.trim().toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '') || 'work';
  const sync = () => {
    const a = agentSel.querySelector('input:checked').value, m = ms.find((x) => x.name === machineSel.value);
    const remote = m && !m.local, there = m?.label || '';
    fill(cmdLabel, 'Add the ', h('span', { class: 'mono' }, `${a}-${slug()}`), ` command to my shell on ${there}`);
    const info = agentInfo(a) || { name: a };
    // An app with its own sign-in keeps to the default folder; a folder that copies the
    // default one's settings keeps its sign-in the same way.
    const how = info.desktop?.ownSignIn ? ` ${info.desktop.tool} always uses ${info.defaultRoot}, so sessions here open in a terminal.`
      : info.inheritsSignIn ? ` ${info.name} keeps its sign-in in it the way your default folder does.` : '';
    fill(what, 'A folder ', h('span', { class: 'mono' }, `~/.hopsesh/accounts/${a}/${slug()}`), ` on ${there}, signed in to ${info.name}. Your other sign-ins aren't touched.`, how);
    fill(where, remote ? [h('span', { style: 'font-size:12.5px;font-weight:500' }, 'Sign in'),
      h('label', { class: 'opt' }, fromHere, h('span', {}, h('b', {}, 'From here'), h('span', { class: 'muted' }, `A tab here shows the sign-in; open its link in your browser on ${sys.here}. The account you choose there is the one ${there} uses.`))),
      h('label', { class: 'opt' }, atScreen, h('span', {}, h('b', {}, `On ${there}'s screen`), h('span', { class: 'muted' }, `For when you're sitting at ${there}.`)))] : []);
  };
  agentSel.onchange = sync; machineSel.onchange = sync; name.oninput = sync;
  const submit = h('button', { class: 'btn primary' }, 'Create and sign in');
  submit.onclick = async () => {
    error.textContent = '';
    if (!name.value.trim()) { error.textContent = 'Give it a name.'; return; }
    const a = agentSel.querySelector('input:checked').value, m = ms.find((x) => x.name === machineSel.value);
    submit.disabled = true;
    try {
      const p = await api('AddAccountFolder', m.local ? '' : m.name, a, name.value.trim(), [], cmd.checked);
      if (m.local) await api('LoginAccount', p.id);
      else await api('SignInOn', m.name, a, p.id, atScreen.checked);
      d.close();
      toast(`“${p.name}” is made on ${m.label}. Finish signing in in the tab; it shows here when you have.`);
      await scan();
    } catch (e) { error.textContent = errText(e); submit.disabled = false; }
  };
  const d = dialog(h('h2', { style: 'margin:0;font-size:16px' }, 'Add an account'),
    h('ul', { class: 'setup-what' }, h('li', {}, 'Keep going when one account hits its limit: continue any session as this one.'), h('li', {}, 'Use both at once, without signing the other out.'), h('li', {}, 'Keep client or work history apart from personal.')),
    h('span', { style: 'font-size:12.5px;font-weight:500' }, 'Agent'), agentSel,
    h('label', { style: 'display:flex;flex-direction:column;gap:6px;font-size:12.5px;font-weight:500' }, 'On which machine', machineSel),
    h('label', { style: 'display:flex;flex-direction:column;gap:6px;font-size:12.5px;font-weight:500' }, 'Name', name),
    where, h('span', { style: 'font-size:12.5px;font-weight:500' }, 'What this creates'), what,
    sys.win ? null : h('label', { class: 'opt' }, cmd, cmdLabel), error,
    h('div', { class: 'dlg-foot' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Cancel'), submit));
  sync(); name.focus();
}

function editDialog(f) {
  const name = h('input', { class: 'field', value: f.name, maxlength: 120, 'aria-label': 'Name' });
  const tags = h('input', { class: 'field', value: (f.tags || []).join(', '), placeholder: 'Personal, Research, Client A', 'aria-label': 'Tags' });
  const error = h('p', { class: 'err', role: 'alert', style: 'margin:0' });
  const d = dialog(h('h2', { style: 'margin:0;font-size:16px' }, `Edit “${f.name}”`),
    h('label', { style: 'display:flex;flex-direction:column;gap:6px;font-size:12.5px' }, 'Name', name),
    h('label', { style: 'display:flex;flex-direction:column;gap:6px;font-size:12.5px' }, 'Tags, separated by commas', tags), error,
    h('div', { class: 'dlg-foot' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Cancel'),
      h('button', { class: 'btn primary', onclick: async () => {
        try { await api('EditAccount', f.profile, name.value, tags.value.split(','), f.generation); d.close(); await reload(); } catch (e) { error.textContent = errText(e); }
      } }, 'Save')));
  name.focus();
}

function newSessionDialog(f) {
  const dir = h('input', { class: 'field mono', placeholder: f.local ? '~/git/my-repo' : `a folder on ${f.machine}`, 'aria-label': 'Folder' });
  const error = h('p', { class: 'err', role: 'alert', style: 'margin:0' });
  const who = f.signedIn ? f.tools.match(/\(([^)]*)\)/)?.[1] || 'its account' : 'its account';
  const d = dialog(h('h2', { style: 'margin:0;font-size:16px' }, `New ${f.agentName} session in “${f.name}”`),
    h('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, f.local ? `It runs here as ${who}.` : `It runs on ${f.machine} as ${who}, in your login session there; you work in it here.`),
    h('label', { style: 'display:flex;flex-direction:column;gap:6px;font-size:12.5px' }, 'In folder (empty: the home folder)', dir), error,
    h('div', { class: 'dlg-foot' }, h('button', { class: 'btn', onclick: () => d.close() }, 'Cancel'),
      h('button', { class: 'btn primary', onclick: async () => {
        const args = [f.local ? '' : f.machine, f.agent, f.default ? '' : f.profile, dir.value.trim().replace(/^~(?=\/|$)/, '')];
        try { await api('NewSessionOn', ...args); d.close(); } catch (e) {
          const msg = errText(e);
          if (!msg.startsWith(RESTART)) { error.textContent = msg; return; }
          // Decision D2: starting it would restart the agent's background service.
          d.close();
          if (!await ask({ title: `Restart ${f.agentName}'s background service?`, body: msg.slice(RESTART.length), ok: 'Restart it and start', danger: true })) return;
          try { await api('AllowNewSessionRestart', args[0], f.agent, args[2]); await api('NewSessionOn', ...args); } catch (e2) { fail(e2); }
        }
      } }, 'Start')));
  dir.focus();
}

// ---- The screen ----------------------------------------------------------------------------
function render() {
  if (current !== 'accounts' || !data) return;
  const d = data;
  const known = d.people.filter((p) => !p.unknown), unknown = d.people.filter((p) => p.unknown);
  fill(view, h('div', { class: 'page' }, h('div', { class: 'page-in accounts-page' },
    h('div', { class: 'account-toolbar' }, h('h1', {}, 'Accounts'), h('span', { class: 'spacer' }),
      h('button', { class: 'btn', disabled: busy, onclick: scan }, busy ? 'Checking…' : 'Check all machines'),
      h('button', { class: 'btn primary', onclick: () => addDialog() }, 'Add an account…')),
    h('p', { class: 'muted', style: 'margin-top:0' }, 'Who each tool is signed in as, on every machine. A session keeps the account that ran it, and continues as whoever is signed in to the tool and machine you open it in.'),
    problem ? h('p', { class: 'err', role: 'alert' }, problem) : null,
    d.move.length ? h('div', { class: 'banner warn', role: 'status' },
      h('span', { style: 'flex:1 1 300px' }, `${d.move.length} account folder${d.move.length === 1 ? ' is' : 's are'} in hopsesh's state folder, where resetting hopsesh would delete ${d.move.length === 1 ? 'it' : 'them'}. Move ${d.move.length === 1 ? 'it' : 'them'} to ~/.hopsesh/accounts? Sessions and sign-ins move along.`),
      h('button', { class: 'btn small primary', onclick: async () => {
        if (!await ask({ title: 'Move the account folders?', body: d.move.map((m) => `${m.name}: ${m.from} → ${m.to}`).join('\n'), ok: 'Move them' })) return;
        try { const done = await api('MoveAccountFolders'); toast(`Moved ${done.length} folder${done.length === 1 ? '' : 's'}`); await scan(); } catch (e) { fail(e); }
      } }, 'Move them')) : null,
    d.notes.map((n) => h('div', { class: 'banner', role: 'note' }, h('b', {}, n.title), h('span', {}, n.text))),
    h('section', { class: 'card', 'aria-labelledby': 'people-h' },
      h('div', { class: 'card-h' }, h('h2', { class: 'name', id: 'people-h' }, 'Your accounts')),
      known.length || unknown.length ? h('div', { class: 'person-grid' }, known.map(personCard), unknown.length ? unknownCard(unknown) : null)
        : h('div', { class: 'empty' }, busy ? 'Checking your machines…' : 'No accounts yet. Check all machines, or add one.')),
    grid(d), folders(d))));
}

screen('accounts', async () => {
  if (!data) loading('Reading your accounts…');
  const visit = navigationID();
  await reload();
  if (visit === navigationID() && data && !data.machines.length) await scan();
});
