# Demo environment

Two made-up machines in Docker, used to record the README demo and to try hopsesh without
owning two computers. Every user, repository, path and prompt here is invented.

| Container | Plays | Has |
|---|---|---|
| `studio` | the machine the sessions live on | user `alice`, repos `webapp` (with a worktree), `api` (unpushed and uncommitted work), `infra`; six Claude Code and Codex sessions, two of them "running" |
| `laptop` | the machine you're at | hopsesh, the desktop app's UI, its own clones of `webapp`, `api` and `infra`, four sessions, SSH access to `studio` |

Git remotes are `github.com/acme/*`, mapped with `insteadOf` to local bare repositories, so
cloning works offline. `claude` and `codex` are small stubs (`claude-stub.sh`, `codex-stub.sh`)
that answer version and account checks; no model is involved.

## Use it

Needs Docker.

```sh
demo/record.sh shell    # a shell as alice on the laptop: try hopsesh, hopsesh ls, hopsesh pull …
demo/record.sh gui      # the desktop app's UI at http://localhost:34115
demo/record.sh          # record demo/hopsesh.tape → docs/assets/demo.gif
demo/record.sh media    # the app's stills and clips → demo/out/media/
```

The containers and their network are removed when the script exits.

## Change the recordings

- `hopsesh.tape` is a [VHS](https://github.com/charmbracelet/vhs) script for the terminal
  demo; edit it and run `demo/record.sh` (writes `docs/assets/demo.gif`).
- The made-up data comes from `internal/devtools/demoseed`.
- `capture/capture.mjs` drives the desktop app's UI with Playwright inside a drawn macOS
  window. `demo/record.sh media` records it in light and dark (`SCHEMES`) and in each mode
  (`MODES`, default `stills hero undo`; `story` is the launch video), then `encode.sh` makes the
  MP4s and GIFs. Everything lands in `demo/out/media/`.
- The narrated cut of the launch video: record the picture without captions
  (`TAG=story-vo CAPTIONS=off MODES=story demo/record.sh media`), generate the script in
  `voiceover.txt` as one voice take with a pause between lines, then
  `demo/narrate.sh take.mp3 [music.mp3]`. It places each line on its beat, renders captions
  of the spoken words in the same style (`capture/captions.mjs`) and mixes the music under
  the voice → `demo/out/media/story-narrated-<scheme>.mp4`.
- `social-card.html` is the social preview and Open Graph card; `demo/cards.sh` renders it into
  `demo/out/cards/` from the plan stills.
- `docs/assets/` holds the copies the README uses: `hero-*.gif`, and `app-*`, `plan-*` and
  `agents-*` (the `main`, `plan` and `settings-agents` stills scaled to 1600 px wide), plus
  `social-preview.png` (`github-social.png`).
