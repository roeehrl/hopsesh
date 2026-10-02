#!/bin/sh
# Seed made-up repos and sessions for alice, let the laptop in with a fresh key, run sshd.
set -eu
rm -f /shared/ready
ssh-keygen -A >/dev/null
su alice -c 'demoseed -role studio'
install -d -o alice -m 700 /home/alice/.ssh
rm -f /shared/id_ed25519 /shared/id_ed25519.pub
ssh-keygen -q -t ed25519 -N '' -C demo -f /shared/id_ed25519
install -o alice -m 600 /shared/id_ed25519.pub /home/alice/.ssh/authorized_keys
touch /shared/ready
exec /usr/sbin/sshd -D -e
