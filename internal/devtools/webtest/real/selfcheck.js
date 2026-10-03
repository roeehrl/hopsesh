// The real app checks itself (the nightly run on macOS and Linux, which have no debugging
// port to drive the window from outside): once the window lists the demo home's sessions,
// it turns on "receive sessions" through the real backend, and the test sees the setting
// saved. Run by test builds only (HOPSESH_E2E_SCRIPT, see cmd/hopsesh-app/testport_on.go).
(async () => {
  const until = async (f, ms) => {
    const end = Date.now() + ms;
    while (Date.now() < end) {
      try { if (f()) return true; } catch { /* not yet */ }
      await new Promise((r) => setTimeout(r, 300));
    }
    return false;
  };
  const listed = await until(() => [...document.querySelectorAll("h1, h2")].some((h) => /All sessions/.test(h.textContent))
    && document.querySelectorAll(".row").length >= 2, 90000);
  if (!listed) return;
  const { Call } = await import("/wails/runtime.js");
  await Call.ByName("github.com/roeehrl/hopsesh/internal/ui/gui.App.SetReceive", true);
})();
