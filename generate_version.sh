#!/bin/sh
# Define the project string to be used
PROJECT=hostnic-cni

# Use `git describe` to get parts for version information and state
GIT_DESCRIBE=$(git describe --long --dirty --tags)
if [[ $? != 0 ]]; then
  echo "No tags!"
  exit
fi

# Parse the output of `git describe`
# VERSION: 	The latest `git tag`
# COMMITS: 	The number of commits ahead workspace is of `git tag`
# SHA1: 	Unique SHA1 reference of current commit state
# DIRTY: 	Whether this workspace contains uncommitted / local changes
IFS='-' read -a myarray <<< "$GIT_DESCRIBE"

VERSION=${myarray[0]}
COMMITS=${myarray[1]}
SHA1=${myarray[2]}

if [ -z ${myarray[3]} ]; then
  DIRTY=false
else
  DIRTY=true
fi

if [[ $(git ls-files --directory --exclude-standard --others -t) ]]; then
    DIRTY=true
fi

# Which branch was the git workspace tracking
BRANCH=$(git rev-parse --abbrev-ref HEAD)

# Generate a well known label based on branch
# Unknown branches will go by their SHA1
case $BRANCH in
  master)
    LABEL="ga"
    ;;
  develop)
    LABEL="dev"
    ;;
  release/*)
    LABEL="rc"
    ;;
  *)
    LABEL=$SHA1
esac

# Generate a label to represent this build
# Combination of various information about the git workspace
# Can be used for a filename of the package
BUILD_LABEL="${PROJECT}-${VERSION}-${COMMITS}-${LABEL}"

if [ "$DIRTY" = true ]; then
  BUILD_LABEL=${BUILD_LABEL}-dirty
fi

# Generate a build date if was not passed into the env or present in parent env
BUILD_DATE=${BUILD_DATE:=$(date -u +%Y%m%d.%H%M%S)}

# Set the variables to be outputted
# All vars that begin with the prefix will be outputted
# Allows callers of the script to inject new vars through ENV
# e.g. Jenkins can set EXPORT_JENKINS_SERVER=build-server
VAR_PREFIX=EXPORT_VAR_

EXPORT_VAR_GIT_DESCRIBE="$GIT_DESCRIBE"
EXPORT_VAR_BRANCH="$BRANCH"
EXPORT_VAR_LABEL="$LABEL"
EXPORT_VAR_VERSION="$VERSION"
EXPORT_VAR_COMMITS="$COMMITS"
EXPORT_VAR_GIT_SHA1="$SHA1"
EXPORT_VAR_DIRTY="$DIRTY"
EXPORT_VAR_BUILD_LABEL="$BUILD_LABEL"
EXPORT_VAR_BUILD_DATE="$BUILD_DATE"


# Get a list of all ENV vars and internal defined vars that begin with prefix
VAR_LIST=$(eval "set -o posix ; set | grep '^$VAR_PREFIX'")

# Generate a JSON document for version_info containing the vars
# Using HERE documents to allow pretty printing
JSON=$(cat <<EOF
{
    "version_info": {
EOF
)

# Generate key value pair for each exported var
# Make the keys lower case
while IFS='=' read -r name value ; do
JSON+="
"
JSON+="        "
# Strip prefix and make lower case
JSON+=\"$(echo ${name#$VAR_PREFIX} | tr '[:upper:]' '[:lower:]')\"
# Value of variable, same as $value also
JSON+=": \"${!name}\","
done <<< "$VAR_LIST"

# Eliminate trailing comma
# Only trim the comma from last matching line, JSON var contains newlines
JSON=$(echo "$JSON" | sed '$ s/,$//')

# End the JSON document
JSON+=$(cat <<-EOF

    }
}
EOF
)

# Based on parameter, output key value pairs or JSON document
case "$1" in
    keyvalue)

    # For each exported var, output key value pair
    # Allows sourcing the output to be used in Makefile
    while IFS='=' read -r name value ; do
    	# Strip prefix
        echo "${name#$VAR_PREFIX}=${!name}"
    done <<< "$VAR_LIST"

    # Also output the JSON document in single line format for completeness
    echo VERSION_INFO_JSON=\'$JSON\'
    ;;

    json)
    # Output the JSON variable, wrap in quotes to preserve newlines
    echo "$JSON"
    ;;

    *)
    echo "Usage: $0 [keyvalue|json]"
    exit 1
    ;;
esac