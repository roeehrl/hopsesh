// The Accounts screen: the accounts you use, who each tool is signed in as on every
// machine (the grid), and the folders tools keep sessions in. A session keeps the account
// that ran it, and continues as whoever is signed in to the tool and machine it opens in.
import { api, h, fill, view, state, screen, go, toast, fail, ask, errText, current, navigationID, sys, on, ago, icon, ICONS, dialog, loading, loadError } from './core.js';

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

// ---- Your accounts ----------------------------------------------------------------
function personCard(p) {
  const agent = p.agent === 'codex' ? 'Codex' : 'Claude';
  return h('article', { class: 'person-card', 'data-person': p.id },
    h('div', { class: 'person-head' }, avatar(p, 34),
      h('div', { style: 'display:flex;flex-direction:column;gap:2px;min-width:0' },
        h('b', { class: 'person-name' }, p.unknown ? `A ${agent} account not signed in anywhere` : p.email),
        h('span', { class: 'muted', style: 'font-size:12.5px' }, p.unknown ? 'Seen only in session records' : p.labels.join(' · ') || 'Signed in'))),
    p.where.length ? h('ul', { class: 'person-where' }, p.where.map((w) => h('li', {}, w))) : null,
    p.unknown ? h('p', { class: 'muted', style: 'margin:0;font-size:12.5px' }, `Ran ${p.sessions} session${p.sessions === 1 ? '' : 's'}. Its email shows once it's signed in to any tool on any machine.`)
      : h('span', { class: 'muted', style: 'font-size:12.5px' }, p.sessions ? `Ran ${p.sessions} session${p.sessions === 1 ? '' : 's'} on ${p.machines} machine${p.machines === 1 ? '' : 's'}` : 'No sessions recorded yet'),
    p.unknown ? h('div', {}, h('button', { class: 'btn small', onclick: () => addDialog({ agent: p.agent || 'claude' }) }, 'Sign it in on a machine…')) : null);
}

// ---- Where you're signed in ---------------------------------------------------------
const TOOLS = [
  ['claude-terminal', 'Claude Code · terminal'],
  ['claude-desktop', 'Claude desktop'],
  ['codex-terminal', 'Codex · terminal'],
  ['codex-app', 'Codex app'],
];

function signInLine(s) {
  const p = person(s.person);
  const who = s.status === 'signed-in' ? (s.email || (p?.unknown ? 'an account not signed in to any tool here' : 'signed in'))
    : s.status === 'signed-out' ? 'Signed out' : s.status === 'failed' ? 'Check failed' : 'Unknown';
  return h('div', { class: 'signin ' + s.status, title: s.detail || '' },
    s.status === 'signed-in' ? avatar(s.person, 20) : h('span', { class: 'avatar empty', 'aria-hidden': 'true' }, s.status === 'signed-out' ? '–' : '?'),
    h('div', { style: 'display:flex;flex-direction:column;min-width:0' },
      h('span', { class: s.status === 'signed-in' ? '' : 'warn' }, who),
      h('span', { class: 'muted mono', style: 'font-size:11px' }, `in ${s.folder} · ${s.path}`),
      s.detail && s.status !== 'signed-in' ? h('span', { class: 'muted', style: 'font-size:11px' }, s.detail) : null));
}

function cell(c) {
  if (!c.installed) return h('div', { class: 'tcell none', role: 'cell' }, h('span', { class: 'muted' }, 'Not installed'));
  return h('div', { class: 'tcell', role: 'cell' },
    c.version ? h('span', { class: 'tver' }, c.version) : null,
    c.note && !c.version ? h('span', { class: 'tver' }, c.note) : null,
    c.signIns.map(signInLine),
    c.note && c.version ? h('span', { class: 'muted', style: 'font-size:11.5px' }, c.note) : null);
}

function machineHead(m) {
  const presence = m.local ? '' : { login: `hopsesh there · checked ${ago(m.checked)}`, installed: 'hopsesh there, not in a login session', older: `hopsesh ${m.hopsesh} there · update needed`, ssh: 'SSH only' }[m.presence] || '';
  return h('div', { class: 'tmach', role: 'rowheader' }, h('b', {}, m.local ? sys.Here : m.machine),
    h('span', { class: m.presence === 'ssh' || m.presence === 'older' ? 'warn' : 'muted', style: 'font-size:11.5px' }, m.local ? `checked ${ago(m.checked)}` : presence));
}

function grid(d) {
  const rows = d.machines.map((m) => {
    // A Mac read over SSH alone can't show its sign-ins (the Keychain is locked to SSH).
    if (!m.local && m.presence === 'ssh' && m.os === 'darwin') {
      return h('div', { class: 'trow', role: 'row' }, machineHead(m),
        h('div', { class: 'tcell wide', role: 'cell' },
          h('span', {}, `hopsesh reads ${m.machine}'s sessions over SSH, but can't see who is signed in there: macOS keeps sign-ins in the Keychain, which is locked to SSH.`),
          h('button', { class: 'btn small', style: 'align-self:flex-start', onclick: () => go('machines') }, `Set up hopsesh on ${m.machine}…`)));
    }
    return h('div', { class: 'trow', role: 'row' }, machineHead(m), TOOLS.map(([t]) => cell(m.cells.find((c) => c.tool === t) || { installed: false })));
  });
  return h('section', { class: 'card', 'aria-labelledby': 'where-h' },
    h('div', { class: 'card-h stacked' }, h('h2', { class: 'name', id: 'where-h' }, "Where you're signed in"),
      h('span', { class: 'muted', style: 'font-size:12.5px' }, "Each cell: the tool's version, then who it's signed in as in each folder. A folder is where a tool keeps its sessions; default is the tool's own (~/.claude, ~/.codex), others are ones you added. Each machine checks its own tools.")),
    h('div', { class: 'tgrid-wrap' }, h('div', { class: 'tgrid', role: 'table', 'aria-label': "Where you're signed in" },
      h('div', { class: 'trow head', role: 'row' }, h('div', { role: 'columnheader' }, 'Machine'), TOOLS.map(([, name]) => h('div', { role: 'columnheader' }, name))),
      rows.length ? rows : h('div', { class: 'empty' }, 'Check all machines to see their tools.'))));
}

// ---- Folders ------------------------------------------------------------------------------
function counts(f) {
  const parts = f.counts.map((c) => h('span', { class: 'count' }, `${c.n} `, avatar(c.person, 16), h('span', { class: 'sr-only' }, person(c.person)?.email || 'an unknown account')));
  if (f.unrecorded) parts.push(h('span', { class: 'count muted' }, `${f.unrecorded} unrecorded`));
  return parts.length ? h('div', { class: 'counts', 'aria-label': 'Sessions by the account that ran them' }, parts) : h('span', { class: 'muted', style: 'font-size:12px' }, 'No sessions yet');
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
    h('div', { style: 'display:flex;gap:8px;align-items:center;flex-wrap:wrap' },
      h('span', { class: 'chip agent-' + f.agent }, f.agentName),
      h('b', { class: 'mono', style: 'font-size:12.5px;overflow-wrap:anywhere' }, `${f.name} · ${f.path}`)),
    h('span', { style: 'font-size:12.5px' }, f.tools),
    f.command ? h('span', { class: 'muted', style: 'font-size:12px' }, 'Run it yourself with ', h('span', { class: 'mono' }, f.command)) : null,
    f.error ? h('span', { class: 'err', style: 'font-size:12px' }, f.error) : null,
    counts(f),
    h('div', { class: 'account-actions' },
      f.canRun ? h('button', { class: 'btn small', onclick: () => newSessionDialog(f) }, 'New session…') : null,
      !f.signedIn ? h('button', { class: 'btn small primary', disabled: !f.local && !f.canRun, title: !f.local && !f.canRun ? `Needs hopsesh running on ${f.machine}` : '', onclick: () => signIn() }, f.error ? 'Sign in again' : 'Sign in') : null,
      h('button', { class: 'btn icon small', disabled: checking.has(f.profile), title: 'Check the sign-in again', 'aria-label': `Check sign-in of ${f.name} on ${f.machine} again`, onclick: () => check(f) }, icon(ICONS.refresh, 13)),
      !f.default ? h('button', { class: 'btn small', onclick: () => editDialog(f) }, 'Edit') : null,
      !f.default ? h('button', { class: 'btn small danger forget', onclick: async () => {
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

export function addDialog({ agent = 'claude', machine = '' } = {}) {
  const ms = machineOptions();
  const pick = ms.find((m) => m.name === machine && !m.why) || ms.find((m) => m.local) || ms[0];
  const agentSel = h('div', { role: 'radiogroup', 'aria-label': 'Agent', class: 'radios' },
    ['claude', 'codex'].map((a) => h('label', {}, h('input', { type: 'radio', name: 'add-agent', value: a, checked: a === agent }), a === 'claude' ? 'Claude Code' : 'Codex')));
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
    fill(what, 'A folder ', h('span', { class: 'mono' }, `~/.hopsesh/accounts/${a}/${slug()}`), ` on ${there}, signed in to ${a === 'claude' ? 'Claude Code' : 'Codex'}. Your other sign-ins aren't touched.`,
      a === 'claude' ? ' Claude desktop always uses ~/.claude, so sessions here open in a terminal.' : ' Codex keeps its sign-in in it the way your default folder does.');
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
        try { await api('NewSessionOn', f.local ? '' : f.machine, f.agent, f.default ? '' : f.profile, dir.value.trim().replace(/^~(?=\/|$)/, '')); d.close(); } catch (e) { error.textContent = errText(e); }
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
      known.length || unknown.length ? h('div', { class: 'person-grid' }, [...known, ...unknown].map(personCard))
        : h('div', { class: 'empty' }, busy ? 'Checking your machines…' : 'No accounts yet. Check all machines, or add one.')),
    grid(d), folders(d))));
}

screen('accounts', async () => {
  if (!data) loading('Reading your accounts…');
  const visit = navigationID();
  await reload();
  if (visit === navigationID() && data && !data.machines.length) await scan();
});
