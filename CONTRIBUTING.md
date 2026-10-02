# Contributing to hopsesh

Thanks for helping. A few ground rules keep the project safe and maintainable.

## Development

```sh
make test     # go vet + go test -race
make lint     # golangci-lint v2 (install separately; config in .golangci.yml)
make build    # builds ./bin/hopsesh
make app      # macOS only: builds dist/macos/hopsesh.app (unsigned unless SIGN_IDENTITY is set)
```

- Go 1.26+. On Linux the desktop-app packages need GTK 4 and WebKitGTK 6
  (`libgtk-4-dev libwebkitgtk-6.0-dev` on Debian/Ubuntu) for `go vet ./...` and the tests.
- `scripts/integration-test.sh` runs a real SSH round trip (Linux, needs sudo; CI runs it).
- Keep dependencies few and permissively licensed (MIT, BSD, Apache-2.0).
- Anything that depends on Claude Code's file formats goes in `internal/core/sessions` or
  `internal/core/rewrite`, with tests. Those formats are not a public API and change
  between Claude Code versions.
- Test fixtures use made-up names, paths and addresses (alice, bob, `100.64.0.x`), never
  real ones from your machines.
- Never add code that copies credentials, calls the Anthropic API directly, or writes into a
  running session's socket. See the principles in [docs/design.md](docs/design.md).
- Remote operations must be read-only unless they are part of a confirmed plan.

## Pull requests

- One logical change per PR, with tests. CI runs on Linux, macOS and Windows.
- Describe user-visible changes in `CHANGELOG.md` under "Unreleased".
- By contributing you agree that your contribution is licensed under Apache-2.0, and you
  certify the [Developer Certificate of Origin](https://developercertificate.org/) by signing
  off your commits (`git commit -s`).

## Conduct

This project follows the [Contributor Covenant 2.1](CODE_OF_CONDUCT.md).
