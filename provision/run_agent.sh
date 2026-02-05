#!/bin/bash
set -uo pipefail

LOG_FILE="${LOG_FILE:-/home/agent/azp-agent.log}"
exec >> "$LOG_FILE" 2>&1

export HOME=/home/agent
cd "${HOME}"

STATUS_FILE="/home/agent/.registration-status"

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
    fi
    sudo poweroff -f
}

trap cleanup EXIT
trap "exit 130" INT
trap "exit 143" TERM

CONFIG_OUTPUT=$(./config.sh --unattended \
    --auth "PAT" \
    --token "${TOKEN}" \
    --work _work \
    --replace \
    --acceptTeeEula \
    "$@" 2>&1)
CONFIG_RC=$?

if [[ $CONFIG_RC -ne 0 ]]; then
    printf "%s\n" "$CONFIG_OUTPUT" > "$STATUS_FILE"
    echo "config.sh failed with exit code $CONFIG_RC"
    exit 1
fi

CONFIGURED=true

./run.sh --once