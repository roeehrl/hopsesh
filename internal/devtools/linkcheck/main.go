// Command linkcheck exercises the app's command-line link from inside a .app bundle
// (it must run as <bundle>/Contents/MacOS/<name>). Use a throwaway HOME.
package main

import (
	"fmt"
	"os"

	"github.com/roeehrl/hopsesh/internal/integrate"
)

func main() {
	show := func(step string) {
		st := integrate.CheckCLI()
		fmt.Printf("%-10s state=%s target=%s cannot=%q\n", step, st.State, st.Target, st.CanInstall)
	}
	show("before")
	if _, err := integrate.InstallCLI(false); err != nil {
		fmt.Println("install error:", err)
		os.Exit(1)
	}
	show("installed")
	if err := integrate.UninstallCLI(); err != nil {
		fmt.Println("uninstall error:", err)
		os.Exit(1)
	}
	show("removed")
}
