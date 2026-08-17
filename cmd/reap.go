package cmd

import (
	"context"
	"fmt"
	"strconv"

	incus "github.com/lxc/incus/v6/client"
	"github.com/sklarsa/incus-azure-pipelines/pool"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(reapCmd)
}

var reapCmd = &cobra.Command{
	Use:   "reap <pool> <agent-index>",
	Short: "force-stop and replace one ephemeral agent instance",
	Long: "Force-stop one ephemeral agent instance. This is destructive and can " +
		"terminate a running job. The daemon reconciler creates a replacement.",
	Args:    cobra.ExactArgs(2),
	PreRunE: loadConfig,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runReap(ctx, c, conf, configPath, args)
	},
}

func runReap(ctx context.Context, server incus.InstanceServer, config CLIConfig, configPath string, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("expected pool and agent index")
	}

	poolName := args[0]
	idx, err := strconv.Atoi(args[1])
	if err != nil {
		return fmt.Errorf("invalid agent index %q: %w", args[1], err)
	}
	if idx < 0 {
		return fmt.Errorf("invalid agent index %d: must be non-negative", idx)
	}

	for _, cfg := range config.Pools {
		if cfg.Name != poolName {
			continue
		}
		if idx >= cfg.AgentCount {
			return fmt.Errorf("invalid agent index %d, pool %q has %d agents", idx, poolName, cfg.AgentCount)
		}

		p, err := pool.NewPool(server, cfg)
		if err != nil {
			return err
		}
		return p.ReapAgent(ctx, idx)
	}

	return fmt.Errorf("pool not found %q in %s", poolName, configPath)
}
