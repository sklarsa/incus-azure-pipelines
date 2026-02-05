package agent

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
		Azure: AzureConfig{
			PAT:  "test-token",
			Pool: "default",
			Url:  "https://dev.azure.com/myorg",
		},
	}
}

func TestNewRepository(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	repo, err := NewRepository(m, conf)
	require.NoError(t, err)
	require.NotNil(t, repo)
	require.NotNil(t, repo.agentRe)
}

func TestNewRepository_InvalidRegexp(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.NamePrefix = "[invalid"

	_, err := NewRepository(m, conf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unable to construct agent regexp")
}

func TestRepository_List(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "other-container"},
	}, nil)

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	agents, err := repo.List()
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	assert.Equal(t, "azp-agent-0", agents[0].Name)
	assert.Equal(t, "azp-agent-1", agents[1].Name)
}

func TestRepository_List_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	_, err = repo.List()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestRepository_Reconcile_AllMissing(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = repo.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.ElementsMatch(t, []int{0, 1, 2}, created)
}

func TestRepository_Reconcile_AllPresent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-1"},
		{Name: "azp-agent-2"},
	}, nil)

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = repo.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	assert.Empty(t, ch)
}

func TestRepository_Reconcile_PartiallyPresent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0"},
		{Name: "azp-agent-2"},
	}, nil)

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = repo.Reconcile(3, ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.Equal(t, []int{1}, created)
}

func TestRepository_Reconcile_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	repo, err := NewRepository(m, testConfig())
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = repo.Reconcile(3, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestRepository_Create(t *testing.T) {
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

	repo, err := NewRepository(m, conf)
	require.NoError(t, err)

	err = repo.Create(context.Background(), 0)
	require.NoError(t, err)
}

func TestRepository_Create_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	m.On("CreateInstance", mock.Anything).Return(nil, fmt.Errorf("disk full"))

	repo, err := NewRepository(m, conf)
	require.NoError(t, err)

	err = repo.Create(context.Background(), 0)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "disk full")
}

func TestRepository_Create_NoCpuLimitWhenZero(t *testing.T) {
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

	repo, err := NewRepository(m, conf)
	require.NoError(t, err)

	err = repo.Create(context.Background(), 0)
	require.NoError(t, err)
}

func TestAgentName(t *testing.T) {
	conf := Config{
		NamePrefix: "azp-agent",
	}
	assert.Equal(t, "azp-agent-0", agentName(conf, 0))
	assert.Equal(t, "azp-agent-42", agentName(conf, 42))
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
			repo, err := NewRepository(m, conf)
			require.NoError(t, err)

			matches := repo.agentRe.FindStringSubmatch(tt.input)
			if tt.match {
				require.NotNil(t, matches)
				assert.Equal(t, tt.wantIdx, matches[1])
			} else {
				assert.Nil(t, matches)
			}
		})
	}
}
