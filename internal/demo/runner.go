// Package demo implements the standalone colorized demo harness for vybe.
package demo

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

// ANSI color constants.
const (
	colorReset  = "\033[0m"
	colorBold   = "\033[1m"
	colorDim    = "\033[2m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
	colorBgBlue = "\033[44m"
)

// Runner holds the demo execution state.
type Runner struct {
	binPath string
	dbPath  string
	agent   string
	out     io.Writer
	color   bool
	fast    bool
}

// NewRunner creates a new demo runner.
// binPath is resolved to an absolute path so that vybeWithDir works correctly
// when cmd.Dir is set (relative paths are resolved relative to cmd.Dir, not cwd).
func NewRunner(binPath, dbPath, agent string, out io.Writer, fast bool) *Runner {
	color := false
	if f, ok := out.(*os.File); ok {
		color = isatty.IsTerminal(f.Fd())
	}
	if abs, err := filepath.Abs(binPath); err == nil {
		binPath = abs
	}
	return &Runner{
		binPath: binPath,
		dbPath:  dbPath,
		agent:   agent,
		out:     out,
		color:   color,
		fast:    fast,
	}
}

func (r *Runner) colorize(code, s string) string {
	if !r.color {
		return s
	}
	return code + s + colorReset
}

// printAct prints an act header.
func (r *Runner) printAct(number int, name string) {
	header := fmt.Sprintf("  Act %d: %s  ", number, name)
	if r.color {
		_, _ = fmt.Fprintf(r.out, "\n%s%s%s\n", colorBold+colorBgBlue+colorWhite, header, colorReset)
	} else {
		_, _ = fmt.Fprintf(r.out, "\n=== Act %d: %s ===\n", number, name)
	}
}

// printNarration prints narration lines.
func (r *Runner) printNarration(lines []string) {
	for _, line := range lines {
		_, _ = fmt.Fprintf(r.out, "  %s\n", r.colorize(colorWhite, line))
	}
	_, _ = fmt.Fprintln(r.out)
}

// printStep prints a step name.
func (r *Runner) printStep(name string) {
	_, _ = fmt.Fprintf(r.out, "  %s %s\n", r.colorize(colorBold+colorCyan, "●"), r.colorize(colorBold+colorCyan, name))
}

// printCommand prints the command being run.
func (r *Runner) printCommand(args []string) {
	_, _ = fmt.Fprintf(r.out, "    %s\n", r.colorize(colorDim, "$ vybe "+strings.Join(args, " ")))
}

// printPass prints a pass indicator.
func (r *Runner) printPass(detail string) {
	msg := r.colorize(colorGreen, "✓")
	if detail != "" {
		_, _ = fmt.Fprintf(r.out, "    %s %s\n", msg, r.colorize(colorGreen, detail))
	} else {
		_, _ = fmt.Fprintf(r.out, "    %s\n", msg)
	}
}

// printFail prints a failure indicator.
func (r *Runner) printFail(err error) {
	_, _ = fmt.Fprintf(r.out, "    %s %s\n", r.colorize(colorRed, "✗"), r.colorize(colorRed, err.Error()))
}

// printDetail prints a detail line.
func (r *Runner) printDetail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(r.out, "      %s\n", r.colorize(colorDim, msg))
}

// printInsight prints a post-step insight in a distinctive dim style.
func (r *Runner) printInsight(msg string) {
	if msg == "" {
		return
	}
	if r.color {
		_, _ = fmt.Fprintf(r.out, "    %s %s\n", colorDim+colorWhite+"→"+colorReset, colorDim+colorWhite+msg+colorReset)
	} else {
		_, _ = fmt.Fprintf(r.out, "    → %s\n", msg)
	}
}

func (r *Runner) vybe(args ...string) (map[string]any, string, error) {
	r.printCommand(args)
	return RunCLIJSON(CLICommandOptions{
		BinPath:      r.binPath,
		DBPath:       r.dbPath,
		Agent:        r.agent,
		IncludeAgent: true,
	}, args...)
}

// vybeWithStdin runs vybe with piped stdin.
func (r *Runner) vybeWithStdin(stdin string, args ...string) (map[string]any, string, error) {
	r.printCommand(args)
	return RunCLIJSON(CLICommandOptions{
		BinPath:      r.binPath,
		DBPath:       r.dbPath,
		Agent:        r.agent,
		Stdin:        stdin,
		IncludeAgent: true,
	}, args...)
}


// vybeWithDir runs vybe with a custom working directory.
func (r *Runner) vybeWithDir(dir string, args ...string) (map[string]any, string, error) {
	r.printCommand(args)
	return RunCLIJSON(CLICommandOptions{
		BinPath:      r.binPath,
		DBPath:       r.dbPath,
		Agent:        r.agent,
		Dir:          dir,
		IncludeAgent: true,
	}, args...)
}

// mustSuccess returns an error if success != true.
func (r *Runner) mustSuccess(m map[string]any, raw string) error {
	if m == nil {
		return fmt.Errorf("nil response (raw: %s)", raw)
	}
	if m["success"] != true {
		return fmt.Errorf("success=false: %s", raw)
	}
	return nil
}

// RunAll runs all acts in order, returning pass/fail counts.
func (r *Runner) RunAll(continueOnError bool) (passed, failed int) {
	ctx := &DemoContext{}

	acts := BuildActs()

	for _, act := range acts {
		r.printAct(act.Number, act.Name)
		r.printNarration(act.Narration)

		for _, step := range act.Steps {
			r.printStep(step.Name)
			err := step.Fn(r, ctx)
			if err != nil {
				r.printFail(err)
				failed++
				if !continueOnError {
					_, _ = fmt.Fprintf(r.out, "\n%s\n", r.colorize(colorRed+colorBold, "Stopped on first failure. Use --continue-on-error to proceed."))
					return passed, failed
				}
			} else {
				r.printPass("")
				r.printInsight(step.Insight)
				passed++
				if !r.fast {
					time.Sleep(2 * time.Second)
				}
			}
		}
	}

	return passed, failed
}
