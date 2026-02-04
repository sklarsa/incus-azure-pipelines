package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validConfigYAML() []byte {
	return []byte(`
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
maxCores: 8
maxRamInGb: 4
tmpfsSizeInGb: 12
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
  pool: default
`)
}

func TestParseConfig_Valid(t *testing.T) {
	cfg, err := parseConfig(validConfigYAML())
	require.NoError(t, err)

	assert.Equal(t, "azure-pipelines", cfg.ProjectName)
	assert.Equal(t, 2, cfg.AgentCount)
	assert.Equal(t, "ubuntu/24.04", cfg.BaseImage)
	assert.Equal(t, 8, cfg.MaxCores)
	assert.Equal(t, 4, cfg.MaxRamInGb)
	assert.Equal(t, 12, cfg.TmpfsSizeInGb)
	assert.Equal(t, "test-token", cfg.Azure.PAT)
	assert.Equal(t, "https://dev.azure.com/myorg", cfg.Azure.Url)
	assert.Equal(t, "default", cfg.Azure.Pool)
	assert.Zero(t, cfg.MetricsPort)
}

func TestParseConfig_MissingProjectName(t *testing.T) {
	data := []byte(`
agentCount: 2
baseImage: ubuntu/24.04
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
  pool: default
`)
	_, err := parseConfig(data)
	assert.Error(t, err)
}

func TestParseConfig_MissingAzure(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
`)
	_, err := parseConfig(data)
	assert.Error(t, err)
}

func TestParseConfig_MissingAzurePAT(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
azure:
  url: https://dev.azure.com/myorg
  pool: default
`)
	_, err := parseConfig(data)
	assert.Error(t, err)
}

func TestParseConfig_MissingAzurePool(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
`)
	_, err := parseConfig(data)
	assert.Error(t, err)
}

func TestParseConfig_InvalidAzureUrl(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 2
baseImage: ubuntu/24.04
azure:
  pat: test-token
  url: not-a-url
  pool: default
`)
	_, err := parseConfig(data)
	assert.Error(t, err)
}

func TestParseConfig_AgentCountBoundaries(t *testing.T) {
	tests := []struct {
		name       string
		agentCount int
		wantErr    bool
	}{
		{"min valid", 1, false},
		{"max valid", 64, false},
		{"below min", 0, true},
		{"above max", 65, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := []byte(`
projectName: azure-pipelines
agentCount: ` + fmt.Sprintf("%d", tt.agentCount) + `
baseImage: ubuntu/24.04
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
  pool: default
`)
			_, err := parseConfig(data)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	_, err := parseConfig([]byte(`{{{not valid yaml`))
	assert.Error(t, err)
}

func TestParseConfig_ProvisionScripts(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 1
baseImage: ubuntu/24.04
provisionScripts:
  - /tmp/setup.sh
  - /opt/scripts/install.sh
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
  pool: default
`)
	cfg, err := parseConfig(data)
	require.NoError(t, err)
	assert.Equal(t, []string{"/tmp/setup.sh", "/opt/scripts/install.sh"}, cfg.ProvisionScripts)
}

func TestParseConfig_MetricsPort(t *testing.T) {
	data := []byte(`
projectName: azure-pipelines
agentCount: 1
baseImage: ubuntu/24.04
metricsPort: 9922
azure:
  pat: test-token
  url: https://dev.azure.com/myorg
  pool: default
`)
	cfg, err := parseConfig(data)
	require.NoError(t, err)
	assert.Equal(t, 9922, cfg.MetricsPort)
}
