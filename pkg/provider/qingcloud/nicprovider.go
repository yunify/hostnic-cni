package qingcloud

import (
	"net"

	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/pkg/server"
)

type QingCloudNicProvider struct {
	resourceStub *QCNicProvider
}

func (provider *QingCloudNicProvider) GetNicsInfo(nicids []*string) ([]*pkg.HostNic, error) {
	return provider.resourceStub.GetNics(nicids)
}

func NewQingCloudNicProvider(provider *QCNicProvider) server.NicProvider {
	return &QingCloudNicProvider{resourceStub: provider}
}

func (provider *QingCloudNicProvider) GenerateNic() (*pkg.HostNic, error) {
	return provider.resourceStub.CreateNic()
}

func (provider *QingCloudNicProvider) ValidateNic(nicid string) bool {
	link, err := pkg.LinkByMacAddr(nicid)
	if err != nil {
		return false
	}
	if link.Attrs().Flags&net.FlagUp != 0 {
		return false
	}
	return true
}

func (provider *QingCloudNicProvider) ReclaimNic(niclist []*string) error {
	return provider.resourceStub.DeleteNics(niclist)
}
