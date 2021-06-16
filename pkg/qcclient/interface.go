package qcclient

import "github.com/yunify/hostnic-cni/pkg/rpc"

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	//node info
	GetInstanceID() string

	//bootstrap
	GetCreatedNics(num, offsite int) ([]*rpc.HostNic, error)

	//vxnet info
	GetVxNets([]string) (map[string]*rpc.VxNet, error)

	//job info
	DescribeNicJobs(ids []string) ([]string, map[string]bool, error)

	//nic operations
	CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string, disableIP int) ([]*rpc.HostNic, string, error)
	GetNics(nics []string) (map[string]*rpc.HostNic, error)
	DeleteNics(nicIDs []string) error
	DeattachNics(nicIDs []string, sync bool) (string, error)
	AttachNics(nicIDs []string) (string, error)
	GetAttachedNics() ([]*rpc.HostNic, error)

	CreateVIPs(vxnet *rpc.VxNet) (string, error)
	DescribeVIPs(vxnet *rpc.VxNet) ([]*rpc.VIP, error)
	DeleteVIPs(vips []string) (string, error)
}

var (
	QClient QingCloudAPI
)
