// The terminal window's side of the stream protocol (internal/core/pty/stream.go): one
// TerminalStream connection per tab, and the TerminalTabsStream list. The page has no Wails
// bindings: these streams are all it can reach.
const hostParams = new URLSearchParams(location.search);
const isolated = hostParams.has("host");
const Stream = isolated ? name => {
 const u = new URL("/stream", location.href); u.protocol = "ws:";
 for (const k of ["host", "view"]) u.searchParams.set(k, hostParams.get(k));
 u.searchParams.set("name", name); return new WebSocket(u);
} : (await import("/wails/runtime.js")).Stream;

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
    s.onclose = ev => { connection(false); if (ev.code !== 1008) setTimeout(open, 1500); };
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
  let drawn = 0, frozen = false, ready = false, checkpointResolve, checkpointReject;
  let pendingSize, pendingInput = [];
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
      case "z":
        Promise.resolve(handlers.restored?.()).then(() => {
          ready = true;
          if (pendingSize) send(frame("r", u16pair(...pendingSize)));
          for (const text of pendingInput) send(frame("i", text));
          pendingInput = [];
        });
        break;
      case "h":
        frozen = true;
        Promise.resolve(handlers.checkpoint?.()).then(snapshot => s.send(frame("v", snapshot || ""))).catch(() => s.send(frame("v", "")));
        break;
      case "v":
        if (body.length) { frozen = false; checkpointReject?.(new Error(dec.decode(body))); }
        else checkpointResolve?.();
        break;
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
  s.onclose = () => { checkpointReject?.(new Error("view disconnected")); handlers.closed?.(); };
  return {
    // input: what the user typed or pasted, or the emulator's answer to a query.
    input: (text) => { if (frozen) return; if (!ready) { if (pendingInput.length < 128) pendingInput.push(text); return; } send(frame("i", text)); },
    resize: (cols, rows) => { if (frozen) return; if (!ready) { pendingSize=[cols,rows]; return; } send(frame("r", u16pair(cols, rows))); },
    prepare: () => new Promise((resolve,reject) => { checkpointResolve=resolve; checkpointReject=reject; send(frame("q")); }),
    // link: the user clicked a link; hopsesh asks before opening it (http and https only).
    link: (url) => send(frame("l", url)),
    close: () => send(frame("c")),
    detach: () => s.close(),
  };
}
