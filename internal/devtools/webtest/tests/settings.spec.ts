import { test, expect } from "@playwright/test";
import { fresh, menu, row } from "./helpers";

test.beforeEach(async ({ page }) => fresh(page));

test("runtime diagnostics download contains health and excludes raw observation evidence", async ({ page }) => {
  await menu(page, "settings");
  const downloadReady=page.waitForEvent("download");
  await page.getByRole("button",{name:"Download diagnostics"}).click();
  const download=await downloadReady;
  expect(download.suggestedFilename()).toBe("hopsesh-runtime-diagnostics.json");
  const stream=await download.createReadStream();
  const chunks: Buffer[]=[];
  for await(const chunk of stream!)chunks.push(Buffer.from(chunk));
  const data=JSON.parse(Buffer.concat(chunks).toString());
  expect(data.schema).toBe(1);
  expect(data.observation).toHaveProperty("fresh");
  expect(data).not.toHaveProperty("settings");
  expect(data.observation).not.toHaveProperty("data");
  expect(data.observation).not.toHaveProperty("entries");
  expect(data.relay).not.toHaveProperty("token");
  expect(data.logs).not.toHaveProperty("contents");
});

test("settings tabs, agents and their capabilities", async ({ page }) => {
  await menu(page, "settings");
  const tabs = page.getByRole("tablist", { name: "Settings" });
  await tabs.getByRole("tab", { name: "Agents" }).click();
  await expect(page.getByRole("heading", { name: "Agents" })).toBeVisible();
  await expect(page.getByText("Claude Code", { exact: true }).first()).toBeVisible();
  await expect(page.locator(".cap", { hasText: "continues in other agents" }).first()).toBeVisible();
  await page.keyboard.press("ArrowDown");
  await expect(tabs.getByRole("tab", { name: "Terminal" })).toHaveAttribute("aria-selected", "true");
});

test("turning an agent off hides its sessions", async ({ page }) => {
  await menu(page, "settings");
  await page.getByRole("tab", { name: "Agents" }).click();
  await page.getByRole("switch", { name: "Codex on" }).click();
  await expect(page.getByRole("switch", { name: "Codex on" })).toHaveAttribute("aria-checked", "false");
  await menu(page, "refresh");
  await expect(row(page, "Find the codeword")).toBeVisible({ timeout: 30_000 });
  await expect(row(page, "What is the codeword in notes.txt?")).toHaveCount(0); // Codex's
});
