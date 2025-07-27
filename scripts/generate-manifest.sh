#!/usr/bin/env bash
set -euo pipefail

# This script generates a Terraform Registry manifest with real SHA256s (as "shasum")
# from the GoReleaser SHA256SUMS file.
# Usage: run after GoReleaser, in the project root.

PROVIDER_NAME="ankra"
VERSION="$1"
MANIFEST_PATH="dist/terraform-provider-${PROVIDER_NAME}_v${VERSION}_manifest.json"
SHA256SUMS_FILE="dist/terraform-provider-${PROVIDER_NAME}_v${VERSION}_SHA256SUMS"

mkdir -p dist
if [[ ! -f "$SHA256SUMS_FILE" ]]; then
  echo "SHA256SUMS file not found: $SHA256SUMS_FILE"
  exit 1
fi

echo "{" > "$MANIFEST_PATH"
echo "  \"version\": \"$VERSION\"," >> "$MANIFEST_PATH"
echo "  \"protocols\": [\"5.0\"]," >> "$MANIFEST_PATH"
echo "  \"platforms\": [" >> "$MANIFEST_PATH"

FIRST=1
# Read each line: checksum and filename
while read -r SHASUM FILENAME; do
  FILENAME="${FILENAME#./}"
  # Only process .zip artifacts
  if [[ "$FILENAME" =~ \.zip$ ]]; then
    if [[ "$FILENAME" =~ terraform-provider-${PROVIDER_NAME}_v${VERSION}_([a-z0-9]+)_([a-z0-9]+)\.zip ]]; then
      OS="${BASH_REMATCH[1]}"
      ARCH="${BASH_REMATCH[2]}"
    else
      echo "Could not parse OS/ARCH from $FILENAME"
      exit 1
    fi

    # comma-separate entries
    if [[ $FIRST -eq 0 ]]; then
      echo "," >> "$MANIFEST_PATH"
    fi
    FIRST=0

    # emit platform object with "shasum" key
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

echo "  ]" >> "$MANIFEST_PATH"
echo "}" >> "$MANIFEST_PATH"

echo "Manifest generated at $MANIFEST_PATH"
