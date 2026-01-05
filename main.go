package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	defaultAgentPrefix = "azp-agent"
	defaultImageAlias  = "azp-agent"
	agentUser          = "agent"
	agentUid           = 1100
	agentGid           = 1100
	defaultMetricsPort = 9922
)

var (
	agentsToCreate = make(chan int)

	inFlight = &sync.Map{}
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	var (
		runFlag    bool
		provision  bool
		logs       int
		configPath string
	)

	flag.BoolVar(&runFlag, "run", false, "run the orchestrator daemon")
	flag.BoolVar(&provision, "provision", false, "provision the base instance and exit")
	flag.IntVar(&logs, "logs", -1, "get agent logs by index (base 0)")
	flag.StringVar(&configPath, "config", "./config.yaml", "path of config file")

	flag.Parse()

	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config at %s: %w", configPath, err)
	}

	conf, err := parseConfig(data)
	if err != nil {
		return fmt.Errorf("error parsing config at %s: %w", configPath, err)
	}

	c, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		return err
	}
	defer c.Disconnect()

	listener, err := c.GetEvents()
	if err != nil {
		return fmt.Errorf("error setting up incus event listener: %w", err)
	}

	h, err := listener.AddHandler(nil, func(e api.Event) {

		meta := map[string]any{}
		if err := json.Unmarshal(e.Metadata, &meta); err != nil {
			slog.Error("error unmarshaling event", "err", err, "meta", meta)
			return
		}

		if meta["level"] == "info" &&
			meta["message"] == "Deleted instance" {

			context, ok := meta["context"].(map[string]any)
			if !ok {
				slog.Warn("unexpected event format, no 'context' map found", "data", e)
				return
			}

			project, ok := context["project"].(string)
			if !ok {
				slog.Error("unexpected event format, context.project is not a string", "data", e)
				return
			}

			if project != conf.ProjectName {
				return
			}

			instance, ok := context["instance"].(string)
			if !ok {
				slog.Error("unexpected event format, context.instance is not a string", "data", e)
				return
			}

			matches := agentRe.FindStringSubmatch(instance)
			if len(matches) >= 1 {
				slog.Info("container deleted", "name", instance, "project", project)
				idx, err := strconv.Atoi(matches[1])
				if err != nil {
					slog.Error("agent name should end in an integer, something went wrong", "name", instance)
					return
				}
				agentsToCreate <- idx
			}
		}

	})

	if err != nil {
		slog.Error("error adding incus event handler", "err", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh

		if err := listener.RemoveHandler(h); err != nil {
			slog.Error("error removing event handler", "err", err)
		}
		listener.Disconnect()

		cancel()

	}()

	c = c.UseProject(conf.ProjectName)

	if provision {
		fmt.Printf("provisioning base instance %q\n", conf.BaseImage)
		return provisionBaseInstance(ctx, c, conf)
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
			return err
		}
		return op.Wait()
	}

	if runFlag {
		wg := &sync.WaitGroup{}

		wg.Go(func() {
			slog.Info("starting goroutine", "type", "agent-builder")

			for {
				idx, open := <-agentsToCreate
				if !open {
					slog.Info("exiting goroutine", "type", "agent-builder")
					return
				}

				if _, exists := inFlight.LoadOrStore(idx, true); !exists {
					go func() {
						defer inFlight.Delete(idx)

						slog.Info("creating agent", "idx", idx)

						if err := createAgent(ctx, c, conf, idx); err != nil {
							slog.Error("failed to create agent", "idx", idx, "err", err)
							agentsCreatedErrorMetric.Add(1)
							return
						}

						agentsCreatedMetric.Add(1)
					}()
				}
			}
		})

		wg.Go(func() {
			slog.Info("starting goroutine", "type", "reconciler")

			if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
				slog.Error("reconcile failed", "err", err)
			}

			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-ctx.Done():
					close(agentsToCreate)
					slog.Info("exiting goroutine", "type", "reconciler")
					return
				case <-ticker.C:
					if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
						slog.Error("reconcile failed", "err", err)
					}
				}
			}
		})

		wg.Go(func() {
			slog.Info("starting goroutine", "type", "event-listener")
			if err := listener.Wait(); err != nil {
				slog.Error("event listener error", "err", err)
			}
			slog.Info("exiting goroutine", "type", "event-listener")
		})

		wg.Go(func() {
			runReaper(ctx, c, conf)
		})

		wg.Go(func() {
			agentUptime := newAgentUptimeCollector(c)
			prometheus.MustRegister(agentUptime)

			slog.Info("starting goroutine", "type", "metrics-server")
			if conf.MetricsPort == 0 {
				conf.MetricsPort = defaultMetricsPort
			}

			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())

			server := &http.Server{
				Addr:    fmt.Sprintf(":%d", conf.MetricsPort),
				Handler: mux,
			}

			go func() {
				<-ctx.Done()
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := server.Shutdown(shutdownCtx); err != nil {
					slog.Error("error shutting down metrics server", "err", err)
				}
			}()

			slog.Info("binding metrics-server", "port", conf.MetricsPort)
			if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("metrics server error", "error", err)
			}

			slog.Info("exiting goroutine", "type", "metrics-server")
		})

		wg.Wait()
		return nil
	}

	flag.PrintDefaults()
	return fmt.Errorf("no command specified")
}
