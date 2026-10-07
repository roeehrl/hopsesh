// User-initiated work only. Background RPCs never generate a global spinner.
// Keep feedback outside the initiating control: menus close and rows can re-render.
const active = new Map();
const tracked = new WeakSet();
export function pending(label, work, key = Symbol()) {
  if (active.has(key)) return active.get(key);
  const note = document.createElement('div');
  note.className = 'operation-status';
  note.setAttribute('role', 'status');
  note.setAttribute('aria-live', 'polite');
  note.textContent = `${label.replace(/[.…]+$/, '')}…`;
  document.body.append(note);
  const timer = setTimeout(() => { note.textContent += ' Still working…'; layout(); }, 15000);
  // Defer work until the key is registered; keyboard and pointer entry points share it.
  const result = Promise.resolve().then(work).finally(() => {
    clearTimeout(timer);
    note.remove();
    active.delete(key);
    layout();
  });
  active.set(key, result);
  tracked.add(result);
  layout();
  return result;
}
function layout() {
  let bottom = 16;
  for (const note of document.querySelectorAll('.operation-status')) {
    note.style.bottom = `${bottom}px`;
    bottom += note.getBoundingClientRect().height + 8;
  }
}

// Declarative DOM handlers must return their work. Only asynchronous click/change/
// submit handlers participate; synchronous navigation, typing and polling stay quiet.
export function feedback(el, fn, report) {
  let running = false;
  return function (event) {
    if (running) { event.preventDefault(); return; }
    const submit = el.matches('form') ? el.querySelector('button[type=submit], input[type=submit]') : null;
    const label = el.dataset.busyLabel || submit?.textContent || el.getAttribute('aria-label') ||
      (el.matches('input,select') ? el.closest('label')?.textContent || 'Saving changes' : el.textContent) || 'Working';
    const controls = el.matches('form') ? [...el.querySelectorAll('input,select,textarea,button[type=submit]')] : [el];
    const disabled = controls.map(c => c.disabled);
    let result;
    try { result = fn.call(this, event); } catch (err) { report(err); return; }
    if (!result?.then) return result;
    running = true;
    el.setAttribute('aria-busy', 'true');
    el.classList.add('is-pending');
    submit?.classList.add('is-pending');
    const announcement = document.createElement('span');
    announcement.className = 'visually-hidden';
    announcement.setAttribute('role', 'status');
    announcement.textContent = label.trim() + ' — in progress';
    el.after(announcement);
    for (const c of controls) if ('disabled' in c) c.disabled = true;
    return (tracked.has(result) ? result : pending(label.trim().replace(/\s+/g, ' ').slice(0, 100), () => result)).catch(report).finally(() => {
      running = false;
      el.removeAttribute('aria-busy');
      el.classList.remove('is-pending');
      submit?.classList.remove('is-pending');
      announcement.remove();
      controls.forEach((c, i) => { if ('disabled' in c) c.disabled = disabled[i]; });
    });
  };
}
