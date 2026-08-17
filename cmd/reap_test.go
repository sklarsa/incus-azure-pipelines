package cmd

import (
	"context"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/sklarsa/incus-azure-pipelines/pool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func reapTestConfig() CLIConfig {
	return CLIConfig{Pools: []pool.Config{{
		Name:       "build-pool",
		AgentCount: 2,
		Azure:      pool.AzureConfig{PAT: "pat", Url: "https://dev.azure.com/org"},
		Incus:      pool.IncusConfig{Image: "image"},
	}}}
}

func TestRunReap_Success(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	m.On("UpdateInstanceState", "build-pool-1", mock.MatchedBy(func(req api.InstanceStatePut) bool {
		return req.Action == "stop" && req.Force
	}), "").Return(op, nil)

	err := runReap(context.Background(), m, reapTestConfig(), "config.yaml", []string{"build-pool", "1"})
	require.NoError(t, err)
}

func TestRunReap_InvalidPoolOrIndex(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	tests := []struct {
		name string
		args []string
	}{
		{name: "pool not found", args: []string{"missing", "0"}},
		{name: "negative index", args: []string{"build-pool", "-1"}},
		{name: "index out of range", args: []string{"build-pool", "2"}},
		{name: "non-numeric index", args: []string{"build-pool", "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := runReap(context.Background(), m, reapTestConfig(), "config.yaml", tt.args)
			assert.Error(t, err)
		})
	}
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}
