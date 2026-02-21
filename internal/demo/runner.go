// Package demo implements the standalone colorized demo harness for vybe.
package demo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
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
		fmt.Fprintf(r.out, "\n%s%s%s\n", colorBold+colorBgBlue+colorWhite, header, colorReset)
	} else {
		fmt.Fprintf(r.out, "\n=== Act %d: %s ===\n", number, name)
	}
}

// printNarration prints narration lines.
func (r *Runner) printNarration(lines []string) {
	for _, line := range lines {
		fmt.Fprintf(r.out, "  %s\n", r.colorize(colorWhite, line))
	}
	fmt.Fprintln(r.out)
}

// printStep prints a step name.
func (r *Runner) printStep(name string) {
	fmt.Fprintf(r.out, "  %s %s\n", r.colorize(colorBold+colorCyan, "●"), r.colorize(colorBold+colorCyan, name))
}

// printCommand prints the command being run.
func (r *Runner) printCommand(args []string) {
	fmt.Fprintf(r.out, "    %s\n", r.colorize(colorDim, "$ vybe "+strings.Join(args, " ")))
}

// printPass prints a pass indicator.
func (r *Runner) printPass(detail string) {
	msg := r.colorize(colorGreen, "✓")
	if detail != "" {
		fmt.Fprintf(r.out, "    %s %s\n", msg, r.colorize(colorGreen, detail))
	} else {
		fmt.Fprintf(r.out, "    %s\n", msg)
	}
}

// printFail prints a failure indicator.
func (r *Runner) printFail(err error) {
	fmt.Fprintf(r.out, "    %s %s\n", r.colorize(colorRed, "✗"), r.colorize(colorRed, err.Error()))
}

// printDetail prints a detail line.
func (r *Runner) printDetail(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(r.out, "      %s\n", r.colorize(colorDim, msg))
}

// printInsight prints a post-step insight in a distinctive dim style.
func (r *Runner) printInsight(msg string) {
	if msg == "" {
		return
	}
	if r.color {
		fmt.Fprintf(r.out, "    %s %s\n", colorDim+colorWhite+"→"+colorReset, colorDim+colorWhite+msg+colorReset)
	} else {
		fmt.Fprintf(r.out, "    → %s\n", msg)
	}
}

// vybe runs the vybe binary with --db-path and --agent flags.
// parseLastJSON parses the last valid JSON line from multi-line output.
// Some commands (e.g. hook install) may emit a schema-check error line followed
// by the actual result. We take the last successfully parseable JSON object.
func parseLastJSON(raw string) (map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err == nil {
			return m, nil
		}
	}
	// Fall back to parsing the whole raw as single JSON
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return nil, fmt.Errorf("parse JSON: %w (output: %s)", err, raw)
	}
	return m, nil
}

func (r *Runner) vybe(args ...string) (map[string]any, string, error) {
	fullArgs := append([]string{"--db-path", r.dbPath, "--agent", r.agent}, args...)
	r.printCommand(args)
	cmd := exec.Command(r.binPath, fullArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return nil, raw, nil
	}
	m, err := parseLastJSON(raw)
	if err != nil {
		return nil, raw, err
	}
	return m, raw, nil
}

// vybeWithStdin runs vybe with piped stdin.
func (r *Runner) vybeWithStdin(stdin string, args ...string) (map[string]any, string, error) {
	fullArgs := append([]string{"--db-path", r.dbPath, "--agent", r.agent}, args...)
	r.printCommand(args)
	cmd := exec.Command(r.binPath, fullArgs...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return nil, raw, nil
	}
	m, err := parseLastJSON(raw)
	if err != nil {
		return nil, raw, err
	}
	return m, raw, nil
}

// vybeRaw runs vybe with only --db-path (no --agent).
func (r *Runner) vybeRaw(args ...string) string {
	fullArgs := append([]string{"--db-path", r.dbPath}, args...)
	r.printCommand(args)
	cmd := exec.Command(r.binPath, fullArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	_ = cmd.Run()
	return strings.TrimSpace(stdout.String())
}

// vybeWithDir runs vybe with a custom working directory.
func (r *Runner) vybeWithDir(dir string, args ...string) (map[string]any, string, error) {
	fullArgs := append([]string{"--db-path", r.dbPath, "--agent", r.agent}, args...)
	r.printCommand(args)
	cmd := exec.Command(r.binPath, fullArgs...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return nil, raw, nil
	}
	m, err := parseLastJSON(raw)
	if err != nil {
		return nil, raw, err
	}
	return m, raw, nil
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

// getStr extracts a nested string field from the parsed JSON.
func getStr(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		if mm, ok := cur.(map[string]any); ok {
			cur = mm[k]
		} else {
			return ""
		}
	}
	if s, ok := cur.(string); ok {
		return s
	}
	return ""
}

// rid generates a deterministic request ID for a given phase and step.
func rid(phase string, step int) string {
	return fmt.Sprintf("demo_%s_%d", phase, step)
}

// hookStdin builds the JSON stdin payload for hook commands.
func hookStdin(eventName, sessionID, cwd, source, prompt, toolName string) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"prompt":          prompt,
		"tool_name":       toolName,
		"tool_input":      map[string]any{},
		"tool_response":   map[string]any{},
		"source":          source,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}

// hookStdinWithToolInput builds the JSON stdin payload for hook commands with tool input.
func hookStdinWithToolInput(eventName, sessionID, cwd, toolName string, toolInput map[string]any) string {
	payload := map[string]any{
		"cwd":             cwd,
		"session_id":      sessionID,
		"hook_event_name": eventName,
		"tool_name":       toolName,
		"tool_input":      toolInput,
		"tool_response":   map[string]any{"output": "ok"},
		"source":          "",
	}
	data, _ := json.Marshal(payload)
	return string(data)
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
					fmt.Fprintf(r.out, "\n%s\n", r.colorize(colorRed+colorBold, "Stopped on first failure. Use --continue-on-error to proceed."))
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
