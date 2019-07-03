package qcclient

import (
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"
	"io/ioutil"

	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg/retry"
	"github.com/yunify/hostnic-cni/pkg/types"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"
	qcutil "github.com/yunify/qingcloud-sdk-go/utils"
	"k8s.io/klog"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	
	nicPrefix = "hostnic_"
     instanceIDFile   = "/host/etc/qingcloud/instance-id"
	defaultOpTimeout     = 180 * time.Second
	defaultWaitInterval  = 10 * time.Second
	waitNicLocalTimeout  = 20 * time.Second
	waitNicLocalInterval = 2 * time.Second
	nicNumLimit          = 60

	retryTimes    = 3
	retryInterval = time.Second * 5
)

var _ QingCloudAPI = &qingcloudAPIWrapper{}

type qingcloudAPIWrapper struct {
	nicService      *service.NicService
	jobService      *service.JobService
	vxNetService    *service.VxNetService
	instanceService *service.InstanceService
	vpcService      *service.RouterService

	instanceID string
}

func NewQingCloudClient() (QingCloudAPI, error) {
	content, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		return nil, fmt.Errorf("Load instance-id from %s error: %v", instanceIDFile, err)
	}
	qsdkconfig, err := config.NewDefault()
	if err != nil {
		return nil, err
	}
	if err = qsdkconfig.LoadUserConfig(); err != nil {
		return nil, err
	}
	qcService, err := service.Init(qsdkconfig)
	if err != nil {
		return nil, err
	}
	nicService, err := qcService.Nic(qsdkconfig.Zone)
	if err != nil {
		return nil, err
	}
	jobService, err := qcService.Job(qsdkconfig.Zone)
	if err != nil {
		return nil, err
	}
	vxNetService, err := qcService.VxNet(qsdkconfig.Zone)
	if err != nil {
		return nil, err
	}

	instanceService, err := qcService.Instance(qsdkconfig.Zone)
	if err != nil {
		return nil, err
	}
	vpcService, _ := qcService.Router(qsdkconfig.Zone)
	p := &qingcloudAPIWrapper{
		nicService:      nicService,
		jobService:      jobService,
		vxNetService:    vxNetService,
		instanceService: instanceService,
		vpcService:      vpcService,
		instanceID:      string(content),
	}
	return p, nil
}

func (q *qingcloudAPIWrapper) GetInstanceID() string {
	return q.instanceID
}

func (q *qingcloudAPIWrapper) CreateNic(vxnet string) (*types.HostNic, error) {
	input := &service.CreateNicsInput{
		VxNet:   &vxnet,
		NICName: service.String(nicPrefix + q.instanceID),
	}
	output, err := q.nicService.CreateNics(input)
	//TODO check too many nic in vDxnet err, and retry with another vxnet.
	if err != nil {
		return nil, err
	}

	if *output.RetCode == 0 && len(output.Nics) > 0 {
		qcnic := output.Nics[0]
		var hostnic *types.HostNic

		retry.Do(5, time.Second*3, func() error {
			hostNics, err := q.GetNics([]string{*qcnic.NICID})
			if err != nil {
				return err
			}
			if len(hostNics) == 0 {
				return fmt.Errorf("get empty nic")
			}
			hostnic = hostNics[0]
			return nil
		})
		vn, err := q.GetVxNet(vxnet)
		if err != nil {
			klog.Errorf("Failed to get vxnet of this nic")
			return nil, err
		}
		hostnic.VxNet = vn
		err = q.attachNic(hostnic)
		if err != nil {
			klog.Errorf("Failed to attach nic %s", *qcnic.NICID)
			q.DeleteNic(*qcnic.NICID)
			return nil, err
		}
		return hostnic, nil
	}
	return nil, fmt.Errorf("Failed to creat nic, error: %s", *output.Message)
}

func (q *qingcloudAPIWrapper) GetAttachedNICs(vxnet string) ([]*types.HostNic, error) {
	output, err := q.nicService.DescribeNics(&service.DescribeNicsInput{
		Instances: []*string{&q.instanceID},
		Limit:     service.Int(nicNumLimit),
		VxNets:    []*string{&vxnet},
		VxNetType: []*int{service.Int(1)},
	})
	if err != nil {
		return nil, err
	}
	result := make([]*types.HostNic, 0)
	for _, nic := range output.NICSet {
		if strings.HasPrefix(*nic.NICName, nicPrefix) {
			h := &types.HostNic{
				ID: *nic.NICID,
				VxNet: &types.VxNet{
					ID: *nic.VxNetID,
				},
				HardwareAddr: *nic.NICID,
				Address:      *nic.PrivateIP,
				DeviceNumber: *nic.Sequence,
				IsPrimary:    false,
			}
			result = append(result, h)
		}
	}
	return result, nil
}

func (q *qingcloudAPIWrapper) attachNic(nic *types.HostNic) error {
	input := &service.AttachNicsInput{Nics: []*string{&nic.HardwareAddr}, Instance: &q.instanceID}
	output, err := q.nicService.AttachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		err := q.waitNic(nic.ID, jobID)
		if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("AttachNics output [%+v] error", *output)
}

func (q *qingcloudAPIWrapper) waitNic(nicid, jobid string) error {
	klog.V(2).Infoln("Waiting for nic attached")
	err := qcutil.WaitForSpecific(func() bool {
		link, err := types.LinkByMacAddr(nicid)
		if err != nil {
			return false
		}
		if link.Attrs().Flags&net.FlagUp != 0 && link.Attrs().OperState&netlink.OperUp != 0 {
			return true
		}
		return false
	}, waitNicLocalTimeout, waitNicLocalInterval)
	if _, ok := err.(*qcutil.TimeoutError); ok {
		klog.V(2).Infof("Wait nic %s by local timeout", nicid)
		err = client.WaitJob(q.jobService, jobid, defaultOpTimeout, defaultWaitInterval)
	}
	return err
}

func (q *qingcloudAPIWrapper) DeleteNic(nicID string) error {
	return q.DeleteNics([]string{nicID})
}

func (q *qingcloudAPIWrapper) detachNics(nicIDs []string) error {
	input := &service.DetachNicsInput{Nics: service.StringSlice(nicIDs)}
	output, err := q.nicService.DetachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		//TODO optimize detachNic wait
		err := client.WaitJob(q.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
		if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("DetachNics output error %s", *output.Message)
}

func (q *qingcloudAPIWrapper) DeleteNics(nicIDs []string) error {
	err := q.detachNics(nicIDs)
	if err != nil {
		klog.Errorf("Failed to detach nics")
		return err
	}
	input := &service.DeleteNicsInput{Nics: service.StringSlice(nicIDs)}
	output, err := q.nicService.DeleteNics(input)
	if err != nil {
		klog.Errorf("Failed to delete nics from %s", q.instanceID)
		return err
	}
	if *output.RetCode == 0 {
		return nil
	}
	return fmt.Errorf("DeleteNics output [%+v] error", *output)
}

func (q *qingcloudAPIWrapper) GetVxNet(vxNet string) (*types.VxNet, error) {
	output, err := q.GetVxNets([]string{vxNet})
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, fmt.Errorf(ErrorVxNetNotFound)
	}
	return output[0], nil
}

func (q *qingcloudAPIWrapper) GetVxNets(ids []string) ([]*types.VxNet, error) {
	input := &service.DescribeVxNetsInput{VxNets: service.StringSlice(ids)}
	output, err := q.vxNetService.DescribeVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		var vxNets []*types.VxNet
		for _, qcVxNet := range output.VxNetSet {
			vxnetItem := &types.VxNet{ID: *qcVxNet.VxNetID, RouterID: *qcVxNet.VpcRouterID}
			if qcVxNet.Router != nil {
				vxnetItem.GateWay = *qcVxNet.Router.ManagerIP
				_, vxnetItem.Network, _ = net.ParseCIDR(*qcVxNet.Router.IPNetwork)
			}
			vxNets = append(vxNets, vxnetItem)
		}
		return vxNets, nil
	}
	return nil, fmt.Errorf("DescribeVxNets invalid output [%+v]", *output)
}

func (q *qingcloudAPIWrapper) GetNics(ids []string) ([]*types.HostNic, error) {
	input := &service.DescribeNicsInput{
		Nics: service.StringSlice(ids),
	}
	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		var niclist []*types.HostNic
		for _, nic := range output.NICSet {
			niclist = append(niclist, &types.HostNic{
				ID: *nic.NICID,
				VxNet: &types.VxNet{
					ID: *nic.VxNetID,
				},
				HardwareAddr: *nic.NICID,
				Address:      *nic.PrivateIP,
			})
		}
		return niclist, nil
	}
	return nil, fmt.Errorf("DescribeNics Failed [%+v]", *output)
}

func (q *qingcloudAPIWrapper) CreateVxNet(name string) (*types.VxNet, error) {
	input := &service.CreateVxNetsInput{
		VxNetType: service.Int(1),
		VxNetName: &name,
	}
	output, err := q.vxNetService.CreateVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		return &types.VxNet{
			Name: name,
			ID:   *output.VxNets[0],
		}, nil
	}
	return nil, fmt.Errorf("Failed to create vxnet %s,err:%s", name, *output.Message)
}

func (q *qingcloudAPIWrapper) GetNodeVPC() (*types.VPC, error) {
	input := &service.DescribeInstancesInput{
		Instances: []*string{&q.instanceID},
		Verbose:   service.Int(1),
	}
	output, err := q.instanceService.DescribeInstances(input)
	if err != nil {
		return nil, err
	}
	if len(output.InstanceSet) <= 0 {
		return nil, fmt.Errorf("Cannot find the instance %s", q.instanceID)
	}
	instanceItem := output.InstanceSet[0]
	var vxnetIds []string
	for _, vxnetItem := range instanceItem.VxNets {
		vxnetIds = append(vxnetIds, *vxnetItem.VxNetID)
	}
	vxnets, err := q.GetVxNets(vxnetIds)
	if err != nil {
		return nil, err
	}
	var routerID string
	for _, vxnetItem := range vxnets {
		if routerID == "" {
			routerID = vxnetItem.RouterID
		} else if routerID != vxnetItem.RouterID {
			return nil, fmt.Errorf("Vxnet is not under the same VPC's management")
		}
	}
	return q.GetVPC(routerID)
}

func (q *qingcloudAPIWrapper) GetVPC(id string) (*types.VPC, error) {
	input := &service.DescribeRoutersInput{
		Routers: []*string{&id},
	}
	output, err := q.vpcService.DescribeRouters(input)
	if err != nil {
		return nil, err
	}
	if len(output.RouterSet) == 0 {
		return nil, fmt.Errorf(ErrorVPCNotFound)
	}
	vpc := &types.VPC{
		ID: *output.RouterSet[0].RouterID,
	}
	_, net, err := net.ParseCIDR(*output.RouterSet[0].VpcNetwork)
	if err != nil {
		return nil, err
	}
	vpc.Network = net
	err = retry.Do(3, time.Second*5, func() error {
		vpc.VxNets, err = q.GetVPCVxNets(vpc.ID)
		if err != nil {
			klog.V(3).Infof("[Will retry] Error in get vxnets of vpc %s", vpc.ID)
			return err
		}
		return nil
	})
	if err != nil {
		klog.Errorf("Failed to get vxnets in this vpc %s", vpc.ID)
	}
	return vpc, nil
}

func (q *qingcloudAPIWrapper) GetVPCVxNets(vpcid string) ([]*types.VxNet, error) {
	input := &service.DescribeRouterVxNetsInput{
		Router: &vpcid,
	}
	output, err := q.vpcService.DescribeRouterVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode != 0 {
		err := fmt.Errorf("Failed to call 'DescribeRouterVxNets',err: %s", *output.Message)
		return nil, err
	}
	result := make([]*types.VxNet, 0)
	for _, vxnet := range output.RouterVxNetSet {
		v := new(types.VxNet)
		v.ID = *vxnet.VxNetID
		_, v.Network, _ = net.ParseCIDR(*vxnet.IPNetwork)
		result = append(result, v)
	}
	return result, nil
}

func (q *qingcloudAPIWrapper) JoinVPC(network, vxnetID, vpcID string) error {
	input := &service.JoinRouterInput{
		VxNet:     &vxnetID,
		Router:    &vpcID,
		IPNetwork: &network,
	}
	output, err := q.vpcService.JoinRouter(input)
	if err != nil {
		return err
	}
	return client.WaitJob(q.jobService, *output.JobID, defaultOpTimeout, defaultWaitInterval)
}

func (q *qingcloudAPIWrapper) LeaveVPC(vxnetID, vpcID string) error {
	input := &service.LeaveRouterInput{
		Router: &vpcID,
		VxNets: []*string{&vxnetID},
	}
	output, err := q.vpcService.LeaveRouter(input)
	if err != nil {
		return err
	}
	err = client.WaitJob(q.jobService, *output.JobID, defaultOpTimeout, defaultWaitInterval)
	if err != nil {
		return err
	}
	return q.DeleteVxNet(vxnetID)
}

func (q *qingcloudAPIWrapper) DeleteVxNet(id string) error {
	input := &service.DeleteVxNetsInput{
		VxNets: []*string{&id},
	}
	output, err := q.vxNetService.DeleteVxNets(input)
	if err != nil {
		return err
	}
	if *output.RetCode != 0 {
		return fmt.Errorf("Failed to delete vxnet %s, err: %s", id, *output.Message)
	}
	return nil
}

func (q *qingcloudAPIWrapper) GetPrimaryNIC() (*types.HostNic, error) {
	input := &service.DescribeNicsInput{
		Instances: []*string{&q.instanceID},
		Status:    service.String("in-use"),
		Limit:     service.Int(nicNumLimit),
		VxNetType: []*int{service.Int(1)},
	}
	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		return nil, err
	}
	for _, nic := range output.NICSet {
		if *nic.Role == 1 {
			return &types.HostNic{
				ID: *nic.NICID,
				VxNet: &types.VxNet{
					ID: *nic.VxNetID,
				},
				HardwareAddr: *nic.NICID,
				Address:      *nic.PrivateIP,
				IsPrimary:    true,
				DeviceNumber: *nic.Sequence,
			}, nil
		}
	}
	return nil, fmt.Errorf("Could not find the primary nic of instance %s", q.instanceID)
}
