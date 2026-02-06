package pool

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func testConfig() Config {
	return Config{
		Name:       "azp-agent",
		AgentCount: 3,
		Azure: AzureConfig{
			PAT: "test-token",
			Url: "https://dev.azure.com/myorg",
		},
		Incus: IncusConfig{
			Image: "test-image",
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
	conf.Name = "[invalid"

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
	err = pool.Reconcile(ch)
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
	err = pool.Reconcile(ch)
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
	err = pool.Reconcile(ch)
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
	err = pool.Reconcile(ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPool_Create(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.Incus.TmpfsSizeInGb = 12
	conf.Incus.MaxCores = 4
	conf.Incus.MaxRamInGb = 8

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		tmpfs, hasTmpfs := req.Devices["tmpfs"]
		return req.Name == "azp-agent-0" &&
			req.Start == true &&
			req.InstancePut.Ephemeral == true &&
			req.Config["limits.cpu.allowance"] == "400%" &&
			req.Config["limits.memory"] == "8GiB" &&
			hasTmpfs &&
			tmpfs["size"] == "12GiB"
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

func TestPool_Create_NoLimitsWhenZero(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.Incus.MaxCores = 0
	conf.Incus.MaxRamInGb = 0
	conf.Incus.TmpfsSizeInGb = 0

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.MatchedBy(func(req api.InstancesPost) bool {
		_, hasCpu := req.Config["limits.cpu.allowance"]
		_, hasMem := req.Config["limits.memory"]
		_, hasTmpfs := req.Devices["tmpfs"]
		return !hasCpu && !hasMem && !hasTmpfs
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

	idx, err := pool.agentIndex("azp-agent-0")
	require.NoError(t, err)
	assert.Equal(t, 0, idx)

	idx, err = pool.agentIndex("azp-agent-12")
	require.NoError(t, err)
	assert.Equal(t, 12, idx)

	_, err = pool.agentIndex("other-agent-0")
	assert.ErrorIs(t, err, errNotPoolAgent)

	_, err = pool.agentIndex("azp-agent-abc")
	assert.ErrorIs(t, err, errNotPoolAgent)
}

func TestAgentRe_Matching(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := Config{
		Name: "azp-agent",
	}
	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	tests := []struct {
		name    string
		input   string
		match   bool
		wantIdx string
	}{
		{"valid 0", "azp-agent-0", true, "0"},
		{"valid 12", "azp-agent-12", true, "12"},
		{"valid 100", "azp-agent-100", true, "100"},
		{"no match prefix", "other-agent-0", false, ""},
		{"no match suffix", "azp-agent-abc", false, ""},
		{"empty suffix", "azp-agent-", false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

func TestPool_Reconcile_ZeroAgents(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	conf := testConfig()
	conf.AgentCount = 0

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(ch)
	require.NoError(t, err)
	close(ch)

	assert.Empty(t, ch)
}

func TestPool_Reconcile_MaxAgents(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	conf := testConfig()
	conf.AgentCount = 64

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	ch := make(chan int, 64)
	err = pool.Reconcile(ch)
	require.NoError(t, err)
	close(ch)

	var created []int
	for idx := range ch {
		created = append(created, idx)
	}

	assert.Len(t, created, 64)
	assert.Equal(t, 0, created[0])
	assert.Equal(t, 63, created[63])
}

func TestPool_CreateAgent_InFlight(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	// Simulate in-flight by storing in the map
	pool.inFlight.Store(0, true)

	// Call should skip since already in-flight
	err = pool.CreateAgent(context.Background(), 0)
	require.NoError(t, err)

	// CreateInstance should not have been called
	m.AssertNotCalled(t, "CreateInstance", mock.Anything)
}

func TestPool_ListAgentsFull(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{Instance: api.Instance{Name: "azp-agent-0"}},
		{Instance: api.Instance{Name: "azp-agent-1"}},
		{Instance: api.Instance{Name: "other-container"}},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	agents, err := pool.ListAgentsFull()
	require.NoError(t, err)
	assert.Len(t, agents, 2)
	assert.Equal(t, "azp-agent-0", agents[0].Name)
	assert.Equal(t, "azp-agent-1", agents[1].Name)
}

func TestPool_ListAgentsFull_Error(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	_, err = pool.ListAgentsFull()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPool_Reap_SkipsNonRunning(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{Name: "azp-agent-0"},
			State:    &api.InstanceState{Status: "Stopped"},
		},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	// Should not attempt to stop or exec anything
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_SkipsNilState(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{Name: "azp-agent-0"},
			State:    nil,
		},
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_SkipsYoungContainers(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now(), // Just created
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)

	conf := testConfig()
	conf.Incus.StartupGracePeriod = 5 * time.Minute

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_SkipsRunningAgentProcess(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now().Add(-10 * time.Minute),
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)

	// pgrep returns 0 (process found)
	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	execOp.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(0)},
	})
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	conf := testConfig()
	conf.Incus.StartupGracePeriod = time.Minute

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	// Should not stop the instance
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_ReapsStaleAgent(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now().Add(-10 * time.Minute),
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)

	// pgrep returns 1 (process not found)
	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	execOp.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(1)},
	})
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	// Should stop the instance
	stopOp := mocks.NewMockOperation(t)
	stopOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("UpdateInstanceState", "azp-agent-0", mock.MatchedBy(func(req api.InstanceStatePut) bool {
		return req.Action == "stop" && req.Force == true
	}), "").Return(stopOp, nil)

	conf := testConfig()
	conf.Incus.StartupGracePeriod = time.Minute

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	m.AssertCalled(t, "UpdateInstanceState", "azp-agent-0", mock.Anything, "")
}

func TestPool_Reap_SkipsInFlight(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now().Add(-10 * time.Minute),
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)

	// pgrep returns 1 (process not found)
	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	execOp.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(1)},
	})
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	conf := testConfig()
	conf.Incus.StartupGracePeriod = time.Minute

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	// Mark as in-flight
	pool.inFlight.Store(0, true)

	err = pool.Reap(context.Background())
	require.NoError(t, err)

	// Should not stop the instance
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_ListError(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	err = pool.Reap(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "connection refused")
}

func TestPool_AgentLogs_InvalidIndex(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	var buf bytes.Buffer
	err = pool.AgentLogs(10, &buf) // Pool has 3 agents (0, 1, 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid agent index 10")
}

func TestPool_AgentLogs_ExecError(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(nil, fmt.Errorf("instance not found"))

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	var buf bytes.Buffer
	err = pool.AgentLogs(0, &buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance not found")
}

func TestPool_AgentLogs_Success(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	execOp := mocks.NewMockOperation(t)
	execOp.On("Wait").Return(nil)
	m.On("ExecInstance", "azp-agent-1", mock.Anything, mock.Anything).Return(execOp, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	var buf bytes.Buffer
	err = pool.AgentLogs(1, &buf)
	require.NoError(t, err)
}

func TestPool_AgentLogs_WaitError(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	execOp := mocks.NewMockOperation(t)
	execOp.On("Wait").Return(fmt.Errorf("wait failed"))
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	var buf bytes.Buffer
	err = pool.AgentLogs(0, &buf)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "wait failed")
}

func TestPool_Create_WithAgentPrefix(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	conf := testConfig()
	conf.AgentPrefix = "custom-prefix"

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)

	m.On("CreateInstance", mock.Anything).Return(op, nil)
	m.On("CreateInstanceFile", "azp-agent-0", "/home/agent/.token", mock.Anything).Return(nil)

	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("ExecInstance", "azp-agent-0", mock.MatchedBy(func(req api.InstanceExecPost) bool {
		for i, arg := range req.Command {
			if arg == "--agent" && i+1 < len(req.Command) {
				return req.Command[i+1] == "custom-prefix-0"
			}
		}
		return false
	}), mock.Anything).Return(execOp, nil)

	pool, err := NewPool(m, conf)
	require.NoError(t, err)

	err = pool.CreateAgent(context.Background(), 0)
	require.NoError(t, err)
}
