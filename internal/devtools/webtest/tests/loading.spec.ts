import { test, expect, type Route } from '@playwright/test';
import { fresh, menu, row, details, action } from './helpers';
function gate() { let release!: () => void; const promise = new Promise<void>(r => { release = r; }); return { promise, release }; }
const error = (r: Route) => r.fulfill({ json: { error: 'Synthetic slow-service failure' } });
test.beforeEach(async ({ page }) => fresh(page));

for (const method of ['Machines', 'Accounts', 'Settings', 'Activity']) {
  test(`${method}: loading, inline failure and explicit retry`, async ({ page }, testInfo) => {
    const wait = gate(); let first = true;
    if (method === 'Accounts') await menu(page, 'settings');
    await page.route('**/call', async r => {
      if (r.request().postDataJSON().m === method && first) { first = false; await wait.promise; await error(r); }
      else await r.continue();
    });
    if (method === 'Accounts') await page.getByRole('button', { name: 'Accounts', exact: true }).click();
    else await menu(page, method.toLowerCase());
    await expect(page.locator('#view [role=status]')).toContainText(/Reading|Looking/);
    await expect(page.getByRole('heading', { name: 'All sessions' })).toHaveCount(0);
    if (method === "Machines") await page.screenshot({ path: testInfo.outputPath("machines-loading.png") });
    wait.release();
    await expect(page.locator('#view [role=alert]')).toContainText('Synthetic slow-service failure');
    await page.getByRole('button', { name: 'Try again', exact: true }).click();
    await expect(page.getByRole('heading', { name: method === 'Settings' ? 'General' : method, exact: true })).toBeVisible();
    await expect(page.locator('#view .load-error')).toHaveCount(0);
  });
}

test('an older Machines visit cannot overwrite a newer visit', async ({ page }) => {
  const wait = gate(); let n = 0;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'Machines' && ++n === 1) { await wait.promise; await error(r); }
    else await r.continue();
  });
  await menu(page, 'machines'); await expect(page.locator('#view [role=status]')).toBeVisible();
  await page.getByRole('button', { name: 'Back to sessions' }).click(); await menu(page, 'machines');
  await expect(page.getByRole('heading', { name: 'Machines', exact: true })).toBeVisible();
  wait.release(); await expect(page.locator('#view .load-error')).toHaveCount(0);
});

test('account save prevents duplicate submissions and preserves values on failure', async ({ page }) => {
  await menu(page, 'settings'); await page.getByRole('button', { name: 'Accounts', exact: true }).click();
  await page.getByRole('button', { name: 'Add account', exact: true }).click();
  const d = page.locator('dialog.account-editor'); await d.getByLabel('Name', { exact: true }).fill('Second personal account');
  const wait = gate(); let calls = 0;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'RegisterAccount') { calls++; await wait.promise; await error(r); } else await r.continue();
  });
  const submit = d.getByRole('button', { name: 'Add account', exact: true }); await submit.click();
  await expect(d.locator('form')).toHaveAttribute('aria-busy', 'true'); await expect(submit).toBeDisabled();
  await d.locator('form').evaluate((f: HTMLFormElement) => f.requestSubmit()); expect(calls).toBe(1);
  wait.release(); await expect(d.getByRole('alert')).toContainText('Synthetic slow-service failure');
  await expect(submit).toBeEnabled(); await expect(d.getByLabel('Name', { exact: true })).toHaveValue('Second personal account');
  await expect(page.locator('.operation-status')).toHaveCount(0);
});

test('terminal settings failure is retryable rather than an endless spinner', async ({ page }) => {
  let first = true;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'TerminalSettings' && first) { first = false; await error(r); } else await r.continue();
  });
  await menu(page, 'settings'); await page.getByRole('tab', { name: 'Terminal', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('Synthetic slow-service failure');
  await page.getByRole('button', { name: 'Try again' }).click();
  await expect(page.getByRole('combobox', { name: 'Font size' })).toBeVisible();
});

test('settings save locks conflicting controls until defaults are reloaded', async ({ page }) => {
  await menu(page, 'settings'); const wait = gate(); let calls = 0;
  await page.route('**/call', async r => { if (r.request().postDataJSON().m === 'SaveSettings') { calls++; await wait.promise; } await r.continue(); });
  const previews = page.getByRole('checkbox', { name: /Show conversation previews/ }); await previews.uncheck();
  await expect(page.getByRole('status').filter({ hasText: 'Saving changes…' })).toBeVisible();
  await expect(page.getByRole('checkbox', { name: /Show each agent/ })).toBeDisabled();
  wait.release(); await expect(previews).toBeEnabled(); await expect(previews).not.toBeChecked(); expect(calls).toBe(1);
});

test('launch progress deduplicates keyboard launch and clears on failure', async ({ page }) => {
  await row(page, 'What is the codeword in notes.txt?').click(); const wait = gate(); let calls = 0;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'ResumeSession') { calls++; await wait.promise; await error(r); } else await r.continue();
  });
  await details(page).locator('#act-primary').click();
  await expect(page.locator('.operation-status').filter({ hasText: /Opening/ })).toBeVisible();
  await row(page, 'What is the codeword in notes.txt?').press('ControlOrMeta+Enter'); expect(calls).toBe(1);
  wait.release(); await expect(page.locator('#toast')).toContainText('Synthetic slow-service failure');
  await expect(page.locator('.operation-status')).toHaveCount(0); await expect(details(page).locator('#act-primary')).toBeEnabled();
});

test('conversation preview has spoken loading feedback and an explicit retry', async ({ page }) => {
  let first = true; const wait = gate();
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'Preview' && first) { first = false; await wait.promise; await error(r); } else await r.continue();
  });
  await row(page, 'Find the codeword').click(); await expect(details(page).getByRole('status')).toContainText('Reading conversation…');
  wait.release(); await expect(details(page).getByText(/Preview not available/)).toBeVisible();
  await details(page).getByRole('button', { name: 'Try again' }).click(); await expect(details(page).locator('.msg').first()).toBeVisible();
});

test('quick access initial failure and refresh failure recover', async ({ page }) => {
  let initial = true;
  await page.route('**/call', async r => {
    const m = r.request().postDataJSON().m;
    // Keep the outage active through background publications, until the user
    // explicitly retries. One failed response can otherwise immediately recover.
    if (m === 'QuickSnapshot' && initial) await error(r);
    else if (m === 'QuickRefresh') await error(r); else await r.continue();
  });
  await page.goto('/quick.html'); await expect(page.getByRole('alert')).toContainText('Synthetic slow-service failure');
  initial = false;
  await page.getByRole('button', { name: 'Try again' }).click(); const refresh = page.locator('.quick-refresh');
  await expect(refresh).toBeEnabled(); await refresh.click(); await expect(refresh).toBeEnabled(); await expect(refresh).toContainText('Refresh');
  await expect(page.locator('#quick-error')).toContainText('Synthetic slow-service failure');
});

test('copy failure never claims success', async ({ page }) => {
  await row(page, 'Find the codeword').click();
  await page.route('**/call', async r => { if (r.request().postDataJSON().m === 'CopyText') await error(r); else await r.continue(); });
  await action(page, 'more', 'Copy session ID'); await expect(page.locator('#toast')).toContainText('Synthetic slow-service failure');
  await expect(page.locator('#toast')).not.toContainText('Copied');
});

test('new plan options wait for prior planning; obsolete results never enable Apply', async ({ page }) => {
  await row(page, 'Find the codeword').click(); await action(page, 'move', /^Continue with Codex…/);
  const sheet = page.locator('#sheet');
  await expect(sheet.getByRole('button', { name: /Continue in Codex/ })).toBeEnabled();
  const first = gate(), second = gate(); let plans = 0;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'Plan') { plans++; await (plans === 1 ? first.promise : second.promise); }
    await r.continue();
  });
  await sheet.getByRole('radio', { name: 'Briefing only' }).click();
  await expect(sheet.locator('#go')).toBeDisabled();
  await sheet.getByRole('radio', { name: 'History + bounded context' }).click();
  expect(plans).toBe(1); first.release();
  await expect.poll(() => plans).toBe(2);
  await expect(sheet.locator('#go')).toBeDisabled();
  second.release(); await expect(sheet.locator('#go')).toBeEnabled();
  await expect(sheet.locator('.box.kept')).toContainText('2 messages');
});

test('session refresh keeps results visible and exposes a persistent retry on failure', async ({ page }) => {
  const wait = gate(); let first = true;
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'Scan' && first) { first = false; await wait.promise; await error(r); } else await r.continue();
  });
  await page.locator('#btn-refresh').click();
  await expect(row(page, 'Find the codeword')).toBeVisible(); await expect(page.locator('#fresh')).toHaveText('Refreshing…');
  wait.release(); await expect(page.getByRole('alert')).toContainText('Showing the previous results');
  await page.getByRole('button', { name: 'Retry refresh' }).click(); await expect(page.getByRole('alert')).toHaveCount(0);
});

test('terminal window shows connection progress before its module loads', async ({ page }) => {
  const wait = gate(); await page.route('**/terminal/terminal.js', async r => { await wait.promise; await r.continue(); });
  await page.goto('/terminal/?renderer=dom', { waitUntil: 'commit' });
  await expect(page.locator('#connection')).toContainText('Connecting'); await expect(page.locator('#new-shell')).toBeDisabled();
  wait.release(); await expect(page.locator('#connection')).toBeHidden(); await expect(page.locator('#new-shell')).toBeEnabled();
});

for (const [url, module] of [['/quick.html', '**/quick.js'], ['/terminal/', '**/terminal/terminal.js']]) {
  test(`${url} shows module failure and reload recovery`, async ({ page }) => {
    let first = true;
    await page.route(module, async r => { if (first) { first = false; await r.abort('failed'); } else await r.continue(); });
    await page.goto(url); await expect(page.getByRole('alert')).toContainText('Couldn’t open this window');
    await page.getByRole('button', { name: 'Reload window' }).click();
    if (url === '/quick.html') await expect(page.locator('.quick-refresh')).toBeVisible();
    else await expect(page.locator('#connection')).toBeHidden();
  });
}

test('slow actions keep feedback after a menu closes and respect reduced motion', async ({ page }, testInfo) => {
  await row(page, 'What is the codeword in notes.txt?').click(); await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.clock.install(); const wait = gate();
  await page.route('**/call', async r => {
    if (r.request().postDataJSON().m === 'ResumeSession') { await wait.promise; await error(r); } else await r.continue();
  });
  await action(page, 'places', /^Resume in hopsesh Terminal/);
  await expect(page.getByRole('menu')).toHaveCount(0);
  await page.clock.fastForward(15001);
  const note = page.locator('.operation-status').filter({ hasText: 'Still working' }); await expect(note).toBeVisible();
  expect(await note.evaluate(el => getComputedStyle(el, '::before').animationName)).toBe('none');
  await page.screenshot({ path: testInfo.outputPath('slow-launch.png') });
  wait.release(); await expect(page.locator('.operation-status')).toHaveCount(0);
});
