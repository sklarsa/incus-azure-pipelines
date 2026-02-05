package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	incus "github.com/lxc/incus/v6/client"
	"github.com/spf13/cobra"
)

var (
	ctx, cancel = context.WithCancel(context.Background())

	configPath string
	conf       cliConfig
	c          incus.InstanceServer
)

func init() {
	rootCmd.PersistentFlags().StringVar(&configPath, "config", "./config.yaml", "path to config file")
}

var rootCmd = &cobra.Command{
	Use:           "incus-azure-pipelines",
	Short:         "Run Azure Pipelines Agents powered by Incus",
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		go func() {
			<-sigCh
			cancel()
		}()

		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("error reading config at %s: %w", configPath, err)
		}

		conf, err = parseConfig(data)
		if err != nil {
			return fmt.Errorf("error parsing config at %s: %w", configPath, err)
		}

		c, err = incus.ConnectIncusUnix("", nil)
		if err != nil {
			return err
		}
		slog.Info("connected to local incus daemon")

		return err

	},
	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		c.Disconnect()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
