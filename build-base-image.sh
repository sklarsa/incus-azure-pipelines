#!/bin/bash

set -euo pipefail

get_agent_download_url() {
    local arch
    arch=$(uname -m)
    
    case "$arch" in
        x86_64)  arch_suffix="x64" ;;
        aarch64) arch_suffix="arm64" ;;
        armv7l)  arch_suffix="arm" ;;
        *)
            echo "unsupported architecture: $arch" >&2
            return 1
            ;;
    esac
    
    local release_json
    release_json=$(curl -fsSL "https://api.github.com/repos/microsoft/azure-pipelines-agent/releases/latest")
    
    echo "$release_json" | jq -r --arg suffix "vsts-agent-linux-${arch_suffix}-" \
        '.assets[] | select(.name | startswith($suffix)) | .browser_download_url'
}

sudo distrobuilder build-incus distrobuilder/azure-agent.yml \
    --import-into-incus=azp-agent-base 

