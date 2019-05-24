package ipam

import (
	"fmt"

	"github.com/yunify/hostnic-cni/pkg/types"
	"k8s.io/klog"
)

const (
	NodeAnnotationVxNet = "node.beta.kubernetes.io/vxnet"
)

func (s *IpamD) EnsureVxNet() error {
	if !s.isInformerStarted {
		return fmt.Errorf("NodeInformer is not started")
	}
	node, err := s.K8sClient.GetCurrentNode()
	if err != nil {
		klog.Errorf("Failed to get current node")
		return err
	}
	s.NodeName = node.Name
	if vxnet, ok := node.Annotations[NodeAnnotationVxNet]; ok {
		v, err := s.qcClient.GetVxNet(vxnet)
		if err != nil {
			return err
		}
		s.vxnet = v
		return nil
	}
	return nil
}

func (s *IpamD) createNewVxnet() (*types.VxNet, error) {
	vxnet, err := s.qcClient.CreateVxNet(fmt.Sprintf("HOSTNIC_%s_vxnet", s.nodeInfo.NodeName))
	if err != nil {
		klog.Errorln("Failed to call create Vxnet")
		return nil, err
	}
}
