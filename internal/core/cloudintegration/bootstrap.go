// Package cloudintegration describes and prepares provider-specific startup.
// Installing a binary does not imply a running or authorized cloud connector.
package cloudintegration

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"regexp"
	"text/template"

	"github.com/roeehrl/hopsesh/packaging"
)

const DownloadOrigin = "https://downloads.hopsesh.codonic.dev"

type Bootstrap struct {
	Provider          string          `json:"provider"`
	Version           string          `json:"version"`
	Origin            string          `json:"origin"`
	InstallScript     string          `json:"installScript"`
	ClaudeHookScript  string          `json:"claudeHookScript,omitempty"`
	ClaudeHookEntry   json.RawMessage `json:"claudeHookEntry,omitempty"`
	StartSkill        string          `json:"startSkill,omitempty"`
	Callback          string          `json:"callback"`
	CallbackSupported bool            `json:"callbackSupported"` // documented mechanism, not live qualification
	Reason            string          `json:"reason"`
	Connected         bool            `json:"connected"`
}

func Plan(provider, version, origin string) (Bootstrap, error) {
	p := Bootstrap{Provider: provider, Version: version, Origin: origin}
	if !regexp.MustCompile(`^0\.5\.[0-9]+(?:-[A-Za-z0-9][A-Za-z0-9.-]*)?$`).MatchString(version) {
		return p, errors.New("cloud bootstrap requires an explicit immutable 0.5 release version")
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" && u.Path != "/" {
		return p, errors.New("downloads must use a verified HTTPS origin without credentials, path or query")
	}
	p.Origin = u.Scheme + "://" + u.Host
	if !regexp.MustCompile(`^[A-Za-z0-9.-]+$`).MatchString(u.Hostname()) && net.ParseIP(u.Hostname()) == nil {
		return p, errors.New("downloads host must be a DNS name or IP address")
	}
	switch provider {
	case "claude-hosted":
		p.Callback = "SessionStart (single repository)"
		p.CallbackSupported = true
		p.Reason = "Setup installs the binary. A separate SessionStart hook must start a fresh session-scoped connector. Multi-repository startup requires separate qualification."
		p.ClaudeHookScript = "#!/bin/sh\nset -eu\n[ \"${CLAUDE_CODE_REMOTE:-}\" = true ] || exit 0\nexec \"$HOME/.local/share/hopsesh/cloud/v" + version + "/hopsesh\" cloud-integration prepare --claude-hook --quiet\n"
		p.ClaudeHookEntry = json.RawMessage(`{"matcher":"startup|resume|clear|compact|fork","hooks":[{"type":"command","command":"sh \"$CLAUDE_PROJECT_DIR\"/.hopsesh/cloud-session-start.sh","timeout":10}]}`)
	case "codex-current":
		p.Callback = "Repository guidance / Start skill"
		p.Reason = "Review and commit the repository startup instructions, then update the editable environment checkout to that commit. Run the Install script and verify the helper before publishing: new tasks inherit the prepared filesystem. Saving the script alone does not install it. Repository guidance and the optional Start skill are instructions, not guaranteed lifecycle callbacks. Verify delivery and actual task identity in a fresh task; preparation remains disconnected."
		p.StartSkill = "For a normal Codex Cloud task only, locate the verified binary at $HOME/.local/share/hopsesh/cloud/v" + version + "/hopsesh. Skip environment setup/edit conversations, local sessions and other agents. If the execution surface is uncertain, report it and stop. Run cloud-integration prepare --provider codex-current --workspace <absolute checked-out repository> --session <actual task ID> only when the real task ID is available from documented task context or an explicitly supplied current task URL. Never infer identity from checkout paths, environment IDs, configuration IDs or prior tasks. If unavailable, report that session binding is unsupported and request the current task URL rather than inventing an ID. Prepare once for this incarnation, not on every message. Do not reuse a prior incarnation, copy setup secrets, enable a device receiver, connect, enroll, export transcripts or change permissions from these instructions. Separate pinned-peer approval and a bounded routing credential are required to run the connector. Current Codex Cloud native transcript export remains unavailable until qualified."
	case "codex-legacy":
		p.Callback = "Explicit task invocation"
		p.Reason = "Setup and maintenance stages do not prove per-task startup. Setup secrets must not be copied into the agent stage. Agent networking must independently allow the relay host and POST."
	case "work-cloud":
		p.Callback = "Explicit supported environment invocation"
		p.Reason = "Work Cloud is qualified separately. Neither native transcript export nor a persistent background process is assumed."
	default:
		return p, errors.New("unknown cloud execution surface")
	}
	t, err := template.New("bootstrap").Parse(installTemplate)
	if err != nil {
		return p, err
	}
	var script bytes.Buffer
	err = t.Execute(&script, struct {
		Bootstrap
		PublicKey string
	}{p, packaging.ReleasePublicKey})
	p.InstallScript = script.String()
	return p, err
}

const installTemplate = `#!/bin/sh
set -eu
umask 077
for c in curl openssl tar awk; do command -v "$c" >/dev/null 2>&1 || { printf '%s\n' "hopsesh: $c is required" >&2; exit 1; }; done
case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) printf '%s\n' 'hopsesh: unsupported cloud OS' >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) printf '%s\n' 'hopsesh: unsupported cloud architecture' >&2; exit 1 ;; esac
case "${HOME:-}" in /*) ;; *) printf '%s\n' 'hopsesh: absolute HOME is required' >&2; exit 1 ;; esac
version='{{.Version}}'
origin='{{.Origin}}'
archive="hopsesh_${version}_${os}_${arch}.tar.gz"
base="$origin/releases/v$version"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM
fetch() {
  code=$(curl --fail --silent --show-error --proto '=https' --max-time 60 --max-filesize 209715200 --write-out '%{http_code}' --output "$2" "$1") || return 1
  case "$code" in 2??) ;; *) printf '%s\n' 'hopsesh: download redirected or refused; nothing installed' >&2; return 1 ;; esac
}
fetch "$base/checksums.txt" "$tmp/checksums.txt"
fetch "$base/checksums.txt.sig" "$tmp/checksums.txt.sig"
cat > "$tmp/release-key.pub" <<'HOPSESH_RELEASE_KEY'
{{.PublicKey}}HOPSESH_RELEASE_KEY
openssl dgst -sha256 -verify "$tmp/release-key.pub" -signature "$tmp/checksums.txt.sig" "$tmp/checksums.txt" >/dev/null 2>&1 || { printf '%s\n' 'hopsesh: release signature failed; nothing installed' >&2; exit 1; }
want=$(awk -v f="$archive" '$2 == f || $2 == "*"f {print $1}' "$tmp/checksums.txt")
case "$want" in ''|*[!a-f0-9]*) printf '%s\n' 'hopsesh: invalid archive checksum' >&2; exit 1 ;; esac
[ "${#want}" -eq 64 ] || exit 1
fetch "$base/$archive" "$tmp/$archive"
got=$(openssl dgst -sha256 "$tmp/$archive" | awk '{print $NF}')
[ "$got" = "$want" ] || { printf '%s\n' 'hopsesh: archive checksum failed; nothing installed' >&2; exit 1; }
tar -xzf "$tmp/$archive" -C "$tmp" hopsesh
[ -f "$tmp/hopsesh" ] && [ ! -L "$tmp/hopsesh" ] || exit 1
dir="$HOME/.local/share/hopsesh/cloud/v$version"
mkdir -p "$dir"
staged=$(mktemp "$dir/.hopsesh-XXXXXXXX")
if ! cp "$tmp/hopsesh" "$staged" || ! chmod 0755 "$staged" || ! mv -f "$staged" "$dir/hopsesh"; then rm -f "$staged"; exit 1; fi
printf '%s\n' "Installed verified hopsesh $version at $dir/hopsesh. Connector is not started or enrolled."
`
