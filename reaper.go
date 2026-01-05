package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

const (
	// Don't consider instances younger than this for reaping
	startupGracePeriod = 2 * time.Minute

	// How often the reaper checks for stale instances
	reaperInterval = 30 * time.Second
)

func runReaper(ctx context.Context, c incus.InstanceServer, conf Config) {
	slog.Info("starting goroutine", "type", "reaper")

	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("exiting goroutine", "type", "reaper")
			return
		case <-ticker.C:
			reapStaleInstances(ctx, c, conf)
		}
	}
}

func reapStaleInstances(ctx context.Context, c incus.InstanceServer, conf Config) {
	instances, err := c.GetInstancesFull(api.InstanceTypeContainer)
	if err != nil {
		slog.Error("reaper: failed to list instances", "err", err)
		return
	}

	now := time.Now()

	for _, instance := range instances {
		matches := agentRe.FindStringSubmatch(instance.Name)
		if matches == nil {
			continue
		}

		idx, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}

		// Skip if already being created/reaped
		if _, exists := inFlight.Load(idx); exists {
			slog.Debug("reaper: skipping instance",
				"reason", "in-flight",
				"idx", idx,
			)
			continue
		}

		// Skip if container is not running
		status := instance.State.Status
		if status != "Running" {
			slog.Debug("reaper: skipping instance",
				"reason", fmt.Sprintf("container status: %s", status),
				"idx", idx,
			)
			continue
		}

		// Skip if container is too young
		age := now.Sub(instance.CreatedAt)
		if age < startupGracePeriod {
			slog.Debug("reaper: skipping instance",
				"reason", "age < grace period",
				"age", age,
				"idx", idx,
			)
			continue
		}

		// Check if agent process is running
		running, err := isAgentProcessRunning(ctx, c, idx)
		if err != nil {
			slog.Warn("reaper: health check failed", "idx", idx, "err", err)
			continue
		}

		if running {
			continue
		}

		if _, exists := inFlight.LoadOrStore(idx, true); exists {
			continue
		}

		// Stale - reap it
		slog.Info("reaper: reaping stale instance", "idx", idx, "age", age)

		err = reapInstance(ctx, c, idx)
		inFlight.Delete(idx)

		if err != nil {
			slog.Error("reaper: failed to reap", "idx", idx, "err", err)
			agentsReapedErrorMetric.Add(1)
		} else {
			agentsReapedMetric.Add(1)
		}
	}
}

func isAgentProcessRunning(ctx context.Context, c incus.InstanceServer, idx int) (bool, error) {
	op, err := c.ExecInstance(
		agentName(idx),
		api.InstanceExecPost{
			Command: []string{
				"pgrep",
				"-u",
				agentUser,
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

	if err := op.WaitContext(ctx); err != nil {
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

func reapInstance(ctx context.Context, c incus.InstanceServer, idx int) error {
	name := agentName(idx)

	op, err := c.UpdateInstanceState(name, api.InstanceStatePut{
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

	if err := op.WaitContext(ctx); err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return nil
		}
		return err
	}

	return nil
}
