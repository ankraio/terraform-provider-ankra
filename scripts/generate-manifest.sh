#!/usr/bin/env bash
set -euo pipefail

# This script generates a Terraform Registry release‑manifest (with "shasum" entries)
# and then appends that manifest file’s checksum into the SHA256SUMS file so it can be signed.
# Usage: run after GoReleaser, in the project root.
# Example: bash scripts/generate-manifest.sh "0.1.0-rc01"

PROVIDER_NAME="ankra"
VERSION="$1"
DIST_DIR="dist"
MANIFEST_PATH="${DIST_DIR}/terraform-provider-${PROVIDER_NAME}_v${VERSION}_manifest.json"
SHA256SUMS_FILE="${DIST_DIR}/terraform-provider-${PROVIDER_NAME}_v${VERSION}_SHA256SUMS"

mkdir -p "$DIST_DIR"

if [[ ! -f "$SHA256SUMS_FILE" ]]; then
  echo "ERROR: SHA256SUMS file not found: $SHA256SUMS_FILE"
  exit 1
fi

# 1) Write the manifest JSON
{
  echo "{"
  echo "  \"version\": \"${VERSION}\","
  echo "  \"protocols\": [\"5.0\"],"
  echo "  \"platforms\": ["
} > "$MANIFEST_PATH"

FIRST=1
while read -r SHASUM FILENAME; do
  # Only include .zip artifacts
  if [[ "$FILENAME" =~ \.zip$ ]]; then
    # Extract os/arch from the filename
    if [[ "$FILENAME" =~ terraform-provider-${PROVIDER_NAME}_v${VERSION}_([a-z0-9]+)_([a-z0-9]+)\.zip ]]; then
      OS="${BASH_REMATCH[1]}"
      ARCH="${BASH_REMATCH[2]}"
    else
      echo "ERROR: Could not parse OS/ARCH from $FILENAME"
      exit 1
    fi

    # comma‐separate entries
    if [[ $FIRST -eq 0 ]]; then
      echo "," >> "$MANIFEST_PATH"
    fi
    FIRST=0

    # emit the platform entry
    cat >> "$MANIFEST_PATH" <<EOF
    {
      "os":       "$OS",
      "arch":     "$ARCH",
      "filename": "$FILENAME",
      "shasum":   "$SHASUM"
    }
EOF
  fi
done < <(awk '{print $1, $2}' "$SHA256SUMS_FILE")

# close out the JSON
{
  echo ""
  echo "  ]"
  echo "}"
} >> "$MANIFEST_PATH"

echo "✔︎ Manifest generated at $MANIFEST_PATH"

# 2) Append the manifest’s checksum into your SHA256SUMS file
MANIFEST_BASENAME=$(basename "$MANIFEST_PATH")
MANIFEST_SHASUM=$(sha256sum "$MANIFEST_PATH" | awk '{print $1}')
echo "${MANIFEST_SHASUM}  ${MANIFEST_BASENAME}" >> "$SHA256SUMS_FILE"
echo "✔︎ Appended manifest checksum to $SHA256SUMS_FILE"
