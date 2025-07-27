#!/usr/bin/env bash
set -euo pipefail

# This script writes the minimal Terraform Registry manifest (format v1)
# into dist/terraform-provider-ankra_v${VERSION}_manifest.json.
# Usage: run after GoReleaser, in the project root:
#   bash scripts/generate-manifest.sh "0.1.0-rc01"

PROVIDER_NAME="ankra"
VERSION="$1"
DIST_DIR="dist"
MANIFEST_PATH="${DIST_DIR}/terraform-provider-${PROVIDER_NAME}_v${VERSION}_manifest.json"

mkdir -p "$DIST_DIR"

cat > "$MANIFEST_PATH" <<EOF
{
  "version": 1,
  "metadata": {
    "protocol_versions": ["6.0"]
  }
}
EOF

echo "✔︎ Manifest generated at $MANIFEST_PATH"
