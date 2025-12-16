package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

const (
	defaultAgentPrefix = "azp-agent"
	defaultImageAlias  = "azp-agent"
	agentUser          = "agent"
)

var (
	agentsToCreate = make(chan int)

	inFlight = &sync.Map{}
)

func main() {

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		cancel()
	}()

	var (
		run        bool
		provision  bool
		logs       int
		configPath string
	)

	flag.BoolVar(&run, "run", false, "run the orchestrator daemon")
	flag.BoolVar(&provision, "provision", false, "provision the base instance and exit")
	flag.IntVar(&logs, "logs", -1, "get agent logs by index (base 0)")
	flag.StringVar(&configPath, "config", "./config.yaml", "path of config file")

	flag.Parse()

	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("error reading config at %s: %s", configPath, err.Error())
	}

	conf, err := parseConfig(data)
	if err != nil {
		log.Fatalf("error parsing config at %s: %s", configPath, err.Error())
	}

	c, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Disconnect()

	c = c.UseProject(conf.ProjectName)

	if provision {
		fmt.Printf("provisioning base instance %q\n", conf.BaseImage)
		if err := provisionBaseInstance(ctx, c, conf); err != nil {
			log.Fatal(err)
		}
		return
	}

	if logs > -1 {

		op, err := c.ExecInstance(
			agentName(logs),
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

	if run {

		wg := &sync.WaitGroup{}

		wg.Go(func() {

			for {
				idx, open := <-agentsToCreate
				if !open {
					return
				}

				fmt.Printf("Creating agent %d\n", idx)

				go func() {
					defer inFlight.Delete(idx)

					if err := createAgent(ctx, c, conf, idx); err != nil {
						slog.Error("failed to create agent", "idx", idx, "err", err)
					}
				}()

			}
		})

		wg.Go(func() {
			if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
				slog.Error("reconcile failed", "err", err)
			}

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

	flag.PrintDefaults()
	os.Exit(-1)

}
