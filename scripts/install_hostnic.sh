#!/bin/sh
set -e
echo "===== Starting installing HOSTNIC-CNI ========="

cp /app/hostnic /host/opt/cni/bin/
cp /app/10-hostnic.conf /host/etc/cni/net.d/
cp /app/99-loopback.conf /host/etc/cni/net.d/


echo "===== Starting HOSTNIC-AGENT ==========="
/app/hostnic-agent
