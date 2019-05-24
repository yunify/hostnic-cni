package k8sclient

import (
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	corev1informer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
)

const (
	NodeNameEnvKey = "MY_NODE_NAME"
)

type K8sHelper interface {
	Start(stopCh <-chan struct{})
	GetCurrentNode() (*corev1.Node, error)
}

type k8sHelper struct {
	nodeName     string
	nodeInformer corev1informer.NodeInformer
}

func (k *k8sHelper) GetCurrentNode() (*corev1.Node, error) {
	return k.nodeInformer.Lister().Get(k.nodeName)
}

func (k *k8sHelper) Start(stopCh <-chan struct{}) {
	k.nodeInformer.Informer().Run(stopCh)
}

func NewK8sHelper() K8sHelper {
	nodeName := os.Getenv(NodeNameEnvKey)
	config, err := rest.InClusterConfig()
	if err != nil {
		klog.Fatal("Failed to get k8s config", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		klog.Fatal("Failed to get k8s clientset", err)
	}
	kubeInformerFactory := informers.NewSharedInformerFactory(clientset, time.Minute*1)
	nodeInformer := kubeInformerFactory.Core().V1().Nodes()
	return &k8sHelper{
		nodeName:     nodeName,
		nodeInformer: nodeInformer,
	}
}
