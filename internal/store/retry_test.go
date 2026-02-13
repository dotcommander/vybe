package store

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsVersionConflict(t *testing.T) {
	require.False(t, IsVersionConflict(nil))
	require.True(t, IsVersionConflict(ErrVersionConflict))
	require.True(t, IsVersionConflict(errors.New("wrapped: version conflict while updating")))
	require.False(t, IsVersionConflict(errors.New("database is locked")))
}
