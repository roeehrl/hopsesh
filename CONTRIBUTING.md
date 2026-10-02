# Contributing to hopsesh

Thanks for helping. A few ground rules keep the project safe and maintainable.

## Development

```sh
make test     # go vet + go test -race
make build    # builds ./bin/hopsesh
make lint     # golangci-lint (install separately)
```

- Go 1.24+. Keep dependencies few and permissively licensed (MIT, BSD, Apache-2.0).
- Anything that depends on Claude Code's file formats goes in `internal/core/sessions` or
  `internal/core/rewrite`, with golden-file tests. Those formats are not a public API and change
  between Claude Code versions.
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
