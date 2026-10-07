#!/usr/bin/env bash
# macOS Instruments qualification of an isolated, empty headless workload.
# Usage: scripts/runtime-energy-check.sh /absolute/hopsesh /new/output-directory
# This creates no login service and never targets an installed app's namespace.
set -euo pipefail
[[ $(uname -s) == Darwin ]] || { echo 'This qualification requires macOS Instruments.' >&2; exit 1; }
[[ $# == 2 && "$1" == /* && "$2" == /* ]] || { echo 'Pass an absolute CLI path and a new absolute output directory.' >&2; exit 1; }
taskBinary=$1
taskOutput=$2
[[ -x "$taskBinary" && ! -e "$taskOutput" && ! -L "$taskOutput" ]] || { echo 'CLI missing or output directory already exists.' >&2; exit 1; }
"$taskBinary" runtime doctor --help | grep 'Print redacted runtime health' >/dev/null || { echo 'CLI lacks current runtime diagnostics; rebuild the qualification binary.' >&2; exit 1; }
xcrun --find xctrace >/dev/null
umask 077
mkdir "$taskOutput"
taskWork=$(mktemp -d "$taskOutput/private.XXXXXX")
mkdir "$taskWork/home" "$taskWork/config" "$taskWork/state"
cat > "$taskWork/config/config.toml" <<'CONFIG'
schema = 5
layout = "flat"
update_check = "off"
[runtime]
reconcile_seconds = 300
[agents.claude]
disabled = true
[agents.codex]
disabled = true
[agents.copilot]
disabled = true
[agents.jules]
disabled = true
[agents.devin]
disabled = true
[agents.amp]
disabled = true
CONFIG
run() {
  env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="$taskWork/home" USERPROFILE="$taskWork/home" HOPSESH_CONFIG_DIR="$taskWork/config" HOPSESH_STATE_DIR="$taskWork/state" HOPSESH_MACHINE=energy-disposable HOPSESH_TAILSCALE=off "$taskBinary" "$@"
}
taskOwnerPID=''
taskWatchers=()
stopWatchers() {
  for taskPID in ${taskWatchers[@]+"${taskWatchers[@]}"}; do
    if jobs -pr | grep -qx "$taskPID"; then kill -INT "$taskPID" 2>/dev/null || true; fi
    wait "$taskPID" 2>/dev/null || true
  done
  taskWatchers=()
}
cleanup() {
  stopWatchers
  if [[ -n "$taskOwnerPID" ]]; then
    run runtime stop >/dev/null 2>&1 || true
    if jobs -pr | grep -qx "$taskOwnerPID"; then kill -TERM "$taskOwnerPID" 2>/dev/null || true; fi
    wait "$taskOwnerPID" 2>/dev/null || true
  fi
}
trap cleanup EXIT
env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="$taskWork/home" USERPROFILE="$taskWork/home" HOPSESH_CONFIG_DIR="$taskWork/config" HOPSESH_STATE_DIR="$taskWork/state" HOPSESH_MACHINE=energy-disposable HOPSESH_TAILSCALE=off "$taskBinary" runtime serve > "$taskWork/owner.json" 2> "$taskWork/owner.log" &
taskOwnerPID=$!
for taskAttempt in {1..100}; do
  if run runtime status > "$taskWork/status.json" 2>/dev/null && jq -e --argjson pid "$taskOwnerPID" '.pid == $pid and .mode == "headless"' "$taskWork/status.json" >/dev/null; then break; fi
  [[ $taskAttempt -lt 100 ]] || { echo 'Private runtime did not become ready.' >&2; exit 1; }
  sleep 0.1
done
for taskCount in 0 1 5; do
  for (( taskIndex=0; taskIndex<taskCount; taskIndex++ )); do
    # Each watcher is a direct child for precise joined cleanup.
    env -i PATH=/usr/bin:/bin:/usr/sbin:/sbin HOME="$taskWork/home" USERPROFILE="$taskWork/home" HOPSESH_CONFIG_DIR="$taskWork/config" HOPSESH_STATE_DIR="$taskWork/state" HOPSESH_MACHINE=energy-disposable HOPSESH_TAILSCALE=off "$taskBinary" runtime observe --watch > "$taskWork/watch-$taskIndex.jsonl" 2> "$taskWork/watch-$taskIndex.log" &
    taskWatchers+=("$!")
  done
  sleep 1
  run runtime doctor > "$taskOutput/idle-$taskCount-before.json"
  # System Trace otherwise retains only its default five-second rolling window.
  xcrun xctrace record --template 'System Trace' --attach "$taskOwnerPID" --time-limit 10s --window 10s --no-prompt --output "$taskOutput/idle-$taskCount.trace" > "$taskOutput/idle-$taskCount-record.log" 2>&1
  run runtime doctor > "$taskOutput/idle-$taskCount-after.json"
  xcrun xctrace export --input "$taskOutput/idle-$taskCount.trace" --toc > "$taskWork/toc.xml"
  # Share table descriptions only. A raw trace/TOC contains machine and process
  # metadata and stays a private local profiling artifact.
  python3 - "$taskWork/toc.xml" "$taskOutput/idle-$taskCount-tables.xml" <<'PY'
import sys
import xml.etree.ElementTree as ET
source = ET.parse(sys.argv[1])
tables = ET.Element("tables")
for table in source.findall("./run/data/table"):
    tables.append(ET.Element("table", table.attrib))
ET.ElementTree(tables).write(sys.argv[2], encoding="utf-8", xml_declaration=True)
PY
  stopWatchers
done
python3 - "$taskOutput" <<'PY'
import json
import pathlib
import sys
root = pathlib.Path(sys.argv[1])
samples = []
for clients in (0, 1, 5):
    before = json.loads((root / f"idle-{clients}-before.json").read_text())
    after = json.loads((root / f"idle-{clients}-after.json").read_text())
    if not before.get("connected") or not after.get("connected"):
        raise SystemExit("Private runtime disconnected during recording")
    delta = {key: after["scheduler"][key] - before["scheduler"][key]
             for key in ("collections", "notifications", "failures")}
    if any(delta.values()):
        raise SystemExit("Empty idle workload collected, notified or failed during recording")
    samples.append({"clients": clients, "schedulerDelta": delta})
report = {"schema": 1, "workload": "empty-headless", "recordSeconds": 10,
          "retainedWindowSeconds": 10, "samples": samples,
          "hardwareWakeupBudgetQualified": False, "guiBaselineQualified": False}
(root / "summary.json").write_text(json.dumps(report, indent=2) + "\n")
PY
echo "Recorded three attached-process traces in $taskOutput. Interpret the trace data before claiming a wakeup budget; this empty workload is separate from the profile/peer and GUI baselines."
