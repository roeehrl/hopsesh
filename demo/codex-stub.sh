#!/bin/sh
# Stand-in for the codex binary in the demo containers (no model, no network). It answers
# what hopsesh asks: --version, and one-shot `codex app-server` JSON-RPC requests
# (initialize, thread/read, thread/name/set, account/read) with a made-up account.
case "${1:-}" in
  --version) echo "codex-cli 0.153.2" ;;
  app-server)
    while IFS= read -r line; do
      id=$(printf '%s' "$line" | sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p')
      [ -n "$id" ] || continue
      case "$line" in
        *'"account/read"'*)
          printf '{"id":%s,"result":{"account":{"type":"chatgpt","email":"alice@example.com","planType":"plus"}}}\n' "$id" ;;
        *) printf '{"id":%s,"result":{}}\n' "$id" ;;
      esac
    done ;;
  *) echo "(demo: this is where Codex would resume the session)" ;;
esac
