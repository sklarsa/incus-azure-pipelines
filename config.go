package main

import (
	"github.com/go-playground/validator/v10"
	"github.com/goccy/go-yaml"
)

type Config struct {
	// ProjectName is the name of the incus project used for Azure Pipelines Agent runners
	ProjectName string `json:"projectName" validate:"required"`
	// AgentCount is the number of agents to run on this node
	AgentCount int `json:"agentCount" validate:"min=1,max=64"`
	// BaseImage is the incus image used to run the Azure Pipelines Agent
	BaseImage string `json:"baseImage" validate:"required"`
	// MaxCores specifies the max number of cores that each agent can use. Used to set limits.cpu.allowance
	// for percentage-based soft limits
	MaxCores int `json:"maxCores" validate:"min=0"`
	// MaxRamInGb specifies the max amount of RAM that each agent can use
	MaxRamInGb int `json:"maxRamInGb" validate:"min=0"`
	// TmpfsSizeInGb specifies the maximum size of the tmpfs directory mounted to /tmp in each agent container
	TmpfsSizeInGb int `json:"tmpfsSizeInGb" validate:"min=0"`
	// Azure specific settings
	Azure AzureConfig `json:"azure" validate:"required"`
	// ProvisionScripts is a list of local file paths containing scripts that are run after the initial
	// base image provisioning. This allows users to customize their agent environments.
	ProvisionScripts []string `json:"provisionScripts" validate:"dive,filepath"`
}

type AzureConfig struct {
	// PAT is an Azure Devops personal access token used for registering the agents
	PAT string `json:"pat" validate:"required"`
	// Pool is the pool name for the agent to join
	Pool string `json:"pool" validate:"required"`
	// Url of the server. For example: https://dev.azure.com/myorganization or http://my-azure-devops-server:8080/tfs
	Url string `json:"url" validate:"required,url"`
}

func parseConfig(data []byte) (Config, error) {
	config := Config{}

	if err := yaml.Unmarshal(data, &config); err != nil {
		return config, err
	}

	v := validator.New(validator.WithRequiredStructEnabled())

	return config, v.Struct(config)

}
