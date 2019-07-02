package fake

import (
	"github.com/yunify/hostnic-cni/pkg/k8sclient"
	corev1 "k8s.io/api/core/v1"
)

type FakeK8sHelper struct {
	nodeAnnotation map[string]string
	currentPods    []*k8sclient.K8SPodInfo
}

func (f *FakeK8sHelper) Start(stopCh <-chan struct{}) error {
	return nil
}

func (f *FakeK8sHelper) GetCurrentNode() (*corev1.Node, error) {
	node := &corev1.Node{}
	node.SetAnnotations(f.nodeAnnotation)
	return node, nil
}

func (f *FakeK8sHelper) UpdateNodeAnnotation(key, value string) error {
	f.nodeAnnotation[key] = value
	return nil
}

func (f *FakeK8sHelper) GetCurrentNodePods() ([]*k8sclient.K8SPodInfo, error) {
	return f.currentPods, nil
}

func (f *FakeK8sHelper) AddPod(pod *k8sclient.K8SPodInfo) {
	f.currentPods = append(f.currentPods, pod)
}
