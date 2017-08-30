package server

import (
	"github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg/messages"
	"golang.org/x/net/context"
)

//DaemonServer Daemon Server process which manages nics for nic plugin
type DaemonServerHandler struct {
	nicpool *NicPool
}

func NewDaemonServerHandler(nicpool *NicPool) *DaemonServerHandler {
	if nicpool == nil {
		return nil
	}
	return &DaemonServerHandler{
		nicpool: nicpool,
	}
}

func (daemon *DaemonServerHandler) AllocateNic(context context.Context, request *messages.AllocateNicRequest) (*messages.AllocateNicResponse, error) {
	nic,gateway, err := daemon.nicpool.BorrowNic(request.AutoAssignGateway)
	if err != nil {
		logrus.Errorf("Failed to borrow nic from pool,%v", err)
		return nil, err
	}
	response := &messages.AllocateNicResponse{
		Nicid:      nic.HardwareAddr,
		Nicgateway: nic.VxNet.GateWay,
		Nicip:      nic.Address,
		Niccidr:    nic.VxNet.Network,
	}
	if request.AutoAssignGateway && gateway != nil {
		response.Servicegateway = *gateway
	}
	return response, nil
}

func (daemon *DaemonServerHandler) FreeNic(context context.Context, request *messages.FreeNicRequest) (*messages.FreeNicResponse, error) {
	err := daemon.nicpool.ReturnNic(request.Nicid)
	return &messages.FreeNicResponse{}, err
}
