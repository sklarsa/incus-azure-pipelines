package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"math"
	"os"
	"regexp"
	"strconv"
	"sync"
	"time"

	incus "github.com/lxc/incus/client"
	"github.com/lxc/incus/shared/api"
)

const (
	defaultAgentPrefix = "azp-agent"
)

var (
	mu      = &sync.Mutex{}
	agentRe = regexp.MustCompile("^" + defaultAgentPrefix + `-(\d{1,2})$`)
)

func main() {

	conf := Config{
		ProjectName: "azure-pipelines",
		AgentCount:  8,
		BaseImage:   "byoc-test-debian-base",
	}

	c, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Disconnect()

	// todo: we need to make sure the project has a profile, since default profile may not work
	if err = ensureProject(c, conf.ProjectName); err != nil {
		log.Fatal(err)
	}

	c = c.UseProject(conf.ProjectName)

	agentsToCreate := make(chan int)
	go func() {
		for {
			idx, open := <-agentsToCreate
			if !open {
				return
			}

			fmt.Printf("Creating agent %d\n", idx)

			if err := createAgent(context.Background(), c, conf, idx); err != nil {
				slog.Error("failed to create agent", "idx", idx, "err", err)
			}
		}
	}()

	go func() {
		for {
			if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
				log.Fatal(err)
			}
			time.Sleep(5 * time.Second)
		}
	}()

	listener, err := c.GetEvents()
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Disconnect()

	t, err := listener.AddHandler(nil, func(e api.Event) {
		fmt.Fprintf(os.Stdout, `New Event
timestamp = %s
type = %s
data = %s

`,
			e.Timestamp,
			e.Type,
			e.Metadata,
		)
	})
	if err != nil {
		log.Fatal(err)
	}
	defer listener.RemoveHandler(t)

	listener.Wait()

}

func ensureProject(c incus.InstanceServer, name string) error {
	projects, err := c.GetProjectNames()
	if err != nil {
		return fmt.Errorf("error listing project names: %w", err)
	}

	for _, p := range projects {
		if p == name {
			return nil
		}
	}

	return c.CreateProject(api.ProjectsPost{
		Name: name,
	})
}

func reconcileAgents(c incus.InstanceServer, conf Config, agentsToCreate chan<- int) error {
	var (
		expectedInstances uint64 = math.MaxUint64 >> (63 - conf.AgentCount)
		instancesFound    uint64 = 0
	)

	mu.Lock()
	defer mu.Unlock()

	instances, err := c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return fmt.Errorf("unable to list instances in project %q: %w",
			conf.ProjectName,
			err,
		)
	}

	for _, i := range instances {
		if i.Name == conf.BaseImage {
			continue
		}

		matches := agentRe.FindStringSubmatch(i.Name)
		if matches == nil {
			// todo: delete the agent if it's invalid
			return fmt.Errorf("invalid agent name %q", i.Name)

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

func createAgent(ctx context.Context, c incus.InstanceServer, conf Config, idx int) error {

	// Create lxd container
	op, err := c.CreateInstance(api.InstancesPost{
		Name: conf.AgentName(idx),
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Source: conf.BaseImage,
			Type:   "copy",
		},
		Start: true,
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"environment.testing1234": "thisworks",
			},
			Ephemeral: true,
		},
	})
	if err != nil {
		return fmt.Errorf("incus create error: %w", err)
	}

	if err = op.WaitContext(ctx); err != nil {
		return fmt.Errorf("incus create error: %w", err)
	}

	return nil
}

type Config struct {
	ProjectName string `validate:"required"`
	AgentCount  int    `validate:"min=1,max=64"`
	BaseImage   string `validate:"required"`
}

func (c Config) AgentName(idx int) string {
	return fmt.Sprintf("%s-%d", defaultAgentPrefix, idx)
}
