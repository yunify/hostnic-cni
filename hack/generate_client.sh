#!/bin/bash

set -e

GV="network:v1alpha1 vxnetpool:v1alpha1"

rm -rf ./pkg/client
./hack/generate_group.sh "client,lister,informer" github.com/yunify/hostnic-cni/pkg/client github.com/yunify/hostnic-cni/pkg/apis "$GV" --output-base=./  -h "$PWD/hack/boilerplate.go.txt"
mv github.com/yunify/hostnic-cni/pkg/client ./pkg/
rm -rf ./github.com/yunify
