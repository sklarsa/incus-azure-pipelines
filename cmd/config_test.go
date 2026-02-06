package cmd

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConfig_ValidSinglePool(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 2
    azure:
      pat: "test-token"
      url: "https://dev.azure.com/myorg"
    incus:
      image: "ubuntu-agent"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, config.Pools, 1)
	assert.Equal(t, "my-pool", config.Pools[0].Name)
	assert.Equal(t, 2, config.Pools[0].AgentCount)
	assert.Equal(t, "test-token", config.Pools[0].Azure.PAT)
	assert.Equal(t, "ubuntu-agent", config.Pools[0].Incus.Image)
}

func TestParseConfig_ValidMultiplePools(t *testing.T) {
	yaml := `
pools:
  - name: pool-one
    agentCount: 2
    azure:
      pat: "token-1"
      url: "https://dev.azure.com/org1"
    incus:
      image: "image-1"
  - name: pool-two
    agentCount: 4
    azure:
      pat: "token-2"
      url: "https://dev.azure.com/org2"
    incus:
      image: "image-2"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	require.Len(t, config.Pools, 2)
	assert.Equal(t, "pool-one", config.Pools[0].Name)
	assert.Equal(t, "pool-two", config.Pools[1].Name)
}

func TestParseConfig_DefaultMetricsPort(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, 9922, config.MetricsPort)
}

func TestParseConfig_CustomMetricsPort(t *testing.T) {
	yaml := `
metricsPort: 8080
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, 8080, config.MetricsPort)
}

func TestParseConfig_DefaultDaemonIntervals(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, 5*time.Second, config.Daemon.ReconcileInterval)
	assert.Equal(t, 30*time.Second, config.Daemon.ReaperInterval)
}

func TestParseConfig_CustomDaemonIntervals(t *testing.T) {
	yaml := `
daemon:
  reconcileInterval: 10s
  reaperInterval: 60s
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, 10*time.Second, config.Daemon.ReconcileInterval)
	assert.Equal(t, 60*time.Second, config.Daemon.ReaperInterval)
}

func TestParseConfig_InvalidYAML(t *testing.T) {
	yaml := `
pools:
  - name: [invalid yaml
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_MissingRequiredPoolName(t *testing.T) {
	yaml := `
pools:
  - agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_MissingRequiredAzurePAT(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_MissingRequiredIncusImage(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus: {}
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_AgentCountTooLow(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 0
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_AgentCountTooHigh(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 100
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_DuplicatePoolNames(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
  - name: my-pool
    agentCount: 2
    azure:
      pat: "token2"
      url: "https://dev.azure.com/org2"
    incus:
      image: "img2"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_InvalidPoolName(t *testing.T) {
	yaml := `
pools:
  - name: "invalid pool name with spaces"
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_NegativeMetricsPort(t *testing.T) {
	yaml := `
metricsPort: -1
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	_, err := parseConfig([]byte(yaml))
	assert.Error(t, err)
}

func TestParseConfig_OptionalIncusSettings(t *testing.T) {
	yaml := `
pools:
  - name: my-pool
    agentCount: 2
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
      maxCores: 4
      maxRamInGb: 8
      tmpfsSizeInGb: 2
      projectName: "my-project"
      startupGracePeriod: 60s
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	assert.Equal(t, 4, config.Pools[0].Incus.MaxCores)
	assert.Equal(t, 8, config.Pools[0].Incus.MaxRamInGb)
	assert.Equal(t, 2, config.Pools[0].Incus.TmpfsSizeInGb)
	assert.Equal(t, "my-project", config.Pools[0].Incus.ProjectName)
	assert.Equal(t, 60*time.Second, config.Pools[0].Incus.StartupGracePeriod)
}

func TestParseConfig_EmptyInput(t *testing.T) {
	// Empty input resets defaults due to YAML unmarshalling behavior
	config, err := parseConfig([]byte(""))
	require.NoError(t, err)
	assert.Empty(t, config.Pools)
	// With empty input, defaults get overwritten by zero values
	assert.Equal(t, 0, config.MetricsPort)
}

func TestParseConfig_PartialConfig_PreservesDefaults(t *testing.T) {
	// When only some fields are provided, defaults for other fields are preserved
	yaml := `
pools:
  - name: my-pool
    agentCount: 1
    azure:
      pat: "token"
      url: "https://dev.azure.com/org"
    incus:
      image: "img"
`
	config, err := parseConfig([]byte(yaml))
	require.NoError(t, err)
	// Defaults should still be set for fields not in YAML
	assert.Equal(t, 9922, config.MetricsPort)
	assert.Equal(t, 5*time.Second, config.Daemon.ReconcileInterval)
	assert.Equal(t, 30*time.Second, config.Daemon.ReaperInterval)
}
