#!/bin/sh

sysctl -w net.ipv4.conf.eth0.rp_filter=0
sysctl -w net.ipv4.conf.default.rp_filter=0
sysctl -w net.ipv4.conf.all.rp_filter=0

cp /app/hostnic /opt/cni/bin/
cp /etc/hostnic/10-hostnic.conf /etc/cni/net.d/
