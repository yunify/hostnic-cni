# hostnic-cni

[English](README.md)|中文

**hostnic-cni** 是一个 [Container Network Interface](https://github.com/containernetworking/cni) 插件。 本插件会直接调用 IaaS 的接口去创建网卡并且关联到容器的网络命名空间。当前支持的 IaaS：[QingCloud](http://qingcloud.com)，未来会支持更多。

### 使用说明

1. 从 [CNI release 页面](https://github.com/containernetworking/cni/releases)  下载 CNI 包，解压到 /opt/cni/bin 下。
2. 从 [release 页面](https://github.com/yunify/hostnic-cni/releases) 下载 hostnic 放置到 /opt/cni/bin/ 路径下。
3. 增加 cni 的配置

```bash

cat >/etc/cni/net.d/10-hostnic.conf <<EOF
{
    "cniVersion": "0.3.0",
    "name": "hostnic",
    "type": "hostnic",
    "provider": "qingcloud",
    "args": {
      "QyAccessKeyID": "TZKPBMMIPQITZSWTECKD",
      "QySecretAccessKey": "biST961HwPb5ZL7KdWTMeHmIf1v02VjTsK33hytB",
      "zone": "pek3a",
      "vxNets":["vxnet-oilq879"],
      "isGateway": true
    }
}

EOF

cat >/etc/cni/net.d/99-loopback.conf <<EOF
{
	"cniVersion": "0.2.0",
	"type": "loopback"
}
EOF
```
### 配置说明
* **provider** IaaS 的提供方，当前只支持 qingcloud，未来会支持更多。
* **vxNets** nic 所在的私有网络，数组格式，支持多个，多个私有网络必须在同一个 vpc 下。
