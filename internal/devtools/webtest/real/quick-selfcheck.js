// Runs inside the native Quick access webview, never in the production build.
(async()=>{
 const {Call,Events}=await import('/wails/runtime.js');
 const phase=name=>{console.info('hopsesh Quick selfcheck: '+name);return Events.Emit('hopsesh:selfcheck',name);};
 phase('popup runtime ready');
 const call=(name,...args)=>Call.ByName('github.com/roeehrl/hopsesh/internal/ui/gui.App.'+name,...args);
 const until=async f=>{for(let n=0;n<300;n++){if(await f())return;await new Promise(r=>setTimeout(r,300));}throw Error('Quick access native check timed out');};
 await until(async()=> (await call('DesktopSettings')).effective==='both');
 phase('desktop supports both windows');
 const button=text=>[...document.querySelectorAll('button')].find(b=>b.textContent===text);
 await until(()=>button('Recent'));
 button('Recent').click();
 await until(()=>document.querySelector('.quick-row'));
 document.querySelector('.quick-row').click();
 phase('selected recent session; waiting for preview');
 await until(()=>document.querySelector('.quick-message'));
 if(document.documentElement.scrollWidth>innerWidth)throw Error('Quick access overflows');
 button('Open session details').click();
 await call('SetReceive',true);
 phase('completed');
})().catch(async error=>{
 console.error('hopsesh Quick selfcheck failed: '+(error.stack||error));
 const {Events}=await import('/wails/runtime.js');
 await Events.Emit('hopsesh:selfcheck','FAILED: '+(error.stack||error));
});
