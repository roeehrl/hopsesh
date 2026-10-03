// Final captures of the hopsesh app for the README, the website and the launch: the app
// (served by the demo laptop) inside a drawn macOS window, as transparent PNG stills
// (MODE=stills) or as a recorded flow with keycap overlays (MODE=hero or MODE=undo).
// Run by demo/record.sh; every name and path on screen is made-up demo data.
import { chromium } from "playwright";
import { mkdirSync, renameSync, readdirSync, writeFileSync } from "node:fs";

const url = process.env.APP_URL || "http://laptop:34115/";
const mode = process.env.MODE || "stills";
const scheme = process.env.SCHEME || "light";
const tag = process.env.TAG || mode; // output name; e.g. TAG=story-vo for the narrated cut's picture
const captions = process.env.CAPTIONS !== "off"; // off: the narrated cut adds its own, timed to the voice
const out = process.env.OUT || "/demo/out/media";
mkdirSync(out, { recursive: true });

const W = 1280, H = 800, PAD = 80; // window size, margin around it for the shadow
const dark = scheme === "dark";
const frameHTML = `<!doctype html><html><head><style>
  html,body{margin:0;width:${W + 2 * PAD}px;height:${H + 2 * PAD}px;overflow:hidden;
    background:${mode === "stills" ? "transparent" : dark ? "radial-gradient(circle at 30% 20%,#1c2a28,#0d1312)" : "radial-gradient(circle at 30% 20%,#f3f7f6,#dfe9e6)"};
    font-family:-apple-system,"SF Pro Text","Inter",system-ui,sans-serif}
  #stage{position:absolute;inset:0;transform-origin:0 0;transition:transform 1.4s cubic-bezier(.45,0,.25,1)}
  #win{position:absolute;left:${PAD}px;top:${PAD}px;width:${W}px;height:${H}px;border-radius:12px;overflow:hidden;
    box-shadow:0 0 0 1px ${dark ? "rgba(255,255,255,.12)" : "rgba(0,0,0,.14)"},0 28px 70px rgba(0,0,0,${dark ? ".55" : ".28"}),0 8px 20px rgba(0,0,0,.12)}
  #app{width:100%;height:100%;border:0;display:block}
  #lights{position:absolute;left:${PAD + 18}px;top:${PAD + 17}px;display:flex;gap:8px;pointer-events:none}
  #lights i{width:12px;height:12px;border-radius:50%;display:block;box-shadow:inset 0 0 0 .5px rgba(0,0,0,.18)}
  #cap{position:absolute;left:50%;bottom:${PAD / 2 + 34}px;transform:translateX(-50%);max-width:1000px;padding:12px 22px;border-radius:14px;
    background:rgba(18,22,21,.82);color:#f4f6f5;font:600 26px/1.3 -apple-system,"SF Pro Display",system-ui,sans-serif;text-align:center;
    opacity:0;transition:opacity .35s;backdrop-filter:blur(8px)}
  #cap.on{opacity:1}
  #card{position:absolute;inset:0;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:22px;
    background:${dark ? "#0f1514" : "#f3f6f5"};color:${dark ? "#ecebe5" : "#1d1c19"};opacity:0;transition:opacity .7s;pointer-events:none;text-align:center}
  #card.on{opacity:1}
  #card .nm{display:flex;align-items:center;gap:22px;font:500 72px/1 "SF Mono",ui-monospace,monospace;letter-spacing:-1px}
  #card .nm img{width:96px;height:96px}
  #card .tg{font:600 40px/1.25 -apple-system,"SF Pro Display",system-ui,sans-serif;max-width:1000px}
  #card .sm{font:400 22px/1.4 -apple-system,system-ui,sans-serif;opacity:.7}
  #card .fine{font:400 15px/1.4 -apple-system,system-ui,sans-serif;opacity:.5;margin-top:20px}
  #keys{position:absolute;left:50%;bottom:${PAD / 2 - 6}px;transform:translateX(-50%);display:flex;gap:6px;opacity:0;transition:opacity .18s}
  #keys.on{opacity:1}
  #keys kbd{min-width:34px;padding:6px 10px;border-radius:8px;text-align:center;font:600 17px/1 -apple-system,system-ui,sans-serif;
    background:${dark ? "#f4f6f5" : "#1f2422"};color:${dark ? "#1f2422" : "#f4f6f5"};box-shadow:0 2px 0 rgba(0,0,0,.25)}
</style></head><body>
  <div id="stage">
    <div id="win"><iframe id="app" src="${url}"></iframe></div>
    <div id="lights"><i style="background:#ff5f57"></i><i style="background:#febc2e"></i><i style="background:#28c840"></i></div>
  </div>
  <div id="cap"></div>
  <div id="card"></div>
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
// Story helpers: captions, eased zooms on the window (window coordinates), and full cards.
async function caption(t) {
  if (!captions) return;
  await page.evaluate((t) => { const c = document.getElementById("cap"); if (t) { c.textContent = t; c.classList.add("on"); } else c.classList.remove("on"); }, t);
}
async function zoom(x, y, s) {
  await page.evaluate(([x, y, s, pad, vw, vh]) => {
    const st = document.getElementById("stage");
    const cx = x + pad, cy = y + pad;
    st.style.transform = s === 1 ? "none" : `translate(${vw / 2 - cx * s}px, ${vh / 2 - cy * s}px) scale(${s})`;
  }, [x, y, s, PAD, W + 2 * PAD, H + 2 * PAD]);
}
async function card(html) {
  await page.evaluate((h) => { const c = document.getElementById("card"); if (h) { c.innerHTML = h; c.classList.add("on"); } else c.classList.remove("on"); }, html);
}
async function still(name) {
  await wait(700);
  await page.screenshot({ path: `${out}/${name}-${scheme}.png`, omitBackground: true });
  console.log("saved", `${name}-${scheme}.png`);
}
const text = (t) => app.getByText(t, { exact: false }).first();

// The story opens on its title card while the app loads; encode.sh trims the wait.
const t0 = Date.now();
const titleCard = () => `<div class="nm"><img src="data:image/svg+xml;base64,${process.env.LOGO_B64 || ""}">hopsesh</div><div class="tg">Continue any coding-agent session — on any machine, in any agent.</div>`;
if (mode === "story") await page.evaluate((h) => { const c = document.getElementById("card"); c.style.transition = "none"; c.innerHTML = h; c.classList.add("on"); }, titleCard());

// Ready: the session list has loaded.
await text("Fix flaky checkout tests").waitFor({ timeout: 90000 });
await wait(1500);
// mark records when each story beat starts (seconds into the trimmed video), so the
// voiceover lines of cut B can be placed on their beats.
const marks = {};
let tStart = 0;
const mark = (name) => { marks[name] = +((Date.now() - tStart) / 1000).toFixed(2); };
if (mode === "story") {
  tStart = Date.now() - 300;
  writeFileSync(`${out}/${tag}-${scheme}.start`, String(Math.max(0, (tStart - t0) / 1000)));
  await page.evaluate(() => { document.getElementById("card").style.transition = ""; });
}

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
} else if (mode === "story") {
  // The launch video (cut A, captions only); see launch/VIDEO-0.3.md for the storyboard.
  const logo = `<img src="data:image/svg+xml;base64,${process.env.LOGO_B64 || ""}">`;
  mark("title");
  await wait(3200);
  await card(null);
  await wait(500);
  mark("sessions");
  await caption("Every session. Every agent. Every machine.");
  await zoom(470, 380, 1.3);
  await wait(3200);
  await zoom(640, 400, 1);
  await wait(900);
  mark("pick");
  await caption("Pick one. Continue it in Codex.");
  await keys(["⌘", "K"], openPalette, 400);
  await page.keyboard.type("flaky", { delay: 110 });
  await wait(500);
  await paletteTo("Continue in Codex", true);
  await keys(["↩"], () => page.keyboard.press("Enter"), 200);
  await text("Carried over").waitFor({ timeout: 60000 });
  mark("plan");
  await caption("See what carries over — before anything changes.");
  await wait(600);
  await zoom(640, 290, 1.55);
  await wait(3600);
  await zoom(640, 600, 1.3);
  await wait(2400);
  await zoom(640, 400, 1);
  await wait(900);
  mark("result");
  await caption("Repo matched. Code synced. Codex briefed.");
  await keys(["⌘", "↩"], () => page.keyboard.press("Control+Enter"), 300);
  await text("continues in Codex").waitFor({ timeout: 120000 });
  await wait(3000);
  mark("undo");
  await caption("Changed your mind? Undo — on every machine.");
  await text("Back to sessions").click();
  await openPalette();
  await page.keyboard.type("activity");
  await page.keyboard.press("Enter");
  await text("Everything hopsesh changed").waitFor({ timeout: 30000 });
  await wait(1200);
  await app.getByRole("button", { name: /^Undo$/ }).first().click();
  await wait(700);
  const confirm = app.getByRole("dialog").getByRole("button", { name: /^Undo/ });
  if (await confirm.count()) await confirm.first().click();
  await wait(3200);
  await caption(null);
  mark("end");
  await card(`<div class="nm">${logo}hopsesh</div><div class="sm">Claude Code and Codex · CLI, TUI and an app for macOS and Windows</div><div class="tg">codonic.dev/apps/hopsesh/get-started</div><div class="fine">Unofficial; not affiliated with Anthropic or OpenAI.</div>`);
  await wait(5200);
  writeFileSync(`${out}/${tag}-${scheme}.marks.json`, JSON.stringify(marks, null, 1));
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
  if (raw.length) renameSync(`${out}/raw/${raw[0]}`, `${out}/${tag}-${scheme}.webm`);
  console.log("saved", `${tag}-${scheme}.webm`);
}
