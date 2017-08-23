# hostnic-cni

[English](README.md)|中文

**hostnic-cni** 是一个 [Container Network Interface](https://github.com/containernetworking/cni) 插件。 本插件会直接调用 IaaS 的接口去创建网卡并且关联到容器的网络命名空间。当前支持的 IaaS：[QingCloud](http://qingcloud.com)，未来会支持更多。

### 使用说明

1. 从 [CNI release 页面](https://github.com/containernetworking/cni/releases)  下载 CNI 包，解压到 /opt/cni/bin 下。
1. 从 [release 页面](https://github.com/yunify/hostnic-cni/releases) 下载 hostnic 放置到 /opt/cni/bin/ 路径下。
1. 增加 IaaS 的 sdk 配置文件

    ```bash
    cat >/etc/qingcloud/client.yaml <<EOF
    qy_access_key_id: "Your access key id"
    qy_secret_access_key: "Your secret access key"
    # your instance zone
    zone: "pek3a"
    EOF
    ```
    
1. 启动后台进程

    后台进程主要负责后台异步的进行网卡的申请销毁。给hostnic程序提供服务。它监听本地端口，维护网卡信息，并管理缓存网卡池
    启动后台进程需要如下参数

    ```bash
    [root@i-zwa7jztl hostnic-cni]# bin/daemon help start
    hostnic-cni is a Container Network Interface plugin.

    This plugin will create a new nic by IaaS api and attach to host,
    then move the nic to container network namespace

    Usage:
      daemon start [flags]

    Flags:
          --PoolSize int              网卡缓冲池大小(default 3)
          --QyAccessFilePath string   青云api key配置文件 (default "/etc/qingcloud/client.yaml")
          --bindAddr string           后台进程监听端口(e.g. socket port 127.0.0.1:31080 [fe80::1%lo0]:80 ) (default ":31080")
      -h, --help                      help for start
          --vxnets stringSlice        网卡使用的私网id列表

    Global Flags:
          --config string     config file (default is $HOME/.daemon.yaml)
          --loglevel string   后台进程日志级别(debug,info,warn,error) (default "info")

    ```

    例如

    ```bash
    ./bin/daemon start --bindAddr :31080 --vxnets vxnet-xxxxxxx,vxnet-xxxxxxx --PoolSize 3 --loglevel debug
    INFO[0000] Collect existing nic as gateway cadidate     
    DEBU[0000] Found nic 52:54:03:41:e9:16 on host          
    DEBU[0000] Found nic 52:54:20:82:68:5c on host          
    DEBU[0000] Found nic 52:54:0b:48:04:52 on host          
    INFO[0000] Found following nic as gateway               
    INFO[0000] vxnet: vxnet-oca1g0z gateway: 192.168.4.253  
    INFO[0000] vxnet: vxnet-oilq879 gateway: 192.168.3.251  
    INFO[0000] vxnet: vxnet-2n6g6gx gateway: 192.168.0.3    
    DEBU[0002] start to wait until channel is not full.     
    DEBU[0002] put 52:54:27:6b:17:65 into channel           
    DEBU[0007] start to wait until channel is not full.     
    DEBU[0007] put 52:54:57:83:d0:ab into channel           
    DEBU[0011] start to wait until channel is not full.     
    DEBU[0011] put 52:54:d6:86:46:d6 into channel           
    DEBU[0015] start to wait until channel is not full.   
    ```

    后台进程会将缓冲池充满，并等待请求到来。

1. 增加 cni 的配置

    ```bash
    cat >/etc/cni/net.d/10-hostnic.conf <<EOF
    {
        "cniVersion": "0.3.1",
        "name": "hostnic",
        "type": "hostnic",
        "bindaddr":"localhost:31080",
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



### 配置说明

* **ipam** 给nic设置路由条目。（可选）

### kubernetes用户的说明

kubernetes管理集群时会给每一个服务分配一个集群ip地址。kube-proxy会负责服务负载均衡，由于默认的方案所有pod的网络请求通过主机的网卡转发后才会进入路由器。但给pod单独分配网卡后，流量不再经过kube-proxy，所有调用服务的请求会失败。这里我们提供设置路由的功能来解决这个问题。pod分配网卡后，用户可以指定将集群ip网段的请求转发给虚拟机，交由kube-proxy去处理。由于网关必须满足一定条件，用户可以指定网关为0.0.0.0，插件会自动寻找可用网管，如果都不满足条件，会自动分配一个。
