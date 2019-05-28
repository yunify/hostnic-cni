package ipam

import (
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/hostnic-cni/pkg/server"
	"golang.org/x/net/context"
	"k8s.io/klog"
)

//GRPCServerHandler Daemon Server handler which handle nic requests from nic plugin
type GRPCServerHandler struct {
	nicpool *server.NicPool
	ipamd   *IpamD
}

func NewGRPCServerHandler(nicpool *server.NicPool, ipamd *IpamD) *GRPCServerHandler {
	if nicpool == nil {
		return nil
	}
	return &GRPCServerHandler{
		nicpool: nicpool,
	}
}

func (daemon *GRPCServerHandler) AddNetwork(context context.Context, request *rpc.AddNetworkRequest) (*rpc.AddNetworkReply, error) {
	nic, _, err := daemon.nicpool.BorrowNic(false)
	if err != nil {
		klog.Errorf("Failed to borrow nic from pool,%v", err)
	}
	response := &rpc.AddNetworkReply{
		Success:         err == nil,
		IPv4Addr:        nic.Address,
		IPv4Subnet:      daemon.ipamd.vxnet.Network.String(),
		DeviceNumber:    nic.DeviceNumber,
		UseExternalSNAT: false,
		VPCcidrs:        []string{daemon.ipamd.vpc.Network.String()},
	}
	return response, nil
}

func (daemon *GRPCServerHandler) DelNetwork(context context.Context, request *rpc.DelNetworkRequest) (*rpc.DelNetworkReply, error) {
	err := daemon.nicpool.ReturnNic(request.IPv4Addr)
	//klog.V(1).Infof("Send DelNetworkReply: IPv4Addr %s, DeviceNumber: %d, err: %v", ip, deviceNumber, err)
	return &rpc.DelNetworkReply{Success: err == nil}, err
}
