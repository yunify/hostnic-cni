#!/bin/sh
 
function CleanUp() {
    echo "===== Deleting HOSTNIC-AGENT ==========="
    rm -f /host/opt/cni/bin/hostnic
    rm -f  /host/etc/cni/net.d/10-hostnic.conf 
    rm -f /host/etc/cni/net.d/99-loopback.conf
}

trap CleanUp EXIT SIGINT SIGQUIT

echo "===== Starting installing HOSTNIC-CNI ========="

cp /app/hostnic /host/opt/cni/bin/
cp /app/10-hostnic.conf /host/etc/cni/net.d/
cp /app/99-loopback.conf /host/etc/cni/net.d/


echo "===== Starting HOSTNIC-AGENT ==========="
/app/hostnic-agent -v=2


