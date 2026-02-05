package agent

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
	"github.com/sklarsa/incus-azure-pipelines/provision"
)

var (
	containersToIgnore = map[string]bool{}
)

type Repository struct {
	c       incus.InstanceServer
	conf    Config
	agentRe *regexp.Regexp
}

func NewRepository(c incus.InstanceServer, conf Config) (*Repository, error) {
	r := &Repository{
		c:    c,
		conf: conf,
	}

	var err error
	r.agentRe, err = regexp.Compile("^" + conf.NamePrefix + `-(\d{1,2})$`)
	if err != nil {
		err = fmt.Errorf("unable to construct agent regexp from NamePrefix %q: %w", conf.NamePrefix, err)
	}
	return r, err
}

func (r *Repository) Create(ctx context.Context, idx int) error {

	// todo: check for base image existence

	req := api.InstancesPost{
		Name: agentName(r.conf, idx),
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Alias: r.conf.Image,
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
					"size":   fmt.Sprintf("%dGiB", r.conf.TmpfsSizeInGb),
				},
			},
		},
	}

	if r.conf.MaxCores > 0 {
		req.Config["limits.cpu.allowance"] = fmt.Sprintf("%d%%", r.conf.MaxCores*100)
	}

	if r.conf.MaxRamInGb > 0 {
		req.Config["limits.memory"] = fmt.Sprintf("%dGiB", r.conf.MaxRamInGb)
	}

	op, err := r.c.CreateInstance(req)
	if err != nil {
		return err
	}

	if err = op.WaitContext(ctx); err != nil {
		return err
	}

	if err = r.c.CreateInstanceFile(req.Name, "/home/agent/.token", incus.InstanceFileArgs{
		Content:   strings.NewReader(r.conf.Azure.PAT),
		WriteMode: "overwrite",
		Mode:      400,
		UID:       int64(provision.AgentUid),
		GID:       int64(provision.AgentGid),
	}); err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	op, err = r.c.ExecInstance(
		req.Name,
		api.InstanceExecPost{
			Command: []string{
				"setsid",
				"--fork",
				"/home/agent/run_agent.sh",
				"--agent",
				fmt.Sprintf("%s-%d", hostname, idx),
				"--pool",
				r.conf.Azure.Pool,
				"--url",
				r.conf.Azure.Url,
			},
			Interactive: false,
			WaitForWS:   true,
			User:        provision.AgentUid,
			Group:       provision.AgentGid,
		},
		&incus.InstanceExecArgs{},
	)

	if err != nil {
		return err
	}

	return op.WaitContext(ctx)

}

func (r *Repository) List() ([]api.Instance, error) {
	agents := []api.Instance{}

	allInstances, err := r.c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, err
	}

	for _, i := range allInstances {
		matches := r.agentRe.FindStringSubmatch(i.Name)
		if len(matches) > 0 {
			agents = append(agents, i)
		} else {
			slog.Debug("ignoring container", "name", i.Name, "reason", "no regexp match on name")
		}
	}

	return agents, nil

}

func (r *Repository) Reconcile(desiredAgentCount int, agentsToCreate chan<- int) error {

	var (
		expectedInstances uint64 = math.MaxUint64 >> (63 - desiredAgentCount)
		instancesFound    uint64 = 0
	)

	instances, err := r.List()
	if err != nil {
		return err
	}

	for _, i := range instances {
		if _, found := containersToIgnore[i.Name]; found {
			continue
		}

		matches := r.agentRe.FindStringSubmatch(i.Name)
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

	for idx := range desiredAgentCount {
		if (1<<idx)&instancesToCreate > 0 {
			agentsToCreate <- idx
		}
	}

	return nil

}

func agentName(c Config, idx int) string {
	return fmt.Sprintf("%s-%d", c.NamePrefix, idx)
}
