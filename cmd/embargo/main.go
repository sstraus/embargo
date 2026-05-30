package main

import (
	"os"
	"runtime/debug"

	"github.com/sstraus/embargo/internal/cli"
)

// Set via -ldflags at release time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(buildInfo()))
}

// buildInfo returns the version metadata. Release builds inject it via -ldflags;
// for `go install ...@version` and local builds (where ldflags are absent) it
// falls back to the module version and VCS stamps embedded by the Go toolchain,
// so `embargo --version` reports something real instead of "dev".
func buildInfo() cli.BuildInfo {
	bi := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	if version != "dev" {
		return bi // injected at release time; authoritative
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return bi
	}
	if v := info.Main.Version; v != "" && v != "(devel)" {
		bi.Version = v
	}
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			bi.Commit = s.Value
		case "vcs.time":
			bi.Date = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				bi.Commit += "-dirty"
			}
		}
	}
	return bi
}
