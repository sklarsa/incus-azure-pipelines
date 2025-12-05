#!/bin/bash
set -euo pipefail

cd /home/agent

TOKEN_FILE="/home/agent/.token"

if [[ ! -f "$TOKEN_FILE" ]]; then
    echo "Token file not found"
    exit 1
fi

TOKEN=$(cat "$TOKEN_FILE")
rm -f "$TOKEN_FILE"


cleanup() {
  trap "" EXIT INT TERM

  if [ -e ./config.sh ]; then
    echo "Removing Azure Pipelines agent from pool..."

    # Now that agent is stopped, removal should succeed
    ./config.sh remove --unattended --auth "PAT" --token "${TOKEN}" || {
      echo "Warning: Failed to remove agent from pool. It may need manual cleanup."
    }
  fi

  sudo shutdown now
}

trap "cleanup; exit 0" EXIT
trap "cleanup; exit 130" INT
trap "cleanup; exit 143" TERM

./config.sh --unattended \
  --auth "PAT" \
  --token "${TOKEN}" \
  --work _work \
  --replace \
  --acceptTeeEula \
  "$@"

./run.sh --once