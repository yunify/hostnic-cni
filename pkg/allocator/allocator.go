package allocator

import (
	"encoding/json"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
)

const (
	NicPhaseInit            string = "Init"
	NicPhaseCreateAndAttach string = "CreateAndAttach"
	NicPhaseJoinBridge      string = "JoinBridge"
	NicPhaseSetRouteTable   string = "SetRouteTable"
	NicPhaseSucceeded       string = "Succeeded"
	NicPhaseUnknown         string = "Unknown"
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
	savePod := n.Pods[getKey(pod)]
	saveStatus := n.Nic.Phase
	n.Pods[getKey(pod)] = pod
	n.Nic.Phase = rpc.Phase_Succeeded
	if err := db.SetNetworkInfo(n.Nic.VxNet.ID, n); err != nil {
		if savePod == nil {
			delete(n.Pods, getKey(pod))
			n.Nic.Phase = saveStatus
		} else {
			n.Pods[getKey(pod)] = savePod
			n.Nic.Phase = saveStatus
		}
		return err
	}
	return nil
}

func (n *nicStatus) removeNicPod(pod *rpc.PodInfo) error {
	save := n.Pods[getKey(pod)]
	delete(n.Pods, getKey(pod))
	if err := db.SetNetworkInfo(n.Nic.VxNet.ID, n); err != nil {
		n.Pods[getKey(pod)] = save
		return err
	}
	return nil
}

func (n *nicStatus) isOK() bool {
	return n.Nic.Phase == rpc.Phase_Succeeded
}

func (n *nicStatus) getPhase() string {
	switch n.Nic.Phase {
	case rpc.Phase_Init:
		return NicPhaseInit
	case rpc.Phase_CreateAndAttach:
		return NicPhaseCreateAndAttach
	case rpc.Phase_JoinBridge:
		return NicPhaseJoinBridge
	case rpc.Phase_SetRouteTable:
		return NicPhaseSetRouteTable
	case rpc.Phase_Succeeded:
		return NicPhaseSucceeded
	}
	return NicPhaseUnknown
}

type Allocator struct {
	lock sync.RWMutex
	nics map[string]*nicStatus
	conf conf.PoolConf
}

func (a *Allocator) setNicStatus(nic *rpc.HostNic, pahse rpc.Phase) error {
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

func (a *Allocator) getNicRouteTableNum(nic *rpc.HostNic) int32 {
	if nic.RouteTableNum <= 0 {
		exists := make(map[int]bool)
		for _, nic := range a.nics {
			exists[int(nic.Nic.RouteTableNum)] = true
		}
		for start := a.conf.RouteTableBase; ; start++ {
			if !exists[start] {
				log.Infof("assign nic %s routetable num %d", nic.ID, start)
				return int32(start)
			}
		}
	} else {
		return nic.RouteTableNum
	}
}

func (a *Allocator) addNicPod(nic *rpc.HostNic, info *rpc.PodInfo) error {
	log.Println("nic add pod:", nic, info)
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

func (a *Allocator) removeNicPod(nic *rpc.HostNic, info *rpc.PodInfo) error {
	if status, ok := a.nics[nic.VxNet.ID]; ok {
		if err := status.removeNicPod(info); err != nil {
			return err
		}
	}

	return nil
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
	log.Infof("get vxnet %s", vxnet)
	return result[vxnet], nil
}

func (a *Allocator) canAlloc() int {
	return a.conf.MaxNic - len(a.nics)
}

func (a *Allocator) AllocHostNic(args *rpc.PodInfo) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	vxnetName := args.VxNet
	if nic, ok := a.nics[vxnetName]; ok {
		log.Printf("find hostNic for vxnet %s: %v %s", vxnetName, nic.Nic, nic.getPhase())
		if nic.isOK() {
			// just update Nic's pods
			if err := a.addNicPod(nic.Nic, args); err != nil {
				log.Errorf("addNicPod %v pod %v failed: %v", nic, args, err)
			}
			return nic.Nic, nil
		} else {
			// create bridge and rule here
			phase, err := networkutils.NetworkHelper.SetupNetwork(nic.Nic)
			if err != nil {
				if err := a.setNicStatus(nic.Nic, phase); err != nil {
					log.Errorf("setNicStatus %v phase %v failed: %v", nic, phase, err)
				}
				return nil, err
			}
			if err := a.addNicPod(nic.Nic, args); err != nil {
				log.Errorf("addNicPod %v pod %v failed: %v", nic, args, err)
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
	log.Infof("create and attach nic %v", nics)

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

	log.Infof("nic %v attach success", nics)

	nics[0].Reserved = true
	nics[0].RouteTableNum = a.getNicRouteTableNum(nics[0])

	// create bridge and rule here
	phase, err := networkutils.NetworkHelper.SetupNetwork(nics[0])
	if err != nil {
		if e := a.setNicStatus(nics[0], phase); e != nil {
			log.Errorf("setNicStatus %v phase %v failed: %v", nics[0], phase, e)
		}
		return nil, err
	}

	if e := a.addNicPod(nics[0], args); e != nil {
		log.Errorf("addNicPod %v pod %v failed: %v", nics[0], args, e)
	}

	return nics[0], nil
}

func (a *Allocator) FreeHostNic(args *rpc.PodInfo, peek bool) (*rpc.HostNic, string, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	for _, status := range a.nics {
		if pod, ok := status.Pods[getKey(args)]; ok {
			log.Println("get pod from status:", pod)
			a.removeNicPod(status.Nic, pod)
			args.Containter = pod.Containter
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
}

func (a *Allocator) Start(stopCh <-chan struct{}) error {
	go a.run(stopCh)
	return nil
}

func (a *Allocator) run(stopCh <-chan struct{}) {
	jobTimer := time.NewTicker(time.Duration(a.conf.Sync) * time.Second).C
	for {
		select {
		case <-stopCh:
			log.Info("stoped allocator")
			return
		case <-jobTimer:
			log.Infof("period job sync")
			a.HostNicCheck()
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
		log.WithError(err).Fatalf("failed restore allocator from leveldb")
	}

	//
	// restore create nics
	//
	nics, err := qcclient.QClient.GetCreatedNics(constants.NicNumLimit, 0)
	if err != nil {
		log.WithError(err).Fatalf("failed to get created nics")
	}

	for _, nic := range nics {
		if status, ok := Alloc.nics[nic.VxNet.ID]; !ok {
			// nic not attached at this node
		} else {
			// TODO: maybe we need rebuild bridge and rules
			Alloc.setNicStatus(status.Nic, rpc.Phase_Init)
			log.Infof("restore create nic %s %s routetable num %d", nic.ID, status.Nic.ID, status.Nic.RouteTableNum)
		}
	}
}

func getKey(info *rpc.PodInfo) string {
	return info.Containter
}
