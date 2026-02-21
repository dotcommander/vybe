// Command demo runs a colorized, self-contained demonstration of all vybe capabilities.
// It shells out to the vybe binary and exercises all 72 steps from the integration test.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/dotcommander/vybe/internal/demo"
)

func main() {
	var binPath string
	var continueOnError bool
	var fast bool
	flag.StringVar(&binPath, "bin", "", "Path to vybe binary (default: builds from source)")
	flag.BoolVar(&continueOnError, "continue-on-error", false, "Continue after step failures")
	flag.BoolVar(&fast, "fast", false, "Skip 2s pause after each successful step")
	flag.Parse()

	if binPath == "" {
		tmpDir, err := os.MkdirTemp("", "vybe-demo-bin-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create temp dir: %v\n", err)
			os.Exit(1)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		binPath = filepath.Join(tmpDir, "vybe")
		fmt.Fprintln(os.Stderr, "Building vybe binary...")
		buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/vybe")
		buildCmd.Stdout = os.Stderr
		buildCmd.Stderr = os.Stderr
		if err := buildCmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to build vybe: %v\n", err)
			os.Exit(1)
		}
	}

	dbDir, err := os.MkdirTemp("", "vybe-demo-db-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create DB dir: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = os.RemoveAll(dbDir) }()
	dbPath := filepath.Join(dbDir, "vybe-demo.db")

	r := demo.NewRunner(binPath, dbPath, "demo-agent", os.Stdout, fast)
	passed, failed := r.RunAll(continueOnError)

	_, _ = fmt.Fprintf(os.Stdout, "\n%d passed, %d failed, %d total\n", passed, failed, passed+failed)
	if failed > 0 {
		os.Exit(1)
	}
}
