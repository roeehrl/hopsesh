// The real app checks itself (the nightly run on macOS and Linux, which have no debugging
// port to drive the window from outside): once the window lists the demo home's sessions,
// it turns on "receive sessions" through the real backend, and the test sees the setting
// saved. Run by test builds only (HOPSESH_E2E_SCRIPT, see cmd/hopsesh-app/testport_on.go).
(async () => {
  const { Call, Window, Events } = await import("/wails/runtime.js");
  const phase = name => { console.info('hopsesh selfcheck: '+name); return Events.Emit('hopsesh:selfcheck',name); };
  phase('main runtime ready; waiting for session list');
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
  if (!listed) throw Error('Session list did not render two demo rows');
  phase('session list rendered');
  const call=(name,...args)=>Call.ByName("github.com/roeehrl/hopsesh/internal/ui/gui.App."+name,...args);
  const d=await call("DesktopSettings");
  const input={mode:"app",close:"keep",attention:true,previews:true,login:d.login};
  for(const mode of ["app",...(d.capabilities.tray?["both"]:[]),...(d.capabilities.hideApp&&d.capabilities.tray?["tray"]:[]),"app"]) {
    phase('saving desktop mode '+mode);
    await call("SaveDesktop",{...input,mode});
    const saved=await call("DesktopSettings");
    if(saved.effective!==mode)throw Error("Desktop mode not applied: "+mode);
  }
  // Exercise the actual close hook with no tray: the webview and backend must
  // survive, and the same route used by a second app launch must reopen it.
  phase('closing and reopening main window');
  await Window.Close();
  await new Promise(r=>setTimeout(r,300));
  await call("QuickOpen","sessions","","");
  if(!await until(()=>document.querySelectorAll(".row").length>=2,10000))throw Error("Background close could not reopen the existing window");
  const q=await call("QuickSnapshot");
  phase('checking shared Quick inventory');
  if(!q.scan||q.scan.total<2)throw Error("Quick access has no shared inventory");
  const entry=q.scan.groups.flatMap(g=>g.entries)[0];
  try {
    await call("QuickOpen","sessions",entry.machine,entry.key);
  } catch (error) {
    const current=await call("ScanSnapshot");
    phase('Quick selection rejected: '+JSON.stringify({requested:{machine:entry.machine,key:entry.key},quickRevision:q.scan.revision,quickCached:q.scan.cached,quickDiscovering:q.scan.discovering,currentRevision:current.revision,currentDiscovering:current.discovering,current:current.groups.flatMap(g=>g.entries).map(e=>({machine:e.machine,key:e.key}))}));
    throw error;
  }
  if(!await until(()=>document.querySelector('.row[aria-selected="true"]'),10000))throw Error("Quick access did not select a row");
  if(d.capabilities.tray){
    phase('opening native Quick popup');
    await call("SaveDesktop",{...input,mode:"both"});
    await call("QuickShow"); // popup selfcheck writes the completion marker
    await Events.Emit('hopsesh:selfcheck-quick-start');
  }else {
    await call("SetReceive", true);
    phase('completed without native tray');
  }
})().catch(async error=>{
  const detail=[error?.message||String(error),error?.stack].filter(Boolean).join('\n');
  console.error('hopsesh selfcheck failed: '+detail);
  const { Events } = await import('/wails/runtime.js');
  await Events.Emit('hopsesh:selfcheck','FAILED: '+detail);
});
