package qingcloud

import (
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/qingcloud-sdk-go/service"
	"fmt"
	"errors"
	"github.com/yunify/qingcloud-sdk-go/client"
	"time"
	"github.com/yunify/qingcloud-sdk-go/config"
	"math/rand"
	"net"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	defaultOpTimeout    = 180 * time.Second
	defaultWaitInterval = 5 * time.Second
)


type QCNicProvider struct{
	nicService *service.NicService
	vxNetService *service.VxNetService
	jobService *service.JobService
	vxNets     []string
}

func NewQCNicProvider(configFile string, vxNets []string) (*QCNicProvider, error){
	if len(vxNets) == 0 {
		return nil, errors.New("vxnets miss.")
	}

	config, err := config.NewDefault()
	if err != nil {
		return nil, err
	}

	if configFile != "" {
		err := config.LoadConfigFromFilepath(configFile)
		if err != nil {
			return nil, err
		}
	}

	qcService, err := service.Init(config)
	if err != nil {
		return nil, err
	}
	nicService, err := qcService.Nic(config.Zone)
	if err != nil {
		return nil, err
	}
	jobService, err := qcService.Job(config.Zone)
	if err != nil {
		return nil, err
	}
	vxNetService, err := qcService.VxNet(config.Zone)
	if err != nil {
		return nil, err
	}

	p := &QCNicProvider{
		nicService: nicService,
		jobService: jobService,
		vxNetService: vxNetService,
		vxNets:     vxNets,
	}
	return p, nil
}

//TODO check vxnet capacity
func (p *QCNicProvider) chooseVxNet() string {
	if len(p.vxNets) == 1 {
		return p.vxNets[0]
	}else {
		idx := rand.Intn(len(p.vxNets))
		return p.vxNets[idx]
	}
}

func (p *QCNicProvider) CreateNic(instanceID string) (*pkg.HostNic, error) {
	vxNetID := p.chooseVxNet()
	vxNet, err := p.getVxNet(vxNetID)
	if err != nil {
		return nil, err
	}

	input := &service.CreateNicsInput{
		VxNet: &vxNetID,
		NICName:pkg.StringPtr(fmt.Sprintf("hostnic_%s",instanceID))}
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
	input := &service.AttachNicsInput{Nics:[]*string{&hostNic.HardwareAddr}, Instance: &instanceID}
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
	input := &service.DetachNicsInput{Nics:[]*string{&nicID}}
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
	input := &service.DeleteNicsInput{Nics:[]*string{&nicID}}
	output, err := p.nicService.DeleteNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		return nil
	}
	return errors.New(fmt.Sprintf("DeleteNics output [%+v] error", *output))
}

func (p *QCNicProvider) getVxNet(vxNet string) (*pkg.VxNet, error){
	input := &service.DescribeVxNetsInput{VxNets:[]*string{&vxNet}}
	output, err := p.vxNetService.DescribeVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		qcVxNet := output.VxNetSet[0]
		_, ipNet, err := net.ParseCIDR(*qcVxNet.Router.IPNetwork)
		if err != nil {
			return nil, err
		}
		vxNet := &pkg.VxNet{ID: *qcVxNet.VxNetID, GateWay: net.ParseIP(*qcVxNet.Router.ManagerIP), Network: *ipNet}
		return vxNet, nil
	}
	return nil, fmt.Errorf("DescribeVxNets invalid output [%+v]", *output)
}