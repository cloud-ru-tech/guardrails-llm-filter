// Package version holds the build identity of the binary. The values are
// overridden at build time via -ldflags "-X .../internal/version.Version=..."
// (see the Makefile / Dockerfile); the defaults apply to `go run` and tests.
package version

var (
	// Version is the release version (git tag or "dev").
	Version = "dev"
	// Commit is the short git commit the binary was built from.
	Commit = "none"
	// Date is the build timestamp (RFC 3339) or "unknown".
	Date = "unknown"
)
