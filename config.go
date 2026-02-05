package main

import (
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
	"github.com/sklarsa/incus-azure-pipelines/pool"
)

type Config struct {
	Pools []pool.Config
	// MetricsPort is the port number that servers prometheus metrics
	MetricsPort int `json:"metricsPort" validate:"min=0"`
}

func parseConfig(data []byte) (Config, error) {
	config := Config{}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
