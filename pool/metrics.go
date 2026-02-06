package pool

import (
	"log/slog"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type agentUptimeCollector struct {
	p    *Pool
	desc *prometheus.Desc
}

func newAgentUptimeCollector(p *Pool) *agentUptimeCollector {
	return &agentUptimeCollector{
		p: p,
		desc: prometheus.NewDesc(
			"iap_agent_uptime",
			"Time (in seconds) an agent is up and running",
			[]string{"idx"},
			map[string]string{
				"pool": p.Name(),
			},
		),
	}
}

func (c *agentUptimeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *agentUptimeCollector) Collect(ch chan<- prometheus.Metric) {
	instances, err := c.p.ListAgents()
	if err != nil {
		slog.Error("error obtaining instance list from incus", "err", err)
		return
	}

	for _, i := range instances {

		idx, err := c.p.agentIndex(i.Name)
		if err != nil {
			continue
		}

		val := time.Since(i.CreatedAt).Seconds()

		m, err := prometheus.NewConstMetric(
			c.desc,
			prometheus.GaugeValue,
			val,
			strconv.Itoa(idx),
		)

		if err != nil {
			slog.Error("error producing agent uptime metric", "err", err)
			return
		}

		ch <- m

	}
}

var agentsCreatedMetric = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "iap_agents_created_success",
		Help: "Count of the number of agents created by the orchestrator",
	},
	[]string{"pool"},
)

var agentsCreatedErrorMetric = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "iap_agents_created_error",
		Help: "Count of the number of errors that have occurred while creating an agent",
	},
	[]string{"pool"},
)

var agentsReapedMetric = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "iap_agents_reaped",
		Help: "Count of stale agents reaped",
	},
	[]string{"pool"},
)

var agentsReapedErrorMetric = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "iap_agents_reaped_error",
		Help: "Count of errors while reaping stale agents",
	},
	[]string{"pool"},
)
