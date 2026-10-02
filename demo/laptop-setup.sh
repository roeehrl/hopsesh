#!/bin/sh
# Prepare alice on the laptop: her own repos and sessions, SSH access to studio, and
# hopsesh with studio allowed and its host key trusted.
set -eu
su alice -c 'demoseed -role laptop'
install -d -o alice -m 700 /home/alice/.ssh
install -o alice -m 600 /shared/id_ed25519 /home/alice/.ssh/id_ed25519
printf 'Host studio\n  User alice\n  IdentityFile ~/.ssh/id_ed25519\n' > /home/alice/.ssh/config
chown alice /home/alice/.ssh/config
su alice -c 'hopsesh hosts add studio studio >/dev/null && hopsesh trust studio --yes >/dev/null'
sed -i 's|^repos_dir = .*|repos_dir = "/home/alice/src"|' /home/alice/.config/hopsesh/config.toml
grep -q '^update_check' /home/alice/.config/hopsesh/config.toml || sed -i '1i update_check = "off"' /home/alice/.config/hopsesh/config.toml
cat >> /home/alice/.bashrc <<'RC'
export PS1='\[\e[2m\]alice@laptop\[\e[0m\] \[\e[36m\]\w\[\e[0m\] $ '
cd ~
RC
