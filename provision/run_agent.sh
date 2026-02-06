#!/bin/bash
set -uo pipefail

LOG_FILE="${LOG_FILE:-/home/agent/azp-agent.log}"
exec >> "$LOG_FILE" 2>&1

export HOME=/home/agent
cd "${HOME}"

TOKEN_FILE="/home/agent/.token"
if [[ ! -f "$TOKEN_FILE" ]]; then
    echo "Token file not found"
    exit 1
fi

TOKEN=$(cat "$TOKEN_FILE")
rm -f "$TOKEN_FILE"

./config.sh --unattended \
    --auth "PAT" \
    --token "${TOKEN}" \
    --work _work \
    --replace \
    --acceptTeeEula \
    "$@" || { echo "config.sh failed"; exit 1; }

./run.sh --once

./config.sh remove --unattended --auth "PAT" --token "${TOKEN}" 

sudo poweroff -f