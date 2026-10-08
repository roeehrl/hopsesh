// Test builds only: collect WebView2 diagnostics while the real PTY probe runs.
// Node 24's built-in CDP WebSocket avoids adding dependencies to native checks.
const port = process.argv[2];
const seen = new Set(), sockets = new Set();
const clean = value => String(value).replace(/([?&](?:host|view)=)[^&\s"']+/g, '$1[redacted]');
const end = Date.now() + 90_000;
while (Date.now() < end) {
  try {
    const pages = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
    for (const page of pages) {
      if (!page.webSocketDebuggerUrl || seen.has(page.id)) continue;
      seen.add(page.id);
      const ws = new WebSocket(page.webSocketDebuggerUrl); sockets.add(ws);
      let id = 0;
      const send = (method, params = {}) => ws.send(JSON.stringify({ id: ++id, method, params }));
      ws.onopen = () => {
        send('Runtime.enable'); send('Log.enable'); send('Network.enable');
        send('Runtime.evaluate', { expression: 'JSON.stringify({url:location.href,body:document.body?.innerText})', returnByValue: true });
      };
      ws.onmessage = e => {
        const m = JSON.parse(e.data);
        if (['Runtime.exceptionThrown', 'Runtime.consoleAPICalled', 'Log.entryAdded', 'Network.loadingFailed'].includes(m.method) || m.result?.result?.value) {
          console.log(clean(JSON.stringify(m)));
        }
      };
      ws.onerror = () => {};
      ws.onclose = () => sockets.delete(ws);
    }
  } catch { if (seen.size && !sockets.size) break; }
  await new Promise(resolve => setTimeout(resolve, 300));
}
for (const ws of sockets) ws.close();
