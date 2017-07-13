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
	"net"
	"time"

	"github.com/yunify/hostnic-cni/pkg"
	"github.com/yunify/hostnic-cni/provider"
	"github.com/yunify/qingcloud-sdk-go/client"
	"github.com/yunify/qingcloud-sdk-go/config"
	"github.com/yunify/qingcloud-sdk-go/service"
	qcutil "github.com/yunify/qingcloud-sdk-go/utils"
	"github.com/yunify/qingstor-sdk-go/logger"
)

const (
	instanceIDFile = "/etc/qingcloud/instance-id"
)

func init() {
	rand.Seed(time.Now().UnixNano())
	provider.Register("qingcloud", New)
}

const (
	defaultOpTimeout     = 180 * time.Second
	defaultWaitInterval  = 10 * time.Second
	waitNicLocalTimeout  = 20 * time.Second
	waitNicLocalInterval = 2 * time.Second
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
	qsdkconfig, err := config.NewDefault()
	if err != nil {
		return nil, err
	}
	if qcniconfig.ProviderConfigFile != "" {
		if err = qsdkconfig.LoadConfigFromFilepath(qcniconfig.ProviderConfigFile); err != nil {
			return nil, err
		}
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

//CreateNic create network interface card and attach to host machine
func (p *QCNicProvider) CreateNic() (*pkg.HostNic, error) {
	vxNetID := p.chooseVxNet()
	return p.CreateNicInVxnet(vxNetID)
}

//CreateNicInVxnet create network interface card in vxnet and attach to host
func (p *QCNicProvider) CreateNicInVxnet(vxNetID string) (*pkg.HostNic, error) {
	instanceID, err := loadInstanceID()
	if err != nil {
		return nil, err
	}

	vxNet, err := p.GetVxNet(vxNetID)
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
		return true
	}, waitNicLocalTimeout, waitNicLocalInterval)
	if _, ok := err.(*qcutil.TimeoutError); ok {
		logger.Info("Wait nic %s by local timeout", nicID)
		err = client.WaitJob(p.jobService, jobID, defaultOpTimeout, defaultWaitInterval)
	}
	return err
}

func (p *QCNicProvider) detachNic(nicID string) error {
	input := &service.DetachNicsInput{Nics: []*string{&nicID}}
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

//DeleteNic delete nic from host
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
	return fmt.Errorf("DeleteNics output [%+v] error", *output)
}

//GetNicsUnderCurNamesp get a list of nics on current host machine
func (p *QCNicProvider) GetNicsUnderCurNamesp(vxNetID *string) (result []*pkg.HostNic, err error) {
	//get a list of nic
	localnics, err := net.Interfaces()

	if err != nil {
		return nil, err
	}

	vxnet, err := p.GetVxNet(*vxNetID)
	if err != nil {
		return nil, err
	}

	//get vxnet info
	_, network, err := net.ParseCIDR(vxnet.Network)
	if err != nil {
		return nil, err
	}
	for _, nic := range localnics {

		if nic.Flags&net.FlagUp != 0 && nic.Flags&net.FlagLoopback == 0 {

			addrs, err := nic.Addrs()
			if err != nil {
				return nil, err
			}
			for _, addr := range addrs {
				ip, _, err := net.ParseCIDR(addr.String())

				if err != nil {
					return nil, err
				}
				if network.Contains(ip) {
					result = append(result, &pkg.HostNic{
						ID:           nic.HardwareAddr.String(),
						VxNet:        vxnet,
						HardwareAddr: nic.HardwareAddr.String(),
						Address:      ip.String(),
					})
				}
			}
		}
	}
	return result, err
}

func (p *QCNicProvider) GetVxNets() []string {
	return p.vxNets
}

func (p *QCNicProvider) GetVxNet(vxNet string) (*pkg.VxNet, error) {
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
	content, err := ioutil.ReadFile(instanceIDFile)
	if err != nil {
		return "", fmt.Errorf("Load instance-id from %s error: %v", instanceIDFile, err)
	}
	return string(content), nil
}
