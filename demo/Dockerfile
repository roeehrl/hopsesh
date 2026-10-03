# Demo environment for the README recordings: two made-up machines on one network.
# See demo/README.md. Build context is the repository root.
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The version the recordings show (override with --build-arg VERSION=…).
ARG VERSION=0.3.0
RUN LD="-X github.com/roeehrl/hopsesh/internal/version.Version=${VERSION}" \
 && CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -o /out/hopsesh ./cmd/hopsesh \
 && CGO_ENABLED=0 go build -trimpath -o /out/demoseed ./internal/devtools/demoseed \
 && CGO_ENABLED=0 go build -trimpath -ldflags "$LD" -tags server -o /out/hopsesh-app ./cmd/hopsesh-app

# "studio": the machine the sessions live on.
FROM debian:bookworm-slim AS studio
RUN apt-get update && apt-get install -y --no-install-recommends openssh-server git procps ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -s /bin/bash alice && mkdir -p /run/sshd /srv/git && chown alice /srv/git
COPY --from=build /out/hopsesh /out/demoseed /usr/local/bin/
COPY demo/claude-stub.sh /usr/local/bin/claude
COPY demo/codex-stub.sh /usr/local/bin/codex
COPY demo/studio-entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]

# "laptop": where hopsesh runs and VHS records.
FROM ghcr.io/charmbracelet/vhs AS laptop
RUN apt-get update && apt-get install -y --no-install-recommends openssh-client git procps \
 && rm -rf /var/lib/apt/lists/* \
 && useradd -m -s /bin/bash alice && mkdir -p /srv/git && chown alice /srv/git
COPY --from=build /out/hopsesh /out/demoseed /out/hopsesh-app /usr/local/bin/
COPY demo/claude-stub.sh /usr/local/bin/claude
COPY demo/codex-stub.sh /usr/local/bin/codex
COPY demo/laptop-setup.sh /usr/local/bin/laptop-setup
