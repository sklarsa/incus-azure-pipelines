package pool

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sklarsa/incus-azure-pipelines/provision"
)

// defaultOperationTimeout is the default timeout for Incus operations.
const defaultOperationTimeout = 30 * time.Second

func waitOp(ctx context.Context, op incus.Operation, timeout time.Duration) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	err := op.WaitContext(timeoutCtx)
	if err != nil && timeoutCtx.Err() != nil && ctx.Err() == nil {
		return fmt.Errorf("operation timed out after %s: %w", timeout, err)
	}
	return err
}

type Pool struct {
	c        incus.InstanceServer
	conf     Config
	agentRe  *regexp.Regexp
	inFlight *sync.Map
	logger   *slog.Logger
}

func NewPool(c incus.InstanceServer, conf Config) (*Pool, error) {
	if conf.Incus.ProjectName != "" {
		c = c.UseProject(conf.Incus.ProjectName)
		slog.Info("using project", "name", conf.Incus.ProjectName)
	} else {
		slog.Info("using default project")
	}

	p := &Pool{
		c:        c,
		conf:     conf,
		inFlight: &sync.Map{},
		logger:   slog.With("pool", conf.Name, "project", conf.Incus.ProjectName),
	}

	var err error
	p.agentRe, err = regexp.Compile("^" + conf.Name + `-(\d+)$`)
	if err != nil {
		return nil, fmt.Errorf("unable to construct agent regexp from Name %q: %w", conf.Name, err)
	}

	err = prometheus.DefaultRegisterer.Register(newAgentUptimeCollector(p))
	var are prometheus.AlreadyRegisteredError
	if errors.As(err, &are) {
		err = nil
	}
	return p, err
}

func (p *Pool) CreateAgent(ctx context.Context, idx int) error {
	if idx >= p.conf.AgentCount {
		return fmt.Errorf("cannot create agent at index %d, capacity is %d", idx, p.conf.AgentCount)
	}

	// todo: check for base image existence

	if _, exists := p.inFlight.LoadOrStore(idx, true); exists {
		p.logger.Warn("skipping agent creation",
			"reason", "in-flight",
			"idx", idx,
		)
		return nil
	}
	defer p.inFlight.Delete(idx)

	createErr := func() error {

		req := api.InstancesPost{
			Name: p.AgentName(idx),
			Type: api.InstanceTypeContainer,
			Source: api.InstanceSource{
				Alias: p.conf.Incus.Image,
				Type:  "image",
			},
			Start: true,
			InstancePut: api.InstancePut{
				Config: map[string]string{
					"boot.host_shutdown_action": "force-stop",
					"raw.lxc":                   "lxc.cgroup2.memory.oom.group = 1",
					"security.nesting":          "true",
				},
				Ephemeral: true,
				Devices:   map[string]map[string]string{},
			},
		}

		if p.conf.Incus.MaxCores > 0 {
			req.Config["limits.cpu.allowance"] = fmt.Sprintf("%d%%", p.conf.Incus.MaxCores*100)
		}

		if p.conf.Incus.MaxRamInGb > 0 {
			req.Config["limits.memory"] = fmt.Sprintf("%dGiB", p.conf.Incus.MaxRamInGb)
		}

		if p.conf.Incus.TmpfsSizeInGb > 0 {
			req.Devices["tmpfs"] = map[string]string{
				"type":   "disk",
				"source": "tmpfs:",
				"path":   "/tmp",
				"size":   fmt.Sprintf("%dGiB", p.conf.Incus.TmpfsSizeInGb),
			}
		}

		op, err := p.c.CreateInstance(req)
		if err != nil {
			return err
		}

		if err = waitOp(ctx, op, 2*time.Minute); err != nil {
			return err
		}

		if err = p.c.CreateInstanceFile(req.Name, "/home/agent/.token", incus.InstanceFileArgs{
			Content:   strings.NewReader(p.conf.Azure.PAT),
			WriteMode: "overwrite",
			Mode:      400,
			UID:       int64(provision.AgentUid),
			GID:       int64(provision.AgentGid),
		}); err != nil {
			return err
		}

		agentPrefix := p.conf.AgentPrefix
		if agentPrefix == "" {
			var err error
			agentPrefix, err = os.Hostname()
			if err != nil {
				return err
			}
		}

		execPost := api.InstanceExecPost{
			Command: []string{
				"setsid",
				"--fork",
				"/home/agent/run_agent.sh",
				"--agent", fmt.Sprintf("%s-%d", agentPrefix, idx),
				"--pool", p.conf.Name,
				"--url", p.conf.Azure.Url,
			},
			Interactive: false,
			WaitForWS:   true,
		}

		if len(p.conf.Env) > 0 {
			execPost.Environment = p.conf.Env
		}

		op, err = p.c.ExecInstance(
			req.Name,
			execPost,
			&incus.InstanceExecArgs{},
		)

		if err != nil {
			return err
		}

		return waitOp(ctx, op, defaultOperationTimeout)

	}()

	if createErr == nil {
		agentsCreatedMetric.WithLabelValues(p.conf.Name).Inc()
	} else {
		agentsCreatedErrorMetric.WithLabelValues(p.conf.Name).Inc()
	}

	return createErr

}

func (p *Pool) isAgent(i api.Instance) bool {
	matches := p.agentRe.FindStringSubmatch(i.Name)
	return len(matches) > 0
}

func (p *Pool) ListAgents() ([]api.Instance, error) {
	agents := []api.Instance{}
	allInstances, err := p.c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return nil, err
	}

	for _, i := range allInstances {
		if p.isAgent(i) {
			agents = append(agents, i)
		}
	}

	return agents, nil
}

func (p *Pool) ListAgentsFull() ([]api.InstanceFull, error) {
	agents := []api.InstanceFull{}
	allInstances, err := p.c.GetInstancesFull(api.InstanceTypeContainer)
	if err != nil {
		return nil, err
	}

	for _, i := range allInstances {
		if p.isAgent(i.Instance) {
			agents = append(agents, i)
		}
	}

	return agents, nil
}

func (p *Pool) Reconcile(agentsToCreate chan<- int) error {
	instancesFound := make(map[int]struct{}, p.conf.AgentCount)

	instances, err := p.ListAgents()
	if err != nil {
		return err
	}

	for _, i := range instances {
		matches := p.agentRe.FindStringSubmatch(i.Name)
		if len(matches) < 2 {
			return fmt.Errorf("instance name %q did not match agent regex", i.Name)
		}
		idx, err := strconv.Atoi(matches[1])
		if err != nil {
			return err
		}
		instancesFound[idx] = struct{}{}
	}

	for idx := range p.conf.AgentCount {
		if _, exists := instancesFound[idx]; !exists {
			agentsToCreate <- idx
		}
	}

	return nil
}

func (p *Pool) Reap(ctx context.Context) error {
	now := time.Now()

	instances, err := p.ListAgentsFull()
	if err != nil {
		return err
	}

	for _, instance := range instances {

		idx, err := p.agentIndex(instance.Name)
		if err != nil {
			continue
		}

		// Skip if container is not running
		if instance.State == nil {
			p.logger.Debug("reaper: skipping instance",
				"reason", "instance state unknown",
				"idx", idx,
			)
			continue
		}

		status := instance.State.Status
		if status != "Running" {
			p.logger.Debug("reaper: skipping instance",
				"reason", fmt.Sprintf("container status: %s", status),
				"idx", idx,
			)
			continue
		}

		// Skip if container is too young
		age := now.Sub(instance.CreatedAt)
		if age < p.conf.Incus.StartupGracePeriod {
			p.logger.Debug("reaper: skipping instance",
				"reason", "age < grace period",
				"age", age,
				"idx", idx,
			)
			continue
		}

		// Check if agent process is running
		running, err := p.isAgentProcessRunning(ctx, idx)
		if err != nil {
			p.logger.Warn("reaper: health check failed", "idx", idx, "err", err)
			continue
		}

		if running {
			p.logger.Debug("reaper: skipping instance",
				"reason", "agent process is running",
				"age", age,
				"idx", idx,
			)
			continue
		}

		if _, exists := p.inFlight.LoadOrStore(idx, true); exists {
			p.logger.Debug("reaper: skipping instance",
				"reason", "in-flight",
				"idx", idx,
			)
			continue
		}

		// Stale - reap it
		p.logger.Info("reaper: reaping stale instance", "idx", idx, "age", age)

		err = p.reapInstance(ctx, idx)
		p.inFlight.Delete(idx)

		if err != nil {
			p.logger.Error("reaper: failed to reap", "idx", idx, "err", err)
			agentsReapedErrorMetric.WithLabelValues(p.conf.Name).Inc()
		} else {
			agentsReapedMetric.WithLabelValues(p.conf.Name).Inc()
		}
	}

	return nil

}

func (p *Pool) isAgentProcessRunning(ctx context.Context, idx int) (bool, error) {
	op, err := p.c.ExecInstance(
		p.AgentName(idx),
		api.InstanceExecPost{
			Command: []string{
				"pgrep",
				"-u",
				provision.AgentUser,
				"-f",
				"run_agent.sh",
			},
			WaitForWS:   true,
			Interactive: false,
		},
		&incus.InstanceExecArgs{},
	)
	if err != nil {
		return false, fmt.Errorf("exec failed: %w", err)
	}

	if err := waitOp(ctx, op, defaultOperationTimeout); err != nil {
		return false, fmt.Errorf("wait failed: %w", err)
	}

	meta := op.Get().Metadata
	if meta == nil {
		return false, fmt.Errorf("metadata is nil")
	}

	returnCode, ok := meta["return"].(float64)
	if !ok {
		return false, fmt.Errorf("return code not found")
	}

	return int(returnCode) == 0, nil
}

func (p *Pool) reapInstance(ctx context.Context, idx int) error {
	name := p.AgentName(idx)

	op, err := p.c.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Force:   true,
		Timeout: 30,
	}, "")
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	if err := waitOp(ctx, op, 45*time.Second); err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	return nil
}

// errNotPoolAgent is returned when a container name does not match this pool's naming pattern.
var errNotPoolAgent = errors.New("not a pool agent")

// agentIndex returns the 0-based index of an agent based on its name.
// Returns ErrNotPoolAgent if the name doesn't match this pool's agent pattern.
func (p *Pool) agentIndex(name string) (int, error) {
	matches := p.agentRe.FindStringSubmatch(name)
	if len(matches) == 0 {
		return 0, errNotPoolAgent
	}

	idx, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, fmt.Errorf("parse agent index from %q: %w", name, err)
	}

	return idx, nil
}

func (p *Pool) AgentName(idx int) string {
	return fmt.Sprintf("%s-%d", p.conf.Name, idx)
}

func (p *Pool) AgentLogs(ctx context.Context, idx int, w io.Writer) error {

	if idx >= p.conf.AgentCount {
		return fmt.Errorf("invalid agent index %d, pool %q has %d agents", idx, p.Name(), p.conf.AgentCount)
	}

	op, err := p.c.ExecInstance(
		p.AgentName(idx),
		api.InstanceExecPost{
			Command:     []string{"cat", "/home/agent/azp-agent.log"},
			WaitForWS:   true,
			Interactive: false,
		}, &incus.InstanceExecArgs{
			Stdout: w,
		},
	)
	if err != nil {
		return err
	}
	return waitOp(ctx, op, defaultOperationTimeout)
}

func (p *Pool) Name() string {
	return p.conf.Name
}

func (p *Pool) Project() string {
	return p.conf.Incus.ProjectName
}
