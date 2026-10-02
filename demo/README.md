# Demo environment

Two made-up machines in Docker, used to record the README demo and to try hopsesh without
owning two computers. Every user, repository, path and prompt here is invented.

| Container | Plays | Has |
|---|---|---|
| `studio` | the machine the sessions live on | user `alice`, repos `webapp` (with a worktree), `api` (unpushed and uncommitted work), `infra`; five sessions, two of them "running" |
| `laptop` | the machine you're at | hopsesh, the desktop app's UI, its own clones of `webapp` and `api`, two sessions, SSH access to `studio` |

Git remotes are `github.com/acme/*`, mapped with `insteadOf` to local bare repositories, so
cloning works offline. `claude` is a small stub (`claude-stub.sh`) that answers `--version`
and `auth status`; no model is involved.

## Use it

Needs Docker.

```sh
demo/record.sh shell    # a shell as alice on the laptop: try hopsesh, hopsesh ls, hopsesh pull …
demo/record.sh gui      # the desktop app's UI at http://localhost:34115
demo/record.sh          # record demo/hopsesh.tape → docs/assets/demo.gif
```

The containers and their network are removed when the script exits.

## Change the recordings

- `hopsesh.tape` is a [VHS](https://github.com/charmbracelet/vhs) script; edit it and run
  `demo/record.sh`.
- The made-up data comes from `internal/devtools/demoseed`.
- `docs/assets/app-light.png` and `app-dark.png` are screenshots of `record.sh gui` at
  1280×800 (device scale 2).
- `social-card.html` is the repository's social preview (1280×640). It expects a TUI frame at
  `demo/out/tui-browse.png`, for example
  `ffmpeg -ss 5 -i docs/assets/demo.gif -frames:v 1 demo/out/tui-browse.png`.
