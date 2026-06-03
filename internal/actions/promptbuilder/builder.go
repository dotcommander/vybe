// Package promptbuilder assembles the resume-context prompt as an ordered
// series of fixed and budget-charged sections. It records each section call
// so callers can unit-test assembly behavior directly, without parsing the
// rendered string. Render output is a plain byte stream — no domain types.
package promptbuilder

import (
	"io"
	"strings"
	"unicode/utf8"
)

// SectionKind distinguishes always-written fixed sections from budget-charged ones.
type SectionKind int

const (
	// Fixed sections always render regardless of remaining budget.
	Fixed SectionKind = iota
	// Budgeted sections are charged against the remaining budget and may be
	// truncated or skipped when the budget is exhausted.
	Budgeted
)

// SectionRecord captures one write call for test inspection.
// Written reports how many lines were actually emitted (0 = fully skipped,
// e.g. budget exhausted). Cost is the token cost charged for budgeted writes.
type SectionRecord struct {
	Kind    SectionKind
	Written int
	Cost    int
}

// Builder accumulates prompt text. Construct with New. Budget is shared across
// all Budgeted writes and decremented in place. Not safe for concurrent use.
type Builder struct {
	b        strings.Builder
	budget   int
	sections []SectionRecord
}

// New returns a Builder with the given token budget for Budgeted sections.
func New(budget int) *Builder {
	return &Builder{budget: budget}
}

// estimateTokens estimates token count using the chars/4 heuristic.
func estimateTokens(s string) int {
	return (utf8.RuneCountInString(s) + 3) / 4
}

// EstimateTokens exposes the heuristic for callers/tests that need it.
func EstimateTokens(s string) int { return estimateTokens(s) }

// WriteFixed appends s unconditionally (ignores budget) and records a Fixed section.
func (p *Builder) WriteFixed(s string) {
	if s == "" {
		p.sections = append(p.sections, SectionRecord{Kind: Fixed, Written: 0})
		return
	}
	p.b.WriteString(s)
	p.sections = append(p.sections, SectionRecord{Kind: Fixed, Written: 1})
}

// WriteBudgetedLine appends line if it fits in the remaining budget, charging
// its token cost. Returns true if written. Does NOT record a section — used
// internally and for trailing single-line writes (e.g. a caveat).
func (p *Builder) WriteBudgetedLine(line string) bool {
	cost := estimateTokens(line)
	if cost > p.budget {
		return false
	}
	p.b.WriteString(line)
	p.budget -= cost
	return true
}

// WriteBudgetedSection writes header + lines one line at a time, charging each
// against the budget; stops at the first line that exceeds it. The header is
// prepended onto the first line only. Records a Budgeted section with the count
// of lines actually written and the total cost charged.
func (p *Builder) WriteBudgetedSection(header string, lines []string) {
	if len(lines) == 0 || p.budget <= 0 {
		p.sections = append(p.sections, SectionRecord{Kind: Budgeted, Written: 0})
		return
	}
	before := p.budget
	written := 0
	for i, line := range lines {
		if i == 0 {
			line = header + line
		}
		if !p.WriteBudgetedLine(line) {
			break
		}
		written++
	}
	p.sections = append(p.sections, SectionRecord{Kind: Budgeted, Written: written, Cost: before - p.budget})
}

// Budget returns the remaining budget (for tests).
func (p *Builder) Budget() int { return p.budget }

// Sections returns the recorded section calls in order (for tests).
func (p *Builder) Sections() []SectionRecord { return p.sections }

// Len returns the current rendered byte length.
func (p *Builder) Len() int { return p.b.Len() }

// String returns the accumulated prompt text.
func (p *Builder) String() string { return p.b.String() }

// Render writes the accumulated prompt to w.
func (p *Builder) Render(w io.Writer) (int, error) {
	return io.WriteString(w, p.b.String())
}
