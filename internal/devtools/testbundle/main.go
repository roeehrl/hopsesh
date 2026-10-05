// Command testbundle checks and builds hopsesh's test bundle: the stand-in agents (the
// fakeagent program, built for each platform), the agents' fixtures, the specs of the
// compiled-in modules, and the payloads other projects contribute under testbundle/
// (scrubbed hook payloads, running-session registry entries). A release publishes it as
// hopsesh-testbundle-<version>.tar.gz, so a project that bundles hopsesh tests against
// exactly the version it ships. Layout and contribution rules: testbundle/README.md.
//
//	testbundle check                         validate the contributions and the fixtures
//	testbundle build -version V -out DIR     check, then write DIR/hopsesh-testbundle-V.tar.gz and .sha256
//
// Run from the repository root. It is not shipped.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "check":
		fs := flag.NewFlagSet("check", flag.ExitOnError)
		root := fs.String("root", ".", "the repository root")
		_ = fs.Parse(os.Args[2:])
		files, errs := check(*root)
		report(errs)
		fmt.Printf("testbundle: %d files checked\n", len(files))
	case "build":
		fs := flag.NewFlagSet("build", flag.ExitOnError)
		root := fs.String("root", ".", "the repository root")
		version := fs.String("version", "", "the hopsesh version (\"0.4.0\")")
		out := fs.String("out", "dist/testbundle", "the folder for the archive and its checksum")
		commit := fs.String("commit", "", "the commit (default: git rev-parse HEAD)")
		_ = fs.Parse(os.Args[2:])
		if *version == "" {
			usage()
		}
		name, err := build(*root, *version, *commit, *out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "testbundle:", err)
			os.Exit(1)
		}
		fmt.Println(name)
	default:
		usage()
	}
}

func report(errs []string) {
	if len(errs) == 0 {
		return
	}
	for _, e := range errs {
		fmt.Fprintln(os.Stderr, "testbundle:", e)
	}
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: testbundle check [-root DIR] | build -version V [-out DIR] [-commit SHA] [-root DIR]")
	os.Exit(2)
}
