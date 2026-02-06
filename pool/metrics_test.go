package pool

import (
	"testing"
	"time"

	"github.com/lxc/incus/v6/shared/api"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/sklarsa/incus-azure-pipelines/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentUptimeCollector_Describe(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	collector := newAgentUptimeCollector(pool)

	ch := make(chan *prometheus.Desc, 1)
	collector.Describe(ch)
	close(ch)

	desc := <-ch
	require.NotNil(t, desc)
	assert.Contains(t, desc.String(), "iap_agent_uptime")
}

func TestAgentUptimeCollector_Collect_NoAgents(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	collector := newAgentUptimeCollector(pool)

	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch)
	close(ch)

	// Should have no metrics when no agents exist
	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	assert.Empty(t, metrics)
}

func TestAgentUptimeCollector_Collect_WithAgents(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)

	createdAt := time.Now().Add(-5 * time.Minute)
	m.On("GetInstances", api.InstanceTypeContainer).Return([]api.Instance{
		{Name: "azp-agent-0", CreatedAt: createdAt},
		{Name: "azp-agent-1", CreatedAt: createdAt},
		{Name: "other-container", CreatedAt: createdAt}, // Should be filtered out
	}, nil)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	collector := newAgentUptimeCollector(pool)

	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch)
	close(ch)

	var metrics []prometheus.Metric
	for metric := range ch {
		metrics = append(metrics, metric)
	}

	require.Len(t, metrics, 2)

	// Extract and verify the actual metric labels
	idxLabels := make(map[string]bool)
	for _, metric := range metrics {
		var dtoMetric dto.Metric
		err := metric.Write(&dtoMetric)
		require.NoError(t, err)

		// Find the idx label
		for _, label := range dtoMetric.Label {
			if label.GetName() == "idx" {
				idxLabels[label.GetValue()] = true
			}
		}

		// Verify uptime is approximately 5 minutes (300 seconds)
		uptime := dtoMetric.Gauge.GetValue()
		assert.InDelta(t, 300, uptime, 5, "uptime should be ~300 seconds")
	}

	// Verify we got metrics for agent indices 0 and 1
	assert.True(t, idxLabels["0"], "should have metric for idx=0")
	assert.True(t, idxLabels["1"], "should have metric for idx=1")
	assert.Len(t, idxLabels, 2, "should have exactly 2 unique idx labels")
}

func TestAgentUptimeCollector_Collect_ListError(t *testing.T) {
	m := mocks.NewMockInstanceServer(t)
	m.On("GetInstances", api.InstanceTypeContainer).Return(nil, assert.AnError)

	pool, err := NewPool(m, testConfig())
	require.NoError(t, err)

	collector := newAgentUptimeCollector(pool)

	ch := make(chan prometheus.Metric, 10)
	collector.Collect(ch)
	close(ch)

	// Should have no metrics on error
	var metrics []prometheus.Metric
	for m := range ch {
		metrics = append(metrics, m)
	}
	assert.Empty(t, metrics)
}
