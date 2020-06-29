package qcclient

import (
	"encoding/json"
	"fmt"
	"github.com/pkg/errors"
	"io/ioutil"
	"time"

	"github.com/davecgh/go-spew/spew"
	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg/constants"
	rpc "github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"
)

const (
	instanceIDFile      = "/etc/qingcloud/instance-id"
	defaultOpTimeout    = 180 * time.Second
	defaultWaitInterval = 5 * time.Second
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

	userID     string
	instanceID string
	opts       Options
}

// NewQingCloudClient create a qingcloud client to manipulate cloud resources
func SetupQingCloudClient(opts Options) {
	instanceID, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		log.WithError(err).Fatalf("failed to load instance-id")
	}

	qsdkconfig, err := config.NewDefault()
	if err != nil {
		log.WithError(err).Fatal("failed to new sdk default config")
	}
	if err = qsdkconfig.LoadUserConfig(); err != nil {
		log.WithError(err).Fatal("failed to load user config")
	}

	log.Infof("qsdkconfig inited %v", qsdkconfig)

	qcService, err := service.Init(qsdkconfig)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk service")
	}

	nicService, err := qcService.Nic(qsdkconfig.Zone)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk nic service")
	}

	vxNetService, err := qcService.VxNet(qsdkconfig.Zone)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk vxnet service")
	}

	jobService, err := qcService.Job(qsdkconfig.Zone)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk job service")
	}

	instanceService, err := qcService.Instance(qsdkconfig.Zone)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk instance service")
	}

	tagService, err := qcService.Tag(qsdkconfig.Zone)
	if err != nil {
		log.WithError(err).Fatal("failed to init qingcloud sdk tag service")
	}

	//useid
	api, _ := qcService.Accesskey(qsdkconfig.Zone)
	output, err := api.DescribeAccessKeys(&service.DescribeAccessKeysInput{
		AccessKeys: []*string{&qsdkconfig.AccessKeyID},
	})
	if err != nil {
		log.WithError(err).Fatal("failed to DescribeAccessKeys")
	}
	if len(output.AccessKeySet) == 0 {
		log.WithField("output", spew.Sdump(output)).Fatal("DescribeAccessKeys is empty")
	}
	userId := *output.AccessKeySet[0].Owner

	QClient = &qingcloudAPIWrapper{
		nicService:      nicService,
		vxNetService:    vxNetService,
		instanceService: instanceService,
		jobService:      jobService,
		tagService:      tagService,

		userID:     userId,
		instanceID: string(instanceID),
		opts:       opts,
	}
}

func (q *qingcloudAPIWrapper) GetInstanceID() string {
	return q.instanceID
}

func (q *qingcloudAPIWrapper) GetCreatedNics(num, offset int) ([]*rpc.HostNic, error) {
	input := &service.DescribeNicsInput{
		Limit:   &num,
		Offset:  &offset,
		NICName: service.String(constants.NicPrefix + q.instanceID),
	}
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.nicService.DescribeNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to GetCreatedNics")
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})

	output, err := q.nicService.DescribeNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to GetPrimaryNIC")
		return nil, err
	}

	var result []*rpc.HostNic
	for _, nic := range output.NICSet {
		result = append(result, constructHostnic(nil, nic))
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) AttachNics(nicIDs []string) (string, error) {
	input := &service.AttachNicsInput{
		Nics:     service.StringSlice(nicIDs),
		Instance: &q.instanceID,
	}

	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})

	output, err := q.nicService.AttachNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to AttachNics")
		return "", err
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
		ID:             *nic.NICID,
		VxNet:          vxnet,
		HardwareAddr:   *nic.NICID,
		PrimaryAddress: *nic.PrivateIP,
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})

	output, err := q.nicService.DescribeNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to GetNics")
		return nil, err
	}

	result := make(map[string]*rpc.HostNic)
	for _, nic := range output.NICSet {
		result[*nic.NICID] = constructHostnic(nil, nic)
	}

	return result, nil
}

func (q *qingcloudAPIWrapper) CreateNicsAndAttach(vxnet *rpc.VxNet, num int, ips []string) ([]*rpc.HostNic, string, error) {
	nicName := constants.NicPrefix + q.instanceID
	input := &service.CreateNicsInput{
		Count:      service.Int(num),
		VxNet:      &vxnet.ID,
		PrivateIPs: nil,
		NICName:    service.String(nicName),
	}
	if ips != nil {
		input.Count = service.Int(len(ips))
		input.PrivateIPs = service.StringSlice(ips)
	}
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.nicService.CreateNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to create nics")
		return nil, "", err
	}

	var (
		result []*rpc.HostNic
		nics   []string
	)
	for _, nic := range output.Nics {
		result = append(result, &rpc.HostNic{
			ID:             *nic.NICID,
			VxNet:          vxnet,
			HardwareAddr:   *nic.NICID,
			PrimaryAddress: *nic.PrivateIP,
		})
		nics = append(nics, *nic.NICID)
	}

	//may need to tag the card later.
	q.attachNicTag(nics)

	job, err := q.AttachNics(nics)
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.nicService.DetachNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to DeattachNics")
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.nicService.DeleteNics(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to DeleteNics")
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.jobService.DescribeJobs(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to GetJobs")
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
	scopedLog := log.WithFields(log.Fields{
		"input": spew.Sdump(input),
	})
	output, err := q.vxNetService.DescribeVxNets(input)
	scopedLog = scopedLog.WithFields(log.Fields{
		"output": spew.Sdump(output),
	})
	if err != nil {
		scopedLog.WithError(err).Error("failed to GetVxNets")
		return nil, err
	}

	var vxNets []*rpc.VxNet
	for _, qcVxNet := range output.VxNetSet {
		vxnetItem := &rpc.VxNet{
			ID: *qcVxNet.VxNetID,
		}

		if qcVxNet.Router != nil {
			vxnetItem.Gateway = *qcVxNet.Router.ManagerIP
			vxnetItem.Network = *qcVxNet.Router.IPNetwork
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
				&service.ResourceTagPair{
					ResourceID:   &nic,
					ResourceType: service.String(string(constants.ResourceTypeNic)),
					TagID:        service.String(tagID),
				},
			},
		}
		_, _ = q.tagService.AttachTags(input)
	}

	return
}
