// A test page for the terminal's stream (e2e builds only; the app's terminal page comes
// with its own UI). It has no terminal emulator: it shows the output as text with control
// sequences taken out and answers, as xterm.js does, the queries programs and the Windows
// pseudoconsole send (device attributes, status, cursor position), so whoever waits for an
// answer goes on.
import { watchTabs, connectTab } from "./terminal.js";

const $ = (s) => document.querySelector(s);
const dec = new TextDecoder();
let tab = null;

// The queries answered, and xterm.js's answers.
const QUERIES = /\x1b\[(>?)0?c|\x1b\[[56]n/g;
const answer = (q) => (q.endsWith("6n") ? "\x1b[1;1R" : q.endsWith("5n") ? "\x1b[0n" : q.includes(">") ? "\x1b[>0;276;0c" : "\x1b[?1;2c");

const strip = (s) => s.replace(/\x1b\][^\x07\x1b]*(\x07|\x1b\\)/g, "").replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "").replace(/\x1b./g, "").replace(/\r\n?/g, "\n");

watchTabs((tabs) => {
  if (tab || tabs.length === 0) return;
  const info = tabs[0];
  $("#tab").textContent = `${info.title} (${info.backend || info.program})`;
  let tail = "";
  tab = connectTab(info.id, {
    output: (bytes, done) => {
      const text = dec.decode(bytes, { stream: true });
      // Every query that ends in this chunk (one may have begun in the last).
      const buf = tail + text;
      for (const m of buf.matchAll(QUERIES)) {
        if (m.index + m[0].length > tail.length) tab.input(answer(m[0]));
      }
      tail = buf.slice(-8);
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
