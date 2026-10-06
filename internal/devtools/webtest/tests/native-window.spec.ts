import { test, expect } from '@playwright/test';
import { mainWindow } from '../real/window';

for (const path of ['/', '/index.html']) {
  test(`native lookup ignores popup and terminal while main navigates to ${path}`, async ({ browser }) => {
    const context = await browser.newContext();
    try {
      await context.route('http://wails.localhost/**', route => route.fulfill({ body: '<html><body>Window fixture</body></html>', contentType: 'text/html' }));
      const quick = await context.newPage();
      await quick.goto('http://wails.localhost/quick.html');
      const terminal = await context.newPage();
      await terminal.goto('http://wails.localhost/terminal/');
      const main = await context.newPage(); // starts at about:blank, after the other pages
      const found = mainWindow(browser);
      await main.goto('http://wails.localhost' + path);
      expect(await found).toBe(main);
    } finally { await context.close(); }
  });
}
