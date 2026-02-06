package cmd

import (
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
		},
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
