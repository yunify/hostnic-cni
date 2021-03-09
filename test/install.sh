#!/bin/sh

sysctl -w net.ipv4.conf.eth0.rp_filter=0
sysctl -w net.ipv4.conf.default.rp_filter=0
sysctl -w net.ipv4.conf.all.rp_filter=0

cp /mnt/GolandProjects/hostnic-cni/bin/hostnic  /opt/cni/bin/
cp /mnt/GolandProjects/hostnic-cni/test/test.conf /etc/cni/net.d/10-hostnic.conf