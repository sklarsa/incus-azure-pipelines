package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/sklarsa/incus-azure-pipelines/pool"
)

const (
	// Don't consider instances younger than this for reaping
	startupGracePeriod = 2 * time.Minute

	// How often the reaper checks for stale instances
	reaperInterval = 30 * time.Second
)

func runReaper(ctx context.Context, p *pool.Pool, conf Config) {
	slog.Info("starting goroutine", "type", "reaper")

	ticker := time.NewTicker(reaperInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("exiting goroutine", "type", "reaper")
			return
		case <-ticker.C:
			if err := p.Reap(ctx); err != nil {
				slog.Error("reaper error", "err", err)
			}
		}
	}
}
