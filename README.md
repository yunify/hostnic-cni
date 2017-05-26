# hostnic-cni

English|[中文](README_zh.md)

**hostnic-cni** is a [Container Network Interface](https://github.com/containernetworking/cni) plugin. This plugin will create a new nic by IaaS api and attach to host, then move the nic to container network namespace. Support IaaS :[QingCloud](http://qingcloud.com).



### Usage

1. Download CNI package from [CNI release page](https://github.com/containernetworking/cni/releases) and extract to /opt/cni/bin/.
2. Download hostnic from  [release page](https://github.com/yunify/hostnic-cni/releases) , and put to /opt/cni/bin/
3. Add cni config

```bash

cat >/etc/cni/net.d/10-hostnic.conf <<EOF
{
    "cniVersion": "0.3.0",
    "name": "hostnic",
    "type": "hostnic",
    "provider": "qingcloud",
    "args": {
      "providerConfigFile":"/etc/qingcloud/client.yaml",
      "vxNets":["vxnet-xxxxx","vxnet-xxxx"]
    },
    "ipam":{
      "routes":[{"dst":"kubernetes service cidr","gw":"hostip or 0.0.0.0"}]
    },
    "isGateway": true
}
EOF

cat >/etc/cni/net.d/99-loopback.conf <<EOF
{
	"cniVersion": "0.2.0",
	"type": "loopback"
}
EOF
```
3. Add cloud provider config

```bash
cat >/etc/qingcloud/client.yaml <<EOF
qy_access_key_id: "Your access key id"
qy_secret_access_key: "Your secret access key"
# your instance zone
zone: "pek3a"
EOF
```
### CNI config Description
* **provider** IaaS provider, current only support qingcloud
* **providerConfigFile** IaaS provider api config
* **vxNets** nic vxnet, support multi, all vxnet should in same vpc.
* **ipam** add custom routing rules for nic, (optional)

### Special notes for Kubernetes users
Hostnic may not work as expected when it is used with Kubernetes framework due to the constrains in the design of kubernetes. However, we've provided a work around to help users setup kubernetes cluster.

When a new service is defined in kubernetes cluster, it will get a cluster ip. And kube-proxy will maintain a port mapping tables on host machine to redirect service request to corresponding pod. And all of the network payload will be routed to host machine before it is sent to router and the service request will be handled correctly. In this way, kubernetes helps user achieve high availability of service. However, when the pod is attached to network directly(this is what hostnic did), Service ip is not recognied by router and service requests will not be processed.

So we need to find a way to redirect service request to host machine through vpc. Here we implemented a feature to write routing rules defined in network configuration to newly created network interface. And if the host machine doesn't have a nic which is under pod's subnet, you can just set gateway to 0.0.0.0 and network plugin will allocate a new nic which will be used as a gateway, and replace 0.0.0.0 with gateway's ip address automatically.
