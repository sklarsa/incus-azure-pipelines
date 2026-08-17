package pool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const azureAPIVersion = "7.1"

// AzureAgentStatus is the Azure-side state used to decide whether a local
// instance can be safely reaped.
type AzureAgentStatus struct {
	Online   bool
	Assigned bool
}

// AzureAgentClient lists the agents currently registered in an Azure pool.
type AzureAgentClient interface {
	ListAgents(ctx context.Context, poolName string) (map[string]AzureAgentStatus, error)
}

type httpAzureAgentClient struct {
	baseURL *url.URL
	pat     string
	client  *http.Client
}

func newHTTPAzureAgentClient(rawURL, pat string, client *http.Client) (*httpAzureAgentClient, error) {
	baseURL, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse Azure URL: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &httpAzureAgentClient{baseURL: baseURL, pat: pat, client: client}, nil
}

func (c *httpAzureAgentClient) ListAgents(ctx context.Context, poolName string) (map[string]AzureAgentStatus, error) {
	poolsURL, err := c.endpoint("_apis", "distributedtask", "pools")
	if err != nil {
		return nil, err
	}
	query := poolsURL.Query()
	query.Set("poolName", poolName)
	query.Set("api-version", azureAPIVersion)
	poolsURL.RawQuery = query.Encode()

	var pools struct {
		Value []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	if err := c.getJSON(ctx, poolsURL, &pools); err != nil {
		return nil, fmt.Errorf("list Azure pools: %w", err)
	}

	poolID := 0
	for _, candidate := range pools.Value {
		if candidate.Name != poolName {
			continue
		}
		if candidate.ID <= 0 || poolID != 0 {
			return nil, fmt.Errorf("azure pool %q response is ambiguous or malformed", poolName)
		}
		poolID = candidate.ID
	}
	if poolID == 0 {
		return nil, fmt.Errorf("azure pool %q not found", poolName)
	}

	agentsURL, err := c.endpoint("_apis", "distributedtask", "pools", strconv.Itoa(poolID), "agents")
	if err != nil {
		return nil, err
	}
	query = agentsURL.Query()
	query.Set("includeAssignedRequest", "true")
	query.Set("api-version", azureAPIVersion)
	agentsURL.RawQuery = query.Encode()

	type agentRecord struct {
		Name            string          `json:"name"`
		Status          string          `json:"status"`
		AssignedRequest json.RawMessage `json:"assignedRequest"`
	}
	var response struct {
		Value json.RawMessage `json:"value"`
	}
	if err := c.getJSON(ctx, agentsURL, &response); err != nil {
		return nil, fmt.Errorf("list agents in Azure pool %q: %w", poolName, err)
	}
	if len(response.Value) == 0 || string(response.Value) == "null" {
		return nil, fmt.Errorf("azure pool %q response did not contain an agent list", poolName)
	}
	var records []agentRecord
	if err := json.Unmarshal(response.Value, &records); err != nil {
		return nil, fmt.Errorf("decode agents in Azure pool %q: %w", poolName, err)
	}

	agents := make(map[string]AzureAgentStatus, len(records))
	for _, agent := range records {
		if agent.Name == "" {
			return nil, fmt.Errorf("azure pool %q returned an agent without a name", poolName)
		}
		if _, exists := agents[agent.Name]; exists {
			return nil, fmt.Errorf("azure pool %q returned duplicate agent %q", poolName, agent.Name)
		}

		var online bool
		switch strings.ToLower(agent.Status) {
		case "online":
			online = true
		case "offline":
			online = false
		default:
			return nil, fmt.Errorf("azure agent %q has unknown status %q", agent.Name, agent.Status)
		}

		assigned := len(agent.AssignedRequest) > 0 && string(agent.AssignedRequest) != "null"
		agents[agent.Name] = AzureAgentStatus{Online: online, Assigned: assigned}
	}
	return agents, nil
}

func (c *httpAzureAgentClient) endpoint(parts ...string) (*url.URL, error) {
	joined, err := url.JoinPath(c.baseURL.String(), parts...)
	if err != nil {
		return nil, fmt.Errorf("build Azure API URL: %w", err)
	}
	endpoint, err := url.Parse(joined)
	if err != nil {
		return nil, fmt.Errorf("parse Azure API URL: %w", err)
	}
	return endpoint, nil
}

func (c *httpAzureAgentClient) getJSON(ctx context.Context, endpoint *url.URL, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("", c.pat)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("azure API returned HTTP %d", resp.StatusCode)
	}

	decoder := json.NewDecoder(io.LimitReader(resp.Body, 8<<20))
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode Azure response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("decode Azure response: multiple JSON values")
		}
		return fmt.Errorf("decode Azure response: %w", err)
	}
	return nil
}
