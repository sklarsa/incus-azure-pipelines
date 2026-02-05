package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sklarsa/incus-azure-pipelines/daemon"
	"github.com/sklarsa/incus-azure-pipelines/pool"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(runCmd)
}

var runCmd = &cobra.Command{
	Use: "run",
	Run: func(cmd *cobra.Command, args []string) {
		wg := &sync.WaitGroup{}

		for _, cfg := range conf.Pools {
			wg.Go(func() {
				p, err := pool.NewPool(c, cfg)
				if err != nil {
					slog.Error("error initializing agent pool", "err", err, "pool", cfg.NamePrefix)
					return
				}

				daemon.Run(ctx, p, conf.Daemon)
			})
		}

		wg.Go(func() {
			slog.Info("starting goroutine", "type", "metrics-server")

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
	},
}
