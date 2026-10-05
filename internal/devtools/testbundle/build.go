package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/agents/all"
	"github.com/roeehrl/hopsesh/internal/core/registry"
	"github.com/roeehrl/hopsesh/internal/devtools/schemalite"
	"github.com/roeehrl/hopsesh/internal/testkit/fakecloud"
)

// platforms the stand-in program is built for.
var platforms = []string{"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"}

// standInNames are the names the program answers to (internal/devtools/fakeagent).
var standInNames = []string{"claude", "codex", "gh", "jules", "devin", "amp", "fakecloud"}

var semver = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

type manifestFile struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Agent        string `json:"agent,omitempty"`
	AgentVersion string `json:"agentVersion,omitempty"`
	Schema       string `json:"schema,omitempty"`
	SHA256       string `json:"sha256"`
	Size         int    `json:"size"`
}

type bundleManifest struct {
	Bundle  int `json:"bundle"`
	Hopsesh struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	} `json:"hopsesh"`
	StandIns struct {
		Program   string            `json:"program"`
		Names     []string          `json:"names"`
		Platforms []string          `json:"platforms"`
		Versions  map[string]string `json:"versions"`
		Env       map[string]string `json:"env"`
	} `json:"standIns"`
	Agents []bundleAgent  `json:"agents"`
	Files  []manifestFile `json:"files"`
}

type bundleAgent struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Tested   []string `json:"tested"`
	Versions []string `json:"versions"`
}

// file is one file of the archive, in memory.
type file struct {
	path string
	data []byte
	exec bool
}

// build checks the bundle, assembles it and writes the archive and its checksum to out.
// It returns the archive's path. The archive is reproducible: the same commit gives the
// same bytes (entries sorted, every time the commit's, no owners).
func build(root, version, commit, out string) (string, error) {
	if !semver.MatchString(version) {
		return "", fmt.Errorf("version %q is not X.Y.Z", version)
	}
	entries, errs := check(root)
	if len(errs) > 0 {
		return "", fmt.Errorf("the bundle does not check:\n  %s", strings.Join(errs, "\n  "))
	}
	if commit == "" {
		b, err := git(root, "rev-parse", "HEAD")
		if err != nil {
			return "", err
		}
		commit = b
	}
	d, err := git(root, "log", "-1", "--format=%cI", commit)
	if err != nil {
		return "", err
	}
	date, err := time.Parse(time.RFC3339, d)
	if err != nil {
		return "", err
	}

	var files []file
	var m bundleManifest
	m.Bundle = 1
	m.Hopsesh.Version, m.Hopsesh.Commit, m.Hopsesh.Date = version, commit, date.UTC().Format(time.RFC3339)
	m.StandIns.Program = "fakeagent"
	m.StandIns.Names = standInNames
	m.StandIns.Platforms = platforms
	m.StandIns.Versions = map[string]string{"claude": fakecloud.ClaudeVersionLine, "codex": fakecloud.CodexVersionLine}
	m.StandIns.Env = map[string]string{
		"FAKE_AGENT_LOG":  "a file each call is appended to: the program, its arguments, app-server methods",
		"FAKE_CLOUD_DIR":  "the stand-in clouds' state (sessions as JSON files)",
		"FAKE_CLOUD_FAIL": "one failure to play: signed-out, not-eligible, no-env, repo-mismatch, partial, empty, no-branch, archived, push-refused, bad-record, slow",
	}

	add := func(f file, mf manifestFile) {
		sum := sha256.Sum256(f.data)
		mf.Path, mf.SHA256, mf.Size = f.path, hex.EncodeToString(sum[:]), len(f.data)
		files = append(files, f)
		m.Files = append(m.Files, mf)
	}
	versions := map[string][]string{}
	for _, e := range entries {
		b, err := os.ReadFile(e.src)
		if err != nil {
			return "", err
		}
		add(file{path: e.path, data: b}, manifestFile{Kind: e.kind, Agent: e.agent, AgentVersion: e.agentVersion, Schema: e.schema})
		if e.agent != "" && !slices.Contains(versions[e.agent], e.agentVersion) {
			versions[e.agent] = append(versions[e.agent], e.agentVersion)
		}
	}
	var specs []registry.SpecData
	for _, mod := range all.Registry().All() {
		s := mod.Spec()
		specs = append(specs, registry.Data(s))
		v := versions[string(s.ID)]
		sort.Strings(v)
		m.Agents = append(m.Agents, bundleAgent{ID: string(s.ID), Name: s.Name, Tested: append([]string{}, s.Tested...), Versions: append([]string{}, v...)})
	}
	sb, err := json.MarshalIndent(specs, "", "  ")
	if err != nil {
		return "", err
	}
	add(file{path: "agents.json", data: append(sb, '\n')}, manifestFile{Kind: "specs"})

	tmp, err := os.MkdirTemp("", "testbundle")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "-")
		name := "fakeagent"
		if goos == "windows" {
			name += ".exe"
		}
		bin := filepath.Join(tmp, p, name)
		cmd := exec.Command("go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", bin, "./internal/devtools/fakeagent")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+goos, "GOARCH="+goarch)
		if o, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("building the stand-in for %s: %v\n%s", p, err, o)
		}
		b, err := os.ReadFile(bin)
		if err != nil {
			return "", err
		}
		add(file{path: "bin/" + p + "/" + name, data: b, exec: true}, manifestFile{Kind: "program"})
	}

	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	mb, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return "", err
	}
	mb = append(mb, '\n')
	ms, err := schemalite.Load(filepath.Join(root, "testbundle", "schemas", "bundle-manifest.schema.json"))
	if err != nil {
		return "", err
	}
	if errs := ms.Validate(mb); len(errs) > 0 {
		return "", fmt.Errorf("manifest.json: %s", strings.Join(errs, "; "))
	}
	files = append(files, file{path: "manifest.json", data: mb})

	if err := os.MkdirAll(out, 0o755); err != nil {
		return "", err
	}
	name := "hopsesh-testbundle-" + version + ".tar.gz"
	archive, err := tarball("hopsesh-testbundle-"+version, files, date)
	if err != nil {
		return "", err
	}
	dst := filepath.Join(out, name)
	if err := os.WriteFile(dst, archive, 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256(archive)
	if err := os.WriteFile(dst+".sha256", []byte(hex.EncodeToString(sum[:])+"  "+name+"\n"), 0o644); err != nil {
		return "", err
	}
	return dst, nil
}

// tarball writes the files under top/ as a gzipped tar, with folders, sorted, every
// entry dated mtime and owned by no one.
func tarball(top string, files []file, mtime time.Time) ([]byte, error) {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(zw)
	dirs := map[string]bool{}
	hdr := func(name string, mode int64, size int, typ byte) *tar.Header {
		return &tar.Header{Name: name, Mode: mode, Size: int64(size), Typeflag: typ, ModTime: mtime, Format: tar.FormatPAX}
	}
	var mkdir func(string) error
	mkdir = func(d string) error {
		if d == "." || dirs[d] {
			return nil
		}
		if err := mkdir(filepath.ToSlash(filepath.Dir(d))); err != nil {
			return err
		}
		dirs[d] = true
		return tw.WriteHeader(hdr(d+"/", 0o755, 0, tar.TypeDir))
	}
	for _, f := range files {
		p := top + "/" + f.path
		if err := mkdir(filepath.ToSlash(filepath.Dir(p))); err != nil {
			return nil, err
		}
		mode := int64(0o644)
		if f.exec {
			mode = 0o755
		}
		if err := tw.WriteHeader(hdr(p, mode, len(f.data), tar.TypeReg)); err != nil {
			return nil, err
		}
		if _, err := io.Copy(tw, bytes.NewReader(f.data)); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func git(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(b)), nil
}
