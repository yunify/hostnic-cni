# 安装使用hostnic-cni

## 使用k3s搭建环境

* 安装cni标准插件
```
cd /opt/cni/bin/
wget https://github.com/containernetworking/plugins/releases/download/v0.8.7/cni-plugins-linux-amd64-v0.8.7.tgz
tar xvzf cni-plugins-linux-amd64-v0.8.7.tgz
```

### 安装master节点

```
iptables -t filter -P FORWARD ACCEPT
curl https://releases.rancher.com/install-docker/19.03.sh | sh
curl -sfL https://get.k3s.io | INSTALL_K3S_EXEC='--flannel-backend=none --no-flannel' sh -s - --docker
```

* 卸载k3s master

```
/usr/local/bin/k3s-uninstall.sh
rm -fr /var/lib/hostnic/*
```

### 添加agent节点

```
iptables -t filter -P FORWARD ACCEPT
curl https://releases.rancher.com/install-docker/19.03.sh | sh
curl -sfL https://get.k3s.io | K3S_URL=https://172.22.0.7:6443 K3S_TOKEN=K1046198f1051abcb37faeecf2cf92de4b5fc917ebe6f44c20500a4ccbc55b4be59::server:bfbfe223034c7944aac3ed622ec345ef sh -s - --docker
```

以上K3S_URL替换为前面创建的master节点IP地址信息

以上K3S_TOKEN替换为master节点内容 `cat /var/lib/rancher/k3s/server/node-token`

* 卸载k3s agent

```
/usr/local/bin/k3s-agent-uninstall.sh
rm -fr /var/lib/hostnic/*
```

## 安装hostnic

* 安装qingcloud IAAS secret
```bash
cat <<EOF | kubectl apply -f -
apiVersion: v1
stringData:
  config.yaml: |-
    qy_access_key_id: 'xxxxx'
    qy_secret_access_key: 'xxxxx'
    # your instance zone
    zone: "ap2a"
kind: Secret
metadata:
  name: qcsecret
  namespace: kube-system
type: Opaque
EOF
```

* 安装hostnic

```bash
kubectl apply -f https://raw.githubusercontent.com/yunify/hostnic-cni/master/deploy/hostnic.yaml
```

* 配置hostnic

```bash
kubectl edit -n kube-system cm hostnic-cfg-cm
kubectl edit vxnetpool v-pool
kubectl edit -n kube-system cm hostnic-ipam-config
kubectl rollout restart -n kube-system ds hostnic-node
```

hostnic-cfg-cm中包含两个配置大项

1. hostnic

用于配置hostnic-node daemonset

- tag:  hostnic给创建的网卡打上此标签
- maxNic: hostnic最多能分配的网卡， 达到此数之后Pod会创建失败
- sync:  由于网卡的绑定与卸载都是异步操作， 并且没有通知机制， 这里就定义一个轮询网卡相关Job的完成情况。默认值为3.

2. hostnic-cni

用于配置hostnic cni插件， 插件放在`/opt/cni/bin/hostnic`

- vethPrefix: hostnic创建的veth设备前缀 （默认为vnic， 如无必要不需要修改）
- mtu: hostnic创建的veth设备的mtu值（默认为1500， 如无必要不需要修改）
- serviceCIDR: kubernetes集群service网络地址段， 必填字段，根据集群网络规划填写

hostnic-ipam-config中包含两个配置大项

1. subnet-auto-assign

用于配置是否开启自动映射，如果关闭则要手动指定 ipam 中的映射关系；如果开启，则由controller自动进行分配。

2. ipam

用于配置namespace与subnet的映射关系

- Default: 仅当关闭自动映射，且找不到namespace与subnets的映射关系时使用，这时ipam由配置中的vxnet分配ip
- 其他配置: namespace与subnets的映射关系，ipam找到subnets后，会遍历subnets直到分配出ip
- 说明: 一个subnet只能分配给一个namespace，不能分配给多个namespace

## 使用hostnic

* 查看vxnetpool，controller会将vxnet拆分为subnet，ipam通过pod的namespace对应的subnet进行ip分配

```bash
# kubectl get vxnetpool v-pool -oyaml
apiVersion: network.qingcloud.com/v1alpha1
kind: VxNetPool
metadata:
  name: v-pool
spec:
  blockSize: 26
  vxnets:
  - name: vxnet-nj02rbu
  - name: vxnet-vfpl1e1
  - name: vxnet-kuusp12
  - name: vxnet-cwjk6xr
  - name: vxnet-1pcig5z
status:
  pools:
  - ippool: vxnet-nj02rbu
    name: vxnet-nj02rbu
    subnets:
    - 4100-172-16-4-0-26
    - 4100-172-16-4-128-26
    - 4100-172-16-4-192-26
    - 4100-172-16-4-64-26
  - ippool: vxnet-vfpl1e1
    name: vxnet-vfpl1e1
    subnets:
    - 4100-172-16-3-0-26
    - 4100-172-16-3-128-26
    - 4100-172-16-3-192-26
    - 4100-172-16-3-64-26
  - ippool: vxnet-kuusp12
    name: vxnet-kuusp12
    subnets:
    - 4100-172-16-2-0-26
    - 4100-172-16-2-128-26
    - 4100-172-16-2-192-26
    - 4100-172-16-2-64-26
  - ippool: vxnet-cwjk6xr
    name: vxnet-cwjk6xr
    subnets:
    - 4100-172-16-1-0-26
    - 4100-172-16-1-128-26
    - 4100-172-16-1-192-26
    - 4100-172-16-1-64-26
  - ippool: vxnet-1pcig5z
    name: vxnet-1pcig5z
    subnets:
    - 4100-172-16-5-0-26
    - 4100-172-16-5-128-26
    - 4100-172-16-5-192-26
    - 4100-172-16-5-64-26
  ready: true
```

* 查看集群中ipam信息

```bash
# kubectl exec -it hostnic-node-pdpgd -n kube-system -- sh
   /app # cd tools/
   /app/tools # ls
   hostnic-client  ipam-client     patch-node      vxnet-client
   /app/tools # ./ipam-client
   W0824 11:28:49.673855      30 client_config.go:615] Neither --kubeconfig nor --master was specified.  Using the inClusterConfig.  This might not work.
GetUtilization:
   vxnet-my0q9ed: Capacity 256, Unallocated 241, Allocate   0, Reserved  15
      4100-172-22-11-0-27: Capacity  32, Unallocated  30, Allocate   0, Reserved   2
      4100-172-22-11-128-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
      4100-172-22-11-160-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
      4100-172-22-11-192-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
      4100-172-22-11-224-27: Capacity  32, Unallocated  19, Allocate   0, Reserved  13
         4100-172-22-11-32-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
         4100-172-22-11-64-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
         4100-172-22-11-96-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
   vxnet-xpxclb7: Capacity 256, Unallocated 213, Allocate  28, Reserved  15
      4100-172-22-13-0-27: Capacity  32, Unallocated  30, Allocate   0, Reserved   2
      4100-172-22-13-128-27: Capacity  32, Unallocated  28, Allocate   4, Reserved   0
      4100-172-22-13-160-27: Capacity  32, Unallocated  20, Allocate  12, Reserved   0
      4100-172-22-13-192-27: Capacity  32, Unallocated  25, Allocate   7, Reserved   0
      4100-172-22-13-224-27: Capacity  32, Unallocated  16, Allocate   3, Reserved  13
         4100-172-22-13-32-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
         4100-172-22-13-64-27: Capacity  32, Unallocated  30, Allocate   2, Reserved   0
         4100-172-22-13-96-27: Capacity  32, Unallocated  32, Allocate   0, Reserved   0
GetSubnets: autoSign[on]
   kube-node-lease: [4100-172-22-13-32-27]
   kubesphere-system: [4100-172-22-13-128-27]
   t1: [4100-172-22-13-224-27]
   kube-public: [4100-172-22-11-0-27]
   kube-system: [4100-172-22-13-192-27]
   kubesphere-controls-system: [4100-172-22-13-64-27]
   kubesphere-monitoring-federated: [4100-172-22-13-96-27]
   kubesphere-monitoring-system: [4100-172-22-13-160-27]
   default: [4100-172-22-13-0-27]
FreeSubnets: [4100-172-22-11-224-27 4100-172-22-11-64-27 4100-172-22-11-160-27 4100-172-22-11-32-27 4100-172-22-11-96-27 4100-172-22-11-128-27 4100-172-22-11-192-27]
```
