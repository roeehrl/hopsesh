// Small secondary windows must also paint before their module/bridge is available.
// Reloading a window reconnects its UI; it does not retry an operation or restart a tab.
const boot = document.currentScript;
const target = document.querySelector(boot.dataset.status);
const retry = document.createElement('button');
retry.className = 'btn';
retry.textContent = 'Reload window';
retry.onclick = () => location.reload();
const slow = setTimeout(() => {
  if (target.isConnected && !target.hidden) target.append(' Still connecting. ', retry);
}, 15000);
import(new URL(boot.dataset.module, location.href).href).then(() => clearTimeout(slow)).catch(error => {
  clearTimeout(slow);
  const output = target.isConnected ? target : document.querySelector('#view');
  output.hidden = false;
  output.setAttribute('role', 'alert');
  output.textContent = 'Couldn’t open this window. ' + String(error?.message || error) + ' ';
  output.append(retry);
});
