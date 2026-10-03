#!/bin/sh
# Install any missing CLI for the contract tests: AWS CLI v2, Google Cloud CLI
# (with gke-gcloud-auth-plugin), Azure CLI (with the bastion and ssh
# extensions), Huawei Cloud KooCLI and kubectl.
# For CI runners and containers
# (Debian/Ubuntu, amd64 or arm64); uses sudo when not root.
set -eu

arch=$(dpkg --print-architecture) # amd64 | arm64
SUDO=""
[ "$(id -u)" = 0 ] || SUDO="sudo"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

need() { ! command -v "$1" >/dev/null 2>&1; }

if need unzip || need gpg; then
  $SUDO apt-get update -qq
  $SUDO apt-get install -y -qq unzip gnupg ca-certificates curl >/dev/null
fi

if need aws; then
  machine=x86_64
  [ "$arch" = arm64 ] && machine=aarch64
  curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-$machine.zip" -o "$tmp/awscli.zip"
  (cd "$tmp" && unzip -q awscli.zip && $SUDO ./aws/install >/dev/null)
fi

# google_apt adds Google's Cloud SDK apt repo unless one is configured
# already (a second entry with another keyring makes apt refuse to run).
google_apt() {
  if ! grep -rqs packages.cloud.google.com /etc/apt/sources.list /etc/apt/sources.list.d/; then
    curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | $SUDO gpg --dearmor --yes -o /usr/share/keyrings/cloud.google.gpg
    echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" |
      $SUDO tee /etc/apt/sources.list.d/google-cloud-sdk.list >/dev/null
  fi
  $SUDO apt-get update -qq
}

if need gcloud; then
  google_apt
  $SUDO apt-get install -y -qq google-cloud-cli >/dev/null
fi

if need gke-gcloud-auth-plugin; then
  google_apt # the plugin comes from gcloud's apt repo
  $SUDO apt-get install -y -qq google-cloud-cli-gke-gcloud-auth-plugin >/dev/null
fi

if need kubectl; then
  v=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
  curl -fsSL "https://dl.k8s.io/release/$v/bin/linux/$arch/kubectl" -o "$tmp/kubectl"
  $SUDO install -m 0755 "$tmp/kubectl" /usr/local/bin/kubectl
fi

if need az; then
  curl -fsSL https://aka.ms/InstallAzureCLIDeb | $SUDO bash >/dev/null
fi

# az extensions for Azure Bastion shells and tunnels (mek never installs them).
az extension add --name bastion --only-show-errors --yes >/dev/null 2>&1 || az extension add --name bastion --only-show-errors
az extension add --name ssh --only-show-errors --yes >/dev/null 2>&1 || az extension add --name ssh --only-show-errors

if need hcloud; then
  curl -fsSL "https://ap-southeast-3-hwcloudcli.obs.ap-southeast-3.myhuaweicloud.com/cli/latest/huaweicloud-cli-linux-$arch.tar.gz" -o "$tmp/hcloud.tgz"
  tar -xzf "$tmp/hcloud.tgz" -C "$tmp" hcloud
  $SUDO install -m 0755 "$tmp/hcloud" /usr/local/bin/hcloud # the archive ships it as rwx------
fi

for c in aws gcloud gke-gcloud-auth-plugin az hcloud kubectl; do
  printf '%-23s %s\n' "$c" "$(command -v "$c")"
done
