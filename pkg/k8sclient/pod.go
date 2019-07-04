package k8sclient

import (
	"fmt"

	"k8s.io/apimachinery/pkg/labels"
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

// GetCurrentNodePods return the list of pods running on the local nodes
func (k *k8sHelper) GetCurrentNodePods() ([]*K8SPodInfo, error) {
	var localPods []*K8SPodInfo

	if !k.podSynced() {
		klog.V(2).Info("GetCurrentNodePods: informer not synced yet")
		return nil, fmt.Errorf("informer not synced")
	}

	klog.V(2).Infoln("GetCurrentNodePods start ...")
	pods, err := k.podLister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	for _, pod := range pods {
		if pod.Spec.NodeName == k.nodeName && !pod.Spec.HostNetwork {
			localPods = append(localPods, &K8SPodInfo{
				Name:      pod.GetName(),
				Namespace: pod.GetNamespace(),
				UID:       string(pod.GetUID()),
				IP:        pod.Status.PodIP,
				Container: pod.Status.ContainerStatuses[0].ContainerID,
			})
		}
	}
	return localPods, nil
}
