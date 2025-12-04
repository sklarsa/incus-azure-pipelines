#!/bin/bash
set -e

TOKEN_FILE="/run/agent-token"

if [[ ! -f "$TOKEN_FILE" ]]; then
    echo "Token file not found"
    exit 1
fi

TOKEN=$(cat "$TOKEN_FILE")
rm -f "$TOKEN_FILE"

# Only configure if not already configured
if [[ ! -f /home/agent/.agent ]]; then
    /home/agent/config.sh --unattended \
      --agent {{ .AgentName }} \
      --url {{ .ProjectUrl }} \
      --auth "PAT" \
      --token $(cat "${AZP_TOKEN_FILE}") \
      --pool {{ .PoolName }} \
      --work _work \
      --replace \
      --acceptTeeEula & wait $!
fi