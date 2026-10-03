// Command winres writes the Windows resources of a hopsesh program: the icon (at resource
// ID 3, where the Wails runtime loads the window and taskbar icon from), the manifest
// (per-monitor DPI awareness, common controls v6, no elevation) and the version
// information Explorer shows. Go links the .syso file into the build of its folder.
//
//	go run ./internal/devtools/winres -icon icon.png -arch amd64 -version 0.3.0 \
//	    -name hopsesh-app.exe -out cmd/hopsesh-app/rsrc_windows_amd64.syso
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"os"
	"regexp"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
)

const manifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly manifestVersion="1.0" xmlns="urn:schemas-microsoft-com:asm.v1" xmlns:asmv3="urn:schemas-microsoft-com:asm.v3">
  <assemblyIdentity type="win32" name="dev.codonic.hopsesh" version="%s" processorArchitecture="*"/>
  <dependency>
    <dependentAssembly>
      <assemblyIdentity type="win32" name="Microsoft.Windows.Common-Controls" version="6.0.0.0" processorArchitecture="*" publicKeyToken="6595b64144ccf1df" language="*"/>
    </dependentAssembly>
  </dependency>
  <asmv3:application>
    <asmv3:windowsSettings>
      <dpiAware xmlns="http://schemas.microsoft.com/SMI/2005/WindowsSettings">true/pm</dpiAware>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">permonitorv2,permonitor</dpiAwareness>
    </asmv3:windowsSettings>
  </asmv3:application>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>`

func main() {
	iconPath := flag.String("icon", "", "the app icon, a square PNG (internal/devtools/icon makes it)")
	arch := flag.String("arch", "amd64", "amd64 or arm64")
	ver := flag.String("version", "0.0.0", "the release version (a pre-release suffix is dropped from the numeric version)")
	name := flag.String("name", "hopsesh-app.exe", "the program's file name")
	desc := flag.String("description", "hopsesh", "what Explorer shows as the file description")
	out := flag.String("out", "", "the .syso file to write")
	icoOut := flag.String("ico", "", "also write the icon as an .ico file (for the installer)")
	flag.Parse()
	if *iconPath == "" || *out == "" {
		log.Fatal("-icon and -out are required")
	}
	num := numeric(*ver)

	f, err := os.Open(*iconPath)
	if err != nil {
		log.Fatal(err)
	}
	img, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}
	icon, err := winres.NewIconFromResizedImage(img, []int{256, 64, 48, 32, 24, 16})
	if err != nil {
		log.Fatal(err)
	}
	if *icoOut != "" {
		w, err := os.Create(*icoOut)
		if err != nil {
			log.Fatal(err)
		}
		if err := icon.SaveICO(w); err != nil {
			log.Fatal(err)
		}
		if err := w.Close(); err != nil {
			log.Fatal(err)
		}
	}
	rs := winres.ResourceSet{}
	if err := rs.SetIcon(winres.RT_ICON, icon); err != nil { // ID 3: what Wails loads
		log.Fatal(err)
	}
	m, err := winres.AppManifestFromXML([]byte(fmt.Sprintf(manifest, num)))
	if err != nil {
		log.Fatal(err)
	}
	rs.SetManifest(m)

	var vi version.Info
	vi.SetFileVersion(num)
	vi.SetProductVersion(num)
	for k, v := range map[string]string{
		version.ProductName: "hopsesh", version.ProductVersion: *ver, version.FileVersion: *ver,
		version.FileDescription: *desc, version.OriginalFilename: *name, version.InternalName: *name,
		version.LegalCopyright: "hopsesh authors, Apache License 2.0",
	} {
		if err := vi.Set(version.LangDefault, k, v); err != nil {
			log.Fatal(err)
		}
	}
	rs.SetVersionInfo(vi)

	archs := map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64}
	a, ok := archs[*arch]
	if !ok {
		log.Fatalf("unsupported arch %q", *arch)
	}
	w, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := rs.WriteObject(w, a); err != nil {
		log.Fatal(err)
	}
	if err := w.Close(); err != nil {
		log.Fatal(err)
	}
}

// numeric is the four-part version Windows wants: 0.3.0-rc.1 becomes 0.3.0.0.
func numeric(v string) string {
	m := regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(v)
	if m == nil {
		return "0.0.0.0"
	}
	return m[1] + "." + m[2] + "." + m[3] + ".0"
}
