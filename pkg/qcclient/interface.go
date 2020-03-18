package qcclient

import "github.com/yunify/hostnic-cni/pkg/types"

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	QingCloudNetAPI
	QingCloudTagAPI
}

// QingCloudNetAPI  do dirty works on  net interface on qingcloud
type QingCloudNetAPI interface {
	//CreateNicInVxnet create network interface card in vxnet and attach to host
	CreateNic(vxnet string) (*types.HostNic, error)
	DeleteNic(nicID string) error

	GetPrimaryNIC() (*types.HostNic, error)
	//DeleteNic delete nic from host
	DeleteNics(nicIDs []string) error
	GetVxNet(vxNet string) (*types.VxNet, error)
	GetVxNets([]string) ([]*types.VxNet, error)
	DeleteVxNet(string) error
	GetNics([]string) ([]*types.HostNic, error)
	CreateVxNet(name string) (*types.VxNet, error)
	GetAttachedNICs(string) ([]*types.HostNic, error)
	GetVPC(string) (*types.VPC, error)
	GetNodeVPC() (*types.VPC, error)
	GetVPCVxNets(string) ([]*types.VxNet, error)
	JoinVPC(network, vxnetID, vpcID string) error
	LeaveVPC(vxnetID, vpcID string) error
	GetInstanceID() string
}

// QingCloudTagAPI do dirty works of tags on qingcloud
type QingCloudTagAPI interface {
	TagResources(tagid string, resourceType types.ResourceType, ids ...string) error
	CreateTag(label, color string) (string, error)
	GetTagByLabel(label string) (*types.Tag, error)
	GetTagByID(id string) (*types.Tag, error)
}
