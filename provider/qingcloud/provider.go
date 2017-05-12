package qingcloud

import (
	"errors"
	"fmt"

	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/provider"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"

	"io/ioutil"
	"math/rand"
	"time"
)

const (
	INSTANCE_ID_FILE = "/etc/qingcloud/instance-id"
)

func init() {
	rand.Seed(time.Now().UnixNano())
	provider.Register("qingcloud", New)
}

const (
	defaultOpTimeout    = 180 * time.Second
	defaultWaitInterval = 5 * time.Second
)

//QCNicProvider QingCloud Nic provider object
type QCNicProvider struct {
	nicService   *service.NicService
	vxNetService *service.VxNetService
	jobService   *service.JobService
	vxNets       []string
}

// New create new nic provider object
func New(configmap map[string]interface{}) (provider.NicProvider, error) {
	qcniconfig, err := DecodeConfiguration(configmap)
	if err != nil {
		return nil, err
	}
	qsdkconfig, err := config.New(qcniconfig.QyAccessKeyID, qcniconfig.QySecretAccessKey)
	if err != nil {
		return nil, err
	}
	qsdkconfig.Zone = qcniconfig.Zone
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

	p := &QCNicProvider{
		nicService:   nicService,
		jobService:   jobService,
		vxNetService: vxNetService,
		vxNets:       qcniconfig.VxNets,
	}
	return p, nil
}

//TODO check vxnet capacity
func (p *QCNicProvider) chooseVxNet() string {
	if len(p.vxNets) == 1 {
		return p.vxNets[0]
	}
	idx := rand.Intn(len(p.vxNets))
	return p.vxNets[idx]
}

func (p *QCNicProvider) CreateNic() (*pkg.HostNic, error) {
	instanceID, err := loadInstanceID()
	if err != nil {
		return nil, err
	}

	vxNetID := p.chooseVxNet()
	vxNet, err := p.getVxNet(vxNetID)
	if err != nil {
		return nil, err
	}

	input := &service.CreateNicsInput{
		VxNet:   &vxNetID,
		NICName: pkg.StringPtr(fmt.Sprintf("hostnic_%s", instanceID))}
	output, err := p.nicService.CreateNics(input)
	//TODO check too many nic in vxnet err, and retry with another vxnet.
	if err != nil {
		return nil, err
	}

	if *output.RetCode == 0 && len(output.Nics) > 0 {
		nic := output.Nics[0]
		hostNic := &pkg.HostNic{ID: *nic.NICID, HardwareAddr: *nic.NICID, Address: *nic.PrivateIP}
		err := p.attachNic(hostNic, instanceID)
		if err != nil {
			return nil, err
		}
		hostNic.VxNet = vxNet
		return hostNic, nil
	}
	return nil, errors.New(fmt.Sprintf("CreateNic output [%+v] error", *output))
}

func (p *QCNicProvider) attachNic(hostNic *pkg.HostNic, instanceID string) error {
	input := &service.AttachNicsInput{Nics: []*string{&hostNic.HardwareAddr}, Instance: &instanceID}
	output, err := p.nicService.AttachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		err := client.WaitJob(p.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New(fmt.Sprintf("AttachNics output [%+v] error", *output))
}

func (p *QCNicProvider) detachNic(nicID string) error {
	input := &service.DetachNicsInput{Nics: []*string{&nicID}}
	output, err := p.nicService.DetachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		err := client.WaitJob(p.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
		if err != nil {
			return err
		}
		return nil
	}
	return errors.New(fmt.Sprintf("DetachNics output [%+v] error", *output))
}

func (p *QCNicProvider) DeleteNic(nicID string) error {
	err := p.detachNic(nicID)
	if err != nil {
		return err
	}
	input := &service.DeleteNicsInput{Nics: []*string{&nicID}}
	output, err := p.nicService.DeleteNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		return nil
	}
	return errors.New(fmt.Sprintf("DeleteNics output [%+v] error", *output))
}

func (p *QCNicProvider) getVxNet(vxNet string) (*pkg.VxNet, error) {
	input := &service.DescribeVxNetsInput{VxNets: []*string{&vxNet}}
	output, err := p.vxNetService.DescribeVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		qcVxNet := output.VxNetSet[0]
		vxNet := &pkg.VxNet{ID: *qcVxNet.VxNetID, GateWay: *qcVxNet.Router.ManagerIP, Network: *qcVxNet.Router.IPNetwork}
		return vxNet, nil
	}
	return nil, fmt.Errorf("DescribeVxNets invalid output [%+v]", *output)
}

func loadInstanceID() (string, error) {
	content, err := ioutil.ReadFile(INSTANCE_ID_FILE)
	if err != nil {
		return "", fmt.Errorf("Load instance-id from %s error: %v", INSTANCE_ID_FILE, err)
	}
	return string(content), nil
}
