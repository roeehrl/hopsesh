# The Linux machine `scripts/remote-scenarios.sh --docker` runs the remote-behaviour
# scenarios in: an sshd, sudo and a throwaway user, nothing of the host's. CI runs the
# script on its own runner instead.
FROM ubuntu:24.04
RUN apt-get update \
  && apt-get install -y --no-install-recommends openssh-server openssh-client sudo git ca-certificates netcat-openbsd procps \
  && rm -rf /var/lib/apt/lists/*
