package provision

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRandomString(t *testing.T) {
	t.Run("returns requested length", func(t *testing.T) {
		s, err := randomString(16)
		require.NoError(t, err)
		assert.Len(t, s, 16)
	})

	t.Run("only contains lowercase alphanumeric chars", func(t *testing.T) {
		s, err := randomString(1000)
		require.NoError(t, err)
		assert.Regexp(t, `^[a-z0-9]+$`, s)
	})

	t.Run("zero length returns empty string", func(t *testing.T) {
		s, err := randomString(0)
		require.NoError(t, err)
		assert.Equal(t, "", s)
	})

	t.Run("consecutive calls return different values", func(t *testing.T) {
		a, err := randomString(32)
		require.NoError(t, err)
		b, err := randomString(32)
		require.NoError(t, err)
		assert.NotEqual(t, a, b)
	})
}
