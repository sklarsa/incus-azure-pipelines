#!/bin/bash
set -euo pipefail

LOG_FILE="${LOG_FILE:-/home/agent/azp-agent.log}"
exec &> >(tee -a "$LOG_FILE")

cd /home/agent

TOKEN_FILE="/home/agent/.token"
if [[ ! -f "$TOKEN_FILE" ]]; then
    echo "Token file not found"
    exit 1
fi

TOKEN=$(cat "$TOKEN_FILE")
rm -f "$TOKEN_FILE"

CONFIGURED=false

cleanup() {
    trap "" EXIT INT TERM
    if [[ "$CONFIGURED" == "true" ]] && [ -e ./config.sh ]; then
        echo "Removing Azure Pipelines agent from pool..."
        ./config.sh remove --unattended --auth "PAT" --token "${TOKEN}" || {
            echo "Warning: Failed to remove agent from pool. It may need manual cleanup."
        }
        sudo poweroff -f
    else
        echo "Agent was not configured successfully, leaving instance for debugging"
    fi
}

trap cleanup EXIT
trap "exit 130" INT
trap "exit 143" TERM

./config.sh --unattended \
    --auth "PAT" \
    --token "${TOKEN}" \
    --work _work \
    --replace \
    --acceptTeeEula \
    "$@"

CONFIGURED=true

./run.sh --once