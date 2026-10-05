// A test page for the terminal's stream (e2e builds only; the app's terminal page comes
// with its own UI). It has no terminal emulator: it shows the output as text with control
// sequences taken out and, like xterm.js, answers a program's device-attributes query
// (DA1), so a program that waits for the answer goes on.
import { watchTabs, connectTab } from "./terminal.js";

const $ = (s) => document.querySelector(s);
const dec = new TextDecoder();
let tab = null;

const strip = (s) => s.replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, "").replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\x1b./g, "").replace(/\r\n?/g, "\n");

watchTabs((tabs) => {
  if (tab || tabs.length === 0) return;
  const info = tabs[0];
  $("#tab").textContent = `${info.title} (${info.backend || info.program})`;
  let seen = "";
  tab = connectTab(info.id, {
    output: (bytes, done) => {
      const text = dec.decode(bytes, { stream: true });
      seen = (seen + text).slice(-16);
      if (seen.includes("\x1b[c")) {
        seen = "";
        tab.input("\x1b[?1;2c"); // xterm.js's answer
      }
      $("#out").textContent += strip(text);
      $("#out").scrollTop = $("#out").scrollHeight;
      done();
    },
    state: (i) => { $("#state").textContent = i.state + (i.reason ? ` (${i.reason})` : "") + (i.state === "exited" ? ` ${i.code}` : ""); },
    error: (e) => { $("#err").textContent = e; },
    closed: () => { $("#state").textContent += " — disconnected"; },
  });
  tab.resize(100, 30);
});

$("#form").addEventListener("submit", (ev) => {
  ev.preventDefault();
  if (tab) tab.input($("#line").value + "\r");
  $("#line").value = "";
});
$("#close").addEventListener("click", () => tab && tab.close());
$("#link").addEventListener("click", () => tab && tab.link("https://example.com/"));
