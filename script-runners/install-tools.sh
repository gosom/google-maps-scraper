#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Detect OS
OS="$(uname)"
if [ "$OS" = "Darwin" ]; then
    PACKAGE_MANAGER="brew"
elif [ -f /etc/debian_version ]; then
    PACKAGE_MANAGER="apt"
elif [ -f /etc/redhat-release ]; then
    PACKAGE_MANAGER="yum"
else
    echo -e "${RED}Unsupported operating system${NC}"
    exit 1
fi

# Function to check if a command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to install Homebrew on macOS
install_homebrew() {
    if ! command_exists brew; then
        echo -e "${YELLOW}Installing Homebrew...${NC}"
        /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
    fi
}

# Function to install tools based on package manager
install_tools() {
    case $PACKAGE_MANAGER in
        brew)
            install_homebrew
            echo -e "${YELLOW}Installing tools with Homebrew...${NC}"
            brew install helm kubectl yamllint
            # Install kubeval manually since it's not in Homebrew
            if ! command_exists kubeval; then
                echo -e "${YELLOW}Installing kubeval...${NC}"
                curl -L -o kubeval-darwin-amd64.tar.gz https://github.com/instrumenta/kubeval/releases/latest/download/kubeval-darwin-amd64.tar.gz
                tar xf kubeval-darwin-amd64.tar.gz
                sudo mv kubeval /usr/local/bin
                rm kubeval-darwin-amd64.tar.gz
            fi
            ;;
        apt)
            echo -e "${YELLOW}Installing tools with apt...${NC}"
            sudo apt-get update
            # Install Helm
            if ! command_exists helm; then
                curl https://baltocdn.com/helm/signing.asc | gpg --dearmor | sudo tee /usr/share/keyrings/helm.gpg > /dev/null
                echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/helm.gpg] https://baltocdn.com/helm/stable/debian/ all main" | sudo tee /etc/apt/sources.list.d/helm-stable-debian.list
                sudo apt-get update
                sudo apt-get install -y helm
            fi
            # Install kubectl
            if ! command_exists kubectl; then
                sudo curl -fsSLo /usr/share/keyrings/kubernetes-archive-keyring.gpg https://packages.cloud.google.com/apt/doc/apt-key.gpg
                echo "deb [signed-by=/usr/share/keyrings/kubernetes-archive-keyring.gpg] https://apt.kubernetes.io/ kubernetes-xenial main" | sudo tee /etc/apt/sources.list.d/kubernetes.list
                sudo apt-get update
                sudo apt-get install -y kubectl
            fi
            # Install yamllint
            sudo apt-get install -y yamllint
            # Install kubeval
            if ! command_exists kubeval; then
                wget https://github.com/instrumenta/kubeval/releases/latest/download/kubeval-linux-amd64.tar.gz
                tar xf kubeval-linux-amd64.tar.gz
                sudo mv kubeval /usr/local/bin
                rm kubeval-linux-amd64.tar.gz
            fi
            ;;
        yum)
            echo -e "${YELLOW}Installing tools with yum...${NC}"
            # Install Helm
            if ! command_exists helm; then
                curl -fsSL -o get_helm.sh https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3
                chmod 700 get_helm.sh
                ./get_helm.sh
                rm get_helm.sh
            fi
            # Install kubectl
            if ! command_exists kubectl; then
                cat <<EOF | sudo tee /etc/yum.repos.d/kubernetes.repo
[kubernetes]
name=Kubernetes
baseurl=https://packages.cloud.google.com/yum/repos/kubernetes-el7-\$basearch
enabled=1
gpgcheck=1
gpgkey=https://packages.cloud.google.com/yum/doc/yum-key.gpg https://packages.cloud.google.com/yum/doc/rpm-package-key.gpg
EOF
                sudo yum install -y kubectl
            fi
            # Install yamllint
            sudo yum install -y python3-pip
            pip3 install --user yamllint
            # Install kubeval
            if ! command_exists kubeval; then
                wget https://github.com/instrumenta/kubeval/releases/latest/download/kubeval-linux-amd64.tar.gz
                tar xf kubeval-linux-amd64.tar.gz
                sudo mv kubeval /usr/local/bin
                rm kubeval-linux-amd64.tar.gz
            fi
            ;;
    esac
}

# Install kind if not present
install_kind() {
    if ! command_exists kind; then
        echo "Installing kind..."
        # Detect OS and architecture
        OS="$(uname -s)"
        ARCH="$(uname -m)"
        
        echo "Detected OS: ${OS}, Architecture: ${ARCH}"
        
        case "${OS}" in
            Linux*)
                case "${ARCH}" in
                    x86_64|amd64) 
                        BINARY="kind-linux-amd64"
                        DOWNLOAD_URL="https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-amd64"
                        ;;
                    aarch64|arm64) 
                        BINARY="kind-linux-arm64"
                        DOWNLOAD_URL="https://kind.sigs.k8s.io/dl/v0.20.0/kind-linux-arm64"
                        ;;
                    *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
                esac
                ;;
            Darwin*)
                case "${ARCH}" in
                    x86_64|amd64)
                        BINARY="kind-darwin-amd64"
                        DOWNLOAD_URL="https://kind.sigs.k8s.io/dl/v0.20.0/kind-darwin-amd64"
                        ;;
                    arm64)
                        BINARY="kind-darwin-arm64"
                        DOWNLOAD_URL="https://kind.sigs.k8s.io/dl/v0.20.0/kind-darwin-arm64"
                        ;;
                    *) echo "Unsupported architecture: ${ARCH}"; exit 1 ;;
                esac
                ;;
            *) echo "Unsupported OS: ${OS}"; exit 1 ;;
        esac
        
        echo "Downloading kind binary for ${OS} ${ARCH}..."
        if ! curl -fsSLo ./kind "${DOWNLOAD_URL}"; then
            echo "Failed to download kind binary"
            exit 1
        fi
        
        chmod +x ./kind
        
        # First try without sudo
        if ! mv ./kind /usr/local/bin/kind 2>/dev/null; then
            echo "Trying with sudo..."
            sudo mv ./kind /usr/local/bin/kind || {
                echo "Failed to move kind binary to /usr/local/bin"
                exit 1
            }
        fi
        
        # Set proper permissions
        sudo chmod 755 /usr/local/bin/kind || {
            echo "Failed to set permissions on kind binary"
            exit 1
        }
        
        # Verify installation
        if ! kind version; then
            echo "Failed to install kind properly"
            # Clean up failed installation
            sudo rm -f /usr/local/bin/kind
            exit 1
        fi
    fi
}

# Clean up any existing kind installation
cleanup_kind() {
    if [ -f /usr/local/bin/kind ]; then
        echo "Removing existing kind installation..."
        sudo rm -f /usr/local/bin/kind
    fi
}

# Main installation process
echo -e "${YELLOW}Installing required tools...${NC}"

# Clean up first
cleanup_kind

# Install kind
install_kind

# Verify installation
if command_exists kind; then
    echo -e "${GREEN}✓ kind installed successfully${NC}"
else
    echo "Failed to install kind"
    exit 1
fi

echo -e "${GREEN}All tools installed successfully!${NC}" 