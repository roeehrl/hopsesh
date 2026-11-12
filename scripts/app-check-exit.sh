#!/usr/bin/env bash
# Sourced by isolated native-app checks. A successful UI/PTY result does not
# qualify shutdown: the app must actually exit, rather than be left behind.
app_check_exit() {
  local app_pid=$1 app_binary=$2 app_log=$3
  for _ in $(seq 1 60); do
    if ! kill -0 "$app_pid" 2>/dev/null; then
      wait "$app_pid" 2>/dev/null || true
      return 0
    fi
    sleep 0.25
  done
  echo "native app did not exit normally after its quit request" >&2
  tail -n 60 "$app_log" >&2
  # Failed check cleanup is restricted to this check's unique temporary binary.
  pkill -KILL -f "$app_binary" 2>/dev/null || true
  kill -KILL "$app_pid" 2>/dev/null || true
  wait "$app_pid" 2>/dev/null || true
  return 1
}
