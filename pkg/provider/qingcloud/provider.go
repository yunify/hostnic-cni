//
// =========================================================================
// Copyright (C) 2017 by Yunify, Inc...
// -------------------------------------------------------------------------
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this work except in compliance with the License.
// You may obtain a copy of the License in the LICENSE file, or at:
//
//  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
// =========================================================================
//

package qingcloud

import (
	"fmt"
	"io/ioutil"
	"math/rand"
	"time"

	"net"

	"github.com/vishvananda/netlink"
	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/logger"
	"github.com/yunify/qingcloud-sdk-go/service"
	qcutil "github.com/yunify/qingcloud-sdk-go/utils"
)

const (
	instanceIDFile = "/etc/qingcloud/instance-id"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

const (
	defaultOpTimeout     = 180 * time.Second
	defaultWaitInterval  = 10 * time.Second
	waitNicLocalTimeout  = 20 * time.Second
	waitNicLocalInterval = 2 * time.Second
)

//QCNicProvider QingCloud Nic provider object
type QCNicProvider struct {
	nicService       *service.NicService
	vxNetService     *service.VxNetService
	jobService       *service.JobService
	instanceService  *service.InstanceService
	vxNets           []string
	Host             *pkg.HostInstance
	isUnderAppCenter bool
}

// New create new nic provider object
func NewQCNicProvider(qyAuthFilePath string, vxnets []string, isUnderAppCenter bool) (*QCNicProvider, error) {
	qsdkconfig, err := config.NewDefault()
	if err != nil {
		return nil, err
	}
	if qyAuthFilePath != "" {
		if err = qsdkconfig.LoadConfigFromFilepath(qyAuthFilePath); err != nil {
			return nil, err
		}
	}
	if len(vxnets) <= 0 {
		return nil, fmt.Errorf("vxnet is empty")
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

	p := &QCNicProvider{
		nicService:       nicService,
		jobService:       jobService,
		vxNetService:     vxNetService,
		vxNets:           vxnets,
		instanceService:  instanceService,
		isUnderAppCenter: isUnderAppCenter,
	}

	//describe instance to test if provider works
	p.Host, err = p.getInstance()
	if err != nil {
		return nil, err
	}

	if p.Host.RouterID == "" {
		return nil, fmt.Errorf("Instance is not managed by router, Please put instance under VPC's supervision")
	}

	var vxnetids []*string
	for _, vxnet := range vxnets {
		id := vxnet
		vxnetids = append(vxnetids, &id)
	}

	vxnetItems, err := p.GetVxNets(vxnetids)
	if err != nil {
		return nil, err
	}

	for _, vxnetItem := range vxnetItems {
		if vxnetItem.RouterID == "" {
			return nil, fmt.Errorf("vxnet %s is not managed by VPC", vxnetItem.ID)
		}
		if vxnetItem.RouterID != p.Host.RouterID {
			return nil, fmt.Errorf("vxnet %s is not managed by the very router where the instance resides.", vxnetItem.ID)
		}
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

//CreateNic create network interface card and attach to host machine
func (p *QCNicProvider) CreateNic() (*pkg.HostNic, error) {
	vxNetID := p.chooseVxNet()
	return p.CreateNicInVxnet(vxNetID)
}

//CreateNicInVxnet create network interface card in vxnet and attach to host
func (p *QCNicProvider) CreateNicInVxnet(vxNetID string) (*pkg.HostNic, error) {
	instanceID := p.Host.InstanceID

	vxNet, err := p.GetVxNet(vxNetID)
	if err != nil {
		return nil, err
	}
	input := &service.CreateNicsInput{
		VxNet:   &vxNetID,
		NICName: pkg.StringPtr(fmt.Sprintf("hostnic_%s", instanceID))}
	output, err := p.nicService.CreateNics(input)
	//TODO check too many nic in vDxnet err, and retry with another vxnet.
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
		niclink, err := pkg.LinkByMacAddr(*nic.NICID)
		if err != nil {
			return nil, err
		}
		err = netlink.LinkSetDown(niclink)
		if err != nil {
			return nil, err
		}
		hostNic.VxNet = vxNet
		return hostNic, nil
	}
	return nil, fmt.Errorf("CreateNic output [%+v] error", *output)
}

func (p *QCNicProvider) attachNic(hostNic *pkg.HostNic, instanceID string) error {
	input := &service.AttachNicsInput{Nics: []*string{&hostNic.HardwareAddr}, Instance: &instanceID}
	output, err := p.nicService.AttachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		err := p.waitNic(hostNic.ID, jobID)
		if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("AttachNics output [%+v] error", *output)
}

func (p *QCNicProvider) waitNic(nicID string, jobID string) error {
	logger.Debug("Wait for nic %v", nicID)
	err := qcutil.WaitForSpecific(func() bool {
		link, err := pkg.LinkByMacAddr(nicID)
		if err != nil {
			return false
		}
		logger.Debug("Find link %s %s", link.Attrs().Name, nicID)
		if link.Attrs().Flags&net.FlagUp != 0 && link.Attrs().OperState&netlink.OperUp != 0 {

			return true
		}
		return false
	}, waitNicLocalTimeout, waitNicLocalInterval)
	if _, ok := err.(*qcutil.TimeoutError); ok {
		logger.Info("Wait nic %s by local timeout", nicID)
		err = client.WaitJob(p.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
	}
	return err
}

func (p *QCNicProvider) detachNic(nicID string) error {
	return p.detachNics([]*string{&nicID})
}

func (p *QCNicProvider) detachNics(nicIDs []*string) error {
	input := &service.DetachNicsInput{Nics: nicIDs}
	output, err := p.nicService.DetachNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		jobID := *output.JobID
		//TODO optimize detachNic wait
		err := client.WaitJob(p.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
		if err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("DetachNics output [%+v] error", *output)
}

func (p *QCNicProvider) DeleteNic(nicID string) error {
	return p.DeleteNics([]*string{&nicID})
}

//DeleteNic delete nic from host
func (p *QCNicProvider) DeleteNics(nicIDs []*string) error {
	err := p.detachNics(nicIDs)
	if err != nil {
		return err
	}
	input := &service.DeleteNicsInput{Nics: nicIDs}
	output, err := p.nicService.DeleteNics(input)
	if err != nil {
		return err
	}
	if *output.RetCode == 0 {
		return nil
	}
	return fmt.Errorf("DeleteNics output [%+v] error", *output)
}

func (p *QCNicProvider) GetVxNet(vxNet string) (*pkg.VxNet, error) {
	result, err := p.GetVxNets([]*string{&vxNet})
	if err != nil {
		return nil, err
	}
	if len(result) > 0 {
		return result[0], nil
	}
	return nil, fmt.Errorf("vxnet %s is not found", vxNet)
}

func (p *QCNicProvider) getInstance() (*pkg.HostInstance, error) {
	id, err := loadInstanceID()
	if err != nil {
		return nil, err
	}
	input := &service.DescribeInstancesInput{
		Instances: []*string{&id},
	}
	if p.isUnderAppCenter {
		flag := 1
		input.IsClusterNode = &flag
	}
	output, err := p.instanceService.DescribeInstances(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		if len(output.InstanceSet) <= 0 {
			return nil, fmt.Errorf("Instance %s not found", id)
		}
		instanceItem := output.InstanceSet[0]
		var vxnetIds []*string
		for _, vxnetItem := range instanceItem.VxNets {
			vxnetIds = append(vxnetIds, vxnetItem.VxNetID)
		}

		vxnets, err := p.GetVxNets(vxnetIds)
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
		return &pkg.HostInstance{
			InstanceID: id,
			RouterID:   routerID,
		}, nil
	}
	return nil, fmt.Errorf("Failed to describe instance, %s", *output.Message)
}

func (p *QCNicProvider) GetVxNets(vxNets []*string) ([]*pkg.VxNet, error) {
	input := &service.DescribeVxNetsInput{VxNets: vxNets}
	output, err := p.vxNetService.DescribeVxNets(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		var vxNets []*pkg.VxNet
		for _, qcVxNet := range output.VxNetSet {
			var routerID string
			if router := *qcVxNet.VpcRouterID; router != "" {
				routerID = router
			} else {
				routerID = ""
			}
			vxnetItem := &pkg.VxNet{ID: *qcVxNet.VxNetID, RouterID: routerID}
			if qcVxNet.Router != nil {
				vxnetItem.GateWay = *qcVxNet.Router.ManagerIP
				vxnetItem.Network = *qcVxNet.Router.IPNetwork
			}
			vxNets = append(vxNets, vxnetItem)
		}
		return vxNets, nil
	}
	return nil, fmt.Errorf("DescribeVxNets invalid output [%+v]", *output)
}

func (p *QCNicProvider) GetNics(idlist []*string) ([]*pkg.HostNic, error) {
	if len(idlist) <= 0 {
		return []*pkg.HostNic{}, nil
	}

	input := &service.DescribeNicsInput{
		Nics: idlist,
	}

	output, err := p.nicService.DescribeNics(input)
	if err != nil {
		return nil, err
	}
	if *output.RetCode == 0 {
		vxnetmap := make(map[string]*pkg.VxNet)
		var niclist []*pkg.HostNic
		for _, nic := range output.NICSet {
			var vxnet *pkg.VxNet
			if item, ok := vxnetmap[*nic.VxNetID]; ok {
				vxnet = item
			} else {
				vxnet, err = p.GetVxNet(*nic.VxNetID)
				if err != nil {
					return []*pkg.HostNic{}, err
				}
				vxnetmap[*nic.VxNetID] = vxnet
			}
			niclist = append(niclist, &pkg.HostNic{
				ID:           *nic.NICID,
				VxNet:        vxnet,
				HardwareAddr: *nic.NICID,
				Address:      *nic.PrivateIP,
			})
		}
		return niclist, nil
	}
	return nil, fmt.Errorf("DescribeNics Failed [%+v]", *output)
}

func loadInstanceID() (string, error) {
	content, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		return "", fmt.Errorf("Load instance-id from %s error: %v", instanceIDFile, err)
	}
	return string(content), nil
}
