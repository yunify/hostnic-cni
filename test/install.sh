#!/bin/sh

cp /mnt/GolandProjects/hostnic-cni/bin/hostnic  /opt/cni/bin/
cp /mnt/GolandProjects/hostnic-cni/test/test.conf /etc/cni/net.d/10-hostnic.conf