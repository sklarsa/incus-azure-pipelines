package main

import (
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
)

type Config struct {
	ProjectName      string      `json:"projectName" validate:"required"`
	AgentCount       int         `json:"agentCount" validate:"min=1,max=64"`
	BaseImage        string      `json:"baseImage" validate:"required"`
	MaxCores         int         `json:"maxCores" validate:"min=0"`
	MaxRamInGb       int         `json:"maxRamInGb" validate:"min=0"`
	TmpfsSizeInGb    int         `json:"tmpfsSizeInGb" validate:"min=0"`
	Azure            AzureConfig `json:"azure" validate:"required"`
	ProvisionScripts []string    `json:"provisionScripts" validate:"dive,filepath"`
}

type AzureConfig struct {
	PAT  string `json:"pat" validate:"required"`
	Pool string `json:"pool" validate:"required"`
	Url  string `json:"url" validate:"required,url"`
}

func parseConfig(data []byte) (Config, error) {
	config := Config{}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
