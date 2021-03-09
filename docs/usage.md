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

- poolHigh： hostnic空闲的网卡数量大于它， 多余的就会被释放
- poolLow:  hostnic空闲的网卡数量小于它， hostnic会分配并挂载网卡达到poolLow
- tag:  hostnic给创建的网卡打上此标签
- maxNic: hostnic最多能分配的网卡， 达到此数之后Pod会创建失败
- vxNets: 字符串数组。 一个vxnet最多能够创建252张网卡， hostnic根据maxNic将vxnet均分给node
- sync:  由于网卡的绑定与卸载都是异步操作， 并且没有通知机制， 这里就定义一个轮询网卡相关Job的完成情况。默认值为3.

2. hostnic-cni

用于配置hostnic cni插件， 插件放在`/opt/cni/bin/hostnic`

- vethPrefix: hostnic创建的veth设备前缀 （默认为vnic， 如无必要不需要修改）
- mtu: hostnic创建的veth设备的mtu值（默认为1500， 如无必要不需要修改）
- serviceCIDR: kubernetes集群service网络地址段， 必填字段，根据集群网络规划填写
- hairpin:  设置同节点pod流量是否发送发送hyper之后再绕回，这样同节点pod流量可以再hyper上采集，同时也可以收到IAAS安全组管控。（默认值为false， 只有kube-proxy模式为iptables时才能将hairpin设置为true）

> Note：启用hairpin模式时，**需要重启节点**， 并且有以下三种情况， 同节点pod流量并不会发往hyper再绕回
> - pod访问同节点的hostnetwork pod
> - pod通过service ip访问后端pod， 同时lb选择的后端pod为同节点的hostnetwork pod
> - pod通过service ip访问后端pod， 同时lb选择的后端pod为自身pod

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

* hairpin模式验证

1. 创建测试客户端`kubectl run multitool --image=praqma/network-multitool --replicas=1`
2. 创建测试服务端`kubectl run nginx --image=nginx:latest --replicas=2; kubectl expose deployment nginx --port 80`
3. 最终pod分布如下， 服务端分布在不同节点
   ![image](https://user-images.githubusercontent.com/3678855/110727895-0904b080-8257-11eb-88d0-7fc6786aa122.png)
4. 在客户端所在节点执行`ip rule | grep ${PodIP} | grep hostnic_`找到客户端挂载的虚拟网卡
   ![image](https://user-images.githubusercontent.com/3678855/110728439-0ce50280-8258-11eb-8ed2-8d19269ccf45.png)

5. 开启另外一个终端在客户端所在节点执行`tcpdump -s0 -nn -i ${vnic}`抓取客户端流量（开启hairpin模式的话，那么客户端访问两个服务端，不论是否在同节点， 客户端进出流量都会经过虚拟网卡）
6. 在客户端执行`curl ${svcIP}`（可多次执行， service负载均衡可能短时间都发往同一后端pod）， 然后观察上一步tcpdump抓取的流量是否包含同节点pod的流量，预期结果应如下图
   ![image](https://user-images.githubusercontent.com/3678855/110728988-e8d5f100-8258-11eb-8880-4e92a708461f.png)



