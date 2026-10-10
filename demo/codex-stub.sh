#!/bin/sh
# Stand-in for the codex binary in the demo containers (no model, no network). It answers
# what hopsesh asks: --version, and one-shot `codex app-server` JSON-RPC requests
# (initialize, thread/read, thread/name/set, account/read, hooks/list) with a made-up account.
# DEMO_HOOK_TRUST (trusted, the default, or untrusted) is how this Codex reviewed the hooks
# in $CODEX_HOME/hooks.json: untrusted shows hopsesh's "approve the hopsesh hooks" warning.
hooks_list() {
  trust=${DEMO_HOOK_TRUST:-trusted}
  file="${CODEX_HOME:-$HOME/.codex}/hooks.json"
  entries=""
  if [ -f "$file" ]; then
    event=""
    # hooks.json as hopsesh writes it: an event name line, then that event's commands.
    while IFS= read -r l; do
      case "$l" in
        *'"SessionStart"'*) event=sessionStart ;;
        *'"UserPromptSubmit"'*) event=userPromptSubmit ;;
        *'"command":'*)
          cmd=$(printf '%s' "$l" | sed 's/^[^:]*: *"//; s/",\{0,1\} *$//')
          entries="$entries${entries:+,}{\"eventName\":\"$event\",\"handlerType\":\"command\",\"command\":\"$cmd\",\"enabled\":true,\"isManaged\":false,\"source\":\"user\",\"trustStatus\":\"$trust\"}" ;;
      esac
    done < "$file"
  fi
  printf '{"id":%s,"result":{"data":[{"cwd":"%s","hooks":[%s],"warnings":[],"errors":[]}]}}\n' "$1" "$HOME" "$entries"
}
case "${1:-}" in
  --version) echo "codex-cli 0.160.1" ;;
  app-server)
    while IFS= read -r line; do
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
      [ -n "$id" ] || continue
      case "$line" in
        *'"account/read"'*)
          printf '{"id":%s,"result":{"account":{"type":"chatgpt","email":"alice@example.com","planType":"plus"}}}\n' "$id" ;;
        *'"hooks/list"'*) hooks_list "$id" ;;
        *) printf '{"id":%s,"result":{}}\n' "$id" ;;
      esac
    done ;;
  *) echo "(demo: this is where Codex would resume the session)" ;;
esac
