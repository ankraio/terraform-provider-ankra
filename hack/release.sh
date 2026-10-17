#!/bin/sh
# Copyright (c) HashiCorp, Inc.
# SPDX-License-Identifier: MPL-2.0

# Publishes the provider release the CHANGELOG's top section names, once.
#
# Runs on every push to main (and a manual run of main). When the top section
# is "## X.Y.Z (YYYY-MM-DD)" and GitHub has no release vX.Y.Z yet, GoReleaser
# builds, signs and publishes it; the Terraform Registry picks the release up
# from GitHub. An "(Unreleased)" top section, or a version already released,
# publishes nothing. Nobody pushes a tag: the release creates it.
#
# Inputs (files under /run/agent-secrets, delivered by Ankra Pipelines):
#   repo_git          the run's git credential, one line of JSON with a token
#   gpg_private_key   the provider signing key, ASCII-armoured
#   gpg_passphrase    its passphrase, only when the key has one
set -eu

secrets=/run/agent-secrets
repository="ankraio/terraform-provider-ankra"

heading="$(grep -m1 '^## ' CHANGELOG.md)"
version="$(printf '%s\n' "${heading}" | sed -n 's/^## \([0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*\) ([0-9]\{4\}-[0-9]\{2\}-[0-9]\{2\})$/\1/p')"
if [ -z "${version}" ]; then
  echo "release: the CHANGELOG's top section is '${heading}', not a dated version; nothing to publish"
  exit 0
fi
tag="v${version}"

token="$(grep -o '"token": *"[^"]*"' "${secrets}/repo_git" | cut -d'"' -f4)"
if [ -z "${token}" ]; then
  echo "release: the git credential carries no token" >&2
  exit 1
fi

# Anything but "not found" is an error: publishing after a failed lookup could
# replace a release that already exists.
status="$(curl -sS -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer ${token}" -H 'Accept: application/vnd.github+json' \
  "https://api.github.com/repos/${repository}/releases/tags/${tag}")"
case "${status}" in
  200)
    echo "release: ${tag} is already published"
    exit 0
    ;;
  404) ;;
  *)
    echo "release: looking up ${tag} returned HTTP ${status}; not publishing" >&2
    exit 1
    ;;
esac

scratch="$(mktemp -d "${GOTMPDIR}/release.XXXXXX")"
GNUPGHOME="${scratch}/gnupg"
export GNUPGHOME
mkdir -m 700 "${GNUPGHOME}"
printf 'allow-loopback-pinentry\n' > "${GNUPGHOME}/gpg-agent.conf"
# The provider key has no passphrase, so no passphrase secret is bound (an
# empty one cannot be). GoReleaser's signing arguments still name a file, so an
# empty one stands in when none is delivered.
GPG_PASSPHRASE_FILE="${secrets}/gpg_passphrase"
if [ ! -f "${GPG_PASSPHRASE_FILE}" ]; then
  GPG_PASSPHRASE_FILE="${scratch}/empty-passphrase"
  : > "${GPG_PASSPHRASE_FILE}"
fi
export GPG_PASSPHRASE_FILE
gpg --batch --pinentry-mode loopback --passphrase-file "${GPG_PASSPHRASE_FILE}" \
  --import "${secrets}/gpg_private_key"
GPG_FINGERPRINT="$(gpg --batch --list-secret-keys --with-colons | awk -F: '/^fpr:/ { print $10; exit }')"
export GPG_FINGERPRINT

# The workspace belongs to another user, so git refuses it without this.
GIT_CONFIG_COUNT=1
GIT_CONFIG_KEY_0=safe.directory
GIT_CONFIG_VALUE_0='*'
export GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0

# GoReleaser reads the version from a tag on HEAD. The tag stays local: the
# GitHub release creates it on this commit (release.target_commitish).
git tag "${tag}" HEAD

goreleaser_version="v2.18.3"
case "$(uname -m)" in
  x86_64 | amd64) goreleaser_arch="x86_64" ;;
  aarch64 | arm64) goreleaser_arch="arm64" ;;
  *) echo "release: no GoReleaser build for $(uname -m)" >&2; exit 1 ;;
esac
archive="goreleaser_Linux_${goreleaser_arch}.tar.gz"
download="https://github.com/goreleaser/goreleaser/releases/download/${goreleaser_version}"
curl -sSfL -o "${scratch}/${archive}" "${download}/${archive}"
curl -sSfL -o "${scratch}/checksums.txt" "${download}/checksums.txt"
(cd "${scratch}" && grep " ${archive}\$" checksums.txt | sha256sum -c -)
tar -xzf "${scratch}/${archive}" -C "${scratch}" goreleaser

GITHUB_TOKEN="${token}" "${scratch}/goreleaser" release --clean
echo "release: published ${tag}"
