#!/bin/bash


export PATH=$PATH:/opt/cni/bin/
export CNI_PATH=/opt/cni/bin/
export NETCONFPATH=/etc/cni/net.d/
export CNI_IFNAME=eth0
export CNI_ARGS="K8S_POD_NAME=redis-6fd6c6d6f9-fmrrv;K8S_POD_NAMESPACE=kubesphere-system;K8S_POD_INFRA_CONTAINER_ID=testcontainer"

ip netns add test
#cnitool $1 hostnic /var/run/netns/test