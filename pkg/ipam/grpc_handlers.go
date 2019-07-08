package ipam

import (
	"net"

	"github.com/yunify/hostnic-cni/pkg/ipam/datastore"
	k8sapi "github.com/yunify/hostnic-cni/pkg/k8sclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"golang.org/x/net/context"
	"k8s.io/klog"
)

//GRPCServerHandler Daemon Server handler which handle nic requests from nic plugin
type GRPCServerHandler struct {
	ipamd *IpamD
}

// NewGRPCServerHandler create a new GRPC handler in IPAMD
func NewGRPCServerHandler(ipamd *IpamD) *GRPCServerHandler {
	return &GRPCServerHandler{
		ipamd: ipamd,
	}
}

// AddNetwork handle add pod request
func (s *GRPCServerHandler) AddNetwork(context context.Context, in *rpc.AddNetworkRequest) (*rpc.AddNetworkReply, error) {
	klog.V(1).Infof("Received AddNetwork for NS %s, Pod %s, NameSpace %s, Container %s, ifname %s",
		in.Netns, in.K8S_POD_NAME, in.K8S_POD_NAMESPACE, in.K8S_POD_INFRA_CONTAINER_ID, in.IfName)

	addr, deviceNumber, err := s.ipamd.dataStore.AssignPodIPv4Address(&k8sapi.K8SPodInfo{
		Name:      in.K8S_POD_NAME,
		Namespace: in.K8S_POD_NAMESPACE,
		Container: in.K8S_POD_INFRA_CONTAINER_ID})

	subnets := make([]string, 0)
	for _, subnet := range s.ipamd.vpcSubnets() {
		subnets = append(subnets, *subnet)
	}
	if s.ipamd.supportVPNTraffic {
		vpnNet := *s.ipamd.vpc.Network
		vpnNet.IP[2] = 255
		vpnNet.Mask = net.IPv4Mask(255, 255, 255, 0)
		subnets = append(subnets, vpnNet.String())
	}

	resp := rpc.AddNetworkReply{
		Success:         err == nil,
		IPv4Addr:        addr,
		IPv4Subnet:      "",
		DeviceNumber:    int32(deviceNumber),
		UseExternalSNAT: false,
		VPCcidrs:        subnets,
	}

	klog.V(1).Infof("Send AddNetworkReply: IPv4Addr %s, DeviceNumber: %d, err: %v", addr, deviceNumber, err)
	return &resp, nil
}

// DelNetwork handle del pod request
func (s *GRPCServerHandler) DelNetwork(context context.Context, in *rpc.DelNetworkRequest) (*rpc.DelNetworkReply, error) {
	klog.V(1).Infof("Received DelNetwork for IP %s, Pod %s, Namespace %s, Container %s",
		in.IPv4Addr, in.K8S_POD_NAME, in.K8S_POD_NAMESPACE, in.K8S_POD_INFRA_CONTAINER_ID)

	ip, deviceNumber, err := s.ipamd.dataStore.UnassignPodIPv4Address(&k8sapi.K8SPodInfo{
		Name:      in.K8S_POD_NAME,
		Namespace: in.K8S_POD_NAMESPACE,
		Container: in.K8S_POD_INFRA_CONTAINER_ID})

	if err != nil && err == datastore.ErrUnknownPod {
		// If L-IPAMD restarts, the pod's IP address are assigned by only pod's name and namespace due to kubelet's introspection.
		ip, deviceNumber, err = s.ipamd.dataStore.UnassignPodIPv4Address(&k8sapi.K8SPodInfo{
			Name:      in.K8S_POD_NAME,
			Namespace: in.K8S_POD_NAMESPACE})
		if err == datastore.ErrUnknownPod {
			klog.Warningf("Detect unhealthy pod %s/%s", in.K8S_POD_NAME, in.K8S_POD_NAMESPACE)
			return &rpc.DelNetworkReply{Success: true}, nil
		}
	}
	klog.V(1).Infof("Send DelNetworkReply: IPv4Addr %s, DeviceNumber: %d, err: %v", ip, deviceNumber, err)

	return &rpc.DelNetworkReply{Success: err == nil, IPv4Addr: ip, DeviceNumber: int32(deviceNumber)}, nil
}
