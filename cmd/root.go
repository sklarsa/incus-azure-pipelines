package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	incus "github.com/lxc/incus/v6/client"
	"github.com/spf13/cobra"
)

var (
	ctx, cancel = context.WithCancel(context.Background())

	configPath string
	conf       CLIConfig
	c          incus.InstanceServer
	logLevel   string
)

func init() {

	rootCmd.PersistentFlags().StringVar(&configPath, "config", "", "path to config file")
	rootCmd.PersistentFlags().StringVarP(&logLevel, "log-level", "l", "info", "log level (debug, info, warn, error)")
}

var rootCmd = &cobra.Command{
	Use:           "incus-azure-pipelines",
	Short:         "Run Azure Pipelines Agents powered by Incus",
	SilenceErrors: true,
	SilenceUsage:  true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if configPath == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("cannot determine home directory, use --config: %w", err)
			}
			configPath = filepath.Join(home, ".incus-azure-pipelines", "config.yaml")
		}

		// Init logging
		var level slog.Level
		if err := level.UnmarshalText([]byte(logLevel)); err != nil {
			fmt.Fprintf(os.Stderr, "invalid log level %q: %v\n", logLevel, err)
			os.Exit(1)
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level: level,
		})))

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
		if c != nil {
			c.Disconnect()
		}

	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
