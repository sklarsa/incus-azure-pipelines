package cmd

import (
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
	"github.com/sklarsa/incus-azure-pipelines/daemon"
	"github.com/sklarsa/incus-azure-pipelines/pool"
)

// CLIConfig is the top-level configuration for the daemon.
type CLIConfig struct {
	// Pools is the list of agent pools to manage.
	Pools []pool.Config `json:"pools,omitempty" validate:"unique=Name,dive"`
	// MetricsPort is the port number that serves Prometheus metrics. Default: 9922
	MetricsPort int `json:"metricsPort,omitempty" validate:"min=0"`
	// Daemon contains settings for the background daemon processes.
	Daemon daemon.Config `json:"daemon,omitempty"`
}

func parseConfig(data []byte) (CLIConfig, error) {
	config := CLIConfig{
		MetricsPort: 9922,
		Daemon: daemon.Config{
			ReconcileInterval: 5 * time.Second,
			ReaperInterval:    30 * time.Second,
			Listener: daemon.ListenerConfig{
				RetryDelay:    1 * time.Second,
				MaxRetryDelay: 1 * time.Minute,
			},
		},
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())
	if err := v.Struct(config); err != nil {
		return config, err
	}

	for _, p := range config.Pools {
		if _, err := p.Azure.ResolvePAT(p.Name); err != nil {
			return config, fmt.Errorf("pool %q: %w", p.Name, err)
		}
	}

	return config, nil
}
