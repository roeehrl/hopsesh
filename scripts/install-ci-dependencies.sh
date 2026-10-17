#!/usr/bin/env bash
# Ubuntu GitHub runner only. Keep package setup bounded and avoid the runner's
# intermittently unreachable HTTP Azure mirror. Package signature checks remain on.
set -euo pipefail
[[ ${GITHUB_ACTIONS:-} == true && ${RUNNER_OS:-} == Linux ]] || { echo 'This helper only configures disposable Linux CI runners.' >&2; exit 1; }
[[ $# -gt 0 ]] || { echo 'Pass the CI dependency packages.' >&2; exit 1; }
for taskSource in /etc/apt/sources.list /etc/apt/sources.list.d/ubuntu.sources; do
  if [[ -f "$taskSource" ]]; then
    sudo sed -i 's|http://azure.archive.ubuntu.com/ubuntu|https://archive.ubuntu.com/ubuntu|g' "$taskSource"
  fi
done
taskOptions=(-o Acquire::Retries=3 -o Acquire::http::Timeout=30 -o Acquire::https::Timeout=30)
sudo apt-get "${taskOptions[@]}" update
sudo apt-get "${taskOptions[@]}" install -y "$@"
