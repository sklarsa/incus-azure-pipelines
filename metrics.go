package main

import (
	"log/slog"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

type agentUptimeCollector struct {
	c    incus.InstanceServer
	desc *prometheus.Desc
}

func newAgentUptimeCollector(c incus.InstanceServer) *agentUptimeCollector {
	return &agentUptimeCollector{
		c: c,
		desc: prometheus.NewDesc(
			"iap_agent_uptime",
			"Time (in seconds) an agent is up and running",
			[]string{"idx"},
			nil,
		),
	}
}

func (c *agentUptimeCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.desc
}

func (c *agentUptimeCollector) Collect(ch chan<- prometheus.Metric) {
	instances, err := c.c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		slog.Error("error obtaining instance list from incus", "err", err)
		return
	}

	for _, i := range instances {
		matches := agentRe.FindStringSubmatch(i.Name)
		if matches == nil {
			continue
		}

		val := time.Since(i.CreatedAt).Seconds()

		idx := matches[1]

		ch <- prometheus.MustNewConstMetric(
			c.desc,
			prometheus.GaugeValue,
			val,
			idx,
		)

	}
}

var agentsCreatedMetric = promauto.NewCounter(
	prometheus.CounterOpts{
		Name: "iap_agents_created_success",
		Help: "Count of the number of agents created by the orchestrator",
	},
)

var agentsCreatedErrorMetric = promauto.NewCounter(
	prometheus.CounterOpts{
		Name: "iap_agents_created_error",
		Help: "Count of the number of errors that have occurred while creating an agent",
	},
)
