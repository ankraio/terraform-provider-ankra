#!/bin/bash
set -euo pipefail

# Build the provider binary
cd "$(dirname "$0")/../.."
echo "Building terraform-provider-ankra..."
go build -o terraform-provider-ankra

# Prepare dev_overrides directory for Terraform
DEV_BIN_DIR="local-providers/dev-bin"
mkdir -p "$DEV_BIN_DIR"
cp terraform-provider-ankra "$DEV_BIN_DIR/terraform-provider-ankra"
chmod +x "$DEV_BIN_DIR/terraform-provider-ankra"

# Move to the test/project directory
cd test/project

export TF_CLI_CONFIG_FILE="$(pwd)/.terraformrc"

# Skip terraform init when using dev_overrides

if [[ "${1:-}" == "--destroy" ]]; then
  echo "Running Terraform destroy (auto-approve)..."
  terraform destroy -auto-approve
else
  echo "Running Terraform apply (auto-approve)..."
  terraform apply -auto-approve
fi
