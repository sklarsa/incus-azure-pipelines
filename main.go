package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"regexp"
	"sync"
	"syscall"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

const (
	defaultAgentPrefix  = "azp-agent"
	defaultBaseInstance = "azp-agent-base"
	agentUser           = "agent"
)

var (
	agentRe        = regexp.MustCompile("^" + defaultAgentPrefix + `-(\d{1,2})$`)
	agentsToCreate = make(chan int)
)

func main() {

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	provision := flag.Bool("provision", false, "provision the base instance and exit")
	logs := flag.Int("logs", -1, "get agent logs by index (0-base)")
	configPath := flag.String("config", "./config.yaml", "path of config file")

	flag.Parse()

	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatalf("error reading config at %s: %s", *configPath, err.Error())
	}

	conf, err := parseConfig(data)
	if err != nil {
		log.Fatalf("error parsing config at %s: %s", *configPath, err.Error())
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

	if *provision {
		fmt.Printf("provisioning base instance %q\n", conf.BaseImage)
		if err := provisionBaseInstance(ctx, c, conf); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *logs > -1 {

		op, err := c.ExecInstance(
			agentName(*logs),
			api.InstanceExecPost{
				Command:     []string{"cat", "/home/agent/azp-agent.log"},
				WaitForWS:   true,
				Interactive: false,
			}, &incus.InstanceExecArgs{
				Stdout: os.Stdout,
			},
		)

		if err != nil {
			log.Fatal(err)
		}

		if err = op.Wait(); err != nil {
			log.Fatal(err)
		}
		return
	}

	wg := &sync.WaitGroup{}

	wg.Go(func() {
		for {
			idx, open := <-agentsToCreate
			if !open {
				return
			}

			fmt.Printf("Creating agent %d\n", idx)

			if err := createAgent(ctx, c, conf, idx); err != nil {
				slog.Error("failed to create agent", "idx", idx, "err", err)
			}
		}
	})

	wg.Go(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				close(agentsToCreate)
				return
			case <-ticker.C:
				if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
					slog.Error("reconcile failed", "err", err)
				}
			}
		}
	})

	wg.Wait()

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
