# shellcheck shell=sh
# Shared by the SSH test scripts: throwaway users of this machine and its sshd, on Linux
# and macOS. Each user plays another machine. Needs sudo.

TH_OS=$(uname -s)

# th_add_user NAME [PASSWORD] creates a user with a home folder (and sets its password).
th_add_user() {
  u=$1 pw=${2:-}
  case $TH_OS in
  Darwin)
    if ! id "$u" >/dev/null 2>&1; then
      [ -n "$pw" ] || pw="pw-$(od -An -N9 -tx1 /dev/urandom | tr -d ' \n')"
      sudo sysadminctl -addUser "$u" -password "$pw" -home "/Users/$u" -shell /bin/sh >/dev/null 2>&1
      sudo createhomedir -c -u "$u" >/dev/null 2>&1 || true
      # Remote Login may be limited to members of this group.
      if dscl . -read /Groups/com.apple.access_ssh >/dev/null 2>&1; then
        sudo dseditgroup -o edit -a "$u" -t user com.apple.access_ssh
      fi
    elif [ -n "$pw" ]; then
      sudo dscl . -passwd "/Users/$u" "$pw"
    fi
    ;;
  *)
    sudo useradd -m -s /bin/sh "$u" 2>/dev/null || true
    if [ -n "$pw" ]; then printf '%s:%s\n' "$u" "$pw" | sudo chpasswd; fi
    ;;
  esac
}

# th_home NAME prints the user's home folder.
th_home() {
  case $TH_OS in
  Darwin) dscl . -read "/Users/$1" NFSHomeDirectory | awk '{print $2}' ;;
  *) getent passwd "$1" | cut -d: -f6 ;;
  esac
}

# th_start_sshd starts the system sshd on port 22 and waits for it.
th_start_sshd() {
  case $TH_OS in
  Darwin)
    nc -z 127.0.0.1 22 2>/dev/null ||
      sudo systemsetup -f -setremotelogin on >/dev/null 2>&1 ||
      sudo launchctl load -w /System/Library/LaunchDaemons/ssh.plist
    ;;
  *)
    sudo systemctl start ssh 2>/dev/null || sudo service ssh start 2>/dev/null || {
      sudo mkdir -p /run/sshd && sudo ssh-keygen -A >/dev/null && sudo /usr/sbin/sshd
    }
    ;;
  esac
  th_wait_port 22
}

# th_wait_port PORT waits up to 30 s for a listener on 127.0.0.1:PORT.
th_wait_port() {
  i=0
  until nc -z 127.0.0.1 "$1" 2>/dev/null; do
    i=$((i + 1))
    if [ "$i" -ge 30 ]; then echo "nothing listens on port $1" >&2; return 1; fi
    sleep 1
  done
}

# th_clean_login_env keeps the runner's own settings out of SSH logins: GitHub's Linux
# runners put their XDG folders in /etc/environment, which every login reads.
th_clean_login_env() {
  if [ "$TH_OS" = Linux ]; then sudo sed -i '/^XDG_[A-Z_]*=/d' /etc/environment; fi
}

# th_privsep_dir creates the folder sshd needs for privilege separation (Linux).
th_privsep_dir() {
  if [ "$TH_OS" = Linux ]; then sudo mkdir -p /run/sshd; fi
}
