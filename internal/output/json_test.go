package output

import (
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

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
