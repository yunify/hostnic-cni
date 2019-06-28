package k8sclient

import (
	"fmt"
	"os"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	coreinformer "k8s.io/client-go/informers/core/v1"
	corev1informer "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	clientsetcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog"
)

const (
	NodeNameEnvKey = "MY_NODE_NAME"
)

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

	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue workqueue.RateLimitingInterface

	nodePods     map[string]*K8SPodInfo
	nodePodsLock sync.RWMutex
}

func (k *k8sHelper) GetCurrentNode() (*corev1.Node, error) {
	return k.nodeInformer.Lister().Get(k.nodeName)
}

func (k *k8sHelper) Start(stopCh <-chan struct{}) error {
	defer utilruntime.HandleCrash()
	defer k.workqueue.ShutDown()

	go k.nodeInformer.Informer().Run(stopCh)
	go k.podInformer.Informer().Run(stopCh)

	// Start the informer factories to begin populating the informer caches
	klog.V(1).Infoln("Starting pod controller")
	go k.podInformer.Informer().Run(stopCh)
	// Wait for the caches to be synced before starting workers
	klog.V(2).Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, k.podSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	klog.V(2).Info("Starting workers")
	// we have only one worker now

	go wait.Until(k.runWorker, time.Second, stopCh)
	klog.V(1).Info("Started workers")
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
		workqueue:     workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Pods"),

		nodePods: make(map[string]*K8SPodInfo),
	}

	klog.V(2).Infoln("Setting up event handlers")
	// Set up an event handler for when pod resources change. This
	// handler will lookup the owner of the given Pod, and if it is
	// owned by a Foo resource will enqueue that Foo resource for
	// processing. This way, we don't need to implement custom logic for
	// handling pod resources. More info on this pattern:
	// https://github.com/kubernetes/community/blob/8cafef897a22026d42f5e5bb3f104febe7e29830/contributors/devel/controllers.md
	podInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: cont.handleObject,
		UpdateFunc: func(old, new interface{}) {
			newPod := new.(*corev1.Pod)
			oldPod := old.(*corev1.Pod)
			if newPod.ResourceVersion == oldPod.ResourceVersion {
				// Periodic resync will send update events for all known Deployments.
				// Two different versions of the same pod will always have different RVs.
				return
			}
			cont.handleObject(new)
		},
		DeleteFunc: cont.handleObject,
	})
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
