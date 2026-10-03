#!/bin/sh
# Install any missing CLI for the contract tests: AWS CLI v2, Google Cloud CLI
# (with gke-gcloud-auth-plugin), Azure CLI, Huawei Cloud KooCLI and kubectl.
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

if need gcloud; then
  curl -fsSL https://packages.cloud.google.com/apt/doc/apt-key.gpg | $SUDO gpg --dearmor --yes -o /usr/share/keyrings/cloud.google.gpg
  echo "deb [signed-by=/usr/share/keyrings/cloud.google.gpg] https://packages.cloud.google.com/apt cloud-sdk main" |
    $SUDO tee /etc/apt/sources.list.d/google-cloud-sdk.list >/dev/null
  $SUDO apt-get update -qq
  $SUDO apt-get install -y -qq google-cloud-cli >/dev/null
fi

if need gke-gcloud-auth-plugin; then
  # From the same apt repo as gcloud; an archive install has components instead.
  $SUDO apt-get update -qq >/dev/null 2>&1 || true
  if ! $SUDO apt-get install -y -qq google-cloud-cli-gke-gcloud-auth-plugin >/dev/null 2>&1; then
    gcloud components install gke-gcloud-auth-plugin --quiet
  fi
fi

if need kubectl; then
  v=$(curl -fsSL https://dl.k8s.io/release/stable.txt)
  curl -fsSL "https://dl.k8s.io/release/$v/bin/linux/$arch/kubectl" -o "$tmp/kubectl"
  $SUDO install -m 0755 "$tmp/kubectl" /usr/local/bin/kubectl
fi

if need az; then
  curl -fsSL https://aka.ms/InstallAzureCLIDeb | $SUDO bash >/dev/null
fi

if need hcloud; then
  curl -fsSL "https://ap-southeast-3-hwcloudcli.obs.ap-southeast-3.myhuaweicloud.com/cli/latest/huaweicloud-cli-linux-$arch.tar.gz" -o "$tmp/hcloud.tgz"
  tar -xzf "$tmp/hcloud.tgz" -C "$tmp" hcloud
  $SUDO install -m 0755 "$tmp/hcloud" /usr/local/bin/hcloud # the archive ships it as rwx------
fi

for c in aws gcloud gke-gcloud-auth-plugin az hcloud kubectl; do
  printf '%-23s %s\n' "$c" "$(command -v "$c")"
done
