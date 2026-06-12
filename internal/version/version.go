// Package version holds version info injected at build time via -ldflags.
// Injection (Makefile):
//
//	-X github.com/zboya/nurvis/internal/version.Version=v1.2.3
//	-X github.com/zboya/nurvis/internal/version.Commit=abc1234
//	-X github.com/zboya/nurvis/internal/version.BuildTime=2026-06-05T12:00:00Z
package version

import "fmt"

// These variables are injected via -ldflags at build time; defaults are placeholders.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildTime = "unknown"
)

// String returns a one-line version string, e.g. "v1.2.3 (abc1234) built 2026-06-05T12:00:00Z".
func String() string {
	return fmt.Sprintf("%s (%s) built %s", Version, Commit, BuildTime)
}
