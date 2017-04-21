# hostnic-cni

English|[中文](README_zh.md)

**hostnic-cni** is a [Container Network Interface](https://github.com/containernetworking/cni) plugin. This plugin will create a new nic by IaaS api and attach to host, then move the nic to container network namespace. Support IaaS :[QingCloud](http://qingcloud.com).



### Usage

1. Download hostnic from  [release page](https://github.com/yunify/hostnic-cni/releases) , and put to /opt/cni/bin/
2. Add cni config

```bash

cat >/etc/cni/net.d/10-hostnic.conf <<EOF
{
    "cniVersion": "0.3.0",
    "name": "hostnic",
    "type": "hostnic",
    "provider": "qingcloud",
    "providerConfigFile":"/etc/qingcloud/client.yaml",
    "vxNets":["vxnet-xxxxx","vxnet-xxxx"],
    "isGateway": true
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

