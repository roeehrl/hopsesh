// Keep this entry point independent of the backend bridge and other modules: its
// failure screen must work even when importing the application itself fails.
(() => {
  const controls = [...document.querySelectorAll(".titlebar button")];
  for (const button of controls) button.disabled = true;
  const retry = document.querySelector("#startup-retry");
  retry.onclick = () => window.location.reload();
  const slow = setTimeout(() => {
    const detail = document.querySelector("#startup-detail");
    if (!detail) return;
    detail.textContent = "Still loading. A machine or agent may be slow to respond. You can wait or reload this window.";
    retry.hidden = false;
  }, 15000);
  import("./app.js").then((app) => app.start()).then(() => {
    clearTimeout(slow);
    for (const button of controls) button.disabled = false;
  }).catch((error) => {
    clearTimeout(slow);
    const panel = document.createElement("section");
    panel.className = "loading";
    panel.setAttribute("role", "alert");
    const title = document.createElement("h1");
    title.style.fontSize = "20px";
    title.textContent = "Hopsesh couldn’t finish opening";
    const detail = document.createElement("p");
    detail.style.cssText = "max-width:540px;overflow-wrap:anywhere;margin:0 0 12px";
    detail.textContent = String(error?.message || error);
    const reload = document.createElement("button");
    reload.className = "btn primary";
    reload.textContent = "Reload window";
    reload.onclick = () => window.location.reload();
    panel.append(title, detail, reload);
    document.querySelector("#view").replaceChildren(panel);
  });
})();
