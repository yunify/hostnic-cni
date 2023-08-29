package main

import (
	"context"
	"flag"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

const (
	topoKey = "topology.kubernetes.io/hostmachine"
)

func getHostForNode(addrs []corev1.NodeAddress, nodes []*rpc.Node) string {
	for _, addr := range addrs {
		if addr.Type == corev1.NodeInternalIP {
			for _, node := range nodes {
				if addr.Address == node.PrivateIP {
					return node.HostMachine
				}
			}
		}
	}

	return ""
}

func patchNode(k8sClient *kubernetes.Clientset, host string, node corev1.Node) error {
	if node.Labels[topoKey] == host {
		return nil
	}

	copy := node.DeepCopy()
	copy.Labels[topoKey] = host
	_, err := k8sClient.CoreV1().Nodes().Update(context.TODO(), copy, metav1.UpdateOptions{})
	return err
}

func handleNode(clusterID string, k8sClient *kubernetes.Clientset) error {
	qcNodes, err := qcclient.QClient.DescribeClusterNodes(clusterID)
	if err != nil {
		klog.Errorf("get cluster %s nodes failed: %v", clusterID, err)
		return err
	}

	k8sNodes, err := k8sClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		klog.Errorf("list k8s nodes failed: %v", err)
		return err
	}

	for _, node := range k8sNodes.Items {
		if host := getHostForNode(node.Status.Addresses, qcNodes); host != "" {
			if err := patchNode(k8sClient, host, node); err != nil {
				klog.Errorf("patch %s failed: %v", topoKey, err)
			} else {
				klog.Infof("patch node %s with hostmachine %s success", node.Name, host)
			}
		} else {
			klog.Errorf("get host for node %s failed", node.Name)
		}
	}
	return nil
}

var clusterID, masterURL, kubeconfig string

func main() {
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to a kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&masterURL, "master", "", "The address of the Kubernetes API server. Overrides any value in kubeconfig. Only required if out-of-cluster.")
	flag.StringVar(&clusterID, "clusterID", "", "clusterID")
	flag.Parse()

	if !strings.HasPrefix(clusterID, "cl-") {
		// use clusterid read from /etc/kubernetes/qingcloud.yaml
		klog.Infof("invalid clusterID %s, try to get clusterID from volumed cluterConfig %s", clusterID, constants.DefaultClusterConfigPath)

		clusterConfig, err := conf.TryLoadClusterConfFromDisk(constants.DefaultClusterConfigPath)
		if err != nil || clusterConfig == nil {
			klog.Fatalf("Error building clusterConfig: %s", err.Error())
		}
		clusterID = clusterConfig.ClusterID
		klog.Infof("get clusterID success: %s", clusterID)

	}

	// setup qcclient, k8s
	qcclient.SetupQingCloudClient(qcclient.Options{})

	cfg, err := clientcmd.BuildConfigFromFlags("", "")
	if err != nil {
		klog.Fatalf("Error building kubeconfig: %v", err)
	}

	k8sClient, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		klog.Fatalf("Error building kubernetes clientset: %v", err)
	}

	if err := handleNode(clusterID, k8sClient); err != nil {
		klog.Infof("handle k8s node failed: %v", err)
	} else {
		klog.Infof("handle k8s node success")
	}
	klog.Flush()
}
