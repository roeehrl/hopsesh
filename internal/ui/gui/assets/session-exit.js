// Ending the running agent releases its in-memory conversation. Stopping a reply
// or hiding a window does not necessarily release it for an on-disk append.
export function sessionExitHelp(agent, desktop = false) {
  if (agent === "claude") return desktop
    ? "In Claude Desktop, select the original conversation in the Code tab, then press ⌘W on Mac or Ctrl+W on Windows. Esc only stops the current response."
    : "If it is in Claude Desktop, select the original conversation in the Code tab and press ⌘W on Mac or Ctrl+W on Windows. If it is in a terminal, type /exit in that conversation. Esc only stops the current response.";
  return desktop
    ? "End the original conversation using its desktop app's session controls. Stopping its current response does not necessarily end the running session."
    : "Exit the agent in the terminal that has the original conversation open. For Codex, type /exit. If it is open in a desktop app, end that conversation there.";
}
