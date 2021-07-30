package allocator

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	log "k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"github.com/yunify/qingcloud-sdk-go/service"
)

type nicStatus struct {
	Nic  *rpc.HostNic
	Pods map[string]*rpc.PodInfo
}

func (n *nicStatus) setNicPhase(pahse rpc.Phase) error {
	save := n.Nic.Phase
	n.Nic.Phase = pahse
	if err := db.SetNetworkInfo(n.Nic.VxNet.ID, n); err != nil {
		n.Nic.Phase = save
		return err
	}
	return nil
}

// always set status to Phase_Succeeded when add NicPod
func (n *nicStatus) addNicPod(pod *rpc.PodInfo) error {
	savePod := n.Pods[getContainterKey(pod)]
	saveStatus := n.Nic.Phase
	n.Pods[getContainterKey(pod)] = pod
	n.Nic.Phase = rpc.Phase_Succeeded
	if err := db.SetNetworkInfo(n.Nic.VxNet.ID, n); err != nil {
		if savePod == nil {
			delete(n.Pods, getContainterKey(pod))
			n.Nic.Phase = saveStatus
		} else {
			n.Pods[getContainterKey(pod)] = savePod
			n.Nic.Phase = saveStatus
		}
		return err
	}
	return nil
}

func (n *nicStatus) delNicPod(pod *rpc.PodInfo) error {
	save := n.Pods[getContainterKey(pod)]
	delete(n.Pods, getContainterKey(pod))
	if err := db.SetNetworkInfo(n.Nic.VxNet.ID, n); err != nil {
		n.Pods[getContainterKey(pod)] = save
		return err
	}
	return nil
}

func (n *nicStatus) isOK() bool {
	return n.Nic.Phase == rpc.Phase_Succeeded
}

func (n *nicStatus) getPhase() string {
	return n.Nic.Phase.String()
}

type Allocator struct {
	lock sync.RWMutex
	nics map[string]*nicStatus
	conf conf.PoolConf
}

func (a *Allocator) setNicStatus(nic *rpc.HostNic, pahse rpc.Phase) error {
	log.Infof("setNicStatus: %s %s", getNicKey(nic), pahse.String())
	if status, ok := a.nics[nic.VxNet.ID]; ok {
		if err := status.setNicPhase(pahse); err != nil {
			return err
		}
	} else {
		nicStatus := nicStatus{
			Nic:  nic,
			Pods: make(map[string]*rpc.PodInfo),
		}
		if err := nicStatus.setNicPhase(pahse); err != nil {
			return err
		} else {
			a.nics[nic.VxNet.ID] = &nicStatus
		}
	}

	return nil
}

func (a *Allocator) addNicPod(nic *rpc.HostNic, info *rpc.PodInfo) error {
	log.Infof("addNicPod: %s %s", getNicKey(nic), getPodKey(info))
	if status, ok := a.nics[nic.VxNet.ID]; ok {
		if err := status.addNicPod(info); err != nil {
			return err
		}
	} else {
		nicStatus := nicStatus{
			Nic:  nic,
			Pods: make(map[string]*rpc.PodInfo),
		}
		if err := nicStatus.addNicPod(info); err != nil {
			return err
		} else {
			a.nics[nic.VxNet.ID] = &nicStatus
		}
	}

	return nil
}

func (a *Allocator) delNicPod(nic *rpc.HostNic, info *rpc.PodInfo) error {
	log.Infof("delNicPod: %s %s", getNicKey(nic), getPodKey(info))
	if status, ok := a.nics[nic.VxNet.ID]; ok {
		if err := status.delNicPod(info); err != nil {
			return err
		}
	}

	return nil
}

func (a *Allocator) getNicRouteTableNum(nic *rpc.HostNic) int32 {
	if nic.RouteTableNum <= 0 {
		exists := make(map[int]bool)
		for _, nic := range a.nics {
			exists[int(nic.Nic.RouteTableNum)] = true
		}
		for start := a.conf.RouteTableBase; ; start++ {
			if !exists[start] {
				log.Infof("Assign nic %s routetable num %d", getNicKey(nic), start)
				return int32(start)
			}
		}
	} else {
		return nic.RouteTableNum
	}
}

func (a *Allocator) getVxnets(vxnet string) (*rpc.VxNet, error) {
	for _, nic := range a.nics {
		if nic.Nic.VxNet.ID == vxnet {
			return nic.Nic.VxNet, nil
		}
	}

	result, err := qcclient.QClient.GetVxNets([]string{vxnet})
	if err != nil {
		return nil, err
	}

	if v, ok := result[vxnet]; !ok {
		return nil, fmt.Errorf("Get vxnet %s from qingcloud: not found", vxnet)
	} else {
		return v, nil
	}
}

func (a *Allocator) canAlloc() int {
	return a.conf.MaxNic - len(a.nics)
}

func (a *Allocator) AllocHostNic(args *rpc.PodInfo) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	vxnetName := args.VxNet
	if nic, ok := a.nics[vxnetName]; ok {
		log.Infof("Find hostNic %s: %s", getNicKey(nic.Nic), nic.getPhase())
		if nic.isOK() {
			// just update Nic's pods
			if err := a.addNicPod(nic.Nic, args); err != nil {
				log.Errorf("addNicPod failed: %s %s %v", getNicKey(nic.Nic), getPodKey(args), err)
			}
			return nic.Nic, nil
		} else {
			// create bridge and rule here
			phase, err := networkutils.NetworkHelper.SetupNetwork(nic.Nic)
			if err != nil {
				if err := a.setNicStatus(nic.Nic, phase); err != nil {
					log.Errorf("setNicStatus failed: %s %s %v", getNicKey(nic.Nic), phase.String(), err)
				}
				return nil, err
			}
			if err := a.addNicPod(nic.Nic, args); err != nil {
				log.Errorf("addNicPod failed: %s %s %v", getNicKey(nic.Nic), getPodKey(args), err)
			}
			return nic.Nic, nil
		}
	}

	if a.canAlloc() <= 0 {
		return nil, constants.ErrNoAvailableNIC
	}

	vxnet, err := a.getVxnets(vxnetName)
	if err != nil {
		return nil, err
	}
	nics, _, err := qcclient.QClient.CreateNicsAndAttach(vxnet, 1, nil, 1)
	if err != nil {
		return nil, err
	}
	log.Infof("create and attach nic %s", getNicKey(nics[0]))

	//wait for nic attach
	for {
		link, err := networkutils.NetworkHelper.LinkByMacAddr(nics[0].HardwareAddr)
		if err != nil && err != constants.ErrNicNotFound {
			return nil, err
		}
		if link != nil {
			break
		}
		time.Sleep(1 * time.Second)
	}

	log.Infof("attach nic %s success", getNicKey(nics[0]))

	nics[0].Reserved = true
	nics[0].RouteTableNum = a.getNicRouteTableNum(nics[0])

	// create bridge and rule here
	phase, err := networkutils.NetworkHelper.SetupNetwork(nics[0])
	if err != nil {
		if err := a.setNicStatus(nics[0], phase); err != nil {
			log.Errorf("setNicStatus failed: %s %s %v", getNicKey(nics[0]), phase.String(), err)
		}
		return nil, err
	}

	if err := a.addNicPod(nics[0], args); err != nil {
		log.Errorf("addNicPod failed: %s %s %v", getNicKey(nics[0]), getPodKey(args), err)
	}

	return nics[0], nil
}

func (a *Allocator) FreeHostNic(args *rpc.PodInfo, peek bool) (*rpc.HostNic, string, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, status := range a.nics {
		if pod, ok := status.Pods[getContainterKey(args)]; ok {
			if err := a.delNicPod(status.Nic, pod); err != nil {
				log.Errorf("delNicPod failed: %s %s %v", getNicKey(status.Nic), getPodKey(args), err)
			}
			return status.Nic, pod.PodIP, nil
		}
	}

	/*
		_, err := qcclient.QClient.DeattachNics([]string{result.Nic.ID}, false)
		if err == nil {
			log.WithError(err).Infof("deattach nic %s", result.Nic.ID)
		} else {
			log.WithError(err).Infof("failed to deattach nic %s", result.Nic.ID)
		}
		return result.Nic, nil
	*/

	return nil, "", nil
}

func (a *Allocator) HostNicCheck() {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, nic := range a.nics {
		if !nic.isOK() {
			phase, err := networkutils.NetworkHelper.CheckAndRepairNetwork(nic.Nic)
			if err := a.setNicStatus(nic.Nic, phase); err != nil {
				log.Errorf("setNicStatus failed: %s %s %v", getNicKey(nic.Nic), phase.String(), err)
			}
			log.Infof("Repair hostNic %s: %s %v", getNicKey(nic.Nic), nic.getPhase(), err)
		}
	}
}

func (a *Allocator) Start(stopCh <-chan struct{}) error {
	go a.run(stopCh)
	return nil
}

func (a *Allocator) GetNics() map[string]*nicStatus {
	return a.nics
}

func (a *Allocator) ClearFreeHostnic(force bool) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	maxVxnetNicsCount := a.getVxnetMaxNicNum()
	if len(a.nics) < a.conf.NodeThreshold && maxVxnetNicsCount < a.conf.VxnetThreshold && !force {
		log.Infof("no hostnic to free:%d %d %d %d", len(a.nics), maxVxnetNicsCount, a.conf.NodeThreshold, a.conf.VxnetThreshold)
		return nil
	}
	var nicsToDel []string
	var vxnetToDel []string
	for k, v := range a.nics {
		log.Infof("clearFreeHostnic: %s %s %s %d %d %d", k, v.Nic.VxNet, v.Nic.ID, maxVxnetNicsCount, a.conf.NodeThreshold, a.conf.VxnetThreshold)
		if len(v.Pods) == 0 {
			nicsToDel = append(nicsToDel, v.Nic.ID)
			vxnetToDel = append(vxnetToDel, v.Nic.VxNet.ID)
			err := networkutils.NetworkHelper.CleanupNetwork(v.Nic)
			if err != nil {
				log.Infof("cleanupNetwork:%v %v", v.Nic, err)
			}
		}
	}
	deattachNics(nicsToDel)
	err := a.deleteNicsWithRetry(constants.FreeHostnicRetry, nicsToDel, vxnetToDel)
	return err
}

func (a *Allocator) getVxnetMaxNicNum() int {
	cache := make(map[string]bool)
	for _, v := range a.nics {
		cache[v.Nic.VxNet.ID] = true
	}
	maxVxnetNicsCount := 0
	for k, _ := range cache {
		//this api can not get nics by all vxnets,so loop to get nics per vxnet,iaas need to fix this?
		num := constants.VxnetNicNumLimit
		offset := 0
		input := &service.DescribeNicsInput{
			Limit:  &num,
			Offset: &offset,
			VxNets: []*string{&k},
		}
		createdNics, err := qcclient.QClient.GetCreatedNics(input)
		if err == nil && len(createdNics) > maxVxnetNicsCount {
			maxVxnetNicsCount = len(createdNics)
		}
	}
	return maxVxnetNicsCount
}

func deattachNics(nics []string) error {
	_, err := qcclient.QClient.DeattachNics(nics, true)
	if err == nil {
		log.Infof("deattach hostnics success:%v", nics)
	} else {
		log.Infof("deattach hostnics failed:%v %v", nics, err)
	}
	return err
}

func (a *Allocator) deleteNicsWithRetry(retry int, nics []string, vxnets []string) error {
	var err error
	for i := 0; i < retry; i++ {
		err = qcclient.QClient.DeleteNics(nics)
		if err == nil || strings.Contains(err.Error(), constants.ResourceNotFound) {
			log.Infof("delete hostnics success:%v", nics)
			for _, vxnet := range vxnets {
				log.Infof("delete cache and db:%v", vxnet)
				db.DeleteNetworkInfo(vxnet)
				delete(a.nics, vxnet)
			}
			break
		} else {
			log.Infof("delete hostnics failed:%d %v %v", i, nics, err)
		}
		time.Sleep(1 * time.Second)
	}
	return err
}

func (a *Allocator) run(stopCh <-chan struct{}) {
	jobTimer := time.NewTicker(time.Duration(a.conf.Sync) * time.Second).C
	freeTimer := time.NewTicker(time.Duration(a.conf.FreePeriod) * time.Minute).C
	for {
		select {
		case <-stopCh:
			log.Info("stoped allocator")
			return
		case <-jobTimer:
			log.Infof("period job sync")
			a.HostNicCheck()
		case <-freeTimer:
			log.Infof("period free sync")
			a.ClearFreeHostnic(false)
		}
	}
}

var (
	Alloc *Allocator
)

func SetupAllocator(conf conf.PoolConf) {
	Alloc = &Allocator{
		nics: make(map[string]*nicStatus),
		conf: conf,
	}

	err := db.Iterator(func(value interface{}) error {
		var nic nicStatus
		json.Unmarshal(value.([]byte), &nic)
		Alloc.nics[nic.Nic.VxNet.ID] = &nic
		return nil
	})
	if err != nil {
		log.Fatalf("Failed to restore allocator from leveldb: %v", err)
	}

	// restore create nics
	num := constants.NicNumLimit
	offset := 0
	input := &service.DescribeNicsInput{
		Limit:   &num,
		Offset:  &offset,
		NICName: service.String(constants.NicPrefix + qcclient.QClient.GetInstanceID()),
	}
	nics, err := qcclient.QClient.GetCreatedNics(input)
	if err != nil {
		log.Fatalf("Failed to get created nics from qingcloud: %v", err)
	}

	for _, nic := range nics {
		if status, ok := Alloc.nics[nic.VxNet.ID]; !ok {
			// nic not attached at this node
		} else {
			Alloc.setNicStatus(status.Nic, rpc.Phase_Init)
			log.Infof("Restore create nic %s %s routetable num %d", nic.ID, getNicKey(status.Nic), status.Nic.RouteTableNum)
		}
	}
}

func getContainterKey(info *rpc.PodInfo) string {
	return info.Containter
}

func getPodKey(info *rpc.PodInfo) string {
	return info.Namespace + "/" + info.Name + "/" + info.Containter
}

func getNicKey(nic *rpc.HostNic) string {
	return nic.VxNet.ID + "/" + nic.ID
}

func GetNicKey(nic *rpc.HostNic) string {
	if nic == nil || nic.VxNet == nil {
		return ""
	}
	return getNicKey(nic)
}
