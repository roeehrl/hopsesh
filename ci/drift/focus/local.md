The modules depend on details the vendors can change in any release: where sessions are
stored and how their files are laid out, the records inside them, how a session is resumed,
the CLI flags and version output hopsesh parses, the Codex app-server JSON-RPC methods,
instruction and skill folders, settings files, the desktop apps, and how accounts and live
sessions are detected.

Three gaps in the Codex module's storage handling are known, whatever the cloud work does.
Report each one that the latest Codex still has (the probe counts its canaries in
`intel/code/codex-canaries.tsv` and lists the commits on Codex's storage code in
`intel/code/codex-commits.txt`) as a risk, or as a break once a release makes it the
common case for hopsesh's users:

- Paginated history is the default for new local threads since Codex 0.158
  (`history_mode: paginated` in `session_meta`). The module refuses to extend such a thread
  (`agents/codex/writer.go`).
- Rollouts may be zstd-compressed (`rollout-….jsonl.zst`). The module lists only files that
  end in `.jsonl` (`isRollout` in `agents/codex/codex.go`).
- A thread can have several rollout files: `thread/revert` writes a new rollout, with its
  own rollout id, under the same thread id. The module expects one file per thread.
