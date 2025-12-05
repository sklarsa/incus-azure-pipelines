package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"math"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	incus "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

const (
	defaultAgentPrefix  = "azp-agent"
	defaultBaseInstance = "azp-agent-base"
	agentUser           = "agent"
)

var (
	mu      = &sync.Mutex{}
	agentRe = regexp.MustCompile("^" + defaultAgentPrefix + `-(\d{1,2})$`)
)

func main() {

	provision := flag.Bool("provision", false, "provision the base instance and exit")
	logs := flag.Int("logs", -1, "get agent logs by index (0-base)")

	flag.Parse()

	tokenData, err := os.ReadFile("/tmp/azp_token")
	if err != nil {
		log.Fatal(err)
	}

	conf := Config{
		ProjectName: "azure-pipelines",
		AgentCount:  1,
		BaseImage:   "ubuntu/24.04",
		MaxCores:    8,
		MaxRamInGb:  4,
		AzurePAT:    string(bytes.TrimSpace(tokenData)),
	}

	c, err := incus.ConnectIncusUnix("", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Disconnect()

	// todo: we need to make sure the project has a profile, since default profile may not work
	if err = ensureProject(c, conf.ProjectName); err != nil {
		log.Fatal(err)
	}

	c = c.UseProject(conf.ProjectName)

	if *provision {
		fmt.Printf("provisioning base instance %q\n", conf.BaseImage)
		if err := provisionBaseInstance(context.Background(), c, conf); err != nil {
			log.Fatal(err)
		}
		return
	}

	if *logs > -1 {

		op, err := c.ExecInstance(
			conf.AgentName(*logs),
			api.InstanceExecPost{
				Command:     []string{"cat", "/home/agent/azp-agent.log"},
				WaitForWS:   true,
				Interactive: false,
			}, &incus.InstanceExecArgs{
				Stdout: os.Stdout,
			},
		)

		if err != nil {
			log.Fatal(err)
		}

		if err = op.Wait(); err != nil {
			log.Fatal(err)
		}
		return
	}

	agentsToCreate := make(chan int)
	go func() {
		for {
			idx, open := <-agentsToCreate
			if !open {
				return
			}

			fmt.Printf("Creating agent %d\n", idx)

			if err := createAgent(context.Background(), c, conf, idx); err != nil {
				slog.Error("failed to create agent", "idx", idx, "err", err)
			}
		}
	}()

	go func() {
		for {
			if err = reconcileAgents(c, conf, agentsToCreate); err != nil {
				log.Fatal(err)
			}
			time.Sleep(5 * time.Second)
		}
	}()

	listener, err := c.GetEvents()
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Disconnect()

	t, err := listener.AddHandler(nil, func(e api.Event) {
		/*
					fmt.Fprintf(os.Stdout, `New Event
			timestamp = %s
			type = %s
			data = %s

			`,
						e.Timestamp,
						e.Type,
						e.Metadata,
					)
		*/
	})

	if err != nil {
		log.Fatal(err)
	}
	defer listener.RemoveHandler(t)

	listener.Wait()

}

func ensureProject(c incus.InstanceServer, name string) error {
	projects, err := c.GetProjectNames()
	if err != nil {
		return fmt.Errorf("error listing project names: %w", err)
	}

	for _, p := range projects {
		if p == name {
			return nil
		}
	}

	return c.CreateProject(api.ProjectsPost{
		Name: name,
	})
}

func reconcileAgents(c incus.InstanceServer, conf Config, agentsToCreate chan<- int) error {
	var (
		expectedInstances uint64 = math.MaxUint64 >> (63 - conf.AgentCount)
		instancesFound    uint64 = 0
	)

	mu.Lock()
	defer mu.Unlock()

	instances, err := c.GetInstances(api.InstanceTypeContainer)
	if err != nil {
		return fmt.Errorf("unable to list instances in project %q: %w",
			conf.ProjectName,
			err,
		)
	}

	for _, i := range instances {
		if i.Name == defaultBaseInstance {
			continue
		}

		matches := agentRe.FindStringSubmatch(i.Name)
		if matches == nil {
			// todo: delete the agent if it's invalid
			return fmt.Errorf("invalid agent name %q", i.Name)

		}

		idx, err := strconv.Atoi(matches[1])
		if err != nil {
			return err
		}

		instancesFound |= 1 << idx
	}

	instancesToCreate := expectedInstances ^ instancesFound

	for idx := range conf.AgentCount {
		if (1<<idx)&instancesToCreate > 0 {
			agentsToCreate <- idx
		}
	}

	return nil

}

func createAgent(ctx context.Context, c incus.InstanceServer, conf Config, idx int) error {

	req := api.InstancesPost{
		Name: conf.AgentName(idx),
		Type: api.InstanceTypeContainer,
		Source: api.InstanceSource{
			Source: defaultBaseInstance,
			Type:   "copy",
		},
		Start: true,
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"boot.host_shutdown_action": "force-stop",
			},
			Ephemeral: true,
		},
	}

	if conf.MaxCores > 0 {
		req.Config["limits.cpu.allowance"] = fmt.Sprintf("%d%%", conf.MaxCores*100)
	}

	if conf.MaxRamInGb > 0 {
		req.Config["limits.memory"] = fmt.Sprintf("%dGiB", conf.MaxRamInGb)
	}

	op, err := c.CreateInstance(req)
	if err != nil {
		return err
	}

	if err = op.WaitContext(ctx); err != nil {
		return err
	}

	if err = c.CreateInstanceFile(req.Name, "/home/agent/.token", incus.InstanceFileArgs{
		Content:   strings.NewReader(conf.AzurePAT),
		WriteMode: "overwrite",
		Mode:      400,
		UID:       1100,
		GID:       1100,
	}); err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return err
	}

	op, err = c.ExecInstance(
		req.Name,
		api.InstanceExecPost{
			Command: []string{
				"setsid",
				"/home/agent/run_agent.sh",
				"--agent",
				fmt.Sprintf("%s-%d", hostname, idx),
				"--pool",
				"hetzner-docker",
				"--url",
				"https://dev.azure.com/questdb",
			},
			Interactive: false,
			WaitForWS:   true,
			User:        1100,
			Group:       1100,
		},
		&incus.InstanceExecArgs{},
	)

	if err != nil {
		return err
	}

	return op.WaitContext(ctx)

}

type Config struct {
	ProjectName string `validate:"required"`
	AgentCount  int    `validate:"min=1,max=64"`
	BaseImage   string `validate:"required"`
	MaxCores    int    `validate:"min=0"`
	MaxRamInGb  int    `validate:"min=0"`
	AzurePAT    string `validate:"required"`
}

func (c Config) AgentName(idx int) string {
	return fmt.Sprintf("%s-%d", defaultAgentPrefix, idx)
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

	op, err = c.UpdateInstanceState(req.Name, api.InstanceStatePut{
		Action: "stop",
		// todo: add timeout
	}, etag)

	if err != nil {
		return err
	}

	return op.WaitContext(ctx)

}
