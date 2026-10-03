// Survey: walk through the 0.3 app's main screens and save numbered screenshots plus the
// visible text, to plan the final captures. Run by demo/record.sh survey.
import { chromium } from "playwright";
import { writeFileSync, mkdirSync } from "node:fs";

const url = process.env.APP_URL || "http://laptop:34115/";
const out = process.env.OUT || "/demo/out/survey";
const scheme = process.env.SCHEME || "light";
mkdirSync(out, { recursive: true });

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1, colorScheme: scheme });
let n = 0;
async function shot(name) {
  n++;
  const f = `${out}/${String(n).padStart(2, "0")}-${name}-${scheme}.png`;
  await page.screenshot({ path: f });
  writeFileSync(f.replace(/\.png$/, ".txt"), await page.evaluate(() => document.body.innerText));
  console.log("saved", f);
}
async function step(name, fn) {
  try { await fn(); await page.waitForTimeout(1200); await shot(name); }
  catch (e) { console.log("step failed:", name, e.message.split("\n")[0]); }
}

await page.goto(url);
await page.getByText("Fix flaky checkout tests").first().waitFor({ timeout: 60000 });
await page.waitForTimeout(1500);
await shot("main");
await step("details", () => page.getByText("Fix flaky checkout tests").first().click());
await step("plan-sheet", () => page.getByRole("button", { name: /Continue in Codex/ }).first().click().then(() => page.waitForTimeout(4000)));
await step("plan-sheet-scrolled", async () => { await page.mouse.move(640, 500); await page.mouse.wheel(0, 700); });
await step("plan-sheet-bottom", async () => { await page.mouse.wheel(0, 2000); });
await step("result", async () => { await page.keyboard.press("Control+Enter"); await page.waitForTimeout(8000); });
await step("after-escape", async () => { await page.keyboard.press("Escape"); });
await step("activity", () => page.locator("text=Activity").first().click({ force: true }));
await step("palette", async () => { await page.getByText("Search sessions or run a command").first().click(); await page.keyboard.type("flaky"); });
await step("palette-commands", async () => { await page.keyboard.press("Control+a"); await page.keyboard.type("settings"); });
await step("settings", async () => { await page.keyboard.press("Enter"); });
await step("machines", async () => { await page.keyboard.press("Escape"); await page.getByText("Search sessions or run a command").first().click(); await page.keyboard.type("machines"); await page.keyboard.press("Enter"); });
await browser.close();
