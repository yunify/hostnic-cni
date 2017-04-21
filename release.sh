#!/usr/bin/env bash

echo "Build hostnic"
mkdir -p bin/
env GOOS=linux GOARCH=amd64 go build -o bin/hostnic ./hostnic/
tar -C bin/ -czf bin/hostnic.tar.gz hostnic
