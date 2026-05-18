#!/bin/sh

# This script is used to bump the version of the SDK. It will write that new
# version as inngestgo.SDKVersion. Its only stdout is the next version, so the
# caller can capture it and assign to a variable.
#
# Usage:
#   ./release/set_version.sh <version>
#
# Example:
#   ./release/set_version.sh v0.16.0
#   ./release/set_version.sh 0.16.0

set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

print() {
  # Print to stderr so that the caller doesn't capture it.
  echo "$1" >&2
}

if [ "$#" -ne 1 ]; then
  print "Usage: ./release/set_version.sh <version>"
  exit 1
fi

NEXT_VERSION=${1#v}

case "$NEXT_VERSION" in
  *[!0-9.]* | *.*.*.* | .* | *. | *..* | "")
    print "Error: invalid version: $1"
    exit 1
    ;;
esac

if ! printf "%s\n" "$NEXT_VERSION" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  print "Error: version must be semver major.minor.patch: $1"
  exit 1
fi

print "Setting SDK version to $NEXT_VERSION"

printf "package inngestgo\n\nconst SDKVersion = \"%s\"\n" "$NEXT_VERSION" >"$ROOT_DIR/version.go"
printf "package version\n\nconst SDKVersion = \"%s\"\n" "$NEXT_VERSION" >"$ROOT_DIR/pkg/version/version.go"

# Output the next version, allowing the caller to capture it.
echo "${NEXT_VERSION}"
