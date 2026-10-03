#!/bin/sh
# Seed made-up repos and sessions for alice, let the laptop in with a fresh key, run sshd.
set -eu
rm -f /shared/ready
ssh-keygen -A >/dev/null
install -d -o alice /shared/git
su alice -c 'DEMO_BARE=/shared/git/acme demoseed -role studio'
# One session continued in Codex on this machine, through hopsesh itself, so the demo has a
# real Codex session and the Claude Code copy carries the "continued in Codex" mark.
su alice -c 'mkdir -p ~/.codex; export HOPSESH_MACHINE=studio; hopsesh pull "Rate limiter for the public API" --in codex --yes >/dev/null'
install -d -o alice -m 700 /home/alice/.ssh
rm -f /shared/id_ed25519 /shared/id_ed25519.pub
ssh-keygen -q -t ed25519 -N '' -C demo -f /shared/id_ed25519
install -o alice -m 600 /shared/id_ed25519.pub /home/alice/.ssh/authorized_keys
touch /shared/ready
exec /usr/sbin/sshd -D -e
