package pool

import (
	"fmt"
	"time"

	"github.com/zalando/go-keyring"
)

// KeyringService is the service name used for storing PATs in the system keyring.
const KeyringService = "incus-azure-pipelines"

type Config struct {
	// AgentCount is the number of agents to run on this node
	AgentCount int `json:"agentCount" validate:"required,min=1,max=64"`
	// AgentPrefix overrides the hostname used for Azure agent naming.
	// If not set, defaults to the machine's hostname.
	// Must be unique per host to avoid agent name collisions.
	AgentPrefix string `json:"agentPrefix,omitempty" validate:"omitempty,hostname"`
	// Azure specific settings
	Azure AzureConfig `json:"azure" validate:"required"`
	// Incus specific settings
	Incus IncusConfig `json:"incus" validate:"required"`
	// Name is the name of the Azure Devops pool to run agents for.
	// This is also used to name running containers.
	Name string `json:"name" validate:"required,hostname"`
}

type IncusConfig struct {
	// MaxCores specifies the max number of cores that each agent can use. Used to set limits.cpu.allowance
	// for percentage-based soft limits
	MaxCores int `json:"maxCores,omitempty" validate:"min=0"`
	// MaxRamInGb specifies the max amount of RAM that each agent can use
	MaxRamInGb int `json:"maxRamInGb,omitempty" validate:"min=0"`
	// TmpfsSizeInGb specifies the maximum size of the tmpfs directory mounted to /tmp in each agent container
	TmpfsSizeInGb int `json:"tmpfsSizeInGb,omitempty" validate:"min=0"`
	// ProjectName is the name of the incus project used for Azure Pipelines Agent runners
	ProjectName string `json:"projectName,omitempty"`
	// Image is the Incus image alias to use for agent containers
	Image string `json:"image" validate:"required"`
	// StartupGracePeriod is how long to wait before considering an agent stale
	StartupGracePeriod time.Duration `json:"startupGracePeriod,omitempty"`
}

type AzureConfig struct {
	// PAT is an Azure Devops personal access token used for registering the agents.
	// If omitted, the PAT is read from the system keyring (see "set-token" command).
	PAT string `json:"pat,omitempty"`
	// Url of the server. For example: https://dev.azure.com/myorganization or http://my-azure-devops-server:8080/tfs
	Url string `json:"url" validate:"required,url"`
}

// ResolvePAT returns the PAT from the config file, falling back to the system keyring.
func (a AzureConfig) ResolvePAT(poolName string) (string, error) {
	if a.PAT != "" {
		return a.PAT, nil
	}
	secret, err := keyring.Get(KeyringService, poolName)
	if err != nil {
		return "", fmt.Errorf("no PAT found for pool %q: set azure.pat in config or run 'incus-azure-pipelines set-token %s'", poolName, poolName)
	}
	return secret, nil
}
