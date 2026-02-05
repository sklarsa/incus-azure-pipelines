package cmd

import (
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
	"github.com/sklarsa/incus-azure-pipelines/daemon"
	"github.com/sklarsa/incus-azure-pipelines/pool"
)

type cliConfig struct {

	// ProjectName is the name of the incus project used for Azure Pipelines Agent runners
	ProjectName string `json:"projectName" validate:"required"`
	Pools       []pool.Config
	// MetricsPort is the port number that servers prometheus metrics
	MetricsPort int `json:"metricsPort" validate:"min=0"`
	Daemon      daemon.Config
}

func parseConfig(data []byte) (cliConfig, error) {
	config := cliConfig{}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
