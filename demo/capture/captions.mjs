// Renders the narrated cut's captions as transparent PNGs the size of the video frame, in the
// same pill style as capture.mjs, so narrate.py can overlay them where each phrase is spoken.
// Input: CAPS (a captions JSON from narrate.py plan); output: CAPDIR/cap-NN.png.
import { chromium } from "playwright";
import { mkdirSync, readFileSync } from "node:fs";

const caps = JSON.parse(readFileSync(process.env.CAPS, "utf8"));
const dir = process.env.CAPDIR;
mkdirSync(dir, { recursive: true });
const W = 1280, H = 800, PAD = 80;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: W + 2 * PAD, height: H + 2 * PAD } });
await page.setContent(`<!doctype html><html><head><style>
  html,body{margin:0;width:${W + 2 * PAD}px;height:${H + 2 * PAD}px;background:transparent}
  #cap{position:absolute;left:50%;bottom:${PAD / 2 + 34}px;transform:translateX(-50%);max-width:1000px;padding:12px 22px;border-radius:14px;
    background:rgba(18,22,21,.82);color:#f4f6f5;font:600 26px/1.3 -apple-system,"SF Pro Display",system-ui,sans-serif;text-align:center;width:max-content}
</style></head><body><div id="cap"></div></body></html>`);
for (const [i, c] of caps.entries()) {
  await page.evaluate((t) => { document.getElementById("cap").textContent = t; }, c.text);
  await page.screenshot({ path: `${dir}/cap-${String(i).padStart(2, "0")}.png`, omitBackground: true });
}
await browser.close();
console.log("rendered", caps.length, "captions");
