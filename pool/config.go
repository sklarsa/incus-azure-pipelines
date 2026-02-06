package pool

import "time"

type Config struct {
	// ProjectName is the name of the incus project used for Azure Pipelines Agent runners
	ProjectName string `json:"projectName" validate:"required"`
	// AgentCount is the number of agents to run on this node
	AgentCount int `json:"agentCount,omitempty" validate:"min=1,max=64"`
	// MaxCores specifies the max number of cores that each agent can use. Used to set limits.cpu.allowance
	// for percentage-based soft limits
	MaxCores int `json:"maxCores,omitempty" validate:"min=0"`
	// MaxRamInGb specifies the max amount of RAM that each agent can use
	MaxRamInGb int `json:"maxRamInGb,omitempty" validate:"min=0"`
	// TmpfsSizeInGb specifies the maximum size of the tmpfs directory mounted to /tmp in each agent container
	TmpfsSizeInGb int `json:"tmpfsSizeInGb,omitempty" validate:"min=0"`
	// Azure specific settings
	Azure AzureConfig `json:"azure" validate:"required"`
	// NamePrefix is the prefix used for naming agent containers
	NamePrefix string `json:"namePrefix" validate:"required,hostname"`
	// Image is the Incus image alias to use for agent containers
	Image string `json:"image" validate:"required"`
	// StartupGracePeriod is how long to wait before considering an agent stale
	StartupGracePeriod time.Duration `json:"startupGracePeriod,omitempty"`
}

type AzureConfig struct {
	// PAT is an Azure Devops personal access token used for registering the agents
	PAT string `json:"pat" validate:"required"`
	// Pool is the pool name for the agent to join
	Pool string `json:"pool" validate:"required"`
	// Url of the server. For example: https://dev.azure.com/myorganization or http://my-azure-devops-server:8080/tfs
	Url string `json:"url" validate:"required,url"`
}
