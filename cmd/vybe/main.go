package main

import (
	"os"
	"runtime/debug"

	"github.com/dotcommander/vybe/internal/commands"
)

// version is set via ldflags (-X main.version=v1.0.0) or detected
// automatically from Go module info embedded by go install.
var version = "dev"

func main() {
	if version == "dev" {
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
			version = info.Main.Version
		}
	}
	if err := commands.Execute(version); err != nil {
		os.Exit(1)
	}
}
