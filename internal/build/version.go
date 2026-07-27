// Package build carries the version metadata stamped in at link time.
package build

import "runtime/debug"

// These are set by -ldflags at release time (see .goreleaser.yaml). The
// defaults are what a plain `go build` produces.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// Info returns a one-line human-readable build description.
func Info() string {
	out := "leanctl " + Version

	if Commit != "" {
		out += " (" + shorten(Commit)
		if Date != "" {
			out += ", " + Date
		}

		out += ")"
	}

	return out
}

// GoVersion reports the toolchain the binary was built with.
func GoVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		return info.GoVersion
	}

	return "unknown"
}

func shorten(commit string) string {
	if len(commit) > 8 {
		return commit[:8]
	}

	return commit
}
