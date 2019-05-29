package k8sclient

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog"
)

// K8SPodInfo provides pod info
type K8SPodInfo struct {
	// Name is pod's name
	Name string
	// Namespace is pod's namespace
	Namespace string
	// Container is pod's container id
	Container string
	// IP is pod's ipv4 address
	IP  string
	UID string
}

const (
	// SuccessSynced is used as part of the Event 'reason' when a Foo is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Foo fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"
)

func (k *k8sHelper) runWorker() {
	for k.processNextWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (k *k8sHelper) processNextWorkItem() bool {
	obj, shutdown := k.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer k.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer k.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			k.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Foo resource to be synced.
		if err := k.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			k.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		k.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
		return true
	}

	return true
}

func (k *k8sHelper) syncHandler(key string) error {
	// Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}
	pod, err := k.podLister.Pods(namespace).Get(name)
	if err != nil {
		if errors.IsNotFound(err) {
			k.nodePodsLock.Lock()
			defer k.nodePodsLock.Unlock()
			delete(k.nodePods, key)
			return nil
		}
		return err
	}
	if pod.Spec.NodeName == k.nodeName && !pod.Spec.HostNetwork {
		k.nodePodsLock.Lock()
		defer k.nodePodsLock.Unlock()
		k.nodePods[key] = &K8SPodInfo{
			Name:      pod.GetName(),
			Namespace: pod.GetNamespace(),
			UID:       string(pod.GetUID()),
			IP:        pod.Status.PodIP,
			Container: pod.Status.ContainerStatuses[0].ContainerID,
		}
	}
	return nil
}

func (k *k8sHelper) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if key, err := cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilruntime.HandleError(err)
		return
	} else {
		k.workqueue.Add(key)
	}
}

// GetCurrentNodePods return the list of pods running on the local nodes
func (k *k8sHelper) GetCurrentNodePods() ([]*K8SPodInfo, error) {
	var localPods []*K8SPodInfo

	if !k.podSynced() {
		klog.V(2).Info("GetCurrentNodePods: informer not synced yet")
		return nil, fmt.Errorf("informer not synced")
	}

	klog.V(2).Infoln("GetCurrentNodePods start ...")
	k.nodePodsLock.Lock()
	defer k.nodePodsLock.Unlock()
	for _, pod := range k.nodePods {
		klog.V(2).Infof("GetCurrentNodePods discovered local Pods: %s %s %s %s",
			pod.Name, pod.Namespace, pod.IP, pod.Container)
		localPods = append(localPods, pod)
	}
	return localPods, nil
}
