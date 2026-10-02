#!/bin/sh
# Stand-in for the claude binary in the demo containers (no model, no network).
case "${1:-}" in
  --version) echo "2.1.284 (Claude Code)" ;;
  --help) echo "Usage: claude [options] [prompt]"; echo "  -r, --resume [value]"; echo "  --fork-session"; echo "  --remote-control [name]" ;;
  auth) echo '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","orgId":"demo-org","subscriptionType":"max"}' ;;
  *) echo "(demo: this is where Claude Code would resume the session)" ;;
esac
