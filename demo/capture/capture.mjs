// Final captures of the hopsesh app for the README, the website and the launch: the app
// (served by the demo laptop) inside a drawn macOS window, as transparent PNG stills
// (MODE=stills) or as a recorded flow with keycap overlays (MODE=hero or MODE=undo).
// Run by demo/record.sh; every name and path on screen is made-up demo data.
import { chromium } from "playwright";
import { mkdirSync, renameSync, readdirSync } from "node:fs";

const url = process.env.APP_URL || "http://laptop:34115/";
const mode = process.env.MODE || "stills";
const scheme = process.env.SCHEME || "light";
const out = process.env.OUT || "/demo/out/media";
mkdirSync(out, { recursive: true });

const W = 1280, H = 800, PAD = 80; // window size, margin around it for the shadow
const dark = scheme === "dark";
const frameHTML = `<!doctype html><html><head><style>
  html,body{margin:0;width:${W + 2 * PAD}px;height:${H + 2 * PAD}px;overflow:hidden;
    background:${mode === "stills" ? "transparent" : dark ? "radial-gradient(circle at 30% 20%,#1c2a28,#0d1312)" : "radial-gradient(circle at 30% 20%,#f3f7f6,#dfe9e6)"};
    font-family:-apple-system,"SF Pro Text","Inter",system-ui,sans-serif}
  #win{position:absolute;left:${PAD}px;top:${PAD}px;width:${W}px;height:${H}px;border-radius:12px;overflow:hidden;
    box-shadow:0 0 0 1px ${dark ? "rgba(255,255,255,.12)" : "rgba(0,0,0,.14)"},0 28px 70px rgba(0,0,0,${dark ? ".55" : ".28"}),0 8px 20px rgba(0,0,0,.12)}
  #app{width:100%;height:100%;border:0;display:block}
  #lights{position:absolute;left:${PAD + 18}px;top:${PAD + 17}px;display:flex;gap:8px;pointer-events:none}
  #lights i{width:12px;height:12px;border-radius:50%;display:block;box-shadow:inset 0 0 0 .5px rgba(0,0,0,.18)}
  #keys{position:absolute;left:50%;bottom:${PAD / 2 - 6}px;transform:translateX(-50%);display:flex;gap:6px;opacity:0;transition:opacity .18s}
  #keys.on{opacity:1}
  #keys kbd{min-width:34px;padding:6px 10px;border-radius:8px;text-align:center;font:600 17px/1 -apple-system,system-ui,sans-serif;
    background:${dark ? "#f4f6f5" : "#1f2422"};color:${dark ? "#1f2422" : "#f4f6f5"};box-shadow:0 2px 0 rgba(0,0,0,.25)}
</style></head><body>
  <div id="win"><iframe id="app" src="${url}"></iframe></div>
  <div id="lights"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i></div>
  <div id="keys"></div>
</body></html>`;

const browser = await chromium.launch();
const ctx = await browser.newContext({
  viewport: { width: W + 2 * PAD, height: H + 2 * PAD },
  deviceScaleFactor: mode === "stills" ? 2 : 1,
  colorScheme: scheme,
  ...(mode === "stills" ? {} : { recordVideo: { dir: out + "/raw", size: { width: W + 2 * PAD, height: H + 2 * PAD } } }),
});
const page = await ctx.newPage();
await page.setContent(frameHTML);
const app = page.frameLocator("#app");
const wait = (ms) => page.waitForTimeout(ms);

// keys shows a keycap overlay (for video) while running an action.
async function keys(caps, action, hold = 900) {
  await page.evaluate((c) => {
    const k = document.getElementById("keys");
    k.innerHTML = c.map((x) => `<kbd>${x}</kbd>`).join("");
    k.classList.add("on");
  }, caps);
  await wait(250);
  if (action) await action();
  await wait(hold);
  await page.evaluate(() => document.getElementById("keys").classList.remove("on"));
}
async function still(name) {
  await wait(700);
  await page.screenshot({ path: `${out}/${name}-${scheme}.png`, omitBackground: true });
  console.log("saved", `${name}-${scheme}.png`);
}
const text = (t) => app.getByText(t, { exact: false }).first();

// Ready: the session list has loaded.
await text("Fix flaky checkout tests").waitFor({ timeout: 90000 });
await wait(1500);

// openPalette clicks the search field, which is what ⌘K does in the native app.
async function openPalette() { await app.getByText("Search sessions or run a command").first().click(); await wait(400); }

// paletteTo moves the palette's highlight to the item containing label with ↓ presses.
async function paletteTo(label, showKeys) {
  for (let i = 0; i < 12; i++) {
    const cur = await app.locator(".pal-item[aria-selected=\"true\"]").filter({ hasText: label }).count();
    if (cur > 0) return;
    if (showKeys) await keys(["↓"], () => page.keyboard.press("ArrowDown"), 350);
    else await page.keyboard.press("ArrowDown");
  }
}

if (mode === "stills") {
  await text("Fix flaky checkout tests").click();
  await still("main");
  await app.getByRole("button", { name: /Continue in Codex/ }).first().click();
  await text("Carried over").waitFor({ timeout: 60000 });
  await still("plan");
  await page.keyboard.press("Control+Enter");
  await text("continues in Codex").waitFor({ timeout: 120000 });
  await still("result");
  await text("Back to sessions").click();
  await openPalette();
  await page.keyboard.type("activity");
  await page.keyboard.press("Enter");
  await text("Everything hopsesh changed").waitFor({ timeout: 30000 });
  await still("activity");
  await openPalette();
  await page.keyboard.type("flaky", { delay: 60 });
  await still("palette");
  await page.keyboard.press("Control+a");
  await page.keyboard.type("settings");
  await page.keyboard.press("Enter");
  await app.getByText("Agents", { exact: true }).first().click();
  await still("settings-agents");
  await openPalette();
  await page.keyboard.type("machines");
  await page.keyboard.press("Enter");
  await text("Receive sessions").waitFor({ timeout: 30000 });
  await still("machines");
} else if (mode === "hero") {
  await wait(1200);
  await keys(["⌘", "K"], openPalette, 500);
  await page.keyboard.type("flaky", { delay: 140 });
  await wait(900);
  await paletteTo("Continue in Codex", true);
  await keys(["↩"], () => page.keyboard.press("Enter"), 300);
  await text("Carried over").waitFor({ timeout: 60000 });
  await wait(4200); // time to read from → to and the loss report
  await keys(["⌘", "↩"], () => page.keyboard.press("Control+Enter"), 300);
  await text("continues in Codex").waitFor({ timeout: 120000 });
  await wait(3500);
} else if (mode === "undo") {
  await text("Fix flaky checkout tests").click();
  await app.getByRole("button", { name: /Continue in Codex/ }).first().click();
  await text("Carried over").waitFor({ timeout: 60000 });
  await page.keyboard.press("Control+Enter");
  await text("continues in Codex").waitFor({ timeout: 120000 });
  await wait(1500);
  await keys(["⌥", "⌘", "Z"], () => app.getByRole("button", { name: /^Undo/ }).first().click(), 500);
  await wait(4000);
}

await ctx.close();
await browser.close();
if (mode !== "stills") {
  const raw = readdirSync(out + "/raw").filter((f) => f.endsWith(".webm"));
  if (raw.length) renameSync(`${out}/raw/${raw[0]}`, `${out}/${mode}-${scheme}.webm`);
  console.log("saved", `${mode}-${scheme}.webm`);
}
