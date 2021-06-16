# HostNIC 原理
hostnic 是一个符合CNI(https://github.com/containernetworking/cni)规划的插件，能够被kubelet调用为一个Pod创建网络。

## HostNIC 软件架构
![软件架构](arch.svg)

hostnic由两部分组成，一个是符合CNI规范的命令行工具`hostnic`，一个是用于IPAM的GRPC服务`hostnic-daemon`。

`hostnic`实现了CNI，每次创建Pod或者删除Pod，kubelet都会调用这个二进制文件，而这个二进制文件会向Daemon通过GRPC的方式请求IP或者删除IP，如果请求成功，就会在本地执行一些网络规则的创建和删除，包括使用路由策略，iptables等。每个node上都会有一个hostnic二进制文件
`hostnic-daemon`是数据中心，，主要负责本地的IPAM，并不是全局的IPAM。它主要的工作有以下：
1. 负责向iaas申请/卸载网卡，并把网卡加入IP池或从IP池中删除。
2. 响应GRPC请求，正确地赋予/删除Pod IP
3. 不断与k8s api server和IAAS server同步，保证Pod IP的正确性
4. 启动时动态写入CNI config文件，根据用户的设定调整hostnic插件的环境变量

## 通信原理
k8s对于CNI插件有以下要求：
1. Pod 和Pod通信要在无NAT的情况下能够互相通信，也能互相看到对方正确的IP
2. Pod 和Node也要能在无NAT的情况下互相通信，并且互相能够看到正确的IP
3. 支持Hostnetwork

hostnic插件是基于上述原则进行设计的，网络的架构如下：
![网络架构](pod.png)
如图中所示，首先会将IAAS的网卡挂载到主机上，然后在主机端将其IP移除（这是Pod 所用，主机端有这个IP会导致回路不正确）。每当有一个Pod 需要IP时，hostnic插件会做如下操作：
1. 向Daemon获取一个IP信息（包括ip，mac，以及对应的namespace等）
2. 启动对应的网卡（同样要再删除一遍IP）
3. 创建一对veth，一端在root namespace，一端在Pod namespace里
4. 在Pod namespace中，创建默认路由，并且指定静态arp，最终网络如下：
   ```bash
    #在Pod内部的网络
    IP address

    # ip addr show
    1: lo: <LOOPBACK,UP,LOWER_UP> mtu 65536 qdisc noqueue state UNKNOWN qlen 1
    link/loopback 00:00:00:00:00:00 brd 00:00:00:00:00:00
    inet 127.0.0.1/8 scope host lo
        valid_lft forever preferred_lft forever
    inet6 ::1/128 scope host 
        valid_lft forever preferred_lft forever
    2: eth0@if123: <BROADCAST,MULTICAST,UP,LOWER_UP,M-DOWN> mtu 1500 qdisc noqueue state UP 
    link/ether 56:41:95:26:17:41 brd ff:ff:ff:ff:ff:ff
    inet 172.22.0.239  scope global eth0 <<<<<<< 对应的网卡地址
        valid_lft forever preferred_lft forever
    inet6 fe80::5441:95ff:fe26:1741/64 scope link 
        valid_lft forever preferred_lft forever
    路由

    # ip route show
    default via 169.254.1.1 dev eth0  # 所有的Pod都是用的这个magic ip，参考的calico
    169.254.1.1 dev eth0 

    static arp

    # arp -a
    ? (169.254.1.1) at 2a:09:74:cd:c4:62 [ether] PERM on eth0   这个就是veth另外一端(主机侧)的mac
   ```

### From Pod
每个Pod都对应一个独立的路由表(路由表号保证唯一，默认从260开始分配)， 路由表里存在两个表项：默认路由与本地链路路由
```shell
root@node2:~# ip route show table 264
default via 172.22.0.1 dev hostnic_264
172.22.0.0/24 dev hostnic_264 scope link
```
当数据包从Pod出来之后， 经过veth设备到达host network之后， 通过策略路由来控制数据包从上述路由表中查找路由
```bash
root@node2:~# ip rule
1536:   from 172.22.0.247 lookup 261
```

### To Pod
节点中所有Pod的路由都放在同一张路由表中， 默认为main表， 当数据包目的地址为本节点Pod时， 通过策略路由来控制数据包从main表中查找路由
```bash
root@node2:~# ip rule
1535:   from all to 172.22.0.247 lookup main
```
这里可以看到`To Pod`的策略路由优先级高于`From Pod`的策略路由优先级， 这么做是为了做到同节点Pod之间数据不会离开主机。

### hairpin模式

开启hairpin模式时， 同节点的Pod之间的流量都会离开主机， 目前只有kube-proxy为iptables模式时才支持hairpin。 

此模式下， hostnic会在iptables mangle表中加入以下四条规则，用于mark相关数据包，用于后续策略路由控制, 其中172.22.0.22为节点IP地址， 10.233.0.0/18为service网络CIDR
```shell
root@node2:~# iptables -t mangle -S
-A PREROUTING -j HOSTNIC-PREROUTING
-A OUTPUT -j HOSTNIC-OUTPUT
-A HOSTNIC-OUTPUT -m conntrack --ctorigdst 10.233.0.0/18 -j MARK --set-xmark 0x10000/0x10000
-A HOSTNIC-PREROUTING -m conntrack --ctorigdst 172.22.0.22 -j MARK --set-xmark 0x10000/0x10000
```

此外， 需要添加的路由策略如下
```shell
root@node2:~# ip rule
#hostnetwork访问pod
1535:   from all to 172.22.0.247 fwmark 0x10000/0x10000 lookup main 
#Pod访问Service，刚好Service对应的后端Pod为自己， 此时流量不出主机， 不然数据包会存在环路
1535:   from 172.22.0.247 to 172.22.0.247 lookup main
1535:   from all to 172.22.0.247 iif hostnic_261 lookup main  #控制进入Pod的流量
#NodePort访问Pod， NodePort主机与Pod所在节点相同，此时从Pod返回的流量需要查找main表，从主网卡出去
1536:   from 172.22.0.247 fwmark 0x10000/0x10000 lookup main
1536:   from 172.22.0.247 iif vnicfc986c38a13 lookup 261  #控制从Pod出来的流量
```

## IPAM

为了加快Pod获取的速度，`hostnic-daemon`会预先申请一些网卡，同样在Pod删除之后，也会将多余的网卡归还给IAAS
