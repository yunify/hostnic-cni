package qcclient

import "github.com/yunify/hostnic-cni/pkg/types"

const (
	ErrorVxNetNotFound = "Cannot find the vxnet in the cloud"
	ErrorNicNotFound   = "Cannot find the nic in the cloud"
	ErrorVPCNotFound   = "Cannot find the vpc in the cloud"
)

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	QingCloudNetAPI
}

// QingCloudNetAPI  do dirty works on  net interface on qingcloud
type QingCloudNetAPI interface {
	//CreateNicInVxnet create network interface card in vxnet and attach to host
	CreateNic(string) (*types.HostNic, error)
	DeleteNic(nicID string) error
	//DeleteNic delete nic from host
	DeleteNics(nicIDs []string) error
	GetVxNet(vxNet string) (*types.VxNet, error)
	GetVxNets([]string) ([]*types.VxNet, error)
	GetNics([]string) ([]*types.HostNic, error)
	CreateVxNet(name string) (*types.VxNet, error)
	GetAttachedNICs() ([]*types.HostNic, error)
	GetVPC(string) (*types.VPC, error)
	GetNodeRouter() (*types.VPC, error)
}
