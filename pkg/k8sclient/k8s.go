package k8sclient

import (
	"fmt"
	"github.com/yunify/hostnic-cni/pkg/types"
	v1 "k8s.io/api/core/v1"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/informers"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corev1informer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	clientsetcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog"
)

const (
	// NodeNameEnvKey is env to get the name of current node
	NodeNameEnvKey = "MY_NODE_NAME"
)

// K8sHelper is used to commucate with k8s apiserver
type K8sHelper interface {
	Start(stopCh <-chan struct{}) error
	GetCurrentNode() (*corev1.Node, error)
	UpdateNodeAnnotation(key, value string) error
	GetCurrentNodePods() ([]*K8SPodInfo, error)
}

type k8sHelper struct {
	nodeName      string
	nodeInformer  corev1informer.NodeInformer
	nodeInterface clientsetcorev1.NodeInterface

	podInformer coreinformer.PodInformer
	podLister   corelisters.PodLister
	podSynced   cache.InformerSynced
}

func (k *k8sHelper) GetCurrentNode() (*corev1.Node, error) {
	return k.nodeInformer.Lister().Get(k.nodeName)
}

func (k *k8sHelper) Start(stopCh <-chan struct{}) error {
	nodeUpdate := func (old interface{}, new interface{}) {
		oldNode := old.(*v1.Node)
		newNode := new.(*v1.Node)

		newAnnotation := newNode.Annotations[types.NodeAnnotationVxNet]
		oldAnnotation := oldNode.Annotations[types.NodeAnnotationVxNet]
		if newAnnotation != oldAnnotation {
			klog.Infof("k8s node update: from  %s:%s to %s:%s", oldNode.Name, oldAnnotation, newNode, newAnnotation )
			types.NodeNotify <- newAnnotation
		}
	}
	k.nodeInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: nodeUpdate,
	})

	go k.nodeInformer.Informer().Run(stopCh)
	go k.podInformer.Informer().Run(stopCh)

	// Wait for the caches to be synced before starting workers
	klog.V(2).Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, k.podSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	return nil
}

func NewK8sHelper(clientset kubernetes.Interface) K8sHelper {
	nodeName := os.Getenv(NodeNameEnvKey)
	kubeInformerFactory := informers.NewSharedInformerFactory(clientset, time.Minute*1)
	nodeInformer := kubeInformerFactory.Core().V1().Nodes()
	podInformer := kubeInformerFactory.Core().V1().Pods()

	cont := &k8sHelper{
		nodeName:      nodeName,
		nodeInformer:  nodeInformer,
		nodeInterface: clientset.CoreV1().Nodes(),
		podInformer:   podInformer,
		podLister:     podInformer.Lister(),
		podSynced:     podInformer.Informer().HasSynced,
	}
	return cont
}

func (k *k8sHelper) UpdateNodeAnnotation(key, value string) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		node, err := k.GetCurrentNode()
		if err != nil {
			return err
		}
		if node.Annotations == nil {
			node.Annotations = make(map[string]string)
		}

		node.Annotations[key] = value
		_, err = k.nodeInterface.Update(node)
		return err
	})
}
