# Protocol notice

This package talks to iTerm2's scripting API. iTerm2, its Python client library and its
protocol definition (`proto/api.proto`) are licensed under the GNU GPL v2. hopsesh is
Apache-2.0, and GPLv2 code cannot be combined into it, so:

- no iTerm2 file is vendored here, and no code is generated from `api.proto`;
- no iTerm2 source is translated: the messages in `wire.go` are written by hand from the
  protocol's facts needed to interoperate (message and field numbers, enum values, the
  WebSocket subprotocol `api.iterm2.com`, the `x-iterm2-*` handshake headers, the socket
  path and the AppleScript `request cookie and key for app named` command);
- only the requests hopsesh is allowed to make are written at all (see `doc.go` and
  `allowlist_test.go`).

The field numbers were checked against iTerm2's `proto/api.proto` at commit
`df780a15833c2571af385ef23f8858e8b3a2fb77` (github.com/gnachman/iTerm2 mirror,
2026-10-05). The protocol is additive; if iTerm2 renumbers a field this client uses, the
manual check in `scripts/iterm-api-smoke.sh` is where it shows.
