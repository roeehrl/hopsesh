// Shared by all windows; no bindings or storage, so the terminal keeps its isolation.
const system = matchMedia("(prefers-color-scheme: dark)");
let preference = "system";
const listeners = new Set();
export const darkAppearance = () => preference === "dark" || preference === "system" && system.matches;
function apply() {
  const theme = darkAppearance() ? "dark" : "light";
  const changed = document.documentElement.dataset.theme !== theme;
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
  if (changed) for (const fn of listeners) fn();
}
export function setAppearance(mode) {
  preference = mode === "light" || mode === "dark" ? mode : "system";
  document.documentElement.dataset.appearance = preference;
  apply();
}
export function onAppearanceChange(fn) { listeners.add(fn); return () => listeners.delete(fn); }
system.addEventListener("change", () => { if (preference === "system") apply(); });
setAppearance("system");
