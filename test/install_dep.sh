#!/bin/bash

#https://www.cni.dev/docs/cnitool/
#wget https://github.com/containernetworking/plugins/releases/download/v0.8.7/cni-plugins-linux-amd64-v0.8.7.tgz
go get github.com/containernetworking/cni
go install github.com/containernetworking/cni/cnitool

go get github.com/liderman/leveldb-cli