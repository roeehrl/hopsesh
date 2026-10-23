import { test, expect, type Page } from '@playwright/test';
import { readFile } from 'node:fs/promises';
import { createHash } from 'node:crypto';
import { fresh, row, agentRow, action } from './helpers';

const digest = (value: string | Buffer) => createHash('sha256').update(value).digest('hex');
async function history(page: Page) {
  await page.locator('#btn-settings').click();
  await page.getByRole('tab', {name:'History', exact:true}).click();
  await expect(page.getByLabel('Movement ledger', {exact:true})).toBeVisible();
}
async function saveLedger(page: Page, value: string) {
  const saved = page.waitForResponse(r => r.url().endsWith('/call') && r.request().postDataJSON().m === 'SetHistorySettings');
  await page.getByLabel('Movement ledger', {exact:true}).selectOption(value);
  expect((await (await saved).json()).error).toBeFalsy();
  await expect(page.locator('.settings-fields')).toBeEnabled();
}

// The manifest beside a session, as hopsesh reads it.
async function graphOf(page: Page, session: {machine:string, key:string}) {
  const res = await page.request.get('/lineage', {params:session});
  expect(res.ok(), await res.text()).toBeTruthy();
  return res.json();
}

test('History ledger limit persists, rejects intact history, and retries without truncation', async ({page}) => {
  test.setTimeout(120_000);
  await fresh(page);
  await row(page, 'Find the codeword').click();
  const {path:original, ...originalSession} = await page.evaluate(async () => {
    const e = (await import('/core.js')).selected(); return {path:e.path, machine:e.machine, key:e.key};
  });
  await action(page, 'move', /^Continue with Codex/);
  await page.locator('#sheet #go').click();
  await page.getByRole('button', {name:'Back to sessions', exact:true}).click();
  await agentRow(page, 'Find the codeword', 'Codex').click();
  const source = await page.evaluate(async () => {
    const e = (await import('/core.js')).selected(); return {machine:e.machine, key:e.key};
  });
  const seeded = await page.request.post('/history-ledger', {params:source});
  expect(seeded.ok(), await seeded.text()).toBeTruthy();
  const fixture = await seeded.json();
  expect(fixture.bytes).toBeGreaterThan(16 << 20);
  const ledgerBefore = await readFile(fixture.ledger);
  const nativeBefore = await readFile(fixture.path);
  const originalBefore = await readFile(original);
  const originalLedgerBefore = await readFile(original + '.hopsesh.json');
  // The oversized fragment must represent new native work in the destination,
  // rather than making an unchanged conversation appear ready to move back.
  const nativeRecord = JSON.parse(nativeBefore.toString().trimEnd().split('\n').at(-1)!);
  expect(nativeRecord.payload).toMatchObject({type:'message', role:'assistant'});
  const nativeWork = nativeRecord.payload.content.map((c:any) => c.text || '').join('\n');
  expect(Buffer.byteLength(nativeWork)).toBe(fixture.fragmentBytes);
  expect(digest(nativeWork)).toBe(fixture.fragmentHash);
  expect(originalBefore.includes(Buffer.from('LEDGER-BEGIN\n'))).toBe(false);
  const seededGraph = await graphOf(page, source);
  const originalRevisions = new Set((await graphOf(page, originalSession)).revisions.map((r:any) => r.id));
  const destinationCoverage = seededGraph.states.flatMap((s:any) => s.projection || [])
    .flatMap((p:any) => p.fragments || []).filter((f:any) => digest(f.text) === fixture.fragmentHash)
    .flatMap((f:any) => f.coverage || []);
  expect(destinationCoverage.length).toBeGreaterThan(0);
  expect(destinationCoverage.every((id:string) => !originalRevisions.has(id))).toBe(true);

  await history(page);
  await saveLedger(page, '16');
  await page.reload();
  await history(page);
  await expect(page.getByLabel('Movement ledger', {exact:true})).toHaveValue('16');
  const settings = await page.request.post('/call', {data:{m:'HistorySettings', args:[]}});
  expect((await settings.json()).result).toMatchObject({lineageMB:16, effective:{lineageBytes:16 << 20}});
  await page.getByRole('button', {name:'Back to sessions', exact:true}).click();
  // Reload first publishes the saved pre-work return candidate. Wait for the
  // authoritative scan under the new limit before opening the retained menu.
  await expect(page.locator('#fresh')).toContainText('updated', {timeout:30_000});
  await agentRow(page, 'Find the codeword', 'Codex').click();
  await action(page, 'move', /^(Continue with|Move back to) Claude Code/);
  await expect(page.locator('#sheet')).toContainText('saved movement history reached its size limit');
  await expect(page.locator('#sheet')).toContainText('(16 MiB)');
  await expect(page.locator('#sheet')).toContainText('Existing history has not been truncated');
  expect(digest(await readFile(fixture.ledger))).toBe(digest(ledgerBefore));
  expect(digest(await readFile(fixture.path))).toBe(digest(nativeBefore));
  expect(digest(await readFile(original))).toBe(digest(originalBefore));
  expect(digest(await readFile(original + '.hopsesh.json'))).toBe(digest(originalLedgerBefore));

  await page.getByRole('button', {name:'Open history settings', exact:true}).click();
  await expect(page.getByRole('tab', {name:'History', exact:true})).toHaveAttribute('aria-selected', 'true');
  await saveLedger(page, '64');
  const defaults = await page.request.post('/call', {data:{m:'HistorySettings', args:[]}});
  expect((await defaults.json()).result).toMatchObject({lineageMB:0, effective:{lineageBytes:64 << 20}});
  await page.getByRole('button', {name:'Back to sessions', exact:true}).click();
  await agentRow(page, 'Find the codeword', 'Codex').click();
  const retried = page.waitForResponse(r => r.url().endsWith('/call') && r.request().postDataJSON().m === 'Plan');
  await action(page, 'move', /^(Continue with|Move back to) Claude Code/);
  expect((await (await retried).json()).error, 'Raising the ledger limit must clear the previous limit error without a manual refresh').toBeFalsy();
  await expect(page.locator('#sheet #go')).toBeEnabled();
  await page.locator('#sheet #go').click();
  await expect(page.getByRole('heading', {name:/is prepared for Claude Code/})).toBeVisible({timeout:30_000});
  for (const session of [source, originalSession]) {
    const graph = await graphOf(page, session);
    const states = new Map(graph.states.map((s:any) => [s.id, digest(JSON.stringify(s))]));
    for (const state of seededGraph.states) expect(states.get(state.id)).toBe(digest(JSON.stringify(state)));
    const fragments = graph.states.flatMap((s:any) => s.projection || []).flatMap((p:any) => p.fragments || []);
    expect(fragments.some((f:any) => Buffer.byteLength(f.text) === fixture.fragmentBytes && digest(f.text) === fixture.fragmentHash)).toBe(true);
  }
  expect(digest(await readFile(fixture.path))).toBe(digest(nativeBefore));
  expect(digest((await readFile(original)).subarray(0, originalBefore.length))).toBe(digest(originalBefore));
});
