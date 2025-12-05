package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

//go:embed run_agent.sh
var runAgentScript string

func provisionBaseInstance(ctx context.Context, c incus.InstanceServer, conf Config) error {
	i, etag, err := c.GetInstance(defaultBaseInstance)

	// Return on non-404 errors
	if err != nil && !api.StatusErrorCheck(err, http.StatusNotFound) {
		return err
	}

	// Delete base instance if it is found
	// todo: actually just do this build in a tmp container or something and copy it
	if err == nil {
		// assumes base instance is already stopped
		// todo: stop instance if it's not already
		op, err := c.DeleteInstance(i.Name)
		if err != nil {
			return err
		}

		if err = op.WaitContext(ctx); err != nil {
			return err
		}
	}

	req := api.InstancesPost{
		Name: defaultBaseInstance,
		Source: api.InstanceSource{
			Type:     "image",
			Alias:    conf.BaseImage,
			Server:   "https://images.linuxcontainers.org",
			Protocol: "simplestreams",
		},
		Type:  "container",
		Start: true,
	}
	op, err := c.CreateInstance(req)
	if err != nil {
		return err
	}

	if err = op.WaitContext(ctx); err != nil {
		return err
	}

	execReq := api.InstanceExecPost{
		Command:     []string{"bash"},
		WaitForWS:   true,
		Interactive: false,
	}

	agentURL, err := getAgentDownloadURL()
	if err != nil {
		return err
	}

	script := `
set -euo pipefail
AGENT_URL="` + agentURL + `"
AGENT_USER="` + agentUser + `"
AGENT_HOME="/home/${AGENT_USER}"

apt-get update
apt-get install -y curl wget tar sudo

groupadd --gid 1100 "${AGENT_USER}"
useradd -m -s /bin/bash --uid 1100 --gid 1100 "${AGENT_USER}"
echo "${AGENT_USER} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${AGENT_USER}
chmod 440 /etc/sudoers.d/${AGENT_USER}

# Add Docker repo
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc

echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list

# Install
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# Add agent to docker group
usermod -aG docker agent

su - "${AGENT_USER}" -c "
  cd ${AGENT_HOME}
  curl -fsSL -o agent.tar.gz ${AGENT_URL}
  tar -xzf agent.tar.gz
  rm agent.tar.gz
"
`
	args := &incus.InstanceExecArgs{
		Stdin:  strings.NewReader(script),
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}

	op, err = c.ExecInstance(req.Name, execReq, args)
	if err != nil {
		return err
	}

	if err := op.WaitContext(ctx); err != nil {
		return err
	}

	if err := c.CreateInstanceFile(
		req.Name,
		"/home/agent/run_agent.sh",
		incus.InstanceFileArgs{
			Content:   strings.NewReader(runAgentScript),
			Mode:      0744,
			WriteMode: "overwrite",
			GID:       1100,
			UID:       1100,
		},
	); err != nil {
		return err
	}

	// Now execute custom provisioning scripts
	for _, f := range conf.ProvisionScripts {
		data, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("error reading script %s: %w", f, err)
		}

		args := &incus.InstanceExecArgs{
			Stdin:  bytes.NewReader(data),
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}

		op, err = c.ExecInstance(req.Name, execReq, args)
		if err != nil {
			return fmt.Errorf("error executing script %s: %w", f, err)
		}

		if err := op.WaitContext(ctx); err != nil {
			return fmt.Errorf("error executing script %s: %w", f, err)
		}

	}

	// Finally, stop the instance so it can be used for copies
	op, err = c.UpdateInstanceState(req.Name, api.InstanceStatePut{
		Action: "stop",
		// todo: add timeout
	}, etag)

	if err != nil {
		return err
	}

	return op.WaitContext(ctx)

}

// getAgentDownloadURL fetches the latest Azure Pipelines agent release and returns
// the download URL for the current Linux architecture.
func getAgentDownloadURL() (string, error) {
	arch := runtime.GOARCH
	var archSuffix string
	switch arch {
	case "amd64":
		archSuffix = "x64"
	case "arm64":
		archSuffix = "arm64"
	case "arm":
		archSuffix = "arm"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", arch)
	}

	// Get latest version from GitHub
	resp, err := http.Get("https://api.github.com/repos/microsoft/azure-pipelines-agent/releases/latest")
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest release: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to decode release JSON: %w", err)
	}

	// Strip 'v' prefix if present
	version := strings.TrimPrefix(release.TagName, "v")

	// Build URL to Azure CDN
	url := fmt.Sprintf("https://download.agent.dev.azure.com/agent/%s/vsts-agent-linux-%s-%s.tar.gz", version, archSuffix, version)

	return url, nil
}
