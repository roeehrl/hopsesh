// Puts screenshots of the Windows app (from CI's windows-shots artifact: web content only,
// 2560×1600 at 2x) inside a drawn Windows 11 window with a title bar, as transparent PNGs.
// IN: a folder of <name>-<light|dark>@2x.png; OUT: <name>-<scheme>.png (2x).
import { chromium } from "playwright";
import { mkdirSync, readdirSync, readFileSync } from "node:fs";

const inDir = process.env.IN, outDir = process.env.OUT;
const logo = readFileSync(process.env.LOGO || "/demo/../docs/assets/logo.svg").toString("base64");
mkdirSync(outDir, { recursive: true });
const W = 1280, H = 800, BAR = 32, PAD = 80;
const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: W + 2 * PAD, height: H + BAR + 2 * PAD }, deviceScaleFactor: 2 });
for (const f of readdirSync(inDir).filter((f) => f.endsWith("@2x.png"))) {
  const m = f.match(/^(.*)-(light|dark)@2x\.png$/);
  if (!m) continue;
  const [, name, scheme] = m, dark = scheme === "dark";
  const shot = readFileSync(`${inDir}/${f}`).toString("base64");
  const fg = dark ? "#ffffff" : "#1b1b1b";
  await page.setContent(`<!doctype html><html><head><style>
    html,body{margin:0;background:transparent}
    #win{position:absolute;left:${PAD}px;top:${PAD}px;width:${W}px;height:${H + BAR}px;border-radius:8px;overflow:hidden;
      background:${dark ? "#202020" : "#f3f3f3"};
      box-shadow:0 0 0 1px ${dark ? "rgba(255,255,255,.10)" : "rgba(0,0,0,.12)"},0 26px 64px rgba(0,0,0,${dark ? ".5" : ".26"}),0 8px 20px rgba(0,0,0,.12)}
    #bar{height:${BAR}px;display:flex;align-items:center;font:12px "Segoe UI",system-ui,sans-serif;color:${fg}}
    #bar img{width:16px;height:16px;margin:0 8px 0 12px}
    #bar .t{flex:1}
    #bar .b{width:46px;height:${BAR}px;display:flex;align-items:center;justify-content:center}
    #shot{display:block;width:${W}px;height:${H}px}
  </style></head><body><div id="win"><div id="bar"><img src="data:image/svg+xml;base64,${logo}"><span class="t">hopsesh</span>
    <span class="b"><svg width="10" height="10"><line x1="0" y1="5.5" x2="10" y2="5.5" stroke="${fg}" stroke-width="1"/></svg></span>
    <span class="b"><svg width="10" height="10"><rect x=".5" y=".5" width="9" height="9" rx="1.5" fill="none" stroke="${fg}" stroke-width="1"/></svg></span>
    <span class="b"><svg width="10" height="10"><path d="M0 0L10 10M10 0L0 10" stroke="${fg}" stroke-width="1"/></svg></span>
  </div><img id="shot" src="data:image/png;base64,${shot}"></div></body></html>`);
  await page.waitForTimeout(100);
  await page.screenshot({ path: `${outDir}/${name}-${scheme}.png`, omitBackground: true });
  console.log("framed", `${name}-${scheme}.png`);
}
await browser.close();
