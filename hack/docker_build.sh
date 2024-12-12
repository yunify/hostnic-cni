#!/usr/bin/env bash

set -ex
set -o pipefail

# push to qingcloud with default latest tag
REPO="$1"
REPO=${REPO:-qingcloud}
TAG="$2"

# hostnic
docker build -f build/hostnic/Dockerfile -t $REPO/hostnic-plus:$TAG .
docker push $REPO/hostnic-plus:$TAG
# print the full docker image path for your convience
docker images --digests | grep $REPO/hostnic-plus | grep $TAG | awk '{print $1":"$2"@"$3}'

# webhook
docker build -f build/webhook/Dockerfile -t $REPO/hostnic-webhook:$TAG .
docker push $REPO/hostnic-webhook:$TAG
# print the full docker image path for your convience
docker images --digests | grep $REPO/hostnic-webhook | grep $TAG | awk '{print $1":"$2"@"$3}'
