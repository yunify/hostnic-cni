package allocator

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
	log "k8s.io/klog/v2"

	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
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

func (a *Allocator) delNic(vxnet string) error {
	log.Infof("delNic: %s", vxnet)
	if err := db.DeleteNetworkInfo(vxnet); err != nil {
		return err
	}
	delete(a.nics, vxnet)

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

	result, err := qcclient.QClient.GetVxNets([]string{vxnet}, 0)
	if err != nil {
		return nil, err
	}

	if v, ok := result[vxnet]; !ok {
		return nil, fmt.Errorf("get vxnet %s from qingcloud: not found", vxnet)
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
		return nil, fmt.Errorf("create and attach nic failed: %v", err)
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
			nicKey := getNicKey(status.Nic)
			podKey := getPodKey(args)
			if peek {
				klog.Infof("found db record for pod %s[%s] , ip: %s", nicKey, podKey, pod.PodIP)
				return status.Nic, pod.PodIP, nil
			}

			// delete nic pod record, this is the last step for delete a pod
			err := a.delNicPod(status.Nic, pod)
			if err != nil {
				return status.Nic, pod.PodIP, fmt.Errorf("clean db record for pod %s[%s] error: %v", nicKey, podKey, err)
			}
			log.Infof("clean db record for pod %s[%s] success", nicKey, podKey)
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
	klog.Infof("no db record for pod %s", getPodKey(args))
	return nil, "", nil
}

func (a *Allocator) HostNicCheck() {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, nic := range a.nics {
		nicKey := getNicKey(nic.Nic)

		exists := true
		_, err := networkutils.NetworkHelper.LinkByMacAddr(nic.Nic.ID)
		if err == constants.ErrNicNotFound {
			exists = false
		}

		if !nic.isOK() || !exists {
			log.Infof("hostNic %s status: %s , exists: %t, try to repair it", nicKey, nic.getPhase(), exists)
			phase, err := networkutils.NetworkHelper.CheckAndRepairNetwork(nic.Nic)
			if err := a.setNicStatus(nic.Nic, phase); err != nil {
				log.Errorf("setNicStatus failed: %s %s %v", nicKey, phase.String(), err)
			}
			log.Infof("Repair hostNic %s: %s, %v", nicKey, nic.getPhase(), err)
		}
	}
}

func (a *Allocator) IPAddrReNew() {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, nic := range a.nics {
		if nic != nil {
			nicKey := getNicKey(nic.Nic)
			if nic.isOK() && nic.Nic.VxNet.TunnelType == constants.TunnelTypeVlan {
				brName := constants.GetHostNicBridgeName(int(nic.Nic.RouteTableNum))
				// renew ip lease
				err := networkutils.UpdateLinkIPAddrAndLease(nic.Nic)
				if err != nil {
					log.Errorf("renew hostNic %s bridge %s ip addr lease error: %v", nicKey, brName, err)
				} else {
					log.Infof("renew hostNic %s bridge %s ip addr lease success!", nicKey, brName)
				}
			}
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

func (a *Allocator) freeHostnic(nic *rpc.HostNic) error {
	if err := networkutils.NetworkHelper.CleanupNetwork(nic); err != nil {
		log.Errorf("CleanupNetwork for vxnet %s failed: nic %s %v", nic.VxNet.ID, nic.ID, err)
		return err
	}

	if _, err := qcclient.QClient.DeattachNics([]string{nic.ID}, true); err != nil {
		if strings.Contains(err.Error(), constants.ResourceNotFound) {
			log.Infof("nic %s/%s was not found from api, skip DeattachNics and DeleteNics", nic.VxNet.ID, nic.ID)
			return nil
		}
		log.Errorf("DeattachNics for vxnet %s failed: nic %s failed: %v", nic.VxNet.ID, nic.ID, err)
		return err
	}

	if err := qcclient.QClient.DeleteNics([]string{nic.ID}); err != nil && !strings.Contains(err.Error(), constants.ResourceNotFound) {
		log.Errorf("DeleteNics for vxnet %s failed: nic %s failed: %v", nic.VxNet.ID, nic.ID, err)
		// nic has deattached, so return nil to skip repair
	}

	return nil
}

func (a *Allocator) ClearFreeHostnic(force bool) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	// maxVxnetNicsCount := a.getVxnetMaxNicNum()
	// log.Infof("freeHostnic: %d %d %d %d", len(a.nics), maxVxnetNicsCount, a.conf.NodeThreshold, a.conf.VxnetThreshold)
	// if len(a.nics) < a.conf.NodeThreshold && maxVxnetNicsCount < a.conf.VxnetThreshold && !force {
	// 	log.Infof("no hostnic to free: %d %d %d %d", len(a.nics), maxVxnetNicsCount, a.conf.NodeThreshold, a.conf.VxnetThreshold)
	// 	return nil
	// }
	var freeCount int
	var freeNics []string
	log.Infof("freeHostnic: total nics for now %d", len(a.nics))
	defer func() {
		log.Infof("freeHostnic: total free nics count %d, nics: %v", freeCount, freeNics)
	}()

	for vxnet, status := range a.nics {
		if len(status.Pods) == 0 || force {
			nicKey := getNicKey(status.Nic)
			if len(status.Pods) == 0 {
				log.Infof("vxnet %s has no pod left on this node, going to clear free Hostnic %s", vxnet, nicKey)
			}
			if force {
				log.Infof("vxnet %s has %d pods left on this node, force free Hostnic %s", vxnet, len(status.Pods), nicKey)
			}

			if err := a.freeHostnic(status.Nic); err != nil {
				log.Errorf("freeHostnic for vxnet %s failed: nic %s %v", vxnet, status.Nic.ID, err)
				// set status to init to repair nics which free failed
				if err := a.setNicStatus(status.Nic, rpc.Phase_Init); err != nil {
					log.Errorf("setNicStatus failed: %s %s %v", nicKey, rpc.Phase_Init.String(), err)
				}
			} else {
				if err := a.delNic(vxnet); err != nil {
					log.Errorf("delNic failed: %s %v", nicKey, err)
				}
				freeCount++
				freeNics = append(freeNics, nicKey)
				log.Infof("freeHostnic for vxnet %s success: nic %s", vxnet, status.Nic.ID)
			}
		}
	}
	return nil
}

func (a *Allocator) getVxnetMaxNicNum() int {
	maxNicsCount := 0
	for vxnet := range a.nics {
		if nics, err := qcclient.QClient.GetCreatedNicsByVxNet(vxnet); err != nil {
			return 0
		} else {
			if maxNicsCount < len(nics) {
				maxNicsCount = len(nics)
			}
		}
	}
	return maxNicsCount
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
		case <-constants.IpAddrReNewTicker.C:
			log.Infof("ip addr renew sync")
			a.IPAddrReNew()
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
	nics, err := qcclient.QClient.GetCreatedNicsByName(constants.NicPrefix + qcclient.QClient.GetInstanceID())
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
