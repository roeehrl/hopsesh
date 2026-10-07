// The terminal window's side of the stream protocol (internal/core/pty/stream.go): one
// TerminalStream connection per tab, and the TerminalTabsStream list. The page has no Wails
// bindings: these streams are all it can reach.
import { Stream } from "/wails/runtime.js";

const enc = new TextEncoder();
const dec = new TextDecoder();
const HIGH_ACK = 64 * 1024; // ack after this much was drawn (or at once when idle)

function frame(op, body) {
  const b = typeof body === "string" ? enc.encode(body) : body || new Uint8Array(0);
  const f = new Uint8Array(1 + b.length);
  f[0] = op.charCodeAt(0);
  f.set(b, 1);
  return f;
}

function u16pair(a, b) {
  const v = new DataView(new ArrayBuffer(4));
  v.setUint16(0, a);
  v.setUint16(2, b);
  return new Uint8Array(v.buffer);
}

function u32(n) {
  const v = new DataView(new ArrayBuffer(4));
  v.setUint32(0, n);
  return new Uint8Array(v.buffer);
}

const data = (ev) => (typeof ev.data === "string" ? enc.encode(ev.data) : new Uint8Array(ev.data));

// watchTabs calls onMessage with each message of the tabs stream ({tabs, prefs}: every
// tab and the window's settings; {select}: show this tab; {notice, id}: something hopsesh
// could not do), and reconnects when the stream ends. It returns send(request), for the
// window's typed requests ({op, id, size, on}).
export function watchTabs(onMessage, connection = () => {}) {
  let s = null;
  const open = () => {
    connection(false);
    s = Stream("hopsesh.terminal.tabs");
    s.binaryType = "arraybuffer";
    s.onmessage = (ev) => { const message = JSON.parse(dec.decode(data(ev))); if (message.tabs) connection(true); onMessage(message); };
    s.onclose = () => { connection(false); setTimeout(open, 500); };
  };
  open();
  return (req) => {
    if (s && s.readyState === 1) s.send(enc.encode(JSON.stringify(req)));
    else onMessage({ notice: "The terminal is reconnecting. Try again when it is connected." });
  };
}

// connectTab attaches to tab id. handlers: output(Uint8Array, done) — draw it, then call
// done() (with xterm.js: term.write(bytes, done)); state(info); error(text); closed().
// It returns the tab's controls.
export function connectTab(id, handlers) {
  const s = Stream("hopsesh.terminal");
  s.binaryType = "arraybuffer";
  let drawn = 0;
  let queue = []; // frames sent before the stream opened: the attach frame goes first
  const send = (f) => (queue ? queue.push(f) : s.readyState === 1 && s.send(f));
  const flush = () => { if (drawn > 0) { send(frame("k", u32(drawn))); drawn = 0; } };
  s.onopen = () => {
    s.send(frame("a", id));
    for (const f of queue) s.send(f);
    queue = null;
  };
  s.onmessage = (ev) => {
    const f = data(ev);
    const body = f.subarray(1);
    switch (String.fromCharCode(f[0])) {
      case "o":
        handlers.output(body, () => {
          drawn += body.length;
          if (drawn >= HIGH_ACK) flush(); else queueMicrotask(flush);
        });
        break;
      case "s":
        handlers.state(JSON.parse(dec.decode(body)));
        break;
      case "e":
        handlers.error(dec.decode(body));
        break;
    }
  };
  s.onclose = () => handlers.closed && handlers.closed();
  return {
    // input: what the user typed or pasted, or the emulator's answer to a query.
    input: (text) => send(frame("i", text)),
    resize: (cols, rows) => send(frame("r", u16pair(cols, rows))),
    // link: the user clicked a link; hopsesh asks before opening it (http and https only).
    link: (url) => send(frame("l", url)),
    close: () => send(frame("c")),
    detach: () => s.close(),
  };
}
