package qcclient

import (
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"

	"github.com/davecgh/go-spew/spew"
	"github.com/pkg/errors"
	cnet "github.com/projectcalico/libcalico-go/lib/net"
	log "k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/constants"
	rpc "github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"
)

const (
	instanceIDFile      = "/etc/qingcloud/instance-id"
	defaultOpTimeout    = 300 * time.Second
	defaultWaitInterval = 5 * time.Second

	reservedVIPCount              = 12
	reservedVIPCountForVlan int64 = 7 //reserve 1/7 ip for hostnic br

	TunnelTypeVlan = "vlan"
)

var (
	customReservedIPCount int64 = 0
)

type Options struct {
	Tag string
}

var _ QingCloudAPI = &qingcloudAPIWrapper{}

type qingcloudAPIWrapper struct {
	nicService      *service.NicService
	vxNetService    *service.VxNetService
	instanceService *service.InstanceService
	jobService      *service.JobService
	tagService      *service.TagService
	vipService      *service.VIPService
	sgService       *service.SecurityGroupService
	clusterService  *service.ClusterService

	userID     string
	instanceID string
	opts       Options
}

// NewQingCloudClient create a qingcloud client to manipulate cloud resources
func SetupQingCloudClient(opts Options) {
	instanceID, err := os.ReadFile(instanceIDFile)
	if err != nil {
		log.Fatalf("failed to load instance-id: %v", err)
	}

	qsdkconfig, err := config.NewDefault()
	if err != nil {
		log.Fatalf("failed to new sdk default config: %v", err)
	}
	if err = qsdkconfig.LoadUserConfig(); err != nil {
		log.Fatalf("failed to load user config: %v", err)
	}

	log.Infof("qsdkconfig inited: %v", qsdkconfig)

	qcService, err := service.Init(qsdkconfig)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk service: %v", err)
	}

	nicService, err := qcService.Nic(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk nic service: %v", err)
	}

	vxNetService, err := qcService.VxNet(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk vxnet service: %v", err)
	}

	jobService, err := qcService.Job(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk job service: %v", err)
	}

	instanceService, err := qcService.Instance(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk instance service: %v", err)
	}

	tagService, err := qcService.Tag(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk tag service: %v", err)
	}

	vipService, err := qcService.VIP(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk vip service: %v", err)
	}

	sgService, err := qcService.SecurityGroup(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk securityGroup service: %v", err)
	}

	clusterService, err := qcService.Cluster(qsdkconfig.Zone)
	if err != nil {
		log.Fatalf("failed to init qingcloud sdk cluster service: %v", err)
	}

	//useid
	api, _ := qcService.Accesskey(qsdkconfig.Zone)
	output, err := api.DescribeAccessKeys(&service.DescribeAccessKeysInput{
		AccessKeys: []*string{&qsdkconfig.AccessKeyID},
	})
	if err != nil {
		log.Fatalf("failed to DescribeAccessKeys: %v", err)
	}
	if len(output.AccessKeySet) == 0 {
		log.Fatalf("DescribeAccessKeys is empty: %s", spew.Sdump(output))
	}
	userId := *output.AccessKeySet[0].Owner

	QClient = &qingcloudAPIWrapper{
		nicService:      nicService,
		vxNetService:    vxNetService,
		instanceService: instanceService,
		jobService:      jobService,
		tagService:      tagService,
		vipService:      vipService,
		sgService:       sgService,
		clusterService:  clusterService,

		userID:     userId,
		instanceID: string(instanceID),
		opts:       opts,
	}
}

func (q *qingcloudAPIWrapper) GetInstanceID() string {
	return q.instanceID
}

func (q *qingcloudAPIWrapper) GetCreatedNicsByName(name string) ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Limit:   service.Int(constants.NicNumLimit),
		NICName: service.String(name),
	}
	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		log.Errorf("failed to GetCreatedNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var (
		nics   []*rpc.HostNic
		netIDs []string
	)
	for _, nic := range output.NICSet {
		if *nic.Role != 0 {
			continue
		}
		nics = append(nics, constructHostnic(&rpc.VxNet{
			ID: *nic.VxNetID,
		}, nic))
		netIDs = append(netIDs, *nic.VxNetID)
	}

	if len(netIDs) > 0 {
		tmp := removeDupByMap(netIDs)
		vxnets, err := q.GetVxNets(tmp)
		if err != nil {
			return nil, err
		}

		for _, nic := range nics {
			nic.VxNet = vxnets[nic.VxNet.ID]
		}
	}

	return nics, nil
}

func (q *qingcloudAPIWrapper) GetCreatedNicsByVxNet(vxnet string) ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Limit:  service.Int(constants.VxnetNicNumLimit),
		VxNets: []*string{service.String(vxnet)},
	}
	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		log.Errorf("failed to GetCreatedNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var (
		nics   []*rpc.HostNic
		netIDs []string
	)
	for _, nic := range output.NICSet {
		if *nic.Role != 0 {
			continue
		}
		nics = append(nics, constructHostnic(&rpc.VxNet{
			ID: *nic.VxNetID,
		}, nic))
		netIDs = append(netIDs, *nic.VxNetID)
	}

	if len(netIDs) > 0 {
		tmp := removeDupByMap(netIDs)
		vxnets, err := q.GetVxNets(tmp)
		if err != nil {
			return nil, err
		}

		for _, nic := range nics {
			nic.VxNet = vxnets[nic.VxNet.ID]
		}
	}

	return nics, nil
}

func (q *qingcloudAPIWrapper) GetAttachedNics() ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Instances: []*string{&q.instanceID},
		Status:    service.String("in-use"),
		Limit:     service.Int(constants.NicNumLimit + 1),
	}

	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		log.Errorf("failed to GetPrimaryNIC: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var result []*rpc.HostNic
	for _, nic := range output.NICSet {
		result = append(result, constructHostnic(nil, nic))
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) AttachNics(nicIDs []string, sync bool) (string, error) {
	input := &service.AttachNicsInput{
		Nics:     service.StringSlice(nicIDs),
		Instance: &q.instanceID,
	}

	output, err := q.nicService.AttachNics(input)
	if err != nil {
		log.Errorf("failed to AttachNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	if sync {
		return "", client.WaitJob(q.jobService, *output.JobID,
			defaultOpTimeout,
			defaultWaitInterval)
	}

	return *output.JobID, nil
}

// vxnet should not be nil
func constructHostnic(vxnet *rpc.VxNet, nic *service.NIC) *rpc.HostNic {
	if vxnet == nil {
		vxnet = &rpc.VxNet{
			ID: *nic.VxNetID,
		}
	}

	hostnic := &rpc.HostNic{
		ID:           *nic.NICID,
		VxNet:        vxnet,
		HardwareAddr: *nic.NICID,
	}

	if nic.PrivateIP != nil {
		hostnic.PrimaryAddress = *nic.PrivateIP
	}

	if *nic.Role == 1 {
		hostnic.IsPrimary = true
	}

	if *nic.Status == "in-use" {
		hostnic.Using = true
	}

	return hostnic
}

func (q *qingcloudAPIWrapper) GetNics(nics []string) (map[string]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Nics:  service.StringSlice(nics),
		Limit: service.Int(constants.NicNumLimit),
	}

	output, err := q.nicService.DescribeNics(input)
	if err != nil {
		log.Errorf("failed to GetNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	result := make(map[string]*rpc.HostNic)
	for _, nic := range output.NICSet {
		result[*nic.NICID] = constructHostnic(nil, nic)
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string, disableIP int) ([]*rpc.HostNic, string, error) {
	nicName := constants.NicPrefix + q.instanceID
	input := &service.CreateNicsInput{
		Count:      service.Int(num),
		VxNet:      &vxnet.ID,
		PrivateIPs: nil,
		NICName:    service.String(nicName),
		DisableIP:  &disableIP,
	}
	if ips != nil {
		input.Count = service.Int(len(ips))
		input.PrivateIPs = service.StringSlice(ips)
	}

	output, err := q.nicService.CreateNics(input)
	if err != nil {
		log.Errorf("failed to create nics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, "", err
	}

	var (
		result []*rpc.HostNic
		nics   []string
	)
	for _, nic := range output.Nics {
		r := &rpc.HostNic{
			ID:           *nic.NICID,
			VxNet:        vxnet,
			HardwareAddr: *nic.NICID,
		}
		if disableIP == 0 {
			r.PrimaryAddress = *nic.PrivateIP
		}
		result = append(result, r)
		nics = append(nics, *nic.NICID)
	}

	//may need to tag the card later.
	q.attachNicTag(nics)

	job, err := q.AttachNics(nics, false)
	if err != nil {
		_ = q.DeleteNics(nics)
		return nil, "", err
	}

	return result, job, nil
}

func (q *qingcloudAPIWrapper) DeattachNics(nicIDs []string, sync bool) (string, error) {
	if len(nicIDs) <= 0 {
		return "", nil
	}

	input := &service.DetachNicsInput{
		Nics: service.StringSlice(nicIDs),
	}

	output, err := q.nicService.DetachNics(input)
	if err != nil {
		log.Errorf("failed to DeattachNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	if sync {
		return "", client.WaitJob(q.jobService, *output.JobID,
			defaultOpTimeout,
			defaultWaitInterval)
	}

	return *output.JobID, nil
}

func (q *qingcloudAPIWrapper) DeleteNics(nicIDs []string) error {
	if len(nicIDs) <= 0 {
		return nil
	}

	input := &service.DeleteNicsInput{
		Nics: service.StringSlice(nicIDs),
	}

	output, err := q.nicService.DeleteNics(input)
	if err != nil {
		log.Errorf("failed to DeleteNics: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return err
	}

	return nil
}

type nics struct {
	IDs []string `json:"nics"`
}

func (q *qingcloudAPIWrapper) DescribeNicJobs(ids []string) ([]string, map[string]bool, error) {
	input := &service.DescribeJobsInput{
		Jobs:  service.StringSlice(ids),
		Limit: service.Int(constants.NicNumLimit),
	}

	output, err := q.jobService.DescribeJobs(input)
	if err != nil {
		log.Errorf("failed to GetJobs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, nil, err
	}

	working := make(map[string]bool)
	var left []string
	for _, j := range output.JobSet {
		if *j.JobAction == "DetachNics" || *j.JobAction == "AttachNics" {
			if *j.Status == "working" || *j.Status == "pending" {
				left = append(left, *j.JobID)
				tmp := nics{}
				json.Unmarshal([]byte(*j.Directive), &tmp)
				for _, id := range tmp.IDs {
					working[id] = true
				}
			}
		}
	}

	return left, working, nil
}

func (q *qingcloudAPIWrapper) getVxNets(ids []string, public bool) ([]*rpc.VxNet, error) {
	input := &service.DescribeVxNetsInput{
		VxNets: service.StringSlice(ids),
		Limit:  service.Int(constants.NicNumLimit),
	}
	if public {
		input.VxNetType = service.Int(2)
	}

	output, err := q.vxNetService.DescribeVxNets(input)
	if err != nil {
		log.Errorf("failed to GetVxNets: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var vxNets []*rpc.VxNet
	for _, qcVxNet := range output.VxNetSet {
		vxnetItem := &rpc.VxNet{
			ID: *qcVxNet.VxNetID,
		}

		if qcVxNet.Router != nil {
			if *qcVxNet.Router.DYNIPStart == "" || *qcVxNet.Router.DYNIPEnd == "" {
				return nil, fmt.Errorf("vxnet %s should open DHCP", *qcVxNet.VxNetID)
			}
			vxnetItem.Gateway = *qcVxNet.Router.ManagerIP
			vxnetItem.Network = *qcVxNet.Router.IPNetwork
			vxnetItem.IPStart = *qcVxNet.Router.DYNIPStart
			if qcVxNet.TunnelType != nil {
				vxnetItem.TunnelType = *qcVxNet.TunnelType
			}
			if vxnetItem.TunnelType == TunnelTypeVlan {
				// parse ip_network to get mask; if mask is 24, reserve more ip than specifc in config
				_, ipNet, err := net.ParseCIDR(vxnetItem.Network)
				if err != nil {
					log.Errorf("parse ip_network to get mask error: %v", err)
				}
				maskLength, _ := ipNet.Mask.Size()
				if maskLength == 24 {
					log.Infof("vxnet %s ip network %s mask is 24, reserve another 64 ip", *qcVxNet.VxNetID, vxnetItem.Network)
					customReservedIPCount = 64
				}
				vxnetItem.IPEnd = getIPEndAfterReservedForVlan(*qcVxNet.Router.DYNIPStart, *qcVxNet.Router.DYNIPEnd, reservedVIPCountForVlan, customReservedIPCount)
			} else {
				vxnetItem.IPEnd = getIPEndAfterReserved(*qcVxNet.Router.DYNIPEnd, reservedVIPCount, customReservedIPCount)
			}
		} else {
			return nil, fmt.Errorf("vxnet %s should bind to vpc", *qcVxNet.VxNetID)
		}

		vxNets = append(vxNets, vxnetItem)
	}

	return vxNets, nil
}

func (q *qingcloudAPIWrapper) GetVxNets(ids []string) (map[string]*rpc.VxNet, error) {
	if len(ids) <= 0 {
		return nil, errors.WithStack(fmt.Errorf("GetVxNets should not have empty input"))
	}

	vxnets, err := q.getVxNets(ids, false)
	if err != nil {
		return nil, err
	}

	var left []string
	result := make(map[string]*rpc.VxNet, 0)
	for _, vxNet := range vxnets {
		result[vxNet.ID] = vxNet
	}
	for _, id := range ids {
		if result[id] == nil {
			left = append(left, id)
		}
	}
	if len(left) > 0 {
		vxnets, err := q.getVxNets(left, true)
		if err != nil {
			return nil, err
		}
		for _, vxNet := range vxnets {
			result[vxNet.ID] = vxNet
		}
	}

	return result, nil
}

func removeDupByMap(slc []string) []string {
	result := []string{}
	tempMap := map[string]byte{}
	for _, e := range slc {
		l := len(tempMap)
		tempMap[e] = 0
		if len(tempMap) != l {
			result = append(result, e)
		}
	}
	return result
}

func (q *qingcloudAPIWrapper) attachNicTag(nics []string) {
	if q.opts.Tag == "" {
		return
	}
	tagID := q.opts.Tag

	for _, nic := range nics {
		input := &service.AttachTagsInput{
			ResourceTagPairs: []*service.ResourceTagPair{
				{
					ResourceID:   &nic,
					ResourceType: service.String(string(constants.ResourceTypeNic)),
					TagID:        service.String(tagID),
				},
			},
		}
		_, _ = q.tagService.AttachTags(input)
	}
}

func (q *qingcloudAPIWrapper) CreateVIPs(vxnet *rpc.VxNet) (string, error) {
	vipName := constants.NicPrefix + vxnet.ID
	vipRange := fmt.Sprintf("%s-%s", vxnet.IPStart, vxnet.IPEnd)
	count := IPRangeCount(vxnet.IPStart, vxnet.IPEnd)
	input := &service.CreateVIPsInput{
		Count:    &count,
		VIPName:  &vipName,
		VxNetID:  &vxnet.ID,
		VIPRange: &vipRange,
	}

	output, err := q.vipService.CreateVIPs(input)
	if err != nil {
		log.Errorf("failed to CreateVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	return *output.JobID, nil
}

func (q *qingcloudAPIWrapper) DescribeVIPs(vxnet *rpc.VxNet) ([]*rpc.VIP, error) {
	vipName := constants.NicPrefix + vxnet.ID
	input := &service.DescribeVxNetsVIPsInput{
		VIPName: &vipName,
		VxNets:  []*string{&vxnet.ID},
		// TODO: Limit not work, max item is 100
		Limit: service.Int(constants.VIPNumLimit),
	}

	output, err := q.vipService.DescribeVxNetsVIPs(input)
	if err != nil {
		log.Errorf("failed to DescribeVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var vips []*rpc.VIP
	for _, vip := range output.VIPSet {
		vipItem := &rpc.VIP{
			ID:      *vip.VIPID,
			Name:    *vip.VIPName,
			Addr:    *vip.VIPAddr,
			VxNetID: *vip.VxNetID,
		}
		vips = append(vips, vipItem)
	}

	return vips, nil
}

func (q *qingcloudAPIWrapper) DeleteVIPs(vips []string) (string, error) {
	if len(vips) <= 0 {
		return "", nil
	}

	input := &service.DeleteVIPsInput{
		VIPs: service.StringSlice(vips),
	}

	output, err := q.vipService.DeleteVIPs(input)
	if err != nil {
		log.Errorf("failed to DeleteVIPs: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	return *output.JobID, nil
}

func (q *qingcloudAPIWrapper) CreateSecurityGroupRuleForVxNet(sg string, vxnet *rpc.VxNet) (string, error) {
	input := &service.AddSecurityGroupRulesInput{
		SecurityGroup: &sg,
		Rules: []*service.SecurityGroupRule{
			{
				SecurityGroupRuleName: service.String(constants.NicPrefix + vxnet.ID),
				Action:                service.String("accept"),
				Direction:             service.Int(0),
				Priority:              service.Int(0),
				Protocol:              service.String("all"),
				Val3:                  service.String(vxnet.Network),
			},
		},
	}

	output, err := q.sgService.AddSecurityGroupRules(input)
	if err != nil || *output.RetCode != 0 {
		log.Errorf("failed to AddSecurityGroupRules: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	o, err := q.sgService.ApplySecurityGroup(&service.ApplySecurityGroupInput{
		SecurityGroup: service.String(sg),
	})
	if err != nil || *output.RetCode != 0 {
		log.Errorf("failed to ApplySecurityGroup: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	return *o.JobID, nil
}

func (q *qingcloudAPIWrapper) GetSecurityGroupRuleForVxNet(sg string, vxnet *rpc.VxNet) (*rpc.SecurityGroupRule, error) {
	input := &service.DescribeSecurityGroupRulesInput{
		SecurityGroup: service.String(sg),
		Direction:     service.Int(0),
	}

	output, err := q.sgService.DescribeSecurityGroupRules(input)
	if err != nil || *output.RetCode != 0 {
		log.Errorf("failed to DescribeSecurityGroupRules: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	for _, rule := range output.SecurityGroupRuleSet {
		if *rule.Val3 == vxnet.Network && *rule.Action == "accept" && *rule.Protocol == "all" {
			return &rpc.SecurityGroupRule{
				ID:              *rule.SecurityGroupRuleID,
				Name:            *rule.SecurityGroupRuleName,
				SecurityGroupID: *rule.SecurityGroupID,
				Action:          *rule.Action,
				Protocol:        *rule.Protocol,
				Val3:            *rule.Val3,
				Direction:       int32(*rule.Direction),
				Priority:        int32(*rule.Priority),
			}, nil
		}
	}

	return nil, nil
}

func (q *qingcloudAPIWrapper) DeleteSecurityGroupRuleForVxNet(sgr string) error {
	input := &service.DeleteSecurityGroupRulesInput{
		SecurityGroupRules: []*string{service.String(sgr)},
	}

	output, err := q.sgService.DeleteSecurityGroupRules(input)
	if err != nil || *output.RetCode != 0 {
		log.Errorf("failed to DeleteSecurityGroupRules: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return err
	}

	return nil
}

func (q *qingcloudAPIWrapper) DescribeClusterSecurityGroup(clusterID string) (string, error) {
	input := &service.DescribeClustersInput{
		Clusters: []*string{service.String(clusterID)},
	}

	output, err := q.clusterService.DescribeClusters(input)
	if err != nil || *output.RetCode != 0 {
		log.Errorf("failed to DescribeClusters: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return "", err
	}

	for _, cluster := range output.ClusterSet {
		if *cluster.ClusterID == clusterID {
			return *cluster.SecurityGroupID, nil
		}
	}

	return "", nil
}

func (q *qingcloudAPIWrapper) DescribeClusterNodes(clusterID string) ([]*rpc.Node, error) {
	input := &service.DescribeClusterNodesInput{
		Cluster: service.String(clusterID),
	}

	output, err := q.clusterService.DescribeClusterNodes(input)
	if err != nil {
		log.Errorf("failed to DescribeClusterNodes: input (%s) output (%s) %v", spew.Sdump(input), spew.Sdump(output), err)
		return nil, err
	}

	var nodes []*rpc.Node
	for _, node := range output.NodeSet {
		item := &rpc.Node{
			InstanceID:  *node.InstanceID,
			NodeID:      *node.NodeID,
			HostMachine: *node.HostMachine,
			PrivateIP:   *node.PrivateIP,
			ClusterID:   *node.ClusterID,
			Status:      *node.Status,
		}
		nodes = append(nodes, item)
	}

	return nodes, nil
}

func IPRangeCount(from, to string) int {
	startIP := cnet.ParseIP(from)
	endIP := cnet.ParseIP(to)
	startInt := cnet.IPToBigInt(*startIP)
	endInt := cnet.IPToBigInt(*endIP)
	return int(big.NewInt(0).Sub(endInt, startInt).Int64() + 1)
}

func getIPEndAfterReserved(end string, reservedCount, customReservedCount int64) string {
	e := cnet.ParseIP(end)
	i := big.NewInt(0).Sub(cnet.IPToBigInt(*e), big.NewInt(reservedCount+customReservedCount))
	return cnet.BigIntToIP(i).String()
}

func getIPEndAfterReservedForVlan(start, end string, reservedCount, customReservedCount int64) string {
	s := cnet.ParseIP(start)
	e := cnet.ParseIP(end)
	i := big.NewInt(0).Sub(cnet.IPToBigInt(*e), big.NewInt((cnet.IPToBigInt(*e).Int64()-cnet.IPToBigInt(*s).Int64()+1)/reservedCount+customReservedCount))
	return cnet.BigIntToIP(i).String()
}
