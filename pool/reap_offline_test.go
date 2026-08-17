package pool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type fakeAzureAgentClient struct {
	agents map[string]AzureAgentStatus
	err    error
	calls  int
}

func (f *fakeAzureAgentClient) ListAgents(context.Context, string) (map[string]AzureAgentStatus, error) {
	f.calls++
	return f.agents, f.err
}

func processResult(t *testing.T, running bool) *mocks.MockOperation {
	t.Helper()
	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	code := float64(1)
	if running {
		code = 0
	}
	op.On("Get").Return(api.Operation{Metadata: map[string]any{"return": code}})
	return op
}

func oldRunningAgent(name string, now time.Time) api.InstanceFull {
	return api.InstanceFull{
		Instance: api.Instance{Name: name, CreatedAt: now.Add(-10 * time.Minute)},
		State:    &api.InstanceState{Status: "Running"},
	}
}

func expectProcessChecks(m *mocks.MockInstanceServer, name string, wrapper, worker *mocks.MockOperation) {
	m.On("ExecInstance", name, mock.MatchedBy(func(req api.InstanceExecPost) bool {
		return req.Command[len(req.Command)-1] == "run_agent.sh"
	}), mock.Anything).Return(wrapper, nil).Once()
	if worker != nil {
		m.On("ExecInstance", name, mock.MatchedBy(func(req api.InstanceExecPost) bool {
			return req.Command[len(req.Command)-1] == "Agent.Worker"
		}), mock.Anything).Return(worker, nil).Once()
	}
}

func expectStop(m *mocks.MockInstanceServer, t *testing.T, name string) {
	t.Helper()
	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	m.On("UpdateInstanceState", name, mock.MatchedBy(func(req api.InstanceStatePut) bool {
		return req.Action == "stop" && req.Force
	}), "").Return(op, nil).Once()
}

func newOfflineTestPool(t *testing.T, m *mocks.MockInstanceServer, now *time.Time, azure AzureAgentClient) *Pool {
	t.Helper()
	conf := testConfig()
	conf.AgentPrefix = "runner"
	conf.Incus.StartupGracePeriod = time.Minute
	conf.OfflineGracePeriod = 5 * time.Minute
	p, err := NewPool(m, conf)
	require.NoError(t, err)
	p.now = func() time.Time { return *now }
	p.azure = azure
	return p
}

func TestPool_Reap_FirstOfflineObservationIsRetained(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {}}}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	first := p.offlineSince[0]
	assert.Equal(t, now, first)

	now = now.Add(4 * time.Minute)
	require.NoError(t, p.Reap(context.Background()))
	assert.Equal(t, first, p.offlineSince[0])
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_OfflineGraceExpiryReaps(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectStop(m, t, "azp-agent-0")
	p := newOfflineTestPool(t, m, &now, &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {}}})

	require.NoError(t, p.Reap(context.Background()))
	now = now.Add(5 * time.Minute)
	require.NoError(t, p.Reap(context.Background()))
	m.AssertCalled(t, "UpdateInstanceState", "azp-agent-0", mock.Anything, "")
}

func TestPool_Reap_OnlineOrAssignedResetsOfflineObservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status AzureAgentStatus
	}{
		{name: "online", status: AzureAgentStatus{Online: true}},
		{name: "assigned", status: AzureAgentStatus{Assigned: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
			m := mocks.NewMockInstanceServer(t)
			m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
			expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
			expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
			azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {}}}
			p := newOfflineTestPool(t, m, &now, azure)

			require.NoError(t, p.Reap(context.Background()))
			require.Contains(t, p.offlineSince, 0)
			now = now.Add(6 * time.Minute)
			azure.agents["runner-0"] = tc.status
			require.NoError(t, p.Reap(context.Background()))
			assert.NotContains(t, p.offlineSince, 0)
			m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestPool_Reap_AgentWorkerResetsAndSkips(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, true))
	azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {}}}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	now = now.Add(6 * time.Minute)
	require.NoError(t, p.Reap(context.Background()))
	assert.NotContains(t, p.offlineSince, 0)
	assert.Equal(t, 1, azure.calls)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_AzureAPIFailureSkipsAndResets(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {}}}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	now = now.Add(6 * time.Minute)
	azure.err = errors.New("Azure unavailable")
	require.NoError(t, p.Reap(context.Background()))
	assert.NotContains(t, p.offlineSince, 0)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_MissingAzureAgentUsesOfflineGrace(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil).Twice()
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectStop(m, t, "azp-agent-0")
	p := newOfflineTestPool(t, m, &now, &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{}})

	require.NoError(t, p.Reap(context.Background()))
	now = now.Add(5 * time.Minute)
	require.NoError(t, p.Reap(context.Background()))
	m.AssertCalled(t, "UpdateInstanceState", "azp-agent-0", mock.Anything, "")
}

func TestPool_Reap_QueriesAzureOncePerPass(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		oldRunningAgent("azp-agent-0", now),
		oldRunningAgent("azp-agent-1", now),
	}, nil)
	expectProcessChecks(m, "azp-agent-0", processResult(t, true), processResult(t, false))
	expectProcessChecks(m, "azp-agent-1", processResult(t, true), processResult(t, false))
	azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{
		"runner-0": {},
		"runner-1": {},
	}}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	assert.Equal(t, 1, azure.calls)
	assert.Contains(t, p.offlineSince, 0)
	assert.Contains(t, p.offlineSince, 1)
}

func TestPool_Reap_MissingRunAgentSkipsOnlineListener(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil)
	expectProcessChecks(m, "azp-agent-0", processResult(t, false), processResult(t, false))
	m.On("ExecInstance", "azp-agent-0", mock.MatchedBy(func(req api.InstanceExecPost) bool {
		return req.Command[len(req.Command)-1] == "Agent.Listener"
	}), mock.Anything).Return(processResult(t, true), nil).Once()
	azure := &fakeAzureAgentClient{agents: map[string]AzureAgentStatus{"runner-0": {Online: true}}}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	assert.Equal(t, 1, azure.calls)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_MissingRunAgentSkipsActiveWorker(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil)
	expectProcessChecks(m, "azp-agent-0", processResult(t, false), processResult(t, true))
	azure := &fakeAzureAgentClient{err: errors.New("must not be called")}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	assert.Zero(t, azure.calls)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestPool_Reap_MissingRunAgentStillReapsWithoutAzure(t *testing.T) {
	now := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{oldRunningAgent("azp-agent-0", now)}, nil)
	expectProcessChecks(m, "azp-agent-0", processResult(t, false), processResult(t, false))
	m.On("ExecInstance", "azp-agent-0", mock.MatchedBy(func(req api.InstanceExecPost) bool {
		return req.Command[len(req.Command)-1] == "Agent.Listener"
	}), mock.Anything).Return(processResult(t, false), nil).Once()
	expectStop(m, t, "azp-agent-0")
	azure := &fakeAzureAgentClient{err: errors.New("must not be called")}
	p := newOfflineTestPool(t, m, &now, azure)

	require.NoError(t, p.Reap(context.Background()))
	assert.Zero(t, azure.calls)
	m.AssertCalled(t, "UpdateInstanceState", "azp-agent-0", mock.Anything, "")
}
