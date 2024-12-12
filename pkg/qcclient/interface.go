package qcclient

import (
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

// QingCloudAPI is a wrapper interface of qingcloud api
type QingCloudAPI interface {
	//node info
	GetInstanceID() string

	//bootstrap
	GetCreatedNicsByName(name string) ([]*rpc.HostNic, error)

	//vxnet info
	GetVxNets(ids []string, customReservedIPCount int64) (map[string]*rpc.VxNet, error)

	//job info
	DescribeNicJobs(ids []string) ([]string, map[string]bool, error)

	//nic operations
	CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string, disableIP int) ([]*rpc.HostNic, string, error)
	GetNics(nics []string) (map[string]*rpc.HostNic, error)
	DeleteNics(nicIDs []string) error
	DeattachNics(nicIDs []string, sync bool) (string, error)
	AttachNics(nicIDs []string, sync bool) (string, error)
	GetAttachedNics() ([]*rpc.HostNic, error)
	GetCreatedNicsByVxNet(vxnet string) ([]*rpc.HostNic, error)

	CreateVIPs(vxnet *rpc.VxNet) (string, error)
	DescribeVIPs(vxnet *rpc.VxNet) ([]*rpc.VIP, error)
	DeleteVIPs(vips []string) (string, error)

	CreateSecurityGroupRuleForVxNet(sg string, vxnet *rpc.VxNet) (string, error)
	GetSecurityGroupRuleForVxNet(sg string, vxnet *rpc.VxNet) (*rpc.SecurityGroupRule, error)
	DeleteSecurityGroupRuleForVxNet(sgr string) error

	DescribeClusterSecurityGroup(clusterID string) (string, error)
	DescribeClusterNodes(clusterID string) ([]*rpc.Node, error)
}

var (
	QClient QingCloudAPI
)
