package cmd

import (
	"fmt"

	"github.com/creasty/defaults"
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
	MetricsPort int `json:"metricsPort,omitempty" validate:"min=0" default:"9922"`
	// Daemon contains settings for the background daemon processes.
	Daemon daemon.Config `json:"daemon,omitempty"`
}

func parseConfig(data []byte) (CLIConfig, error) {
	config := CLIConfig{}
	if err := defaults.Set(&config); err != nil {
		return config, fmt.Errorf("error setting defaults: %w", err)
	}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
