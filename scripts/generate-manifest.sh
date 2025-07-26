#!/usr/bin/env bash
set -euo pipefail

# Directory where GoReleaser outputs archives (default is dist/)
DIST_DIR="dist"
MANIFEST_PATH="provider/terraform-registry-manifest.json"

# Provider metadata (edit as needed)
PROVIDER_NAME="ankra"
PROVIDER_DESCRIPTION="Terraform provider for Ankra platform."
PROVIDER_HOMEPAGE="https://github.com/ankraio/terraform-provider-ankra"
PROVIDER_REPOSITORY="https://github.com/ankraio/terraform-provider-ankra"

# Get version from the first archive filename (assumes all have the same version)
FIRST_ARCHIVE=$(find "$DIST_DIR" -maxdepth 1 -name "${PROVIDER_NAME}_*.zip" | head -n1)
if [[ -z "$FIRST_ARCHIVE" ]]; then
  echo "No provider archives found in $DIST_DIR."
  exit 1
fi
VERSION=$(basename "$FIRST_ARCHIVE" | sed -E "s/${PROVIDER_NAME}_([v0-9.\-]+)_.*/\1/")

# Start manifest
cat > "$MANIFEST_PATH" <<EOF
{
  "name": "$PROVIDER_NAME",
  "version": "$VERSION",
  "description": "$PROVIDER_DESCRIPTION",
  "homepage": "$PROVIDER_HOMEPAGE",
  "repository": "$PROVIDER_REPOSITORY",
  "platforms": [
EOF

FIRST=1
for ARCHIVE in "$DIST_DIR"/${PROVIDER_NAME}_*.zip; do
  FILENAME=$(basename "$ARCHIVE")
  # Extract OS and ARCH from filename
  # Example: terraform-provider-ankra_1.0.0-rc01_darwin_arm64.zip
  if [[ "$FILENAME" =~ ${PROVIDER_NAME}_[v0-9.\-]+_([a-z0-9]+)_([a-z0-9]+)\.zip ]]; then
    OS="${BASH_REMATCH[1]}"
    ARCH="${BASH_REMATCH[2]}"
  else
    echo "Could not parse OS/ARCH from $FILENAME"
    exit 1
  fi
  SHASUM=$(shasum -a 256 "$ARCHIVE" | awk '{print $1}')
  if [[ $FIRST -eq 0 ]]; then
    echo "," >> "$MANIFEST_PATH"
  fi
  FIRST=0
  cat >> "$MANIFEST_PATH" <<EOF
    {
      "os": "$OS",
      "arch": "$ARCH",
      "filename": "$FILENAME",
      "shasum": "$SHASUM"
    }
EOF

done

cat >> "$MANIFEST_PATH" <<EOF
  ]
}
EOF

echo "Manifest generated at $MANIFEST_PATH"
