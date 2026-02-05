package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type captureHandler struct {
	records []slog.Record
}

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func TestReapStaleInstances_SkipsNonAgentContainers(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{Instance: api.Instance{Name: "some-other-thing"}},
	}, nil)

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReapStaleInstances_SkipsInFlightInstances(t *testing.T) {
	inFlight = &sync.Map{}
	inFlight.Store(0, true)
	defer func() { inFlight = &sync.Map{} }()

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

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReapStaleInstances_SkipsNotRunning(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now().Add(-10 * time.Minute),
			},
			State: &api.InstanceState{Status: "Stopped"},
		},
	}, nil)

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReapStaleInstances_SkipsYoungContainers(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return([]api.InstanceFull{
		{
			Instance: api.Instance{
				Name:      "azp-agent-0",
				CreatedAt: time.Now().Add(-30 * time.Second), // younger than grace period
			},
			State: &api.InstanceState{Status: "Running"},
		},
	}, nil)

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertNotCalled(t, "ExecInstance", mock.Anything, mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReapStaleInstances_ReapsWhenAgentNotRunning(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

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

	// pgrep returns exit code 1 (not found)
	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	execOp.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(1)},
	})
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	// Expect stop call
	stopOp := mocks.NewMockOperation(t)
	stopOp.On("WaitContext", mock.Anything).Return(nil)
	m.On("UpdateInstanceState", "azp-agent-0", mock.MatchedBy(func(s api.InstanceStatePut) bool {
		return s.Action == "stop" && s.Force == true
	}), "").Return(stopOp, nil)

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertCalled(t, "UpdateInstanceState", "azp-agent-0", mock.Anything, "")
}

func TestReapStaleInstances_SkipsWhenAgentRunning(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

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

	// pgrep returns exit code 0 (found)
	execOp := mocks.NewMockOperation(t)
	execOp.On("WaitContext", mock.Anything).Return(nil)
	execOp.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(0)},
	})
	m.On("ExecInstance", "azp-agent-0", mock.Anything, mock.Anything).Return(execOp, nil)

	reapStaleInstances(context.Background(), m, testConfig())

	m.AssertNotCalled(t, "UpdateInstanceState", mock.Anything, mock.Anything, mock.Anything)
}

func TestReapStaleInstances_GetInstancesFullError(t *testing.T) {
	inFlight = &sync.Map{}
	defer func() { inFlight = &sync.Map{} }()

	handler := &captureHandler{}
	orig := slog.Default()
	slog.SetDefault(slog.New(handler))
	defer slog.SetDefault(orig)

	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstancesFull", api.InstanceTypeContainer).Return(nil, fmt.Errorf("connection refused"))

	reapStaleInstances(context.Background(), m, testConfig())

	require.Len(t, handler.records, 1)
	assert.Equal(t, slog.LevelError, handler.records[0].Level)
	assert.Equal(t, "reaper: failed to list instances", handler.records[0].Message)
}

func TestReapInstance(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	m.On("UpdateInstanceState", "azp-agent-5", mock.MatchedBy(func(s api.InstanceStatePut) bool {
		return s.Action == "stop" && s.Force == true && s.Timeout == 30
	}), "").Return(op, nil)

	err := reapInstance(context.Background(), m, 5)
	require.NoError(t, err)
}

func TestReapInstance_NotFound(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	m.On("UpdateInstanceState", "azp-agent-5", mock.Anything, "").
		Return(nil, api.StatusErrorf(http.StatusNotFound, "not found"))

	err := reapInstance(context.Background(), m, 5)
	assert.NoError(t, err)
}

func TestIsAgentProcessRunning_Running(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	op.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(0)},
	})
	m.On("ExecInstance", "azp-agent-3", mock.Anything, mock.Anything).Return(op, nil)

	running, err := isAgentProcessRunning(context.Background(), m, 3)
	require.NoError(t, err)
	assert.True(t, running)
}

func TestIsAgentProcessRunning_NotRunning(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	op.On("Get").Return(api.Operation{
		Metadata: map[string]any{"return": float64(1)},
	})
	m.On("ExecInstance", "azp-agent-3", mock.Anything, mock.Anything).Return(op, nil)

	running, err := isAgentProcessRunning(context.Background(), m, 3)
	require.NoError(t, err)
	assert.False(t, running)
}

func TestIsAgentProcessRunning_ExecError(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	m.On("ExecInstance", "azp-agent-3", mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("container not running"))

	_, err := isAgentProcessRunning(context.Background(), m, 3)
	assert.Error(t, err)
}

func TestIsAgentProcessRunning_NilMetadata(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	op := mocks.NewMockOperation(t)
	op.On("WaitContext", mock.Anything).Return(nil)
	op.On("Get").Return(api.Operation{
		Metadata: nil,
	})
	m.On("ExecInstance", "azp-agent-3", mock.Anything, mock.Anything).Return(op, nil)

	_, err := isAgentProcessRunning(context.Background(), m, 3)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "metadata is nil")
}
