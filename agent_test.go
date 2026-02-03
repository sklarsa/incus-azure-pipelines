package main

import (
	"context"
	"fmt"
	"testing"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		ProjectName: "test",
		AgentCount:  3,
		BaseImage:   "ubuntu/24.04",
		Azure: AzureConfig{
			PAT:  "test-token",
			Pool: "default",
			Url:  "https://dev.azure.com/myorg",
		},
	}
}

func TestReconcileAgents_AllMissing(t *testing.T) {
	m := NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	ch := make(chan int, 64)
	err := reconcileAgents(m, testConfig(), ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.ElementsMatch(t, []int{0, 1, 2}, created)
}

func TestReconcileAgents_AllPresent(t *testing.T) {
	m := NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "azp-agent-2"},
	}, nil)

	ch := make(chan int, 64)
	err := reconcileAgents(m, testConfig(), ch)
	require.NoError(t, err)
	close(ch)

	assert.Empty(t, ch)
}

func TestReconcileAgents_PartiallyPresent(t *testing.T) {
	m := NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-2"},
	}, nil)

	ch := make(chan int, 64)
	err := reconcileAgents(m, testConfig(), ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.Equal(t, []int{1}, created)
}

func TestReconcileAgents_IgnoresNonAgentContainers(t *testing.T) {
	// Reset the global ignore map for this test
	containersToIgnore = map[string]bool{}
	defer func() { containersToIgnore = map[string]bool{} }()

	m := NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "azp-agent-2"},
		{Name: "some-other-container"},
	}, nil)

	ch := make(chan int, 64)
	err := reconcileAgents(m, testConfig(), ch)
	require.NoError(t, err)
	close(ch)

	assert.Empty(t, ch)
	assert.True(t, containersToIgnore["some-other-container"])
}

func TestReconcileAgents_GetInstancesError(t *testing.T) {
	m := NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	ch := make(chan int, 64)
	err := reconcileAgents(m, testConfig(), ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestAgentName(t *testing.T) {
	assert.Equal(t, "azp-agent-0", agentName(0))
	assert.Equal(t, "azp-agent-42", agentName(42))
}

func TestAgentRe(t *testing.T) {
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
			matches := agentRe.FindStringSubmatch(tt.input)
			if tt.match {
				require.NotNil(t, matches)
				assert.Equal(t, tt.wantIdx, matches[1])
			} else {
				assert.Nil(t, matches)
			}
		})
	}
}

func TestCreateAgent(t *testing.T) {
	m := NewMockInstanceServer(t)
	conf := testConfig()
	conf.TmpfsSizeInGb = 12
	conf.MaxCores = 4
	conf.MaxRamInGb = 8

	op := NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		return req.Name == "azp-agent-0" &&
			req.Start == true &&
			req.InstancePut.Ephemeral == true &&
			req.Config["limits.cpu.allowance"] == "400%" &&
			req.Config["limits.memory"] == "8GiB"
	})).Return(op, nil)

	m.On("CreateInstanceFile", "azp-agent-0", "/home/agent/.token", mock.Anything).Return(nil)

	execOp := NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	err := createAgent(context.Background(), m, conf, 0)
	require.NoError(t, err)
}

func TestCreateAgent_CreateInstanceError(t *testing.T) {
	m := NewMockInstanceServer(t)
	conf := testConfig()

	m.On("CreateInstance", mock.Anything).Return(nil, fmt.Errorf("disk full"))

	err := createAgent(context.Background(), m, conf, 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestCreateAgent_NoCpuLimitWhenZero(t *testing.T) {
	m := NewMockInstanceServer(t)
	conf := testConfig()
	conf.MaxCores = 0
	conf.MaxRamInGb = 0

	op := NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		_, hasCpu := req.Config["limits.cpu.allowance"]
		_, hasMem := req.Config["limits.memory"]
		return !hasCpu && !hasMem
	})).Return(op, nil)

	m.On("CreateInstanceFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	execOp := NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("ExecInstance", mock.Anything, mock.Anything, mock.Anything).Return(execOp, nil)

	err := createAgent(context.Background(), m, conf, 0)
	require.NoError(t, err)
}

// Verify the mock satisfies the interface at compile time
var _ incus.InstanceServer = (*MockInstanceServer)(nil)
var _ incus.Operation = (*MockOperation)(nil)
