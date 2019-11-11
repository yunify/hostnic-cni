package ipam

import (
	"fmt"
	"net"
	"time"

	"github.com/yunify/hostnic-cni/pkg/retry"

	"github.com/yunify/hostnic-cni/pkg/errors"
	"github.com/yunify/hostnic-cni/pkg/types"
	"k8s.io/klog"
)

const (
	// NodeAnnotationVxNet will tell hostnic the node which vxnet to use when creating nic
	NodeAnnotationVxNet = "node.beta.kubernetes.io/vxnet"
)

func NameForVxnet(node string) string {
	return fmt.Sprintf("HOSTNIC_%s_vxnet", node)
}

// EnsureVxNet guarantee a vxnet for a node
func (s *IpamD) EnsureVxNet() error {
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
	exsitVxnet, err := s.qcClient.GetVxNetByName(NameForVxnet(s.NodeName))
	if err != nil {
		if !errors.IsResourceNotFound(err) {
			klog.Warningln("Failed to get vxnet by name")
			return err
		}
	} else {
		klog.V(1).Info("successfully get exsiting vxnet for pods")
		if exsitVxnet.RouterID == "" {
			err = s.joinVPC(exsitVxnet)
			if err != nil {
				klog.Errorf("Failed to join exsit vxnet %s to vpc %s", exsitVxnet.ID, s.vpc.ID)
				return err
			}
		}
		s.vxnet = exsitVxnet
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

func (s *IpamD) joinVPC(vxnet *types.VxNet) error {
	vxnets, err := s.qcClient.GetVPCVxNets(s.vpc.ID)
	if err != nil {
		klog.Errorf("Failed to get vxnets in the vpc %s", s.vpc.ID)
		return err
	}
	ip := chooseIPFromVxnet(*s.vpc.Network, vxnets)
	if ip != nil {
		vxnet.Network = ip
		err = s.qcClient.JoinVPC(ip.String(), vxnet.ID, s.vpc.ID)
		if err != nil {
			klog.Errorf("Failed to join vxnet %s to vpc %s", vxnet.ID, s.vpc.ID)
			return err
		}
	}
	s.vpc.VxNets = append(s.vpc.VxNets, vxnet)
	return err
}

func (s *IpamD) createNewVxnet() (*types.VxNet, error) {
	vxnet, err := s.qcClient.CreateVxNet(NameForVxnet(s.NodeName))
	if err != nil {
		klog.Errorln("Failed to call create Vxnet")
		return nil, err
	}
	err = retry.Do(5, time.Second*5, func() error {
		return s.joinVPC(vxnet)
	})
	if err != nil {
		return nil, err
	}
	return vxnet, nil
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
