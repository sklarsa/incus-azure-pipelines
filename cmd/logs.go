package cmd

import (
	"fmt"
	"os"
	"strconv"

	"github.com/sklarsa/incus-azure-pipelines/pool"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs <pool> <agent-index>",
	Short: "output logs for a given agent",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		poolName := args[0]
		idxStr := args[1]

		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			return fmt.Errorf("invalid agent index %q: %w", idxStr, err)
		}

		for _, cfg := range conf.Pools {
			if cfg.Name == poolName {
				if idx >= cfg.AgentCount {
					return fmt.Errorf("invalid agent index %d, pool %q has %d agents", idx, poolName, cfg.AgentCount)
				}

				p, err := pool.NewPool(c, cfg)
				if err != nil {
					return err
				}

				return p.AgentLogs(idx, os.Stdout)
			}

		}

		return fmt.Errorf("pool not found %q in %s", poolName, configPath)

	},
}
