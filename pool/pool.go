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
	c            incus.InstanceServer
	conf         Config
	agentRe      *regexp.Regexp
	agentPrefix  string
	inFlight     *sync.Map
	offlineSince map[int]time.Time
	azure        AzureAgentClient
	now          func() time.Time
	logger       *slog.Logger
}

func NewPool(c incus.InstanceServer, conf Config) (*Pool, error) {
	if conf.Incus.ProjectName != "" {
		c = c.UseProject(conf.Incus.ProjectName)
	}

	p := &Pool{
		c:            c,
		conf:         conf,
		inFlight:     &sync.Map{},
		offlineSince: make(map[int]time.Time),
		now:          time.Now,
	}
	// Log the effective project ("default" when unset) so it's clear where
	// instances are created.
	p.logger = slog.With("pool", conf.Name, "project", p.Project())
	p.logger.Info("initializing pool", "vm", conf.Incus.VM, "image", conf.Incus.Image)

	if p.conf.Incus.VM && p.conf.Incus.TmpfsSizeInGb > 0 {
		p.logger.Warn("ignoring tmpfsSizeInGb for VM pool (not supported for VMs)")
	}

	var err error
	p.agentRe, err = regexp.Compile("^" + conf.Name + `-(\d+)$`)
	if err != nil {
		return nil, fmt.Errorf("unable to construct agent regexp from Name %q: %w", conf.Name, err)
	}

	if p.conf.Incus.StartupGracePeriod == 0 {
		if p.conf.Incus.VM {
			p.conf.Incus.StartupGracePeriod = 5 * time.Minute
		} else {
			p.conf.Incus.StartupGracePeriod = time.Minute
		}
	}
	if p.conf.OfflineGracePeriod == 0 {
		p.conf.OfflineGracePeriod = 5 * time.Minute
	}

	p.agentPrefix = p.conf.AgentPrefix
	if p.agentPrefix == "" {
		p.agentPrefix, err = os.Hostname()
		if err != nil {
			return nil, fmt.Errorf("determine Azure agent name prefix: %w", err)
		}
	}
	p.azure, err = newHTTPAzureAgentClient(p.conf.Azure.Url, p.conf.Azure.PAT, nil)
	if err != nil {
		return nil, err
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
			Type: p.instanceType(),
			Source: api.InstanceSource{
				Alias: p.conf.Incus.Image,
				Type:  "image",
			},
			Start: true,
			InstancePut: api.InstancePut{
				Config: map[string]string{
					"boot.host_shutdown_action": "force-stop",
				},
				Ephemeral: true,
				Devices:   map[string]map[string]string{},
			},
		}

		if p.conf.Incus.VM {
			// VMs have their own kernel; container-only keys are invalid/unneeded.
			if p.conf.Incus.MaxCores > 0 {
				req.Config["limits.cpu"] = fmt.Sprintf("%d", p.conf.Incus.MaxCores)
			}
			if p.conf.Incus.DiskSizeInGb > 0 {
				req.Devices["root"] = map[string]string{
					"type": "disk",
					"path": "/",
					"pool": p.conf.Incus.StoragePool,
					"size": fmt.Sprintf("%dGiB", p.conf.Incus.DiskSizeInGb),
				}
			}
		} else {
			req.Config["raw.lxc"] = "lxc.cgroup2.memory.oom.group = 1"
			req.Config["security.nesting"] = "true"
			if p.conf.Incus.MaxCores > 0 {
				req.Config["limits.cpu.allowance"] = fmt.Sprintf("%d%%", p.conf.Incus.MaxCores*100)
			}
			if p.conf.Incus.TmpfsSizeInGb > 0 {
				req.Devices["tmpfs"] = map[string]string{
					"type":   "disk",
					"source": "tmpfs:",
					"path":   "/tmp",
					"size":   fmt.Sprintf("%dGiB", p.conf.Incus.TmpfsSizeInGb),
				}
			}
		}

		if p.conf.Incus.MaxRamInGb > 0 {
			req.Config["limits.memory"] = fmt.Sprintf("%dGiB", p.conf.Incus.MaxRamInGb)
		}

		op, err := p.c.CreateInstance(req)
		if err != nil {
			return err
		}

		if err = waitOp(ctx, op, 2*time.Minute); err != nil {
			return err
		}

		if p.conf.Incus.VM {
			if err = p.waitForAgent(ctx, req.Name, 3*time.Minute, 2*time.Second); err != nil {
				return err
			}
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

		execPost := api.InstanceExecPost{
			Command: []string{
				"setsid",
				"--fork",
				"/home/agent/run_agent.sh",
				"--agent", p.AzureAgentName(idx),
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
	allInstances, err := p.c.GetInstances(p.instanceType())
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
	allInstances, err := p.c.GetInstancesFull(p.instanceType())
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
	now := p.now()

	instances, err := p.ListAgentsFull()
	if err != nil {
		return err
	}

	var (
		azureAgents map[string]AzureAgentStatus
		azureErr    error
		azureLoaded bool
	)
	loadAzureAgents := func() (map[string]AzureAgentStatus, error) {
		if !azureLoaded {
			azureLoaded = true
			azureAgents, azureErr = p.azure.ListAgents(ctx, p.conf.Name)
		}
		return azureAgents, azureErr
	}

	for _, instance := range instances {
		idx, err := p.agentIndex(instance.Name)
		if err != nil {
			continue
		}

		// Any state where this exact instance cannot yet be judged resets prior
		// observations for the reused pool index.
		if instance.State == nil {
			delete(p.offlineSince, idx)
			p.logger.Debug("reaper: skipping instance",
				"reason", "instance state unknown",
				"idx", idx,
			)
			continue
		}

		status := instance.State.Status
		if status != "Running" {
			delete(p.offlineSince, idx)
			p.logger.Debug("reaper: skipping instance",
				"reason", fmt.Sprintf("container status: %s", status),
				"idx", idx,
			)
			continue
		}

		age := now.Sub(instance.CreatedAt)
		if age < p.conf.Incus.StartupGracePeriod {
			delete(p.offlineSince, idx)
			p.logger.Debug("reaper: skipping instance",
				"reason", "age < grace period",
				"age", age,
				"idx", idx,
			)
			continue
		}

		wrapperRunning, err := p.isAgentProcessRunning(ctx, idx)
		if err != nil {
			delete(p.offlineSince, idx)
			p.logger.Warn("reaper: health check failed", "idx", idx, "err", err)
			continue
		}

		workerRunning, err := p.isAgentWorkerRunning(ctx, idx)
		if err != nil {
			delete(p.offlineSince, idx)
			p.logger.Warn("reaper: worker health check failed", "idx", idx, "err", err)
			continue
		}
		if workerRunning {
			delete(p.offlineSince, idx)
			p.logger.Debug("reaper: skipping instance",
				"reason", "Agent.Worker is running",
				"age", age,
				"idx", idx,
			)
			continue
		}

		controlProcessRunning := wrapperRunning
		if !controlProcessRunning {
			controlProcessRunning, err = p.isAgentListenerRunning(ctx, idx)
			if err != nil {
				delete(p.offlineSince, idx)
				p.logger.Warn("reaper: listener health check failed", "idx", idx, "err", err)
				continue
			}
		}

		reason := "agent control processes are not running"
		if controlProcessRunning {
			agents, err := loadAzureAgents()
			if err != nil {
				delete(p.offlineSince, idx)
				p.logger.Warn("reaper: Azure health check failed; failing closed", "idx", idx, "err", err)
				continue
			}

			azureStatus, found := agents[p.AzureAgentName(idx)]
			if found && (azureStatus.Online || azureStatus.Assigned) {
				delete(p.offlineSince, idx)
				p.logger.Debug("reaper: skipping instance",
					"reason", "Azure agent is online or assigned",
					"age", age,
					"idx", idx,
				)
				continue
			}

			offlineAt, observed := p.offlineSince[idx]
			if !observed {
				p.offlineSince[idx] = now
				p.logger.Info("reaper: observed offline unassigned agent",
					"idx", idx,
					"grace", p.conf.OfflineGracePeriod,
				)
				continue
			}
			offlineFor := now.Sub(offlineAt)
			if offlineFor < p.conf.OfflineGracePeriod {
				p.logger.Debug("reaper: skipping instance",
					"reason", "offline duration < grace period",
					"offline_for", offlineFor,
					"idx", idx,
				)
				continue
			}
			reason = "Azure agent remained offline and unassigned without Agent.Worker"
		}

		if _, exists := p.inFlight.LoadOrStore(idx, true); exists {
			p.logger.Debug("reaper: skipping instance",
				"reason", "in-flight",
				"idx", idx,
			)
			continue
		}

		p.logger.Info("reaper: reaping stale instance", "idx", idx, "age", age, "reason", reason)
		err = p.reapInstance(ctx, idx)
		p.inFlight.Delete(idx)

		if err != nil {
			p.logger.Error("reaper: failed to reap", "idx", idx, "err", err)
			agentsReapedErrorMetric.WithLabelValues(p.conf.Name).Inc()
		} else {
			delete(p.offlineSince, idx)
			agentsReapedMetric.WithLabelValues(p.conf.Name).Inc()
		}
	}

	return nil
}

func (p *Pool) isAgentProcessRunning(ctx context.Context, idx int) (bool, error) {
	return p.isProcessRunning(ctx, idx, "run_agent.sh")
}

func (p *Pool) isAgentWorkerRunning(ctx context.Context, idx int) (bool, error) {
	return p.isProcessRunning(ctx, idx, "Agent.Worker")
}

func (p *Pool) isAgentListenerRunning(ctx context.Context, idx int) (bool, error) {
	return p.isProcessRunning(ctx, idx, "Agent.Listener")
}

func (p *Pool) isProcessRunning(ctx context.Context, idx int, pattern string) (bool, error) {
	op, err := p.c.ExecInstance(
		p.AgentName(idx),
		api.InstanceExecPost{
			Command: []string{
				"pgrep",
				"-u",
				provision.AgentUser,
				"-f",
				pattern,
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

// ReapAgent force-stops one pool instance. Incus removes the instance because
// pool agents are ephemeral, and the reconciler then creates a replacement.
func (p *Pool) ReapAgent(ctx context.Context, idx int) error {
	if idx < 0 || idx >= p.conf.AgentCount {
		return fmt.Errorf("invalid agent index %d, pool %q has %d agents", idx, p.Name(), p.conf.AgentCount)
	}
	if _, exists := p.inFlight.LoadOrStore(idx, true); exists {
		return fmt.Errorf("agent index %d in pool %q already has an operation in flight", idx, p.Name())
	}
	defer p.inFlight.Delete(idx)

	return p.reapInstance(ctx, idx)
}

func (p *Pool) reapInstance(ctx context.Context, idx int) error {
	name := p.AgentName(idx)

	// waitTimeout must exceed stopTimeout so the client-side context doesn't cancel before Incus reports completion.
	stopTimeout := 30
	waitTimeout := 45 * time.Second
	if p.conf.Incus.VM {
		stopTimeout = 60
		waitTimeout = 90 * time.Second
	}

	op, err := p.c.UpdateInstanceState(name, api.InstanceStatePut{
		Action:  "stop",
		Force:   true,
		Timeout: stopTimeout,
	}, "")
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	if err := waitOp(ctx, op, waitTimeout); err != nil {
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

func (p *Pool) AzureAgentName(idx int) string {
	return fmt.Sprintf("%s-%d", p.agentPrefix, idx)
}

// waitForAgent polls a trivial exec until the guest agent responds, up to timeout.
// VMs need their incus-agent running before file push / exec; containers are ready
// immediately so callers should only invoke this for VM pools.
// Near-duplicate of waitBuilderAgent in provision/provision.go — kept separate to avoid a pool<->provision import cycle.
func (p *Pool) waitForAgent(ctx context.Context, name string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		op, err := p.c.ExecInstance(name, api.InstanceExecPost{
			Command:     []string{"true"},
			WaitForWS:   true,
			Interactive: false,
		}, &incus.InstanceExecArgs{})
		if err == nil {
			attemptCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			werr := op.WaitContext(attemptCtx)
			cancel()
			if werr == nil {
				return nil
			}
			lastErr = werr
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("agent on %q not ready after %s: %w", name, timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// instanceType returns the Incus instance type for this pool's agents.
func (p *Pool) instanceType() api.InstanceType {
	if p.conf.Incus.VM {
		return api.InstanceTypeVM
	}
	return api.InstanceTypeContainer
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

// Project returns the effective Incus project for this pool ("default" when
// unset), matching where instances are actually created.
func (p *Pool) Project() string {
	if p.conf.Incus.ProjectName == "" {
		return "default"
	}
	return p.conf.Incus.ProjectName
}
