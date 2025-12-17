package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

var (
	agentRe            = regexp.MustCompile("^" + defaultAgentPrefix + `-(\d{1,2})$`)
	containersToIgnore = map[string]bool{}
)

func createAgent(ctx context.Context, c incus.InstanceServer, conf Config, idx int) error {

	// todo: check for base image existence

	req := api.InstancesPost{
		Name: agentName(idx),
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Alias: defaultImageAlias,
			Type:  "image",
		},
		Start: true,
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"boot.host_shutdown_action": "force-stop",
			},
			Ephemeral: true,
			Devices: map[string]map[string]string{
				"tmpfs": {
					"type":   "disk",
					"source": "tmpfs:",
					"path":   "/tmp",
					"size":   fmt.Sprintf("%dGiB", conf.TmpfsSizeInGb),
				},
			},
		},
	}

	if conf.MaxCores > 0 {
		req.Config["limits.cpu.allowance"] = fmt.Sprintf("%d%%", conf.MaxCores*100)
	}

	if conf.MaxRamInGb > 0 {
		req.Config["limits.memory"] = fmt.Sprintf("%dGiB", conf.MaxRamInGb)
	}

	op, err := c.CreateInstance(req)
	if err != nil {
		return err
	}

	if err = op.WaitContext(ctx); err != nil {
		return err
	}

	if err = c.CreateInstanceFile(req.Name, "/home/agent/.token", incus.InstanceFileArgs{
		Content:   strings.NewReader(conf.Azure.PAT),
		WriteMode: "overwrite",
		Mode:      400,
		UID:       agentUid,
		GID:       agentGid,
	}); err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	op, err = c.ExecInstance(
		req.Name,
		api.InstanceExecPost{
			Command: []string{
				"setsid",
				"--fork",
				"/home/agent/run_agent.sh",
				"--agent",
				fmt.Sprintf("%s-%d", hostname, idx),
				"--pool",
				conf.Azure.Pool,
				"--url",
				conf.Azure.Url,
			},
			Interactive: false,
			WaitForWS:   true,
			User:        agentUid,
			Group:       agentGid,
		},
		&incus.InstanceExecArgs{},
	)

	if err != nil {
		return err
	}

	return op.WaitContext(ctx)

}

func reconcileAgents(c incus.InstanceServer, conf Config, agentsToCreate chan<- int) error {
	var (
		expectedInstances uint64 = math.MaxUint64 >> (63 - conf.AgentCount)
		instancesFound    uint64 = 0
	)

	instances, err := c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return fmt.Errorf("unable to list instances in project %q: %w",
			conf.ProjectName,
			err,
		)
	}

	for _, i := range instances {
		if _, found := containersToIgnore[i.Name]; found {
			continue
		}

		matches := agentRe.FindStringSubmatch(i.Name)
		if matches == nil {
			containersToIgnore[i.Name] = true
			slog.Warn("ignoring container", "name", i.Name, "reason", "no regexp match on name")
			continue
		}

		idx, err := strconv.Atoi(matches[1])
		if err != nil {
			return err
		}

		instancesFound |= 1 << idx
	}

	instancesToCreate := expectedInstances ^ instancesFound

	for idx := range conf.AgentCount {
		if (1<<idx)&instancesToCreate > 0 {
			agentsToCreate <- idx
		}
	}

	return nil

}

func agentName(idx int) string {
	return fmt.Sprintf("%s-%d", defaultAgentPrefix, idx)
}
