#! /bin/sh

# This script is used to bump the version of the SDK. It will also validate that
# the new version matches the SDK version in code (inngestgo.SDKVersion). Its
# only stdout is the next version, so the caller can capture it and assign to a
# variable.
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

BUMP=$1
if [ -z "${BUMP}" ]; then
    echo "Usage: $0 <major|minor|patch>"
    exit 1
fi
if [ "${BUMP}" != "major" ] && [ "${BUMP}" != "minor" ] && [ "${BUMP}" != "patch" ]; then
    echo "Usage: $0 <major|minor|patch>"
    exit 1
fi
print "Bump: ${BUMP}"

# Get the latest version.
git fetch --tags
LATEST_VERSION=$(git tag --sort=-v:refname | grep '^v' | head -n 1)
print "Current version: ${LATEST_VERSION}"

# Parse the latest version into major, minor, and patch.
IFS='.' read -r major minor patch <<< "${LATEST_VERSION#v}"
print "Parsed: major=${major}, minor=${minor}, patch=${patch}"

# Bump the version.
case "${BUMP}" in
    patch) patch=$((patch + 1));;
    minor) minor=$((minor + 1)); patch=0;;
    major) major=$((major + 1)); minor=0; patch=0;;
esac

# Create the next version.
NEXT_VERSION="v${major}.${minor}.${patch}"
print "Next version: ${NEXT_VERSION}"

# Validate the next version.
go run ${SCRIPTS_DIR}/validate_version/main.go "${NEXT_VERSION}"

# Output the next version, allowing the caller to capture it.
echo "${NEXT_VERSION}"
