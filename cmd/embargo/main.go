package main

import (
	"os"

	"github.com/sstraus/embargo/internal/cli"
)

// Set via -ldflags at release time by GoReleaser.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	os.Exit(cli.Execute(cli.BuildInfo{Version: version, Commit: commit, Date: date}))
}
