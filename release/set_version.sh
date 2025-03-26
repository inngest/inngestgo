#! /bin/sh

# This script is used to bump the version of the SDK. It will write that new
# version as inngestgo.SDKVersion. Its only stdout is the next version, so the
# caller can capture it and assign to a variable.
#
# Usage:
#   ./scripts/next_version.sh <major|minor|patch>
#
# Example:
#   ./scripts/next_version.sh major
#   ./scripts/next_version.sh minor
#   ./scripts/next_version.sh patch

SCRIPTS_DIR=$(dirname "$0")

print() {
    # Print to stderr so that the caller doesn't capture it.
    echo "$1" >&2
}

# Get NEXT_VERSION from ../package.json
NEXT_VERSION=$(grep -o '"version": "[^"]*"' "$SCRIPTS_DIR/package.json" | cut -d'"' -f4)

if [ -z "$NEXT_VERSION" ]; then
    print "Error: Could not extract version from package.json"
    exit 1
fi

print "Using version $NEXT_VERSION from package.json"

# Validate the next version.
echo -e "package inngestgo\n\nconst SDKVersion = \"$NEXT_VERSION\"" > ../version.go

# Output the next version, allowing the caller to capture it.
echo "${NEXT_VERSION}"
