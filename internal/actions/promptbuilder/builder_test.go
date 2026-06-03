package promptbuilder

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateTokens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{strings.Repeat("x", 100), 25},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.expected, EstimateTokens(tt.input), "input: %q", tt.input)
	}
}

func TestWriteFixed_AlwaysWritesAndIgnoresBudget(t *testing.T) {
	t.Parallel()
	b := New(0) // zero budget
	b.WriteFixed("HEADER\n")
	b.WriteFixed("") // empty fixed records but writes nothing

	recs := b.Sections()
	require.Len(t, recs, 2)
	assert.Equal(t, Fixed, recs[0].Kind)
	assert.Equal(t, 1, recs[0].Written)
	assert.Equal(t, 0, recs[1].Written)
	assert.Equal(t, "HEADER\n", b.String())
	assert.Equal(t, 0, b.Budget(), "fixed writes must not touch budget")
}

func TestWriteBudgetedSection_RecordsLinesAndChargesBudget(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		budget      int
		header      string
		lines       []string
		wantWritten int
	}{
		{"all fit", 1000, "H:\n", []string{"  a\n", "  b\n"}, 2},
		{"empty lines", 1000, "H:\n", nil, 0},
		{"zero budget", 0, "H:\n", []string{"  a\n"}, 0},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := New(tt.budget)
			b.WriteBudgetedSection(tt.header, tt.lines)
			recs := b.Sections()
			require.Len(t, recs, 1)
			assert.Equal(t, Budgeted, recs[0].Kind)
			assert.Equal(t, tt.wantWritten, recs[0].Written)
		})
	}
}

func TestWriteBudgetedSection_TruncatesWhenBudgetExhausted(t *testing.T) {
	t.Parallel()
	// Each line ~50 tokens; budget only fits a couple.
	line := "  " + strings.Repeat("v", 196) + "\n"
	lines := []string{line, line, line, line, line}
	b := New(120)
	b.WriteBudgetedSection("H:\n", lines)

	recs := b.Sections()
	require.Len(t, recs, 1)
	assert.Less(t, recs[0].Written, len(lines), "budget must truncate the section")
	assert.GreaterOrEqual(t, recs[0].Written, 1, "at least one line should fit")
	assert.Equal(t, recs[0].Cost, 120-b.Budget(), "recorded cost must equal budget consumed")
}

func TestWriteBudgetedLine_ReturnsFalseWhenOverBudget(t *testing.T) {
	t.Parallel()
	b := New(2)
	assert.True(t, b.WriteBudgetedLine("ab\n"), "short line should fit")
	assert.False(t, b.WriteBudgetedLine(strings.Repeat("x", 100)), "long line should not fit")
}

func TestRender_EmitsAccumulatedText(t *testing.T) {
	t.Parallel()
	b := New(1000)
	b.WriteFixed("one\n")
	b.WriteBudgetedSection("two:\n", []string{"  x\n"})
	var sb strings.Builder
	n, err := b.Render(&sb)
	require.NoError(t, err)
	assert.Equal(t, b.Len(), n)
	assert.Equal(t, b.String(), sb.String())
	assert.Equal(t, "one\ntwo:\n  x\n", sb.String())
}
