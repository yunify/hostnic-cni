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
kubectl rollout restart -n kube-system ds hostnic-node
```

hostnic-cfg-cm中包含两个配置大项

1. hostnic

用于配置hostnic-node daemonset

> poolHigh： hostnic空闲的网卡数量大于它， 多余的就会被释放
> 
> poolLow:  hostnic空闲的网卡数量小于它， hostnic会分配并挂载网卡达到poolLow
> 
> tag:  hostnic给创建的网卡打上此标签
> 
> maxNic: hostnic最多能分配的网卡， 达到此数之后Pod会创建失败
> 
> vxNets: 字符串数组。 一个vxnet最多能够创建252张网卡， hostnic根据maxNic将vxnet均分给node
> 
> sync:  由于网卡的绑定与卸载都是异步操作， 并且没有通知机制， 这里就定义一个轮询网卡相关Job的完成情况。默认值为3.

2. hostnic-cni

用于配置hostnic cni插件， 插件放在`/opt/cni/bin/hostnic`
>vethPrefix: hostnic创建的veth设备前缀
> 
>mtu: hostnic创建的veth设备的mtu值

## 使用hostnic

* 给节点加上vxnet注解

```bash
  kubectl  annotate nodes i-xrwbww35  "hostnic.network.kubesphere.io/vxnet"="vxnet-cfn58ev"`
```

* 给工作负载指定vxnet

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: testnginx
  annotations:
    hostnic.network.kubesphere.io/vxnet: vxnet-cfn58ev
  labels:
    env: test
spec:
  containers:
    - name: nginx
      image: nginx:latest
      tty: true
```

* 给工作负载指定IP

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: testnginx
  annotations:
    hostnic.network.kubesphere.io/vxnet: vxnet-cfn58ev
    hostnic.network.kubesphere.io/ip: 172.22.0.88
  labels:
    env: test
spec:
  containers:
    - name: nginx
      image: nginx:latest
      tty: true
```