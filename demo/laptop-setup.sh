#!/bin/sh
# Prepare alice on the laptop: her own repos and sessions, SSH access to studio, and
# hopsesh with studio allowed and its host key trusted.
set -eu
su alice -c 'DEMO_BARE=/shared/git/acme demoseed -role laptop && mkdir -p ~/.codex'
echo 'export HOPSESH_MACHINE=laptop' >> /home/alice/.profile
install -d -o alice -m 700 /home/alice/.ssh
install -o alice -m 600 /shared/id_ed25519 /home/alice/.ssh/id_ed25519
printf 'Host studio\n  User alice\n  IdentityFile ~/.ssh/id_ed25519\n' > /home/alice/.ssh/config
chown alice /home/alice/.ssh/config
su - alice -c 'hopsesh hosts add studio studio >/dev/null && hopsesh trust studio --yes >/dev/null'
sed -i 's|^repos_dir = .*|repos_dir = "/home/alice/src"|' /home/alice/.config/hopsesh/config.toml
for kv in 'update_check = "off"' 'skill_prompt = "declined"' 'cli_prompt = "declined"' 'app_icons = false'; do
  k=${kv%% *}
  grep -q "^$k" /home/alice/.config/hopsesh/config.toml || sed -i "1i $kv" /home/alice/.config/hopsesh/config.toml
done
# History for Activity and "Where it has been": one hop from studio, one continuation here.
su - alice -c 'hopsesh pull "studio:Terraform: move staging to arm64" --clone --yes >/dev/null'
su - alice -c 'hopsesh pull "Paginate /v2/orders" --in codex --yes >/dev/null'
# Protection hooks (hopsesh is on PATH in /usr/local/bin), so a moved original shows as
# blocked. The Codex stand-in trusts them unless DEMO_HOOK_TRUST=untrusted.
su - alice -c 'hopsesh notices install >/dev/null'
cat >> /home/alice/.bashrc <<'RC'
export PS1='\[\e[2m\]alice@laptop\[\e[0m\] \[\e[36m\]\w\[\e[0m\] $ '
cd ~
RC
