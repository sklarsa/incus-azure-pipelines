package pool

import (
	"context"
	"net/http"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPool_ReapAgentValidatesIndex(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	p, err := NewPool(m, testConfig())
	require.NoError(t, err)

	assert.Error(t, p.ReapAgent(context.Background(), -1))
	assert.Error(t, p.ReapAgent(context.Background(), 3))
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_ReapAgentNotFoundIsIdempotent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("UpdateInstanceState", "azp-agent-1", mock.Anything, "").
		Return(nil, api.StatusErrorf(http.StatusNotFound, "instance not found"))
	p, err := NewPool(m, testConfig())
	require.NoError(t, err)

	require.NoError(t, p.ReapAgent(context.Background(), 1))
}
