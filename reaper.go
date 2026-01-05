package main

import (
	"context"
	"fmt"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

var instancesToReap = make(chan int)

func checkIfInstanceIsStale(ctx context.Context, c incus.InstanceServer, idx int) (bool, error) {

	op, err := c.ExecInstance(
		agentName(idx),
		api.InstanceExecPost{
			Command: []string{
				"/bin/bash",
				"-c",
				fmt.Sprintf("ps -u %s | grep run_agent.sh", agentUser),
			},
			WaitForWS:   true,
			Interactive: false,
		},
		&incus.InstanceExecArgs{},
	)
	if err != nil {
		return false, err
	}

	if err := op.WaitContext(ctx); err != nil {
		return false, err
	}

	meta := op.Get().Metadata
	if meta == nil {
		return false, fmt.Errorf("unable to obtain return code. metadata is nil")
	}

	returnCode, ok := meta["return"].(float64)
	if ok {
		exitCode := int(returnCode)
		// if command succeeds, run_agent is still running and no reaping required
		if exitCode == 0 {
			return false, nil
		}
	} else {
		return false, fmt.Errorf("unable to obtain return code. return code not found in metadata")
	}

	// If command returns a non-0, run_agent is not running (or ps has failed)
	// so we need to reap the instance.
	// First, let's lock the instance to ensure that we don't create it during
	// the reaping process
	if _, exists := inFlight.LoadOrStore(idx, true); exists {
		// If the instance is inFlight, we don't need to do anything because
		// it is already being re-created
		return nil
	}
	defer inFlight.Delete(idx)

}
