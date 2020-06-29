package k8s

import (
	"context"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/util/retry"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetCurrentNodePods return the list of pods running on the local nodes
func (k *Helper) GetCurrentNodePods() ([]*rpc.PodInfo, error) {
	var localPods []*rpc.PodInfo

	pods := &corev1.PodList{}
	err := k.client.List(context.Background(), pods, &client.ListOptions{
		//FieldSelector: fields.OneTermEqualSelector("spec.nodeName", k.nodeName),
	})
	if err != nil {
		return nil, err
	}

	for _, pod := range pods.Items {
		if pod.Spec.NodeName != k.nodeName {
			continue
		}

		if pod.Spec.HostNetwork {
			continue
		}

		if pod.Annotations == nil {
			continue
		}

		if pod.Annotations[AnnoHostNicVxnet] == "" {
			continue
		}

		localPods = append(localPods, getPodInfo(&pod))
	}

	return localPods, nil
}

func getPodInfo(pod *corev1.Pod) *rpc.PodInfo {
	tmp := &rpc.PodInfo{
		Name:      pod.GetName(),
		Namespace: pod.GetNamespace(),
	}
	annotations := pod.GetAnnotations()
	if annotations != nil {
		tmp.VxNet = annotations[AnnoHostNicVxnet]
		tmp.HostNic = annotations[AnnoHostNic]
		tmp.PodIP = annotations[AnnoHostNicIP]
		tmp.NicType = annotations[AnnoHostNicType]
	}

	return tmp
}

func (k *Helper) UpdatePodInfo(info *rpc.PodInfo) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		pod := &corev1.Pod{}
		err := k.client.Get(context.Background(), client.ObjectKey{
			Namespace: info.Namespace,
			Name:      info.Name,
		}, pod)
		if err != nil {
			return err
		}

		clone := pod.DeepCopy()
		if clone.Annotations == nil {
			clone.Annotations = make(map[string]string)
		}
		clone.Annotations[AnnoHostNicIP] = info.PodIP
		clone.Annotations[AnnoHostNic] = info.HostNic
		clone.Annotations[AnnoHostNicVxnet] = info.VxNet

		if reflect.DeepEqual(clone, pod) {
			return nil
		}

		return k.client.Update(context.Background(), clone)
	})
}

func (k *Helper) GetPodInfo(namespace, name string) (*rpc.PodInfo, error) {
	pod := &corev1.Pod{}

	err := k.client.Get(context.Background(), client.ObjectKey{
		Namespace: namespace,
		Name:      name,
	}, pod)
	if err != nil {
		return nil, err
	}

	return getPodInfo(pod), nil
}
