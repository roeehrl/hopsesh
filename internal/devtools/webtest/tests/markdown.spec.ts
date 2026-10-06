import { test, expect } from "@playwright/test";
import { fresh, row, details } from "./helpers";

const text = [
  "## A formatted reply", "", "**Bold** and *emphasis*, ~~removed~~, `inline code`, &amp; entities.", "",
  "1. First step", "2. Second step", "   - Nested item", "", "> A quotation", "",
  "```js", "const safe = '<script>literal code</script>';", "```", "",
  "| Name | Result |", "| --- | --- |", "| Example | Passed |", "",
  "- [x] Done", "- [ ] Pending", "", "[Documentation](https://example.com/docs)", "",
  "[Unsafe](javascript:alert(1))", "![Remote image](https://example.com/tracker.png)", "",
  '<img src="https://example.com/evil.png" onerror="window.previewExecuted=true">', "",
  '<script>window.previewExecuted=true</script>', "", "Last paragraph.",
].join("\n");

test("Markdown renders safely in distinct message cards, expands, and carries into the transcript", async ({ page }) => {
  await fresh(page);
  const opened: string[] = [];
  const network: string[] = [];
  page.on("request", (r) => { if (r.url().startsWith("https://example.com")) network.push(r.url()); });
  await page.route("**/call", async (route) => {
    const call = route.request().postDataJSON();
    if (call.m === "Preview") return route.fulfill({ json: { result: {
      items: [{ role: "user", text: "Please **format** this." }, { role: "agent", text }], more: false,
    } } });
    if (call.m === "OpenConversationLink") { opened.push(call.args[0]); return route.fulfill({ json: { result: null } }); }
    return route.continue();
  });
  await row(page, "Find the codeword").click();
  const conv = details(page).locator(".conv");
  await expect(conv.locator(".msg.user strong")).toHaveText("format");
  await expect(conv.locator(".msg.user .msg-expand")).toBeHidden();
  const agent = conv.locator(".msg.agent");
  const more = agent.getByRole("button", { name: "Show more", exact: true });
  await expect(more).toBeVisible();
  await more.focus();
  await page.keyboard.press("Enter");
  await expect(agent.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");
  await expect(agent.getByRole("heading", { name: "A formatted reply" })).toBeVisible();
  await expect(agent.locator("ol > li")).toHaveCount(2);
  await expect(agent.locator("ol ul li")).toHaveText("Nested item");
  await expect(agent.locator("pre code")).toHaveText("const safe = '<script>literal code</script>';");
  await expect(agent.locator("table td")).toHaveText(["Example", "Passed"]);
  await expect(agent.locator(".markdown")).toContainText("& entities.");
  await expect(agent.locator("img, script, iframe, input, [onerror]")).toHaveCount(0);
  await expect(agent.locator("a")).toHaveCount(1);
  expect(await page.evaluate(() => (window as any).previewExecuted)).toBeUndefined();
  expect(network).toEqual([]);
  await agent.getByRole("link", { name: "Documentation" }).click();
  await expect(page.locator("#dlg")).toContainText("https://example.com/docs");
  expect(opened).toEqual([]);
  await page.getByRole("button", { name: "Open link", exact: true }).click();
  await expect.poll(() => opened).toEqual(["https://example.com/docs"]);
  for (const colorScheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme });
    const colors = await conv.locator(".msg").evaluateAll((els) => els.map((el) => getComputedStyle(el).backgroundColor));
    expect(colors[0]).not.toBe(colors[1]);
  }
  await agent.getByRole("button", { name: "Show less" }).click();
  await expect(more).toHaveAttribute("aria-expanded", "false");
  await conv.getByRole("button", { name: "Open transcript" }).click();
  const transcript = page.locator(".transcript");
  await expect(transcript.getByRole("heading", { name: "A formatted reply" })).toBeVisible();
  await expect(transcript.locator(".msg-expand")).toHaveCount(0);
  await expect(transcript).toContainText("Last paragraph.");
});
