// Command conptyfetch gets the pseudoconsole the Windows app carries for its terminal tabs:
// conpty.dll and OpenConsole.exe from Microsoft's Microsoft.Windows.Console.ConPTY NuGet
// package (MIT, https://github.com/microsoft/terminal), the version and hash pinned below.
// It checks the package against NuGet's published SHA-512 before taking anything out of
// it, and writes, into -out:
//
//	conpty.dll                 the package's for -arch
//	x64/OpenConsole.exe        the console host conpty.dll starts on an x64 machine
//	arm64/OpenConsole.exe      … and on an ARM64 one (an amd64 app runs on both)
//	LICENSE-conpty.txt         the licence notice
//
// That is the layout the package's own build files make, and the one
// internal/core/pty looks for in the "conpty" folder beside the app.
//
//	go run ./internal/devtools/conptyfetch -arch amd64 -out dist/windows/tmp/amd64/conpty
//
// -nupkg names a copy of the package: used when it is there (and checked all the same),
// written there after a download otherwise, so one build fetches it once.
package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// The pinned package: its version and NuGet's packageHash (SHA-512 of the .nupkg, base64),
// from https://api.nuget.org/v3/catalog0/data/2026.10.02.21.10.18/microsoft.windows.console.conpty.1.25.260930003.json.
const (
	Version = "1.25.260930003"
	SHA512  = "LuqMk9r5oTzeRne35IOlUdSQ7DkjorOIbcnODGZ81MvmUpTr/1VBQz3fAdCNCwkfuR0Mluq+H7rS3Hes/A1fOw=="
	URL     = "https://api.nuget.org/v3-flatcontainer/microsoft.windows.console.conpty/" + Version + "/microsoft.windows.console.conpty." + Version + ".nupkg"
	maxSize = 32 << 20
)

const license = `conpty.dll and OpenConsole.exe come from Microsoft's Microsoft.Windows.Console.ConPTY
package, version ` + Version + ` (https://www.nuget.org/packages/Microsoft.Windows.Console.ConPTY,
built from https://github.com/microsoft/terminal), under the MIT License:

Copyright (c) Microsoft Corporation. All rights reserved.

MIT License

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED *AS IS*, WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
`

func main() {
	arch := flag.String("arch", "amd64", "the app's architecture: amd64 or arm64")
	out := flag.String("out", "", "the folder to write (made if missing)")
	nupkg := flag.String("nupkg", "", "a copy of the package: used when there, kept there after a download")
	flag.Parse()
	if *out == "" {
		log.Fatal("conptyfetch: -out is required")
	}
	pkg, err := load(*nupkg)
	if err != nil {
		log.Fatalf("conptyfetch: %v", err)
	}
	if err := check(pkg, SHA512); err != nil {
		log.Fatalf("conptyfetch: %v", err)
	}
	if err := extract(pkg, *arch, *out); err != nil {
		log.Fatalf("conptyfetch: %v", err)
	}
	fmt.Printf("conpty %s for %s in %s\n", Version, *arch, *out)
}

func load(path string) ([]byte, error) {
	if path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return b, nil
		}
	}
	c := &http.Client{Timeout: 2 * time.Minute}
	resp, err := c.Get(URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", URL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, err
	}
	if path != "" {
		if err := check(b, SHA512); err == nil {
			_ = os.WriteFile(path, b, 0o644) //nolint:gosec // a public package
		}
	}
	return b, nil
}

// check compares the package's SHA-512 with the pinned one.
func check(pkg []byte, want string) error {
	sum := sha512.Sum512(pkg)
	if got := base64.StdEncoding.EncodeToString(sum[:]); got != want {
		return fmt.Errorf("the package's SHA-512 is %s, not the pinned %s; not using it", got, want)
	}
	return nil
}

// files maps where each file goes to where it is in the package, for an app's arch.
func files(arch string) (map[string]string, error) {
	switch arch {
	case "amd64":
		return map[string]string{
			"conpty.dll":            "runtimes/win-x64/native/conpty.dll",
			"x64/OpenConsole.exe":   "build/native/runtimes/x64/OpenConsole.exe",
			"arm64/OpenConsole.exe": "build/native/runtimes/arm64/OpenConsole.exe",
		}, nil
	case "arm64":
		return map[string]string{
			"conpty.dll":            "runtimes/win-arm64/native/conpty.dll",
			"arm64/OpenConsole.exe": "build/native/runtimes/arm64/OpenConsole.exe",
		}, nil
	}
	return nil, fmt.Errorf("no conpty for %q", arch)
}

// extract writes arch's files and the licence notice into out.
func extract(pkg []byte, arch, out string) error {
	want, err := files(arch)
	if err != nil {
		return err
	}
	zr, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		return err
	}
	inPkg := map[string]*zip.File{}
	for _, f := range zr.File {
		inPkg[f.Name] = f
	}
	for dst, src := range want {
		f, ok := inPkg[src]
		if !ok {
			return fmt.Errorf("%s is not in the package", src)
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		b, err := io.ReadAll(io.LimitReader(rc, maxSize))
		rc.Close()
		if err != nil {
			return err
		}
		if len(b) == 0 {
			return errors.New(src + " is empty")
		}
		p := filepath.Join(out, filepath.FromSlash(dst))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, b, 0o755); err != nil { //nolint:gosec // a program the app runs
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "LICENSE-conpty.txt"), []byte(license), 0o644) //nolint:gosec // a licence notice
}
