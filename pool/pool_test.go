package pool

import (
	"context"
	"fmt"
	"testing"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		ProjectName: "test",
		NamePrefix:  "azp-agent",
		Image:       "test-image",
		AgentCount:  3,
		Azure: AzureConfig{
			PAT:  "test-token",
			Pool: "default",
			Url:  "https://dev.azure.com/myorg",
		},
	}
}

func TestNewPool(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	pool, err := NewPool(m, conf)
	require.NoError(t, err)
	require.NotNil(t, pool)
	require.NotNil(t, pool.agentRe)
}

func TestNewPool_InvalidRegexp(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.NamePrefix = "[invalid"

	_, err := NewPool(m, conf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to construct agent regexp")
}

func TestPool_List(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "other-container"},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	agents, err := pool.ListAgents()
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	assert.Equal(t, "azp-agent-0", agents[0].Name)
	assert.Equal(t, "azp-agent-1", agents[1].Name)
}

func TestPool_List_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	_, err = pool.ListAgents()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPool_Reconcile_AllMissing(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.ElementsMatch(t, []int{0, 1, 2}, created)
}

func TestPool_Reconcile_AllPresent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "azp-agent-2"},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	assert.Empty(t, ch)
}

func TestPool_Reconcile_PartiallyPresent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-2"},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.Equal(t, []int{1}, created)
}

func TestPool_Reconcile_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(3, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPool_Create(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.TmpfsSizeInGb = 12
	conf.MaxCores = 4
	conf.MaxRamInGb = 8

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		return req.Name == "azp-agent-0" &&
			req.Start == true &&
			req.InstancePut.Ephemeral == true &&
			req.Config["limits.cpu.allowance"] == "400%" &&
			req.Config["limits.memory"] == "8GiB"
	})).Return(op, nil)

	m.On("CreateInstanceFile", "azp-agent-0", "/home/agent/.token", mock.Anything).Return(nil)

	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.CreateAgent(context.Background(), 0)
	require.NoError(t, err)
}

func TestPool_Create_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	m.On("CreateInstance", mock.Anything).Return(nil, fmt.Errorf("disk full"))

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.CreateAgent(context.Background(), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestPool_Create_NoCpuLimitWhenZero(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.MaxCores = 0
	conf.MaxRamInGb = 0

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		_, hasCpu := req.Config["limits.cpu.allowance"]
		_, hasMem := req.Config["limits.memory"]
		return !hasCpu && !hasMem
	})).Return(op, nil)

	m.On("CreateInstanceFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("ExecInstance", mock.Anything, mock.Anything, mock.Anything).Return(execOp, nil)

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.CreateAgent(context.Background(), 0)
	require.NoError(t, err)
}

func TestPool_Create_ExceedsCapacity(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.AgentCount = 3

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.CreateAgent(context.Background(), 5)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot create agent at index 5")
}

func TestPool_AgentName(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	assert.Equal(t, "azp-agent-0", pool.AgentName(0))
	assert.Equal(t, "azp-agent-42", pool.AgentName(42))
}

func TestPool_AgentIndex(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	assert.Equal(t, 0, pool.AgentIndex("azp-agent-0"))
	assert.Equal(t, 12, pool.AgentIndex("azp-agent-12"))
	assert.Equal(t, -1, pool.AgentIndex("other-agent-0"))
	assert.Equal(t, -1, pool.AgentIndex("azp-agent-abc"))
}

func TestAgentRe_Matching(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		match   bool
		wantIdx string
	}{
		{"valid 0", "azp-agent-0", true, "0"},
		{"valid 12", "azp-agent-12", true, "12"},
		{"no match prefix", "other-agent-0", false, ""},
		{"no match suffix", "azp-agent-abc", false, ""},
		{"three digits", "azp-agent-100", false, ""},
		{"empty suffix", "azp-agent-", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mocks.NewMockInstanceServer(t)
			conf := Config{
				NamePrefix: "azp-agent",
			}
			pool, err := NewPool(m, conf)
			require.NoError(t, err)

			matches := pool.agentRe.FindStringSubmatch(tt.input)
			if tt.match {
				require.NotNil(t, matches)
				assert.Equal(t, tt.wantIdx, matches[1])
			} else {
				assert.Nil(t, matches)
			}
		})
	}
}
