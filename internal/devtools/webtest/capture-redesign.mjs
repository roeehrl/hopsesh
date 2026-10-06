// Capture the sessions redesign against a separate webtest fixture server (never a
// user's app). Start it on 8766, then: SHOTS_DIR=/path/to/shots node capture-redesign.mjs
import { chromium, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';
const out = process.env.SHOTS_DIR;
if (!out) throw new Error('Set SHOTS_DIR for the screenshots');
const url = process.env.WEBTEST_URL || 'http://127.0.0.1:8766';
if (!['localhost', '127.0.0.1'].includes(new URL(url).hostname)) throw new Error('Use a local webtest fixture server');
mkdirSync(out, { recursive: true });
const browser = await chromium.launch();
try {
  for (const [width, height] of [[1280, 820], [900, 600]]) for (const colorScheme of ['light', 'dark']) {
    const page = await browser.newPage({ viewport: { width, height }, colorScheme });
    const errors = [];
    page.on('pageerror', (err) => errors.push(err.message));
    expect((await page.request.post(`${url}/reset`)).ok()).toBeTruthy();
    await page.goto(url);
    const row = page.locator('.row').filter({ has: page.locator('.t').getByText('Find the codeword', { exact: true }) });
    await row.click();
    await expect(page.locator('.conv .msg.agent')).toBeVisible();
    const shot = (name) => page.screenshot({ path: join(out, `${name}-${width}x${height}-${colorScheme}.png`) });
    await shot('01-inspector');
    await page.locator('#btn-display').click();
    await shot('02-display');
    await page.keyboard.press('Escape');
    await page.locator('#btn-filter').click();
    await shot('03-filter');
    await page.keyboard.press('Escape');
    await page.locator('#act-chevron').click();
    await shot('04-resume-places');
    await page.keyboard.press('Escape');
    await page.locator('#act-move').click();
    await shot('05-move');
    await page.keyboard.press('Escape');
    await page.locator('.divider[data-pane="inspector"]').focus();
    await page.keyboard.press('End');
    await shot('06-wide-inspector');
    expect((await page.request.post(`${url}/reset`)).ok()).toBeTruthy();
    expect((await page.request.post(`${url}/seed?n=209`)).ok()).toBeTruthy();
    await page.reload();
    await expect(page.locator('#list-count')).toHaveText('213 sessions', { timeout: 60000 });
    await page.locator('#btn-display').click();
    await page.getByLabel('Group by', { exact: true }).selectOption('location');
    await page.keyboard.press('Escape');
    await shot('07-213-sessions');
    expect(errors).toEqual([]);
    await page.close();
  }
} finally { await browser.close(); }
console.log(`Saved 28 screenshots to ${out}`);
