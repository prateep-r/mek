#!/bin/sh
# Install any missing cloud CLI for the contract tests: AWS CLI v2, Google
# Cloud CLI, Azure CLI and Huawei Cloud KooCLI. For CI runners and containers
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

if need az; then
  curl -fsSL https://aka.ms/InstallAzureCLIDeb | $SUDO bash >/dev/null
fi

if need hcloud; then
  curl -fsSL "https://ap-southeast-3-hwcloudcli.obs.ap-southeast-3.myhuaweicloud.com/cli/latest/huaweicloud-cli-linux-$arch.tar.gz" -o "$tmp/hcloud.tgz"
  tar -xzf "$tmp/hcloud.tgz" -C "$tmp" hcloud
  $SUDO install -m 0755 "$tmp/hcloud" /usr/local/bin/hcloud # the archive ships it as rwx------
fi

for c in aws gcloud az hcloud; do
  printf '%-7s %s\n' "$c" "$(command -v "$c")"
done
