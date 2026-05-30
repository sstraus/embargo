package main

import (
	"runtime/debug"
	"testing"

	"github.com/sstraus/embargo/internal/cli"
)

func TestEnrichFromBuildInfoUsesModuleVersion(t *testing.T) {
	bi := cli.BuildInfo{Version: "dev", Commit: "none", Date: "unknown"}
	info := &debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		},
	}
	got := enrichFromBuildInfo(bi, info)
	if got.Version != "v1.2.3" {
		t.Errorf("Version = %q, want v1.2.3", got.Version)
	}
	if got.Commit != "abc123" {
		t.Errorf("Commit = %q, want abc123", got.Commit)
	}
	if got.Date != "2026-01-02T03:04:05Z" {
		t.Errorf("Date = %q, want the vcs.time stamp", got.Date)
	}
}

// The dirty suffix must survive regardless of setting order, since the Go
// toolchain does not guarantee vcs.revision precedes vcs.modified.
func TestEnrichFromBuildInfoDirtyIsOrderIndependent(t *testing.T) {
	revisionFirst := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "abc123"},
		{Key: "vcs.modified", Value: "true"},
	}
	modifiedFirst := []debug.BuildSetting{
		{Key: "vcs.modified", Value: "true"},
		{Key: "vcs.revision", Value: "abc123"},
	}
	for name, settings := range map[string][]debug.BuildSetting{
		"revision first": revisionFirst,
		"modified first": modifiedFirst,
	} {
		t.Run(name, func(t *testing.T) {
			got := enrichFromBuildInfo(cli.BuildInfo{Version: "dev"}, &debug.BuildInfo{Settings: settings})
			if got.Commit != "abc123-dirty" {
				t.Errorf("Commit = %q, want abc123-dirty", got.Commit)
			}
		})
	}
}

func TestEnrichFromBuildInfoIgnoresDevelModuleVersion(t *testing.T) {
	bi := cli.BuildInfo{Version: "dev", Commit: "none"}
	got := enrichFromBuildInfo(bi, &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}})
	if got.Version != "dev" {
		t.Errorf("Version = %q, want dev (a (devel) module version must be ignored)", got.Version)
	}
}
