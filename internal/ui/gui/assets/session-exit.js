// Ending the running agent releases its in-memory conversation. Stopping a reply
// or hiding a window does not necessarily release it for an on-disk append.
export function sessionExitHelp(desktop = false) {
  return desktop
    ? "Closing a conversation tab can leave its agent running in the background. Stopping a response is also different from ending the session."
    : "Closing a conversation tab can leave its agent running in the background. In a terminal, use the agent's exit command (for example, type /exit). Stopping a response does not end the session.";
}
