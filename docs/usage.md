# 安装使用hostnic-cni

## 使用k3s搭建环境

* 安装cni标准插件
```
cd /opt/cni/bin/
wget https://github.com/containernetworking/plugins/releases/download/v0.8.7/cni-plugins-linux-amd64-v0.8.7.tgz
tar xvzf cni-plugins-linux-amd64-v0.8.7.tgz
```

### 安装master节点

* 使用运行时docker
```
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
```
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
type: Opaque

```
* 安装hostnic
```
kubectl apply -f https://raw.githubusercontent.com/yunify/hostnic-cni/master/deploy/hostnic.yaml
```

## 使用hostnic

* 给节点加上vxnet注解
  `kubectl  annotate nodes i-xrwbww35  "hostnic.network.kubesphere.io/vxnet"="vxnet-cfn58ev"`

* 给工作负载指定vxnet
```
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
```
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