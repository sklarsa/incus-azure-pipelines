package provision

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestCheckExecExit(t *testing.T) {
	t.Run("zero exit passes", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(nil)
		op.On("Get").Return(api.Operation{Metadata: map[string]any{"return": float64(0)}})
		require.NoError(t, checkExecExit(context.Background(), op, "test step"))
	})

	t.Run("non-zero exit returns an error", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(nil)
		op.On("Get").Return(api.Operation{Metadata: map[string]any{"return": float64(2)}})
		err := checkExecExit(context.Background(), op, "test step")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test step exited with code 2")
	})

	t.Run("missing return code is an error", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(nil)
		op.On("Get").Return(api.Operation{Metadata: map[string]any{}})
		err := checkExecExit(context.Background(), op, "test step")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not determine exit code")
	})
}

func TestInstanceTypeStr(t *testing.T) {
	assert.Equal(t, "container", instanceTypeStr(false))
	assert.Equal(t, "virtual-machine", instanceTypeStr(true))
}

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

// TestWaitCleanupOpIgnoresParentCancellation covers the deferred cleanup path in
// BaseImage, where teardown must still wait for stop/delete operations after the
// main request context has already been canceled.
func TestWaitCleanupOpIgnoresParentCancellation(t *testing.T) {
	type ctxKey string

	const key ctxKey = "key"

	parentCtx, cancel := context.WithCancel(context.WithValue(context.Background(), key, "value"))
	cancel()

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.MatchedBy(func(ctx context.Context) bool {
		if got := ctx.Value(key); got != "value" {
			return false
		}

		if ctx.Err() != nil {
			return false
		}

		deadline, ok := ctx.Deadline()
		if !ok {
			return false
		}

		remaining := time.Until(deadline)
		return remaining > 0 && remaining <= builderCleanupOpTimeout
	})).Return(nil).Once()

	err := waitCleanupOp(parentCtx, op)
	require.NoError(t, err)
}

func TestIsAlreadyStopped(t *testing.T) {
	t.Run("matches the incus already-stopped status error", func(t *testing.T) {
		err := api.StatusErrorf(http.StatusBadRequest, "The instance is already stopped")
		assert.True(t, isAlreadyStopped(err))
	})

	t.Run("does not match other bad-request errors", func(t *testing.T) {
		err := api.StatusErrorf(http.StatusBadRequest, "some other problem")
		assert.False(t, isAlreadyStopped(err))
	})

	t.Run("does not match a plain error", func(t *testing.T) {
		assert.False(t, isAlreadyStopped(errors.New("already stopped")))
	})
}

func TestStopBuilderForCleanup(t *testing.T) {
	alreadyStopped := api.StatusErrorf(http.StatusBadRequest, "The instance is already stopped")

	t.Run("already stopped via WaitContext is not an error", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(alreadyStopped)

		c := mocks.NewMockInstanceServer(t)
		c.On("UpdateInstanceState", "builder", mock.Anything, "").Return(op, nil)

		require.NoError(t, stopBuilderForCleanup(context.Background(), c, "builder"))
	})

	t.Run("already stopped synchronously is not an error", func(t *testing.T) {
		c := mocks.NewMockInstanceServer(t)
		c.On("UpdateInstanceState", "builder", mock.Anything, "").Return(nil, alreadyStopped)

		require.NoError(t, stopBuilderForCleanup(context.Background(), c, "builder"))
	})

	t.Run("a genuine stop failure is returned", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(errors.New("boom"))

		c := mocks.NewMockInstanceServer(t)
		c.On("UpdateInstanceState", "builder", mock.Anything, "").Return(op, nil)

		err := stopBuilderForCleanup(context.Background(), c, "builder")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "boom")
	})

	t.Run("a stopped instance is stopped cleanly", func(t *testing.T) {
		op := mocks.NewMockOperation(t)
		op.On("WaitContext", mock.Anything).Return(nil)

		c := mocks.NewMockInstanceServer(t)
		c.On("UpdateInstanceState", "builder", mock.Anything, "").Return(op, nil)

		require.NoError(t, stopBuilderForCleanup(context.Background(), c, "builder"))
	})
}
