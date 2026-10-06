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
  const call=(name,...args)=>Call.ByName("github.com/roeehrl/hopsesh/internal/ui/gui.App."+name,...args);
  const d=await call("DesktopSettings");
  const input={mode:"app",close:"quit",attention:true,previews:true,login:d.login};
  for(const mode of ["app",...(d.capabilities.tray?["both"]:[]),...(d.capabilities.hideApp&&d.capabilities.tray?["tray"]:[]),"app"]) {
    await call("SaveDesktop",{...input,mode});
    const saved=await call("DesktopSettings");
    if(saved.effective!==mode)throw Error("Desktop mode not applied: "+mode);
  }
  const q=await call("QuickSnapshot");
  if(!q.scan||q.scan.total<2)throw Error("Quick access has no shared inventory");
  const entry=q.scan.groups.flatMap(g=>g.entries)[0];
  await call("QuickOpen","sessions",entry.machine,entry.key);
  if(!await until(()=>document.querySelector('.row[aria-selected="true"]'),10000))throw Error("Quick access did not select a row");
  if(d.capabilities.tray){
    await call("SaveDesktop",{...input,mode:"both"});
    await call("QuickShow"); // popup selfcheck writes the completion marker
  }else await call("SetReceive", true);
})();
