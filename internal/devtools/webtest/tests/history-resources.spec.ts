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

test('History ledger limit persists, rejects intact history, and retries without truncation', async ({page}) => {
  test.setTimeout(120_000);
  await fresh(page);
  await row(page, 'Find the codeword').click();
  const original = await page.evaluate(async () => (await import('/core.js')).selected().path);
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

  await history(page);
  await saveLedger(page, '16');
  await page.reload();
  await history(page);
  await expect(page.getByLabel('Movement ledger', {exact:true})).toHaveValue('16');
  const settings = await page.request.post('/call', {data:{m:'HistorySettings', args:[]}});
  expect((await settings.json()).result).toMatchObject({lineageMB:16, effective:{lineageBytes:16 << 20}});
  await page.getByRole('button', {name:'Back to sessions', exact:true}).click();
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
  for (const path of [fixture.ledger, original + '.hopsesh.json']) {
    const graph = JSON.parse(await readFile(path, 'utf8'));
    const states = new Map(graph.states.map((s:any) => [s.id, digest(JSON.stringify(s))]));
    for (const state of JSON.parse(ledgerBefore.toString()).states) expect(states.get(state.id)).toBe(digest(JSON.stringify(state)));
    const fragments = graph.states.flatMap((s:any) => s.projection || []).flatMap((p:any) => p.fragments || []);
    expect(fragments.some((f:any) => Buffer.byteLength(f.text) === fixture.fragmentBytes && digest(f.text) === fixture.fragmentHash)).toBe(true);
  }
  expect(digest(await readFile(fixture.path))).toBe(digest(nativeBefore));
  expect(digest((await readFile(original)).subarray(0, originalBefore.length))).toBe(digest(originalBefore));
});
