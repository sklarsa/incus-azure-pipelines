set -euo pipefail

AGENT_URL="${1:?Usage: provision.sh <agent_download_url>}"
AGENT_USER="agent"
AGENT_HOME="/home/${AGENT_USER}"

# Install basic utilities
apt-get update
apt-get install -y curl wget tar sudo

# Create agent user with passwordless sudo
useradd -m -s /bin/bash --uid 1100 --gid 1100 "${AGENT_USER}"
echo "${AGENT_USER} ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/${AGENT_USER}
chmod 440 /etc/sudoers.d/${AGENT_USER}

# Download and extract agent as the agent user
su - "${AGENT_USER}" <<EOF
set -euo pipefail
cd "${AGENT_HOME}"
curl -fsSL -o agent.tar.gz "${AGENT_URL}"
tar -xzf agent.tar.gz
rm agent.tar.gz
EOF
