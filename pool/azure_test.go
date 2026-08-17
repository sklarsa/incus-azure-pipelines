package pool

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPAzureAgentClient_ListAgentsPreservesBasePathAndAuthenticates(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		assert.Equal(t, "Basic "+base64.StdEncoding.EncodeToString([]byte(":secret-pat")), r.Header.Get("Authorization"))

		switch requests {
		case 1:
			assert.Equal(t, "/tfs/collection/_apis/distributedtask/pools", r.URL.Path)
			assert.Equal(t, "build-pool", r.URL.Query().Get("poolName"))
			assert.Equal(t, "7.1", r.URL.Query().Get("api-version"))
			_, _ = fmt.Fprint(w, `{"count":1,"value":[{"id":42,"name":"build-pool"}]}`)
		case 2:
			assert.Equal(t, "/tfs/collection/_apis/distributedtask/pools/42/agents", r.URL.Path)
			assert.Equal(t, "true", r.URL.Query().Get("includeAssignedRequest"))
			assert.Equal(t, "7.1", r.URL.Query().Get("api-version"))
			_, _ = fmt.Fprint(w, `{"count":2,"value":[{"name":"runner-0","status":"online","assignedRequest":{"requestId":1}},{"name":"runner-1","status":"offline","assignedRequest":null}]}`)
		default:
			t.Fatalf("unexpected request %d", requests)
		}
	}))
	defer server.Close()

	client, err := newHTTPAzureAgentClient(server.URL+"/tfs/collection", "secret-pat", server.Client())
	require.NoError(t, err)

	agents, err := client.ListAgents(context.Background(), "build-pool")
	require.NoError(t, err)
	assert.Equal(t, map[string]AzureAgentStatus{
		"runner-0": {Online: true, Assigned: true},
		"runner-1": {Online: false, Assigned: false},
	}, agents)
	assert.Equal(t, 2, requests)
}

func TestHTTPAzureAgentClient_ListAgentsFailsClosed(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
	}{
		{name: "non-2xx", statusCode: http.StatusServiceUnavailable, body: `temporarily unavailable`},
		{name: "malformed JSON", statusCode: http.StatusOK, body: `{"value":`},
		{name: "missing agent list", statusCode: http.StatusOK, body: `{}`},
		{name: "null agent list", statusCode: http.StatusOK, body: `{"value":null}`},
		{name: "malformed agent status", statusCode: http.StatusOK, body: `{"value":[{"name":"runner-0","status":"mystery"}]}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if requests == 1 {
					_, _ = fmt.Fprint(w, `{"value":[{"id":42,"name":"build-pool"}]}`)
					return
				}
				w.WriteHeader(tt.statusCode)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			client, err := newHTTPAzureAgentClient(server.URL, "pat", server.Client())
			require.NoError(t, err)
			_, err = client.ListAgents(context.Background(), "build-pool")
			assert.Error(t, err)
		})
	}
}

func TestHTTPAzureAgentClient_RejectsMalformedPoolResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"value":[{"name":"build-pool"}]}`)
	}))
	defer server.Close()

	client, err := newHTTPAzureAgentClient(server.URL, "pat", server.Client())
	require.NoError(t, err)
	_, err = client.ListAgents(context.Background(), "build-pool")
	assert.Error(t, err)
}
