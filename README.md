# hostnic-cni

[![codebeat badge](https://codebeat.co/badges/33b711c7-0d90-4023-8bb1-db32ec32e4b7)](https://codebeat.co/projects/github-com-yunify-hostnic-cni-master)

[![Build Status](https://travis-ci.org/yunify/hostnic-cni.svg?branch=master)](https://travis-ci.org/yunify/hostnic-cni)

English|[中文](README_zh.md)

**hostnic-cni** is a [Container Network Interface](https://github.com/containernetworking/cni) plugin. This plugin will create a new nic by IaaS api and attach to host, then move the nic to container network namespace. Support IaaS :[QingCloud](http://qingcloud.com).

### Usage

1. Download CNI package from [CNI release page](https://github.com/containernetworking/cni/releases) and extract to /opt/cni/bin/.
1. Download hostnic from  [release page](https://github.com/yunify/hostnic-cni/releases) , and put hostnic to /opt/cni/bin/
1. Add cloud provider config

    ```bash
    cat >/etc/qingcloud/client.yaml <<EOF
    qy_access_key_id: "Your access key id"
    qy_secret_access_key: "Your secret access key"
    # your instance zone
    zone: "pek3a"
    EOF
    ```
    
1. Launch daemon process

    Daemon process is used as a nic manager which allocates and destroys nics in the background. It serves requests from hostcni and maintain nic info and nic cache pool.

    it accepts a few params. As listed below.

    ```bash
    [root@i-zwa7jztl bin]# ./daemon start -h
    hostnic-cni is a Container Network Interface plugin.

    This plugin will create a new nic by IaaS api and attach to host,
    then move the nic to container network namespace

    Usage:
      daemon start [flags]
    
    Flags:
          --CleanUpCacheOnExit        Delete cached nic on exit
          --PoolSize int              The size of nic pool (default 3)
          --QyAccessFilePath string   Path of QingCloud Access file (default "/etc/qingcloud/client.yaml")
      -h, --help                      help for start
          --vxnets stringSlice        ids of vxnet
    
    Global Flags:
          --bindAddr string     port of daemon process(e.g. socket port 127.0.0.1:31080 [fe80::1%lo0]:80 ) (default ":31080")
          --config string       config file (default is $HOME/.daemon.yaml)
          --loglevel string     daemon process log level(debug,info,warn,error) (default "info")
          --manageAddr string   addr of daemon monitor(e.g. socket port 127.0.0.1:31080 [fe80::1%lo0]:80 )  (default ":31081")

    ```

    e.g.

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

    The daemon process would fill nic pool with pre-allocated nics and wait until new request comes
    
1. Add cni config

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

### CNI config Description

* **ipam** add custom routing rules for nic, (optional)
* **bindaddr** server addr where daemon listens to

### Special notes for Kubernetes users

Hostnic may not work as expected when it is used with Kubernetes framework due to the constrains in the design of kubernetes. However, we've provided a work around to help users setup kubernetes cluster.

When a new service is defined in kubernetes cluster, it will get a cluster ip. And kube-proxy will maintain a port mapping tables on host machine to redirect service request to corresponding pod. And all of the network payload will be routed to host machine before it is sent to router and the service request will be handled correctly. In this way, kubernetes helps user achieve high availability of service. However, when the pod is attached to network directly(this is what hostnic did), Service ip is not recognied by router and service requests will not be processed.

So we need to find a way to redirect service request to host machine through vpc. Here we implemented a feature to write routing rules defined in network configuration to newly created network interface. And if the host machine doesn't have a nic which is under pod's subnet, you can just set gateway to 0.0.0.0 and network plugin will allocate a new nic which will be used as a gateway, and replace 0.0.0.0 with gateway's ip address automatically.
