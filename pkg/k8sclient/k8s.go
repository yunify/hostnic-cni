package k8sclient

import (
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	corev1informer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	clientsetcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

const (
	NodeNameEnvKey = "MY_NODE_NAME"
)

type K8sHelper interface {
	Start(stopCh <-chan struct{}) error
	GetCurrentNode() (*corev1.Node, error)
	UpdateNodeAnnotation(key, value string) error
}

type k8sHelper struct {
	nodeName      string
	nodeInformer  corev1informer.NodeInformer
	nodeInterface clientsetcorev1.NodeInterface
}

func (k *k8sHelper) GetCurrentNode() (*corev1.Node, error) {
	return k.nodeInformer.Lister().Get(k.nodeName)
}

func (k *k8sHelper) Start(stopCh <-chan struct{}) error {
	go k.nodeInformer.Informer().Run(stopCh)
	// Wait for the caches to be synced before starting workers
	klog.V(2).Infoln("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, k.nodeInformer.Informer().HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}
	return nil
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
		nodeName:      nodeName,
		nodeInformer:  nodeInformer,
		nodeInterface: clientset.CoreV1().Nodes(),
	}
}

func (k *k8sHelper) UpdateNodeAnnotation(key, value string) error {
	return wait.Poll(time.Second*2, time.Second*20, func() (done bool, err error) {
		node, err := k.GetCurrentNode()
		if err != nil {
			return
		}
		node.Annotations[key] = value
		_, err = k.nodeInterface.Update(node)
		if err == nil {
			done = true
		}
		return
	})
}
