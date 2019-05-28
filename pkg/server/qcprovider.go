package server

import (
	"net"

	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/types"
)

type NicProvider interface {
	GenerateNic() (*types.HostNic, error)
	ValidateNic(nicid string) bool
	ReclaimNic([]string) error
	GetNicsInfo([]string) ([]*types.HostNic, error)
	DisableNic(nicid string) error
}

//QingCloudNicProvider nic provider qingcloud implementation
type QingCloudNicProvider struct {
	qcClient qcclient.QingCloudAPI
	vxnet    string
}

func (provider *QingCloudNicProvider) GetNicsInfo(nicids []string) ([]*types.HostNic, error) {
	return provider.qcClient.GetNics(nicids)
}

func NewQingCloudNicProvider(qcClient qcclient.QingCloudAPI, vxnet string) NicProvider {
	return &QingCloudNicProvider{qcClient: qcClient, vxnet: vxnet}
}

func (provider *QingCloudNicProvider) GenerateNic() (*types.HostNic, error) {
	return provider.qcClient.CreateNic(provider.vxnet)
}

func (provider *QingCloudNicProvider) ValidateNic(nicid string) bool {
	link, err := types.LinkByMacAddr(nicid)
	if err != nil {
		return false
	}
	if link.Attrs().Flags&net.FlagUp != 0 {
		return false
	}
	return true
}

func (provider *QingCloudNicProvider) ReclaimNic(niclist []string) error {
	return provider.qcClient.DeleteNics(niclist)
}

func (provider *QingCloudNicProvider) DisableNic(nicid string) error {
	iface, err := types.LinkByMacAddr(nicid)
	if err != nil {
		return err
	}
	err = netlink.LinkSetDown(iface)
	return err
}
