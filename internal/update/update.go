// Package update checks for and installs new hopsesh releases from GitHub.
//
// Trust, in layers:
//  1. The archive must match checksums.txt from the same release (integrity).
//  2. Release builds embed an ECDSA P-256 public key (PublicKey, set with -ldflags);
//     checksums.txt must then carry a valid signature (checksums.txt.sig, ASN.1 DER,
//     made with `openssl dgst -sha256 -sign`). This holds even if the GitHub account
//     or a CDN were compromised. Builds without a key can check but not install.
//  3. On macOS, when the running program is code-signed, the new one must be signed by
//     the same team (and an app must pass Gatekeeper, so it is notarized).
//
// What is replaced follows what is running: the command-line tool, the macOS app (the
// whole hopsesh.app, from the release's disk image) or the Windows app (hopsesh-app.exe
// and hopsesh.exe together, from the release's app zip). Copies installed by Homebrew,
// Scoop or winget are left to those.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// Repo is the GitHub repository releases come from.
const Repo = "roeehrl/hopsesh"

// PublicKey is the base64 DER (PKIX) ECDSA P-256 key release builds embed:
//
//	-X github.com/roeehrl/hopsesh/internal/update.PublicKey=MFkw...
var PublicKey = ""

// API is the GitHub API base (tests point it elsewhere).
var API = "https://api.github.com"

// Release is the latest release's relevant parts.
type Release struct {
	Tag     string            `json:"tag"`
	Version string            `json:"version"`
	URL     string            `json:"url"`
	Assets  map[string]string `json:"-"` // name → download URL
}

// Latest fetches the newest published release.
func Latest(ctx context.Context) (*Release, error) { return fetchRelease(ctx, "latest") }
func ByVersion(ctx context.Context, version string) (*Release, error) {
	if _, ok := parse(version); !ok {
		return nil, errors.New("release version must be a semantic version")
	}
	return fetchRelease(ctx, "tags/"+url.PathEscape("v"+strings.TrimPrefix(version, "v")))
}
func fetchRelease(ctx context.Context, route string) (*Release, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", API+"/repos/"+Repo+"/releases/"+route, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, ErrNoReleases
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r struct {
		Tag    string `json:"tag_name"`
		URL    string `json:"html_url"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return nil, err
	}
	rel := &Release{Tag: r.Tag, Version: strings.TrimPrefix(r.Tag, "v"), URL: r.URL, Assets: map[string]string{}}
	for _, a := range r.Assets {
		rel.Assets[a.Name] = a.URL
	}
	return rel, nil
}

// Newer reports whether version a is newer than b (semantic versions, "v" optional;
// a development build is never newer and is always older than a release).
func Newer(a, b string) bool {
	pa, oka := parse(a)
	pb, okb := parse(b)
	if !oka {
		return false
	}
	if !okb {
		return true
	}
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	// 1.2.3 is newer than 1.2.3-rc1
	return pa[3] == 0 && pb[3] == 1
}

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(-[0-9A-Za-z.-]+)?$`)

func parse(v string) ([4]int, bool) {
	m := semver.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [4]int{}, false
	}
	var out [4]int
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	if m[4] != "" {
		out[3] = 1
	}
	return out, true
}

// ArchiveName is the release archive for this platform (matches .goreleaser.yaml).
func ArchiveName(version string) string {
	ext := "tar.gz"
	if runtime.GOOS == "windows" {
		ext = "zip"
	}
	return fmt.Sprintf("hopsesh_%s_%s_%s.%s", version, runtime.GOOS, runtime.GOARCH, ext)
}

// Kind is what an update replaces.
type Kind string

const (
	KindCLI        Kind = "cli"         // the hopsesh program
	KindMacApp     Kind = "mac-app"     // hopsesh.app, with the command-line tool inside
	KindWindowsApp Kind = "windows-app" // the folder with hopsesh-app.exe and hopsesh.exe
)

// Target is the install the running program belongs to.
type Target struct {
	Kind Kind
	Path string // the program, the .app bundle, or the Windows app's folder
}

// Current is the install the running program belongs to.
func Current() (Target, error) {
	exe, err := os.Executable()
	if err != nil {
		return Target{}, err
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		exe = r
	}
	return targetFor(exe, runtime.GOOS), nil
}

func targetFor(exe, goos string) Target {
	switch goos {
	case "darwin":
		if i := strings.Index(exe, ".app/Contents/"); i >= 0 {
			return Target{KindMacApp, exe[:i+len(".app")]}
		}
	case "windows":
		dir := filepath.Dir(exe)
		if isFile(filepath.Join(dir, "hopsesh-app.exe")) && isFile(filepath.Join(dir, "hopsesh.exe")) {
			return Target{KindWindowsApp, dir}
		}
	}
	return Target{KindCLI, exe}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// AssetName is the release file an update of t comes from (matches the release scripts).
func AssetName(t Target, version string) string {
	switch t.Kind {
	case KindMacApp:
		return fmt.Sprintf("hopsesh-%s-macos-universal.dmg", version)
	case KindWindowsApp:
		return fmt.Sprintf("hopsesh-%s-windows-%s-app.zip", version, runtime.GOARCH)
	}
	return ArchiveName(version)
}

// ManagedBy names the package manager that owns the running program, with the command to
// update it, or "" when hopsesh may replace itself.
func ManagedBy() (string, string) {
	exe, err := os.Executable()
	if err != nil {
		return "", ""
	}
	exe, _ = filepath.EvalSymlinks(exe)
	low := strings.ToLower(filepath.ToSlash(exe))
	switch {
	case strings.Contains(low, "/cellar/") || strings.Contains(low, "/homebrew/") || strings.Contains(low, "/linuxbrew/"):
		return "Homebrew", "brew upgrade hopsesh"
	case strings.Contains(low, "/scoop/apps/"):
		return "Scoop", "scoop update hopsesh"
	case strings.Contains(low, "/winget/") || strings.Contains(low, "/microsoft/winget/"):
		return "winget", "winget upgrade hopsesh"
	}
	return "", ""
}

// ErrNoReleases means the repository has no published release (yet).
var ErrNoReleases = errors.New("no hopsesh release is published yet")

// ErrNoKey means this build has no release key, so it will not install updates.
var ErrNoKey = errors.New("this build has no release signing key, so it cannot verify and install updates; reinstall from a release")

// Install downloads, verifies and installs rel over the install the running program
// belongs to (Current). It returns what it replaced: the program, the app or its folder.
func Install(ctx context.Context, rel *Release) (Target, error) {
	t, err := Current()
	if err != nil {
		return t, err
	}
	if PublicKey == "" {
		return t, ErrNoKey
	}
	if who, how := ManagedBy(); who != "" {
		return t, fmt.Errorf("this copy is managed by %s; update with: %s", who, how)
	}
	name := AssetName(t, rel.Version)
	data, err := download(ctx, rel, name)
	if err != nil {
		return t, err
	}
	switch t.Kind {
	case KindMacApp:
		err = installMacApp(ctx, t.Path, data)
	case KindWindowsApp:
		err = installWindowsApp(t.Path, data, rel.Version)
	default:
		err = installCLI(t.Path, data, name)
	}
	return t, err
}

// download fetches a release file and checks it against checksums.txt, whose signature
// must verify with the embedded release key.
func download(ctx context.Context, rel *Release, name string) ([]byte, error) {
	fileURL, sumURL, sigURL := rel.Assets[name], rel.Assets["checksums.txt"], rel.Assets["checksums.txt.sig"]
	if fileURL == "" || sumURL == "" {
		return nil, fmt.Errorf("release %s has no %s or checksums.txt", rel.Tag, name)
	}
	if sigURL == "" {
		return nil, fmt.Errorf("release %s is not signed (no checksums.txt.sig); not installing", rel.Tag)
	}
	sums, err := fetch(ctx, sumURL, 1<<20)
	if err != nil {
		return nil, err
	}
	sig, err := fetch(ctx, sigURL, 4<<10)
	if err != nil {
		return nil, err
	}
	if err := VerifySignature(sums, sig, PublicKey); err != nil {
		return nil, err
	}
	want, err := checksumFor(sums, name)
	if err != nil {
		return nil, err
	}
	data, err := fetch(ctx, fileURL, 300<<20)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(data)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("%s does not match checksums.txt; not installing", name)
	}
	return data, nil
}

// installCLI replaces the hopsesh program exe with the one in a release archive.
func installCLI(exe string, arch []byte, name string) error {
	bin, err := extract(arch, name)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return fmt.Errorf("cannot write next to %s (%w); reinstall with the install script instead", exe, err)
	}
	if err := sameSigner(exe, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := swap(exe, tmp); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// installWindowsApp replaces hopsesh-app.exe and hopsesh.exe in the app's folder with the
// ones in the release's app zip, and the terminal's pseudoconsole (conptyFiles) when the
// zip has it; every running file is moved aside, and CleanUp removes them later.
func installWindowsApp(dir string, zipData []byte, version string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return err
	}
	names := []string{"hopsesh-app.exe", "hopsesh.exe"}
	files := map[string][]byte{}
	for _, f := range zr.File {
		for _, n := range append(names, conptyFiles...) {
			if f.Name == n && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return err
				}
				b, err := io.ReadAll(io.LimitReader(rc, (200<<20)+1))
				rc.Close()
				if err != nil {
					return err
				}
				if len(b) > 200<<20 {
					return fmt.Errorf("%s exceeds the app archive file limit", n)
				}
				if files[n] != nil {
					return fmt.Errorf("duplicate app archive file %s", n)
				}
				files[n] = b
			}
		}
	}
	for _, n := range names {
		if files[n] == nil {
			return fmt.Errorf("%s is missing from the app zip; not installing", n)
		}
	}
	for _, n := range conptyFiles {
		if files[n] != nil {
			names = append(names, n)
		}
	}
	if err := publishWindowsAppFiles(dir, files, names, (*os.Root).Rename); err != nil {
		return err
	}
	if version != "" {
		setInstalledVersion(version)
	}
	return nil
}

// conptyFiles are the terminal's pseudoconsole in the Windows app's folder (see
// internal/core/pty), which app zips carry from 0.4.0.
var conptyFiles = []string{"conpty/conpty.dll", "conpty/x64/OpenConsole.exe", "conpty/arm64/OpenConsole.exe", "conpty/LICENSE-conpty.txt"}

// CleanUp removes what an earlier update left behind: the programs Windows moved aside,
// and a macOS update's work folder. Best effort; call it at start-up.
func CleanUp() {
	t, err := Current()
	if err != nil {
		return
	}
	switch t.Kind {
	case KindWindowsApp:
		cleanupWindowsApp(t.Path)
	case KindMacApp:
		old, _ := filepath.Glob(filepath.Join(filepath.Dir(t.Path), ".hopsesh-update-*"))
		for _, d := range old {
			if fi, err := os.Stat(d); err == nil && time.Since(fi.ModTime()) > time.Hour {
				_ = os.RemoveAll(d)
			}
		}
	default:
		_ = os.Remove(t.Path + ".old")
	}
}

// CLIPath is the hopsesh command-line program of an install.
func CLIPath(t Target) string {
	switch t.Kind {
	case KindMacApp:
		return filepath.Join(t.Path, "Contents", "Resources", "bin", "hopsesh")
	case KindWindowsApp:
		return filepath.Join(t.Path, "hopsesh.exe")
	}
	return t.Path
}

// Relaunch starts the app at t again (after an update), for the running one to quit.
func Relaunch(t Target) error {
	switch t.Kind {
	case KindMacApp:
		// After a moment, so this copy has quit and macOS opens the new one.
		return proc.Command("/bin/sh", "-c", `sleep 1; exec open -n "$0"`, t.Path).Start()
	case KindWindowsApp:
		return proc.Command(filepath.Join(t.Path, "hopsesh-app.exe")).Start()
	}
	return errors.New("not an app")
}

// VerifySignature checks an ASN.1 ECDSA signature over data with a base64 PKIX key.
func VerifySignature(data, sig []byte, pubB64 string) error {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil {
		if blk, _ := pem.Decode([]byte(pubB64)); blk != nil {
			der = blk.Bytes
		} else {
			return fmt.Errorf("release key: %w", err)
		}
	}
	k, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return fmt.Errorf("release key: %w", err)
	}
	pk, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("release key is not ECDSA")
	}
	h := sha256.Sum256(data)
	if !ecdsa.VerifyASN1(pk, h[:], sig) {
		return errors.New("checksums.txt signature is not valid for this build's release key; not installing")
	}
	return nil
}

func checksumFor(sums []byte, name string) (string, error) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("checksums.txt has no entry for %s", name)
}

func extract(archive []byte, name string) ([]byte, error) {
	want := "hopsesh"
	if runtime.GOOS == "windows" {
		want = "hopsesh.exe"
	}
	if strings.HasSuffix(name, ".zip") {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if filepath.Base(f.Name) == want && !f.FileInfo().IsDir() {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, (200<<20)+1))
			}
		}
		return nil, fmt.Errorf("%s not in archive", want)
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("%s not in archive", want)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == want && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, 200<<20))
		}
	}
}

// sameSigner requires, on macOS, that a signed running program (or app) is replaced only
// by one signed by the same team.
func sameSigner(current, next string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	cur := teamID(current)
	if cur == "" {
		return nil // unsigned build: nothing to compare
	}
	args := []string{"--verify", "--strict"}
	if strings.HasSuffix(next, ".app") {
		args = append(args, "--deep")
	}
	if proc.Command("codesign", append(args, next)...).Run() != nil {
		return errors.New("the downloaded binary's code signature is not valid; not installing")
	}
	if got := teamID(next); got != cur {
		return fmt.Errorf("the downloaded binary is signed by team %q, not %q; not installing", got, cur)
	}
	return nil
}

func teamID(path string) string {
	out, _ := proc.Command("codesign", "-dv", "--verbose=2", path).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(l, "TeamIdentifier="); ok && v != "not set" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// swap replaces exe with next. Windows cannot overwrite a running program, but it can
// rename it, so the old one is moved aside first.
func swap(exe, next string) error {
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(next, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(next, exe)
}

func client() *http.Client { return &http.Client{Timeout: 2 * time.Minute} }

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(API, "http://127.0.0.1") {
		return nil, fmt.Errorf("refusing a non-HTTPS download: %s", url)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("download %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("download %s is larger than expected", url)
	}
	return b, nil
}
