package qcclient

import "github.com/yunify/hostnic-cni/pkg/types"

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	QingCloudNetAPI
	QingCloudTagAPI
}

// QingCloudNetAPI  do dirty works on  net interface on qingcloud
type QingCloudNetAPI interface {
	CreateNicsAndAttach(vxnet types.VxNet, count int) ([]*types.HostNic, error) //not attach
	DeleteNic(nicID string) error                                               //not deattach
	DeleteNics(nicIDs []string) error                                           //not deattach
	DeattachNic(nicIDs string) error
	GetNics([]string) ([]*types.HostNic, error)
	GetPrimaryNIC() (*types.HostNic, error)
	GetAttachedNICs(string) ([]*types.HostNic, error)

	GetVxNet(vxNet string) (*types.VxNet, error)
	GetVxNets([]string) ([]*types.VxNet, error)
	DeleteVxNet(string) error
	CreateVxNet(name string) (*types.VxNet, error)

	GetVPC(string) (*types.VPC, error)
	GetVPCVxNets(string) ([]*types.VxNet, error)
	GetNodeVPC() (*types.VPC, error)
	GetNodeVxnet(vxnetName string) (string, error)
	JoinVPC(network, vxnetID, vpcID string) error
	LeaveVPCAndDelete(vxnetID, vpcID string) error

	GetInstanceID() string
}

// QingCloudTagAPI do dirty works of tags on qingcloud
type QingCloudTagAPI interface {
	TagResources(tagid string, resourceType types.ResourceType, ids ...string) error
	CreateTag(label, color string) (string, error)
	GetTagByLabel(label string) (*types.Tag, error)
	GetTagByID(id string) (*types.Tag, error)
}
