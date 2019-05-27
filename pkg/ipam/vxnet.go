package ipam

import (
	"fmt"
	"net"

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
	klog.V(1).Infof("Will creating a new vxnet for node %s, this will take up one minute", s.NodeName)
	vxnet, err := s.createNewVxnet()
	if err != nil {
		return err
	}
	s.vxnet = vxnet
	err = s.K8sClient.UpdateNodeAnnotation(NodeAnnotationVxNet, vxnet.ID)
	if err != nil {
		klog.Errorf("Could not update nodes annotations, will delete this vxnet %s", vxnet.ID)
		leaveErr := s.qcClient.LeaveVPC(vxnet.ID, s.vpc.ID)
		if leaveErr != nil {
			klog.Errorf("Failed to delete vxnet %s,err:%s, pls manually delete this vxnet in the qingcloud console before using this plugin again", vxnet.ID, leaveErr.Error())
		}
		return err
	}
	klog.V(1).Infof("Vxnet created successfully")
	return nil
}

func (s *IpamD) createNewVxnet() (*types.VxNet, error) {
	vxnet, err := s.qcClient.CreateVxNet(fmt.Sprintf("HOSTNIC_%s_vxnet", s.nodeInfo.NodeName))
	if err != nil {
		klog.Errorln("Failed to call create Vxnet")
		return nil, err
	}
	vxnets, err := s.qcClient.GetVPCVxNets(s.vpc.ID)
	if err != nil {
		klog.Errorf("Failed to get vxnets in the vpc %s", s.vpc.ID)
		return nil, err
	}
	ip := chooseIPFromVxnet(*s.vpc.Network, vxnets)
	if ip != nil {
		vxnet.Network = ip
		err = s.qcClient.JoinVPC(ip.String(), vxnet.ID, s.vpc.ID)
		if err != nil {
			klog.Errorf("Failed to join vxnet %s to vpc %s", vxnet.ID, s.vpc.ID)
			return nil, err
		}
		return vxnet, nil
	}
	return nil, fmt.Errorf("Could not join any vxnets in this vpc %s", s.vpc.ID)
}

func chooseIPFromVxnet(ipnet net.IPNet, vxnets []*types.VxNet) *net.IPNet {
	maps := make(map[string]bool)
	for _, v := range vxnets {
		maps[v.Network.String()] = true
	}
	var index byte = 1
	for ; index < 253; index++ {
		ipnet.IP[2] = index
		ipnet.Mask[2] = 255
		if _, ok := maps[ipnet.String()]; !ok {
			return &ipnet
		}
	}
	return nil
}
