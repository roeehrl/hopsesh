#!/bin/sh
# Weekly upstream-drift probe: what changed in the agents hopsesh drives and the vendor
# clouds it reaches (or, for another repository's manifest, in that project's targets),
# since the versions it was tested with and since last week's run. No model, no secrets
# (GH_TOKEN reads public data and last week's artifact), no logins and no cloud calls:
# the CLIs only print their help. It loops over the targets
# in the manifest (internal/devtools/driftmanifest, or DRIFT_MANIFEST) and writes intel/
# for the review:
#   probe.md        the facts per target; read first
#   manifest.json   the project, what each target watches (and hopsesh's modules)
#   versions.json   [{id, kind, name, tested, baseline, latest}]
#   help/<id>/      help at the baseline (the highest of the tested version and the code's
#                   version floors), at each floor and at the latest; diffs, removed flags,
#                   relies.tsv, floors.tsv (what each floor's gated feature runs, there and latest)
#   formats/<id>.tsv  each record or identity field hopsesh reads: in the docs, in the
#                   week's changes, in the upstream code now and last week
#   sources/        the docs pages; docs-hashes.json; docs/*.diff for pages changed since last week
#   feeds/<id>/     changelog and release entries since the tested version or the last run
#   grep/<id>.txt   lines of those entries and docs diffs that match the target's words
#   issues.json     watched issues: state, last update, comments, changed since last week
#   issues/<id>-search.json, code/<id>-commits.txt, code/<id>-canaries.tsv
#   schema/         Codex app-server schema diffs (baseline → latest, and each floor's methods)
#   real-agents-<id>.txt  hopsesh's real-agent tests against the latest CLI (go test -v)
# The engine is the hopsesh checkout this script is in; it can run from any folder. Needs
# node, go, gh, git, jq and curl.
#   DRIFT_MANIFEST=file   a manifest another repository wrote (ci/drift/manifest.schema.json);
#                         without it, the manifest is built from hopsesh's own modules
#   DRIFT_WORKFLOW, DRIFT_BRANCH  the workflow file and branch whose last successful run
#                         is the baseline (default drift.yml on main)
#   DRIFT_OFFLINE=1       skips everything that uses the network
#   DRIFT_NO_INSTALL=1    installs no CLIs (help comes from the CLIs on PATH, the schema
#                         and real-agent steps are skipped)
#   DRIFT_BASELINE=dir    uses that folder as last week's intel instead of downloading it
set -u
OUT=${1:-intel}
ENGINE=$(cd "$(dirname "$0")/../.." && pwd)
rm -rf "$OUT"
mkdir -p "$OUT/sources" "$OUT/docs" "$OUT/schema" "$OUT/help" "$OUT/feeds/full" "$OUT/grep" "$OUT/issues" "$OUT/code"
OUT=$(cd "$OUT" && pwd)
SCRATCH=$(mktemp -d)
trap 'rm -rf "$SCRATCH"' EXIT
mkdir -p "$SCRATCH/home/.claude" "$SCRATCH/home/.codex" "$SCRATCH/cache" "$SCRATCH/tools"
SECTIONS=$SCRATCH/sections.md
ROWS=$SCRATCH/rows.md
: > "$SECTIONS"
: > "$ROWS"
: > "$SCRATCH/docs.jsonl"
: > "$SCRATCH/issues.jsonl"
echo '[]' > "$OUT/versions.json"

note() { printf '%s\n' "$@" >> "$OUT/probe.md"; }
say() { printf '%s\n' "$@" >> "$SECTIONS"; }
online() { [ "${DRIFT_OFFLINE:-}" != 1 ]; }
installing() { online && [ "${DRIFT_NO_INSTALL:-}" != 1 ]; }
slug() { printf '%s' "$1" | sed -e 's|^https*://||' -e 's|[^A-Za-z0-9._]|-|g' | cut -c1-120; }
sha() { if command -v sha256sum > /dev/null; then sha256sum "$1"; else shasum -a 256 "$1"; fi | cut -d' ' -f1; }
count() { find "$1" -type f -name "${2:-*}" | wc -l | tr -d ' '; }
lines() { if [ -f "$1" ]; then wc -l < "$1" | tr -d ' '; else echo 0; fi; }
ago() { date -u -d "$1 days ago" +%Y-%m-%dT%H:%M:%SZ 2> /dev/null || date -u -v-"$1"d +%Y-%m-%dT%H:%M:%SZ; }
# limited SECONDS CMD...: some help commands print and then wait (claude remote-control).
# The time limit exits 124 (timeout) or 142 (perl's alarm, where there is no timeout).
limited() {
  s=$1
  shift
  if command -v timeout > /dev/null; then timeout -k 5 "$s" "$@"; else perl -e 'alarm shift; exec @ARGV' "$s" "$@"; fi
}

# fetched URL: the path of a cached copy, downloaded once however many targets list it.
# JSON is pretty-printed so that next week's diff is line by line.
fetched() {
  f=$SCRATCH/cache/$(slug "$1")
  if [ ! -s "$f" ]; then
    online || return 1
    curl -fsSL --retry 3 --max-time 120 "$1" -o "$f.raw" 2> /dev/null || return 1
    jq -S . "$f.raw" > "$f" 2> /dev/null || mv "$f.raw" "$f"
    rm -f "$f.raw"
  fi
  printf '%s\n' "$f"
}

# cli PREFIX ARGV...: runs a CLI with no login and no inherited secrets: an empty home,
# private agent folders, no GH_TOKEN. PREFIX is an npm prefix, or empty for PATH.
cli() {
  p=$1
  shift
  limited 60 env -i PATH="${p:+$p/bin:}$PATH" HOME="$SCRATCH/home" CLAUDE_CONFIG_DIR="$SCRATCH/home/.claude" \
    CODEX_HOME="$SCRATCH/home/.codex" CODEX_SQLITE_HOME="$SCRATCH/home/.codex" CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 TERM=dumb NO_COLOR=1 LANG=C.UTF-8 \
    "$@" < /dev/null 2>&1
}

# install PACKAGE VERSION: an npm prefix holding that version, installed once. The
# package's install scripts do not see the token.
install() {
  d=$SCRATCH/tools/$(slug "$1@$2")
  if [ ! -d "$d/bin" ]; then
    env -u GH_TOKEN npm install -g --silent --prefix "$d" "$1@$2" > /dev/null 2>&1 || return 1
  fi
  printf '%s\n' "$d"
}

# flags FILE...: the long flags and the one-letter ones (-v) in help output, one per line.
flags() {
  cat "$@" 2> /dev/null | tr -cs 'A-Za-z0-9-' '\n' | grep -E -- '^(--[A-Za-z0-9][A-Za-z0-9-]*|-[A-Za-z])$' | sort -u
}

# has TOKEN FILE...: a flag anywhere; a subcommand at the start of a help line; or a
# command of several words ("auth status") as its usage line names it.
has() {
  t=$1
  shift
  case $t in
    -*) cat "$@" 2> /dev/null | grep -qE -- "(^|[^A-Za-z0-9-])$t([^A-Za-z0-9-]|\$)" ;;
    *' '*) cat "$@" 2> /dev/null | grep -qE "(^|[[:space:]])$(printf '%s' "$t" | sed 's/  */[[:space:]]+/g')([[:space:]:,]|\$)" ;;
    *) cat "$@" 2> /dev/null | grep -qE "^[[:space:]]+$t([[:space:]:,]|\$)" ;;
  esac
}

# argvname ARGV...: the file name of a help command's output.
argvname() { printf '%s_' "$@" | sed -e 's/[^A-Za-z0-9._-]/_/g' -e 's/_$//'; }

# grepc FIXED FILE: the lines of FILE with FIXED in them (0 without a file).
grepc() { if [ -f "$2" ]; then grep -cF -- "$1" "$2" || true; else echo 0; fi; }

note "# Upstream drift probe, $(date -u +%Y-%m-%d)" ""
(cd "$ENGINE" && go build -o "$SCRATCH/driftmanifest" ./internal/devtools/driftmanifest) || { note "- driftmanifest does not build"; exit 1; }
if [ -n "${DRIFT_MANIFEST:-}" ]; then
  "$SCRATCH/driftmanifest" check "$ENGINE/ci/drift/manifest.schema.json" "$DRIFT_MANIFEST" \
    || { note "- $DRIFT_MANIFEST is not a valid manifest (ci/drift/manifest.schema.json)"; exit 1; }
  jq . "$DRIFT_MANIFEST" > "$OUT/manifest.json"
else
  (cd "$ENGINE" && "$SCRATCH/driftmanifest") > "$OUT/manifest.json" || { note "- driftmanifest failed"; exit 1; }
fi

# Last week: the previous successful run's intel, for the docs hashes, the help of CLIs
# with no tested version, the issues, the canaries and the feeds' dates.
BASE=$SCRATCH/baseline
SINCE=
if [ -n "${DRIFT_BASELINE:-}" ]; then
  BASE=$DRIFT_BASELINE
  SINCE=$(jq -r '.date // empty' "$BASE/run.json" 2> /dev/null)
elif online; then
  prev=$(gh run list --workflow "${DRIFT_WORKFLOW:-drift.yml}" --branch "${DRIFT_BRANCH:-main}" --status success --limit 1 \
    --json databaseId,createdAt --jq '.[0] | "\(.databaseId) \(.createdAt)"' 2> /dev/null)
  if [ -n "$prev" ] && gh run download "${prev% *}" --name drift-intel --dir "$BASE" > /dev/null 2>&1; then
    SINCE=${prev#* }
  fi
fi
BASELINE=
if [ -f "$BASE/docs-hashes.json" ]; then
  note "Compared with the run of $SINCE."
  BASELINE=1
elif [ -n "$SINCE" ]; then
  note "Docs: baseline only (the run of $SINCE kept no hashes). Feeds, commits and issues since that run."
else
  note "Baseline only: no earlier successful run. Feeds, commits and issues cover the last 7 days."
fi
SINCE=${SINCE:-$(ago 7)}
jq -n --arg d "$(date -u +%Y-%m-%dT%H:%M:%SZ)" --arg s "$SINCE" '{date: $d, since: $s}' > "$OUT/run.json"

# Empty lists come out of Go as null.
jq -c '.targets[] | .floors //= [] | .watch |= ({docs: [], feeds: [], help: [], relies: [], issues: [], searches: [], formats: []}
  + with_entries(select(.value != null)))' "$OUT/manifest.json" > "$SCRATCH/targets.jsonl"
while read -r T <&3; do
  get() { printf '%s' "$T" | jq -r "$1"; }
  ID=$(get .id)
  NAME=$(get .name)
  TESTED=$(get .tested)
  # The help and schema are compared from the baseline: never below a version floor the
  # code gates on (the feeds still start at the tested version).
  FROM=$(get '.baseline // empty')
  FROM=${FROM:-$TESTED}
  GREP=$(get .watch.grep)
  say "" "## $NAME ($ID, $(get .kind), $(get .priority) priority)" ""

  # Versions: tested is the module's newest fixture folder; latest from the target's source.
  LATEST=
  if online; then
    ref=$(get '.latest.ref // empty')
    case $(get .latest.from) in
      npm) LATEST=$(npm view "$ref" version 2> /dev/null) ;;
      github-release) LATEST=$(gh api "repos/$ref/releases/latest" --jq .tag_name 2> /dev/null) ;;
      json) LATEST=$(f=$(fetched "$ref") && jq -r "$(get .latest.field)" "$f" 2> /dev/null) ;;
      feed) LATEST=$(f=$(fetched "$ref") && "$SCRATCH/driftmanifest" feed-latest "$f" 2> /dev/null) ;;
    esac
  fi
  jq --arg id "$ID" --arg k "$(get .kind)" --arg n "$NAME" --arg t "$TESTED" --arg b "$FROM" --arg l "$LATEST" \
    '. + [{id: $id, kind: $k, name: $n, tested: $t, baseline: $b, latest: $l}]' "$OUT/versions.json" > "$SCRATCH/v.json" \
    && mv "$SCRATCH/v.json" "$OUT/versions.json"
  if [ "$FROM" != "$TESTED" ]; then
    say "Tested: ${TESTED:-none}. Baseline: $FROM (the code's highest version floor; help and schema are compared from it). Latest: ${LATEST:-unknown}."
  else
    say "Tested: ${TESTED:-none}. Latest: ${LATEST:-unknown}."
  fi
  [ "$(get '.floors | length')" -gt 0 ] && say "Floors: $(get '[.floors[] | "\(.version) (\(.gates))"] | join("; ")')."

  # The CLI that prints the help, at the baseline and the latest version.
  PKG=$(get '.package // empty')
  OLD=
  NEW=
  HAVE_OLD=
  if [ -n "$PKG" ] && installing; then
    pv=$LATEST
    [ "$(get .latest.from)" = npm ] || pv=$(npm view "$PKG" version 2> /dev/null)
    NEW=$(install "$PKG" "$pv") || say "- could not install $PKG@$pv"
    if [ -n "$FROM" ]; then
      if OLD=$(install "$PKG" "$FROM"); then HAVE_OLD=1; else say "- could not install $PKG@$FROM"; fi
    fi
  fi
  VERSION_OUT=
  if [ -n "$(get '.versionArgv // empty')" ]; then
    eval "set -- $(get '.versionArgv | @sh')"
    if [ -n "$NEW" ] || command -v "$1" > /dev/null 2>&1; then
      VERSION_OUT=$(cli "$NEW" "$@")
      say "" "Version output of the latest CLI (the project parses it):" "" '```' "$VERSION_OUT" '```'
      case $VERSION_OUT in
        *"$LATEST"*) ;;
        *) [ -n "$LATEST" ] && say "- **the installed CLI does not report $LATEST**: the help and the real-agent tests below are of another version" ;;
      esac
    fi
  fi

  # Help, which needs no login. Diffed baseline → latest, or with no tested version against
  # last week's. A relied-on flag or subcommand that was there and is gone is a break.
  if [ "$(get '.watch.help | length')" -gt 0 ]; then
    H=$OUT/help/$ID
    mkdir -p "$H"
    get '.watch.help[] | @sh' > "$SCRATCH/help.txt"
    while read -r argv <&4; do
      eval "set -- $argv"
      if ! command -v "$1" > /dev/null 2>&1 && [ -z "$NEW" ]; then
        say "- \`$*\`: $1 is not installed here"
        continue
      fi
      n=$(argvname "$@")
      cli "$NEW" "$@" > "$H/$n.latest.txt"
      st=$?
      # A help that keeps running after printing reaches the time limit.
      [ $st = 0 ] || [ $st = 124 ] || [ $st = 142 ] || say "- \`$*\` exited $st"
      if [ -n "$HAVE_OLD" ]; then
        cli "$OLD" "$@" > "$H/$n.baseline.txt"
        diff -u --label "$n, baseline $FROM" --label "$n, latest $LATEST" "$H/$n.baseline.txt" "$H/$n.latest.txt" > "$H/$n.diff"
      elif [ -f "$BASE/help/$ID/$n.latest.txt" ]; then
        diff -u --label "$n, last week" --label "$n, this week" "$BASE/help/$ID/$n.latest.txt" "$H/$n.latest.txt" > "$H/$n.diff"
      fi
      [ -s "$H/$n.diff" ] || rm -f "$H/$n.diff"
    done 4< "$SCRATCH/help.txt"
    if [ "$(count "$H" '*.latest.txt')" = 0 ]; then
      say "" "Help: none collected."
      rm -rf "$H"
    else
      if [ -n "$HAVE_OLD" ]; then
        against="baseline $FROM"
        set -- "$H"/*.baseline.txt
      else
        against="last week"
        set -- "$BASE/help/$ID"/*.latest.txt
        [ -f "$1" ] || { against="nothing (no earlier help)"; set --; }
      fi
      if [ $# -gt 0 ]; then
        flags "$@" > "$SCRATCH/old-flags"
        flags "$H"/*.latest.txt > "$SCRATCH/new-flags"
        comm -23 "$SCRATCH/old-flags" "$SCRATCH/new-flags" > "$H/removed-flags.txt"
        comm -13 "$SCRATCH/old-flags" "$SCRATCH/new-flags" > "$H/added-flags.txt"
      fi
      printf 'token\tlatest\t%s\n' "$against" > "$H/relies.tsv"
      gone=
      # One token per line: a command of several words ("auth status") stays one token.
      get '.watch.relies[]' > "$SCRATCH/relies.txt"
      while IFS= read -r r <&4; do
        a=no
        b=-
        has "$r" "$H"/*.latest.txt && a=yes
        if [ $# -gt 0 ]; then b=no && has "$r" "$@" && b=yes; fi
        printf '%s\t%s\t%s\n' "$r" "$a" "$b" >> "$H/relies.tsv"
        [ "$a" = no ] && [ "$b" = yes ] && gone="$gone \`$r\`"
      done 4< "$SCRATCH/relies.txt"
      say "" "Help ($(count "$H" '*.latest.txt') commands, against $against): $(count "$H" '*.diff') differ (help/$ID/*.diff)."
      [ $# -gt 0 ] && say "Flags removed: $(lines "$H/removed-flags.txt"); added: $(lines "$H/added-flags.txt")."
      [ -n "$gone" ] && say "**Relied on and gone from the latest help:$gone** (help/$ID/relies.tsv)."
      missing=$(awk -F '\t' 'NR > 1 && $2 == "no" && $3 != "yes" { printf " %s", $1 }' "$H/relies.tsv")
      [ -n "$missing" ] && say "Relied on but not in this help (hidden, or not shipped yet):$missing."
    fi
  fi

  # A protocol schema the CLI generates (the Codex app-server), baseline against latest.
  S=$OUT/schema/$ID
  rm -rf "$SCRATCH/schema-latest"
  if [ "$(get '.watch.schema // empty')" != "" ] && [ -n "$HAVE_OLD" ] && [ -n "$NEW" ]; then
    eval "set -- $(get '.watch.schema.argv | @sh')"
    keep=$(get .watch.schema.keep)
    say "" "Schema from \`$*\`, $FROM → $LATEST:"
    mkdir -p "$S"
    if cli "$OLD" "$@" "$SCRATCH/schema-baseline" > "$S/baseline-generation.txt" &&
       cli "$NEW" "$@" "$SCRATCH/schema-latest" > "$S/latest-generation.txt"; then
      if "$SCRATCH/driftmanifest" schema-diff "$SCRATCH/schema-baseline" "$SCRATCH/schema-latest" "$S" "$keep" > "$S/comparison.txt" 2>&1; then
        say "$(lines "$S/changed-files.txt") files differ (schema/$ID/changed-files.txt); full recursive diffs of the ones the manifest keeps: $(count "$S" '*.diff') (schema/$ID/**/*.diff)."
      else
        say "- schema comparison failed; coverage is incomplete (schema/$ID/comparison.txt)"
      fi
    else
      say "- schema generation failed; coverage is incomplete (schema/$ID/*-generation.txt)"
    fi
    rm -rf "$SCRATCH/schema-baseline"
  fi

  # Floors: the versions the code gates features on. The help (and schema) at each floor:
  # what the gated feature runs must be there at its floor and at the latest.
  if [ "$(get '.floors | length')" -gt 0 ] && [ -n "$PKG" ] && installing; then
    H=$OUT/help/$ID
    mkdir -p "$H"
    printf 'floor\tkind\ttoken\tat floor\tlatest\n' > "$H/floors.tsv"
    get '.watch.help[] | @sh' > "$SCRATCH/help.txt"
    get '.floors[] | @json' > "$SCRATCH/floors.txt"
    gone=
    short=
    while read -r fl <&4; do
      v=$(printf '%s' "$fl" | jq -r .version)
      if ! FV=$(install "$PKG" "$v"); then
        say "- could not install $PKG@$v (a floor); coverage is incomplete"
        continue
      fi
      while read -r argv <&5; do
        eval "set -- $argv"
        cli "$FV" "$@" > "$H/$(argvname "$@").floor-$v.txt"
      done 5< "$SCRATCH/help.txt"
      printf '%s' "$fl" | jq -r '.relies // [] | .[]' > "$SCRATCH/floor-relies.txt"
      while IFS= read -r r <&5; do
        a=no
        b=-
        has "$r" "$H"/*.floor-"$v".txt && a=yes
        if [ "$(count "$H" '*.latest.txt')" != 0 ]; then b=no && has "$r" "$H"/*.latest.txt && b=yes; fi
        printf '%s\thelp\t%s\t%s\t%s\n' "$v" "$r" "$a" "$b" >> "$H/floors.tsv"
        [ "$b" = no ] && gone="$gone \`$r\` ($v)"
        [ "$a" = no ] && short="$short \`$r\` ($v)"
      done 5< "$SCRATCH/floor-relies.txt"
      printf '%s' "$fl" | jq -r '.methods // [] | .[]' > "$SCRATCH/floor-methods.txt"
      if [ -s "$SCRATCH/floor-methods.txt" ] && [ "$(get '.watch.schema // empty')" != "" ]; then
        eval "set -- $(get '.watch.schema.argv | @sh')"
        mkdir -p "$S"
        rm -rf "$SCRATCH/schema-floor"
        fa=no
        fb=no
        if cli "$FV" "$@" "$SCRATCH/schema-floor" > "$S/floor-$v-generation.txt"; then fa=yes; else
          say "- schema generation at the floor $v failed; coverage is incomplete (schema/$ID/floor-$v-generation.txt)"
        fi
        if [ ! -d "$SCRATCH/schema-latest" ] && [ -n "$NEW" ]; then
          cli "$NEW" "$@" "$SCRATCH/schema-latest" > "$S/latest-generation.txt" || say "- schema generation at the latest failed"
        fi
        [ -d "$SCRATCH/schema-latest" ] && fb=yes
        while IFS= read -r m <&5; do
          # - when there is no schema to look in: unknown, not absent.
          a=-
          b=-
          [ $fa = yes ] && { a=no; grep -rqF -- "\"$m\"" "$SCRATCH/schema-floor" && a=yes; }
          [ $fb = yes ] && { b=no; grep -rqF -- "\"$m\"" "$SCRATCH/schema-latest" && b=yes; }
          printf '%s\tschema\t%s\t%s\t%s\n' "$v" "$m" "$a" "$b" >> "$H/floors.tsv"
          [ "$b" = no ] && gone="$gone \`$m\` ($v)"
          [ "$a" = no ] && short="$short \`$m\` ($v)"
        done 5< "$SCRATCH/floor-methods.txt"
        rm -rf "$SCRATCH/schema-floor"
      fi
    done 4< "$SCRATCH/floors.txt"
    say "" "Floors: help (and schema) at each floor, what the gated feature runs there and at the latest (help/$ID/floors.tsv, help/$ID/*.floor-<version>.txt)."
    [ -n "$gone" ] && say "**Gated on a floor and gone from the latest:$gone**."
    [ -n "$short" ] && say "Not there at its floor (the floor is too low, or it is hidden):$short."
  fi
  rm -rf "$SCRATCH/schema-latest"

  # hopsesh's real-agent tests against the latest CLI (no model calls). Only hopsesh's own
  # manifest has them. -v lists every test: a run where everything skipped (the CLI was
  # not found) proves nothing, and says so.
  tests=$(get '.watch.tests // empty')
  if [ -n "$tests" ] && [ -n "$NEW" ]; then
    (cd "$ENGINE" && env -u GH_TOKEN PATH="$NEW/bin:$PATH" HOPSESH_REAL_AGENTS=1 go test ./internal/e2e/ -run "$tests" -count=1 -v) > "$OUT/real-agents-$ID.txt" 2>&1
    st=$?
    pass=$(grep -cE '^ *--- PASS' "$OUT/real-agents-$ID.txt" || true)
    fail=$(grep -cE '^ *--- FAIL' "$OUT/real-agents-$ID.txt" || true)
    skip=$(grep -cE '^ *--- SKIP' "$OUT/real-agents-$ID.txt" || true)
    say "" "Real-agent tests ($tests) against $LATEST: $pass passed, $fail failed, $skip skipped (go test exit $st; real-agents-$ID.txt)."
    [ "$pass" = 0 ] && say "- **no real-agent test passed: coverage is incomplete**"
    say "" '```'
    grep -E '^ *--- (FAIL|SKIP)|^(FAIL|ok|panic)' "$OUT/real-agents-$ID.txt" | tail -n 25 >> "$SECTIONS"
    say '```'
  fi

  # Docs: hashed, and diffed against last week's copy when they changed.
  changed=0
  failed=0
  get '.watch.docs[]' > "$SCRATCH/docs.txt"
  while read -r url <&4; do
    f=$(slug "$url")
    if src=$(fetched "$url"); then
      cp "$src" "$OUT/sources/$f"
      h=$(sha "$OUT/sources/$f")
      was=$([ -n "$BASELINE" ] && jq -r --arg u "$url" 'map(select(.url == $u))[0].sha256 // empty' "$BASE/docs-hashes.json")
      if [ -z "$BASELINE" ]; then
        st=baseline
      elif [ -z "$was" ]; then
        st=new
      elif [ "$was" = "$h" ]; then
        st=same
      else
        st=changed
        changed=$((changed + 1))
        diff -u --label "$url, last week" --label "$url, this week" "$BASE/sources/$f" "$OUT/sources/$f" 2> /dev/null \
          | head -c 200000 > "$OUT/docs/$f.diff"
      fi
    else
      h=
      st=failed
      failed=$((failed + 1))
      say "- could not fetch $url"
    fi
    jq -nc --arg t "$ID" --arg u "$url" --arg f "$f" --arg h "$h" --arg s "$st" \
      '{target: $t, url: $u, file: $f, sha256: $h, status: $s}' >> "$SCRATCH/docs.jsonl"
  done 4< "$SCRATCH/docs.txt"
  ndocs=$(get '.watch.docs | length')
  say "" "Docs: $ndocs pages, $changed changed since last week, $failed not fetched (docs-hashes.json, docs/*.diff)."

  # Feeds: changelog and release entries since the tested version, or since the last run.
  F=$OUT/feeds/$ID
  mkdir -p "$F"
  get '.watch.feeds[] | @json' > "$SCRATCH/feeds.txt"
  while read -r fd <&4; do
    kind=$(printf '%s' "$fd" | jq -r .kind)
    url=$(printf '%s' "$fd" | jq -r '.url // empty')
    case $kind in
      markdown)
        s=$(slug "$url")
        src=$(fetched "$url") || { say "- could not fetch $url"; continue; }
        cp "$src" "$OUT/feeds/full/$s"
        if [ -n "$TESTED" ]; then
          awk -v stop="## $TESTED" '$0 == stop { exit } { print }' "$src" > "$F/$s"
        elif [ -f "$BASE/feeds/full/$s" ]; then
          diff "$BASE/feeds/full/$s" "$src" | sed -n 's/^> //p' > "$F/$s"
        else
          head -n 150 "$src" > "$F/$s"
        fi
        ;;
      feed)
        s=$(slug "$url").md
        src=$(fetched "$url") || { say "- could not fetch $url"; continue; }
        "$SCRATCH/driftmanifest" feed "$src" "$SINCE" > "$F/$s" || say "- could not read the feed $url"
        ;;
      releases)
        repo=$(printf '%s' "$fd" | jq -r .repo)
        tag=$(printf '%s' "$fd" | jq -r '.tag // empty')
        s=releases-$(slug "$repo").md
        online || continue
        : > "$F/$s"
        for page in 1 2 3 4 5 6 7 8; do
          # Small pages: a page of 100 releases with their notes times out at GitHub.
          if ! gh api "repos/$repo/releases?per_page=25&page=$page" > "$SCRATCH/page.json" 2> /dev/null \
            && ! { sleep 5; gh api "repos/$repo/releases?per_page=25&page=$page" > "$SCRATCH/page.json" 2> /dev/null; }; then
            say "- could not read $repo releases (page $page)"
            break
          fi
          # Up to the tested release when there is one, otherwise the ones since the last run.
          jq -r --arg stop "$tag$TESTED" --arg tested "$TESTED" --arg since "$SINCE" '
            (if $tested == "" then null else (map(.tag_name) | index($stop)) end) as $end
            | (if $end == null then . else .[:$end] end)[]
            | select(.prerelease == false and ($tested != "" or .published_at > $since))
            | "### \(.tag_name) (\(.published_at))\n\n\(.body // "")\n"' "$SCRATCH/page.json" >> "$F/$s"
          if [ -n "$TESTED" ]; then
            jq -e --arg stop "$tag$TESTED" 'map(.tag_name) | index($stop) != null' "$SCRATCH/page.json" > /dev/null && break
          else
            jq -e --arg since "$SINCE" 'map(select(.published_at <= $since)) | length > 0' "$SCRATCH/page.json" > /dev/null && break
          fi
          [ "$(jq length "$SCRATCH/page.json")" -lt 25 ] && break
        done
        ;;
    esac
  done 4< "$SCRATCH/feeds.txt"
  [ "$(count "$F")" -gt 0 ] && say "Feeds: $(cat "$F"/* | wc -l | tr -d ' ') lines of new entries (feeds/$ID/)."
  # The words that matter, in the new entries and the docs diffs.
  : > "$OUT/grep/$ID.txt"
  for f in "$F"/*; do
    [ -f "$f" ] && grep -n -i -E -- "$GREP" "$f" | cut -c1-400 | sed "s|^|feeds/$ID/$(basename "$f"):|" >> "$OUT/grep/$ID.txt"
  done
  while read -r url <&4; do
    d=$OUT/docs/$(slug "$url").diff
    [ -s "$d" ] && grep -E '^[-+][^-+]' "$d" | grep -n -i -E -- "$GREP" | cut -c1-400 | sed "s|^|docs/$(basename "$d"):|" >> "$OUT/grep/$ID.txt"
  done 4< "$SCRATCH/docs.txt"
  say "Lines matching \`$GREP\`: $(lines "$OUT/grep/$ID.txt") (grep/$ID.txt)."

  # Issues: state, last update and comments, and new issues from the week's searches.
  if online; then
    upd=0
    for ref in $(get '.watch.issues[]'); do
      gh api "repos/${ref%#*}/issues/${ref#*#}" > "$SCRATCH/issue.json" 2> /dev/null || { say "- could not read $ref"; continue; }
      was=$([ -f "$BASE/issues.json" ] && jq -r --arg r "$ref" 'map(select(.ref == $r))[0].updated_at // empty' "$BASE/issues.json")
      jq -c --arg t "$ID" --arg r "$ref" --arg was "$was" '{target: $t, ref: $r, title: .title, state: .state,
        updated_at: .updated_at, comments: .comments, changed: ($was != "" and $was != .updated_at)}' "$SCRATCH/issue.json" \
        >> "$SCRATCH/issues.jsonl"
      [ -n "$was" ] && [ "$was" != "$(jq -r .updated_at "$SCRATCH/issue.json")" ] && upd=$((upd + 1))
    done
    [ "$(get '.watch.issues | length')" -gt 0 ] && say "Issues: $(get '.watch.issues | length') watched, $upd updated since last week (issues.json)."
    echo '[]' > "$OUT/issues/$ID-search.json"
    get '.watch.searches[]' > "$SCRATCH/searches.txt"
    while read -r q <&4; do
      gh api -X GET search/issues -f q="$q created:>=${SINCE%%T*}" -f per_page=30 > "$SCRATCH/found.json" 2> /dev/null \
        || echo '{"total_count": 0, "items": []}' > "$SCRATCH/found.json"
      jq --arg q "$q" '{query: $q, total: .total_count,
        items: [.items[] | {number, title, state, url: .html_url, created_at}]}' "$SCRATCH/found.json" > "$SCRATCH/search.json"
      jq --slurpfile s "$SCRATCH/search.json" '. + $s' "$OUT/issues/$ID-search.json" > "$SCRATCH/s.json" \
        && mv "$SCRATCH/s.json" "$OUT/issues/$ID-search.json"
    done 4< "$SCRATCH/searches.txt"
    [ "$(get '.watch.searches | length')" -gt 0 ] \
      && say "New issues since $SINCE: $(jq 'map(.total) | add // 0' "$OUT/issues/$ID-search.json") (issues/$ID-search.json)."
    [ "$(jq length "$OUT/issues/$ID-search.json")" = 0 ] && rm -f "$OUT/issues/$ID-search.json"
  fi

  # Code: commits on the watched paths since the last run, and the canaries' file counts.
  if [ "$(get '.watch.code // empty')" != "" ] && online; then
    repo=$(get .watch.code.repo)
    C=$SCRATCH/code-$ID
    get '.watch.code.paths[]' > "$SCRATCH/paths.txt"
    : > "$OUT/code/$ID-commits.txt"
    while read -r p <&4; do
      printf '## %s\n' "$p" >> "$OUT/code/$ID-commits.txt"
      gh api "repos/$repo/commits?path=$p&since=$SINCE&per_page=100" \
        --jq '.[] | "\(.sha[0:12]) \(.commit.committer.date) \(.commit.message | split("\n")[0])"' \
        >> "$OUT/code/$ID-commits.txt" 2> /dev/null || printf 'could not list commits\n' >> "$OUT/code/$ID-commits.txt"
    done 4< "$SCRATCH/paths.txt"
    say "" "Commits on $repo's watched paths since $SINCE: $(grep -cv '^## \|^could not' "$OUT/code/$ID-commits.txt" | tr -d ' ') (code/$ID-commits.txt)."
    if git clone -q --depth 1 --filter=blob:none --sparse "https://github.com/$repo" "$C" 2> /dev/null \
      && sed 's|^|/|' "$SCRATCH/paths.txt" | git -C "$C" sparse-checkout set --no-cone --stdin 2> /dev/null; then
      printf 'canary\tfiles\tlast week\n' > "$OUT/code/$ID-canaries.tsv"
      get '.watch.code.canaries[]' > "$SCRATCH/canaries.txt"
      while read -r c <&4; do
        n=$(cd "$C" && xargs grep -rlF -- "$c" < "$SCRATCH/paths.txt" 2> /dev/null | wc -l | tr -d ' ')
        was=$(awk -F '\t' -v c="$c" '$1 == c { print $2 }' "$BASE/code/$ID-canaries.tsv" 2> /dev/null)
        printf '%s\t%s\t%s\n' "$c" "$n" "${was:--}" >> "$OUT/code/$ID-canaries.tsv"
      done 4< "$SCRATCH/canaries.txt"
      say "" "Canaries in $repo (files that contain each, now and last week):" ""
      awk -F '\t' 'NR == 1 { print "| " $1 " | " $2 " | " $3 " |"; print "|---|---|---|"; next }
        { flag = ($3 != "-" && ($2 == 0) != ($3 == 0)) ? " **changed**" : ""; print "| `" $1 "` | " $2 flag " | " $3 " |" }' \
        "$OUT/code/$ID-canaries.tsv" >> "$SECTIONS"
      # The format fields, counted the same way, for the formats table below.
      get '.watch.formats[].fields[]' | sort -u > "$SCRATCH/fields.txt"
      while IFS= read -r fld <&4; do
        n=$(cd "$C" && xargs grep -rlF -- "$fld" < "$SCRATCH/paths.txt" 2> /dev/null | wc -l | tr -d ' ')
        printf '%s\t%s\n' "$fld" "$n" >> "$SCRATCH/fields-code-$ID.tsv"
      done 4< "$SCRATCH/fields.txt"
    else
      say "- could not check out $repo"
    fi
    rm -rf "$C"
  fi

  # Formats: each record or identity field hopsesh reads, in the target's docs, in this
  # week's changes (new feed entries, changed docs, help and schema diffs) and in the
  # upstream code (now and last week). A field that leaves the code, or that a change
  # names, is what the review reads first.
  if [ "$(get '.watch.formats | length')" -gt 0 ]; then
    mkdir -p "$OUT/formats"
    : > "$SCRATCH/changes.txt"
    cat "$F"/* >> "$SCRATCH/changes.txt" 2> /dev/null
    while read -r url <&4; do
      d=$OUT/docs/$(slug "$url").diff
      [ -s "$d" ] && grep -E '^[-+][^-+]' "$d" >> "$SCRATCH/changes.txt"
    done 4< "$SCRATCH/docs.txt"
    find "$OUT/help/$ID" "$OUT/schema/$ID" -name '*.diff' -type f 2> /dev/null | while read -r d; do
      grep -E '^[-+][^-+]' "$d"
    done >> "$SCRATCH/changes.txt"
    FT=$OUT/formats/$ID.tsv
    printf 'format\tfield\tdocs\tchanges\tcode\tcode last week\n' > "$FT"
    # shellcheck disable=SC2016 # $i is jq's
    get '.watch.formats[] | .id as $i | .fields[] | [$i, .] | @tsv' > "$SCRATCH/format-fields.tsv"
    named=
    left=
    nowhere=
    tab=$(printf '\t')
    while IFS=$tab read -r fid fld <&4; do
      docs=0
      while read -r url <&5; do
        grep -qF -- "$fld" "$OUT/sources/$(slug "$url")" 2> /dev/null && docs=$((docs + 1))
      done 5< "$SCRATCH/docs.txt"
      ch=$(grepc "$fld" "$SCRATCH/changes.txt")
      code=$(awk -F '\t' -v f="$fld" '$1 == f { print $2 }' "$SCRATCH/fields-code-$ID.tsv" 2> /dev/null)
      was=$(awk -F '\t' -v a="$fid" -v f="$fld" '$1 == a && $2 == f { print $5 }' "$BASE/formats/$ID.tsv" 2> /dev/null)
      printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$fid" "$fld" "$docs" "$ch" "${code:--}" "${was:--}" >> "$FT"
      [ "$ch" -gt 0 ] && named="$named \`$fld\`"
      if [ "${code:--}" = 0 ]; then
        case ${was:--} in -|0) nowhere="$nowhere \`$fld\`" ;; *) left="$left \`$fld\` ($fid)" ;; esac
      fi
    done 4< "$SCRATCH/format-fields.tsv"
    say "" "Formats: $(lines "$SCRATCH/format-fields.tsv") fields of $(get '.watch.formats | length') formats hopsesh reads (formats/$ID.tsv)."
    [ -n "$named" ] && say "Named in this week's changes:$named."
    [ -n "$left" ] && say "**Gone from the upstream code since last week:$left**."
    [ -n "$nowhere" ] && say "In none of the watched code paths (a path to add, or not in code):$nowhere."
  fi

  printf '| %s | %s | %s | %s | %s | %s |\n' "$NAME" "${TESTED:-}" "${FROM:-}" "${LATEST:-unknown}" "$changed/$ndocs" \
    "$(lines "$OUT/grep/$ID.txt")" >> "$ROWS"
done 3< "$SCRATCH/targets.jsonl"

jq -s '.' "$SCRATCH/docs.jsonl" > "$OUT/docs-hashes.json"
jq -s '.' "$SCRATCH/issues.jsonl" > "$OUT/issues.json"
note "" "| target | tested | baseline | latest | docs changed | matching lines |" "|---|---|---|---|---|---|"
cat "$ROWS" >> "$OUT/probe.md"
cat "$SECTIONS" >> "$OUT/probe.md"
note "" "Docs and feeds fetched: $(count "$OUT/sources") pages (sources/), $(count "$OUT/feeds/full") changelogs."
