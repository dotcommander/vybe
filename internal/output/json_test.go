package output

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/dotcommander/vybe/internal/models"
	"github.com/stretchr/testify/require"
)

// Compile-time check: models.RecoverableError must satisfy the local recoverableError interface.
var _ recoverableError = (models.RecoverableError)(nil)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	original := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = original }()

	fn()

	require.NoError(t, w.Close())

	b, err := io.ReadAll(r)
	require.NoError(t, err)
	require.NoError(t, r.Close())
	return string(b)
}

func TestSuccessAndError(t *testing.T) {
	s := Success(map[string]string{"k": "v"})
	require.Equal(t, "v1", s.SchemaVersion)
	require.True(t, s.Success)
	require.NotNil(t, s.Data)
	require.Empty(t, s.Error)

	e := Error(errors.New("boom"))
	require.Equal(t, "v1", e.SchemaVersion)
	require.False(t, e.Success)
	require.Nil(t, e.Data)
	require.Equal(t, "boom", e.Error)
}

// Test PrintWith directly (no stdout capture needed)
func TestPrintWith_CompactJSON(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{Writer: &buf, Pretty: false}

	err := PrintWith(cfg, map[string]string{"hello": "world"})
	require.NoError(t, err)
	require.Equal(t, "{\"hello\":\"world\"}\n", buf.String())
}

func TestPrintWith_PrettyJSON(t *testing.T) {
	var buf bytes.Buffer
	cfg := Config{Writer: &buf, Pretty: true}

	err := PrintWith(cfg, map[string]string{"hello": "world"})
	require.NoError(t, err)

	out := buf.String()
	require.Contains(t, out, "\n  \"hello\": \"world\"\n")
	require.True(t, strings.HasPrefix(out, "{\n"))
}

func TestPrint_DefaultCompactJSON(t *testing.T) {
	t.Setenv("VYBE_PRETTY_JSON", "")

	out := captureStdout(t, func() {
		err := Print(map[string]string{"hello": "world"})
		require.NoError(t, err)
	})

	require.Equal(t, "{\"hello\":\"world\"}\n", out)
}

func TestPrint_PrettyJSONEnabled(t *testing.T) {
	for _, value := range []string{"1", "true"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("VYBE_PRETTY_JSON", value)

			out := captureStdout(t, func() {
				err := Print(map[string]string{"hello": "world"})
				require.NoError(t, err)
			})

			require.Contains(t, out, "\n  \"hello\": \"world\"\n")
			require.True(t, strings.HasPrefix(out, "{\n"))
		})
	}
}

func TestPrintSuccessAndPrintError(t *testing.T) {
	t.Setenv("VYBE_PRETTY_JSON", "")

	successOut := captureStdout(t, func() {
		err := PrintSuccess(map[string]int{"count": 2})
		require.NoError(t, err)
	})
	require.Contains(t, successOut, "\"schema_version\":\"v1\"")
	require.Contains(t, successOut, "\"success\":true")
	require.Contains(t, successOut, "\"count\":2")

	errorOut := captureStdout(t, func() {
		err := PrintError(errors.New("bad things"))
		require.NoError(t, err)
	})
	require.Contains(t, errorOut, "\"schema_version\":\"v1\"")
	require.Contains(t, errorOut, "\"success\":false")
	require.Contains(t, errorOut, "\"error\":\"bad things\"")
}

// testRecoverableErr implements the models.RecoverableError interface for testing.
type testRecoverableErr struct {
	msg    string
	code   string
	ctx    map[string]string
	action string
}

func (e *testRecoverableErr) Error() string              { return e.msg }
func (e *testRecoverableErr) ErrorCode() string          { return e.code }
func (e *testRecoverableErr) Context() map[string]string { return e.ctx }
func (e *testRecoverableErr) SuggestedAction() string    { return e.action }

func TestError_EnrichedRecoverableError(t *testing.T) {
	t.Run("plain error has no enriched fields", func(t *testing.T) {
		resp := Error(errors.New("something broke"))
		require.Equal(t, "v1", resp.SchemaVersion)
		require.False(t, resp.Success)
		require.Equal(t, "something broke", resp.Error)
		require.Empty(t, resp.ErrorCode)
		require.Nil(t, resp.ErrorContext)
		require.Empty(t, resp.SuggestedAction)
	})

	t.Run("recoverable error populates all enriched fields", func(t *testing.T) {
		re := &testRecoverableErr{
			msg:    "task claim expired",
			code:   "STALE_CLAIM",
			ctx:    map[string]string{"task_id": "task_123", "agent": "claude"},
			action: "vybe task begin --id task_123 --agent claude --request-id new",
		}
		resp := Error(re)
		require.Equal(t, "v1", resp.SchemaVersion)
		require.False(t, resp.Success)
		require.Equal(t, "task claim expired", resp.Error)
		require.Equal(t, "STALE_CLAIM", resp.ErrorCode)
		require.Equal(t, map[string]string{"task_id": "task_123", "agent": "claude"}, resp.ErrorContext)
		require.Equal(t, "vybe task begin --id task_123 --agent claude --request-id new", resp.SuggestedAction)
	})

	t.Run("recoverable error marshals enriched fields to JSON", func(t *testing.T) {
		t.Setenv("VYBE_PRETTY_JSON", "")
		re := &testRecoverableErr{
			msg:    "task claim expired",
			code:   "STALE_CLAIM",
			ctx:    map[string]string{"task_id": "task_123"},
			action: "retry",
		}
		var buf bytes.Buffer
		cfg := Config{Writer: &buf, Pretty: false}
		err := PrintWith(cfg, Error(re))
		require.NoError(t, err)
		out := buf.String()
		require.Contains(t, out, `"error_code":"STALE_CLAIM"`)
		require.Contains(t, out, `"suggested_action":"retry"`)
		require.Contains(t, out, `"task_id":"task_123"`)
	})

	t.Run("plain error omits enriched fields from JSON", func(t *testing.T) {
		var buf bytes.Buffer
		cfg := Config{Writer: &buf, Pretty: false}
		err := PrintWith(cfg, Error(errors.New("plain")))
		require.NoError(t, err)
		out := buf.String()
		require.NotContains(t, out, "error_code")
		require.NotContains(t, out, "suggested_action")
		require.NotContains(t, out, `"error_context"`)
	})
}

func TestDefaultConfig(t *testing.T) {
	t.Run("default compact", func(t *testing.T) {
		t.Setenv("VYBE_PRETTY_JSON", "")
		cfg := DefaultConfig()
		require.Equal(t, os.Stdout, cfg.Writer)
		require.False(t, cfg.Pretty)
	})

	t.Run("pretty enabled with 1", func(t *testing.T) {
		t.Setenv("VYBE_PRETTY_JSON", "1")
		cfg := DefaultConfig()
		require.Equal(t, os.Stdout, cfg.Writer)
		require.True(t, cfg.Pretty)
	})

	t.Run("pretty enabled with true", func(t *testing.T) {
		t.Setenv("VYBE_PRETTY_JSON", "true")
		cfg := DefaultConfig()
		require.Equal(t, os.Stdout, cfg.Writer)
		require.True(t, cfg.Pretty)
	})
}
