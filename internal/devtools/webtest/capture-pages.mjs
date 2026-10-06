// Visual sweep against synthetic local fixtures; never a user's app.
import { webkit, expect } from "@playwright/test";
import { mkdirSync, readFileSync } from "node:fs";
const out = process.env.SHOTS_DIR,
  url = process.env.WEBTEST_URL || "http://127.0.0.1:8766";
if (!out || !["127.0.0.1", "localhost"].includes(new URL(url).hostname))
  throw Error("Set SHOTS_DIR and use local fixtures");
mkdirSync(out, { recursive: true });
const b = await webkit.launch();
try {
  for (const width of [1280, 900])
    for (const colorScheme of ["light", "dark"]) {
      const p = await b.newPage({
        viewport: { width, height: width === 1280 ? 820 : 600 },
        colorScheme,
      });
      await p.route("**/style.css", (r) =>
        r.fulfill({
          contentType: "text/css",
          body: readFileSync(
            new URL("../../ui/gui/assets/style.css", import.meta.url),
          ),
        }),
      );
      for (const file of ["activity.js", "machines.js"])
        await p.route("**/" + file, (r) =>
          r.fulfill({
            contentType: "text/javascript",
            body: readFileSync(
              new URL("../../ui/gui/assets/" + file, import.meta.url),
            ),
          }),
        );
      await p.route("**/call", async (r) => {
        if (r.request().postDataJSON().m !== "Machines") return r.continue();
        const res = await r.fetch();
        const data = await res.json();
        data.result.found = [
          {
            name: "build-studio",
            destination: "build-studio.example.ts.net",
            os: "darwin",
            via: ["tailscale", "ssh-config"],
            online: true,
          },
          {
            name: "shared-development-workstation",
            destination: "shared-development-workstation.example.ts.net",
            os: "linux",
            via: ["tailscale"],
            online: false,
            owner: "teammate@example.com",
          },
        ];
        await r.fulfill({ response: res, json: data });
      });
      const errors = [];
      p.on("pageerror", (e) => errors.push(e.message));
      await p.request.post(url + "/reset");
      await p.goto(url);
      await expect(
        p.getByRole("heading", { name: "All sessions", exact: true }),
      ).toBeVisible();
      const menu = async (c) =>
        p.evaluate((c) => window.__emit("hopsesh:menu", c), c);
      const shot = async (n) => {
        await p.screenshot({ path: `${out}/${n}-${width}-${colorScheme}.png` });
        expect(
          await p.evaluate(() => ({
            x: document.documentElement.scrollWidth - innerWidth,
            y: document.documentElement.scrollHeight - innerHeight,
          })),
        ).toEqual({ x: 0, y: 0 });
      };
      await menu("machines");
      await expect(
        p.getByRole("heading", { name: "Machines", exact: true }),
      ).toBeVisible();
      await shot("machines");
      await p.getByRole("button", { name: "Add by address…" }).click();
      await shot("add-machine");
      await p.locator("#dlg").getByRole("button", { name: "Cancel" }).click();
      await p
        .getByRole("heading", { name: "Clouds", exact: true })
        .scrollIntoViewIfNeeded();
      await shot("clouds");
      await p.locator(".page").evaluate((e) => (e.scrollTop = e.scrollHeight));
      await shot("clouds-bottom");
      await menu("activity");
      await expect(
        p.getByRole("heading", { name: "Activity", exact: true }),
      ).toBeVisible();
      await shot("activity");
      await menu("settings");
      for (const tab of [
        "General",
        "Agents",
        "Terminal",
        "Skill",
        "Command line",
        "Updates",
      ]) {
        await p.getByRole("tab", { name: tab, exact: true }).click();
        await shot("settings-" + tab.replaceAll(" ", "-"));
        await p
          .locator(".page")
          .evaluate((e) => (e.scrollTop = e.scrollHeight));
        await shot("settings-" + tab.replaceAll(" ", "-") + "-bottom");
      }
      await menu("sessions");
      await p
        .locator(".row")
        .filter({
          has: p.locator(".t").getByText("Find the codeword", { exact: true }),
        })
        .click();
      await p.locator("#act-move").click();
      await p.getByRole("menuitem", { name: /^Continue with Codex/ }).click();
      await expect(
        p.locator("#sheet").getByRole("heading", { name: /Continue.*Codex/ }),
      ).toBeVisible();
      await shot("continue-plan");
      await p
        .locator("#sheet")
        .getByRole("button", { name: /Continue in Codex/ })
        .click();
      await expect(
        p.getByRole("heading", { name: /continues in Codex/ }),
      ).toBeVisible();
      await shot("continue-done");
      await menu("activity");
      await expect(
        p.getByRole("heading", { name: "Activity", exact: true }),
      ).toBeVisible();
      await shot("activity-populated");
      const cr = await p.request.post(url + "/cloud?fail=partial&handed=1");
      const { id } = await cr.json();
      await menu("machines");
      await p
        .getByRole("switch", { name: "Turn on Claude Code cloud", exact: true })
        .click();
      await p.getByRole("button", { name: "Scan them now" }).click();
      if (
        (await p.locator("#btn-sidebar").getAttribute("aria-expanded")) ===
        "false"
      )
        await p.locator("#btn-sidebar").click();
      await p
        .getByRole("navigation", { name: "Places" })
        .getByRole("button", { name: /Claude Code cloud/ })
        .click();
      await p.getByRole("button", { name: "Paste a link…" }).click();
      await p.getByLabel("The session's link or id").fill(id);
      await p
        .locator("#dlg")
        .getByRole("button", { name: "Add", exact: true })
        .click();
      await expect(
        p.getByRole("complementary", { name: "Cloud session details" }),
      ).toBeVisible();
      await shot("cloud-inspector");
      await p
        .getByRole("complementary", { name: "Cloud session details" })
        .getByRole("button", { name: /^Bring to this/ })
        .click();
      await expect(
        p.locator("#sheet").getByRole("heading", { name: /Bring/ }),
      ).toBeVisible();
      await shot("bring-plan");
      await p
        .locator("#sheet")
        .getByRole("button", { name: /Bring here in/ })
        .click();
      await expect(p.locator(".outcome.partial")).toBeVisible({
        timeout: 30000,
      });
      await shot("bring-partial");
      await p.request.post(url + "/reset");
      await p.goto(url);
      await expect(
        p.getByRole("heading", { name: "All sessions", exact: true }),
      ).toBeVisible();
      await menu("machines");
      await p
        .getByRole("switch", { name: "Turn on Claude Code cloud", exact: true })
        .click();
      await p.getByRole("button", { name: "Scan them now" }).click();
      await p
        .locator(".row")
        .filter({
          has: p.locator(".t").getByText("Find the codeword", { exact: true }),
        })
        .click();
      await p.locator("#act-move").click();
      await p
        .getByRole("menuitem", { name: /Hand off to Claude Code cloud/ })
        .click();
      await expect(
        p.locator("#sheet").getByRole("heading", { name: /Hand off/ }),
      ).toBeVisible();
      await shot("handoff-plan");
      await p
        .locator("#sheet")
        .getByRole("button", { name: /^Hand off/ })
        .click();
      await expect(
        p.getByRole("heading", {
          name: "Handed off to Claude Code cloud",
          exact: true,
        }),
      ).toBeVisible({ timeout: 30000 });
      await shot("handoff-done");
      await p.request.post(
        url + "/terminal-test/open?title=Example%20terminal",
      );
      await p.goto(url + "/terminal?renderer=dom");
      await expect(p.getByRole("tab").first()).toBeVisible();
      await expect
        .poll(() => p.evaluate(() => window.hopseshTerminal.text()))
        .toContain("termfake: ready");
      await shot("terminal");
      expect(errors).toEqual([]);
      await p.close();
    }
} finally {
  await b.close();
}
