package daemon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/sklarsa/incus-azure-pipelines/pool"
)

const (
	defaultReconcilePeriod = 5 * time.Second
)

type Config struct {
	ReaperInterval    time.Duration `json:"reaperInterval"`
	ReconcileInterval time.Duration `json:"reconcileInterval"`
}

func Run(ctx context.Context, p *pool.Pool, conf Config) {
	wg := &sync.WaitGroup{}
	agentsToCreate := make(chan int)

	logger := slog.With("pool", p.Name())

	wg.Go(func() {
		logger.Info("starting goroutine", "type", "agent-builder")
		for {
			idx, open := <-agentsToCreate
			if !open {
				logger.Info("exiting goroutine", "type", "agent-builder")
				return
			}

			go func() {
				logger.Info("creating agent", "idx", idx)
				if err := p.CreateAgent(ctx, idx); err != nil {
					logger.Error("failed to create agent", "idx", idx, "err", err)
					return
				}
			}()
		}
	})

	wg.Go(func() {
		logger.Info("starting goroutine", "type", "reconciler")

		// Reconcile immediate upon launch
		if err := p.Reconcile(agentsToCreate); err != nil {
			logger.Error("reconcile failed", "err", err)
		}

		// Then wait for next reconcile trigger until
		// the agentsToCreate chan is closed
		if conf.ReconcileInterval == 0 {
			conf.ReconcileInterval = defaultReconcilePeriod
		}
		ticker := time.NewTicker(conf.ReconcileInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				close(agentsToCreate)
				logger.Info("exiting goroutine", "type", "reconciler")
				return
			case <-ticker.C:
				if err := p.Reconcile(agentsToCreate); err != nil {
					logger.Error("reconcile failed", "err", err)
				}
			}
		}
	})

	wg.Go(func() {
		logger.Info("starting goroutine", "type", "event-listener")
		defer logger.Info("exiting goroutine", "type", "event-listener")

		l, err := pool.NewListener(p, agentsToCreate)
		if err != nil {
			logger.Error("error starting up listener", "err", err)
			return
		}
		defer l.Close()

		<-ctx.Done()

	})

	wg.Go(func() {
		logger.Info("starting goroutine", "type", "reaper")

		ticker := time.NewTicker(conf.ReaperInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				logger.Info("exiting goroutine", "type", "reaper")
				return
			case <-ticker.C:
				if err := p.Reap(ctx); err != nil {
					logger.Error("reaper error", "err", err)
				}
			}
		}
	})

	wg.Wait()
}
