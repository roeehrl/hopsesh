// Package version carries build metadata injected at link time by GoReleaser.
package version

import "fmt"

var (
	// Version is the semantic version, e.g. "0.3.0". "dev" for local builds.
	Version = "dev"
	// Commit is the git commit the binary was built from.
	Commit = "none"
	// Date is the build date in RFC 3339.
	Date = "unknown"
)

// String returns a one-line description of the build.
func String() string {
	return fmt.Sprintf("hopsesh %s (commit %s, built %s)", Version, Commit, Date)
}
