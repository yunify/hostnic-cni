package allocator

import (
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/yunify/hostnic-cni/pkg/conf"
	"github.com/yunify/hostnic-cni/pkg/constants"
	"github.com/yunify/hostnic-cni/pkg/db"
	"github.com/yunify/hostnic-cni/pkg/k8s"
	"github.com/yunify/hostnic-cni/pkg/networkutils"
	"github.com/yunify/hostnic-cni/pkg/qcclient"
	"github.com/yunify/hostnic-cni/pkg/rpc"
	"strconv"
	"strings"
	"sync"
	"time"
)

type nicStatus struct {
	nic  *rpc.HostNic
	info *rpc.PodInfo
}

func (n *nicStatus) setStatus(status rpc.Status) error {
	save := n.nic.Status
	n.nic.Status = status
	if err := db.SetNetworkInfo(n.nic.ID, &rpc.IPAMMessage{
		Args: n.info,
		Nic:  n.nic,
	}); err != nil {
		n.nic.Status = save
		return err
	}
	return nil
}

func (n *nicStatus) isFree() bool {
	return n.nic.Status == rpc.Status_FREE
}

func (n *nicStatus) isUsing() bool {
	return n.nic.Status == rpc.Status_USING
}

type Allocator struct {
	lock      sync.RWMutex
	jobs      []string
	nics      map[string]*nicStatus
	conf      conf.PoolConf
	cachedNet *rpc.VxNet
}

func (a *Allocator) SetCachedVxnet(vxnet string) error {
	a.lock.Lock()
	defer a.lock.Unlock()

	if a.cachedNet != nil && a.cachedNet.ID == vxnet {
		return nil
	}

	var toFree []string
	for _, nic := range a.nics {
		if nic.isFree() && nic.nic.VxNet.ID != vxnet {
			err := nic.setStatus(rpc.Status_DELETING)
			if err != nil {
				return err
			}
			toFree = append(toFree, nic.nic.ID)
		}
	}
	if len(toFree) > 0 {
		jobID, err := qcclient.QClient.DeattachNics(toFree, false)
		if err == nil {
			a.jobs = append(a.jobs, jobID)
			log.Infof("deattach nics %v", toFree)
		} else {
			log.WithError(err).Errorf("failed to deattach nics %v", toFree)
		}
	}

	vxnets, err := qcclient.QClient.GetVxNets([]string{vxnet})
	if err != nil {
		return err
	}
	a.cachedNet = vxnets[vxnet]
	log.Infof("set cache vxnet to %s", vxnet)
	a.cacheHostNic()

	return nil
}

func (a *Allocator) addNicStatus(nic *rpc.HostNic, info *rpc.PodInfo) error {
	if info != nil {
		nic.Status = rpc.Status_USING
	}
	if nic.RouteTableNum <= 0 {
		exists := make(map[int]bool)
		for _, nic := range a.nics {
			exists[int(nic.nic.RouteTableNum)] = true
		}
		for start := a.conf.RouteTableBase; ; start++ {
			if !exists[start] {
				nic.RouteTableNum = int32(start)
				break
			}
		}
		log.Infof("assign nic %s routetable num %d", nic.ID, nic.RouteTableNum)
	}
	status := &nicStatus{
		nic:  nic,
		info: info,
	}

	err := db.SetNetworkInfo(status.nic.ID, &rpc.IPAMMessage{
		Args: status.info,
		Nic:  status.nic,
	})
	if err != nil {
		return err
	}
	a.nics[status.nic.ID] = status
	return nil
}

func (a *Allocator) removeNicStatus(status *nicStatus) error {
	err := db.DeleteNetworkInfo(status.nic.ID)
	if err != nil {
		return err
	}
	delete(a.nics, status.nic.ID)
	return nil
}

func (a *Allocator) cacheHostNic() {
	if a.cachedNet == nil {
		return
	}

	nowAllocated := len(a.nics)

	nowFree := 0
	var toFree []string
	for _, nic := range a.nics {
		if nic.isFree() && nic.nic.VxNet.ID == a.cachedNet.ID {
			nowFree++
			if nowFree > a.conf.PoolHigh {
				if err := nic.setStatus(rpc.Status_DELETING); err != nil {
					log.WithError(err).Errorf("failed to set nic %s to deleting", nic.nic.ID)
				} else {
					log.Infof("free cached nic %s", nic.nic.ID)
					toFree = append(toFree, nic.nic.ID)
				}
			}
		}
	}
	if len(toFree) > 0 {
		jobID, err := qcclient.QClient.DeattachNics(toFree, false)
		if err == nil {
			a.jobs = append(a.jobs, jobID)
			log.Infof("deattach nics %v", toFree)
		} else {
			log.WithError(err).Errorf("failed to deattach nics %v", toFree)
		}
	}

	if nowFree < a.conf.PoolLow {
		if a.canAlloc() <= 0 {
			return
		}

		canAllocNum := a.conf.PoolLow - nowFree
		if canAllocNum > a.conf.MaxNic-nowAllocated {
			canAllocNum = a.conf.MaxNic - nowAllocated
		}

		nics, jobID, err := qcclient.QClient.CreateNicsAndAttach(a.cachedNet, canAllocNum, nil)
		if err != nil {
			log.WithError(err).Errorf("failed to create %d cached nics", canAllocNum)
			return
		}
		a.jobs = append(a.jobs, jobID)

		for _, nic := range nics {
			if err := a.addNicStatus(nic, nil); err != nil {
				log.WithError(err).Errorf("faid to cache nic %s", nic.ID)
			} else {
				log.Infof("cached nic %s", nic.ID)
			}
		}
	}
}

func (a *Allocator) allocHostNic(args *rpc.PodInfo) *rpc.HostNic {
	var result *rpc.HostNic
	for _, nic := range a.nics {
		if nic.isFree() {
			err := a.addNicStatus(nic.nic, args)
			if err == nil {
				result = nic.nic
				break
			}
		}
	}

	return result
}

func (a *Allocator) canAlloc() int {
	return a.conf.MaxNic - len(a.nics)
}

func (a *Allocator) getVxnets(vxnet string) (*rpc.VxNet, error) {
	for _, nic := range a.nics {
		if nic.nic.VxNet.ID == vxnet {
			return nic.nic.VxNet, nil
		}
	}

	result, err := qcclient.QClient.GetVxNets([]string{vxnet})
	if err != nil {
		return nil, err
	}
	log.Infof("get vxnet %s", vxnet)
	return result[vxnet], nil
}

func getKey(info *rpc.PodInfo) string {
	return info.Containter
}

func (a *Allocator) AllocHostNic(args *rpc.PodInfo) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	var result *nicStatus
	for _, nic := range a.nics {
		if nic.isUsing() && getKey(nic.info) == getKey(args) {
			result = nic
		}
	}
	if result != nil {
		return result.nic, nil
	}

	if args.VxNet == "" {
		result := a.allocHostNic(args)
		if result != nil {
			return result, nil
		}

		a.cacheHostNic()

		result = a.allocHostNic(args)
		if result != nil {
			return result, nil
		}

		return nil, constants.ErrNoAvailableNIC
	} else {
		if a.canAlloc() <= 0 {
			return nil, constants.ErrNoAvailableNIC
		}

		var ips []string
		if args.PodIP != "" {
			ips = append(ips, args.PodIP)
		}
		vxnet, err := a.getVxnets(args.VxNet)
		if err != nil {
			return nil, err
		}
		nics, jobID, err := qcclient.QClient.CreateNicsAndAttach(vxnet, 1, ips)
		if err != nil {
			return nil, err
		}
		log.Infof("create and attach nic %v", nics)
		a.jobs = append(a.jobs, jobID)
		nics[0].Reserved = true
		err = a.addNicStatus(nics[0], args)
		if err != nil {
			return nil, err
		}
		return nics[0], nil
	}
}

func (a *Allocator) FreeHostNic(args *rpc.PodInfo, peek bool) (*rpc.HostNic, error) {
	a.lock.Lock()
	defer a.lock.Unlock()

	var result *nicStatus
	for _, nic := range a.nics {
		if nic.isUsing() && getKey(nic.info) == getKey(args) {
			result = nic
		}
	}
	if result == nil {
		return nil, nil
	}

	if !peek {
		status := rpc.Status_FREE
		if result.nic.Reserved || (a.cachedNet != nil && result.nic.VxNet.ID != a.cachedNet.ID) {
			status = rpc.Status_DELETING
		}
		err := result.setStatus(status)
		if err != nil {
			return nil, fmt.Errorf("failed to set nic %s to %d", result.nic.ID, status)
		}
		a.cacheHostNic()
		if status == rpc.Status_DELETING {
			jobId, err := qcclient.QClient.DeattachNics([]string{result.nic.ID}, false)
			if err == nil {
				a.jobs = append(a.jobs, jobId)
				log.WithError(err).Infof("deattach nic %s", result.nic.ID)
			} else {
				log.WithError(err).Infof("failed to deattach nic %s", result.nic.ID)
			}
		}
	}

	args.NicType = result.info.NicType
	args.Netns = result.info.Netns
	args.Containter = result.info.Containter
	args.IfName = result.info.IfName
	return result.nic, nil
}

func (a *Allocator) SyncHostNic(node bool) {
	a.lock.Lock()
	defer a.lock.Unlock()

	if !node {
		if len(a.jobs) <= 0 {
			return
		}
	}

	var (
		all      []string
		using    []string
		deleting []string
		toAttach []string
		toDetach []string
		toDelete []string
		working  = make(map[string]bool)
		left     []string
	)

	for _, nic := range a.nics {
		all = append(all, nic.nic.ID)
		if nic.isFree() || nic.isUsing() {
			using = append(using, nic.nic.ID)
		} else {
			deleting = append(deleting, nic.nic.ID)
		}
	}

	nics, err := qcclient.QClient.GetNics(all)
	if err != nil {
		return
	}

	if len(a.jobs) >= 0 {
		left, working, err = qcclient.QClient.DescribeNicJobs(a.jobs)
		if err != nil {
			return
		}
		a.jobs = left
	}

	for _, id := range using {
		if nics[id] == nil {
			log.Infof("nic missing in get , remove  using nic %s", id)
			a.removeNicStatus(a.nics[id])
			continue
		}

		if working[id] {
			continue
		}

		if !nics[id].Using {
			toAttach = append(toAttach, id)
		}
	}

	for _, id := range deleting {
		if nics[id] == nil {
			log.Infof("nic missing in get, remove deleting nic %s", id)
			a.removeNicStatus(a.nics[id])
			continue
		}

		if working[id] {
			continue
		}

		if nics[id].Using {
			toDetach = append(toDetach, id)
		} else {
			toDelete = append(toDelete, id)
		}
	}

	if len(toAttach) > 0 {
		jobID, err := qcclient.QClient.AttachNics(toAttach)
		if err == nil {
			log.Infof("try to attachnic %v", toAttach)
			a.jobs = append(a.jobs, jobID)
		} else {
			log.WithError(err).Errorf("failed to attachnics %v", toAttach)
		}
	}

	if len(toDelete) > 0 {
		err = qcclient.QClient.DeleteNics(toDelete)
		if err == nil {
			log.Infof("try to delete nic %v", toDelete)
			for _, id := range toDelete {
				log.Infof("nic %s deleeted, remove from status", id)
				a.removeNicStatus(a.nics[id])
			}
		} else {
			log.WithError(err).Errorf("failed to deletenics %v", toDelete)
		}
	}

	if len(toDetach) > 0 {
		jobID, err := qcclient.QClient.DeattachNics(toDetach, false)
		if err == nil {
			log.Infof("try to deattach nic %v", toDetach)
			a.jobs = append(a.jobs, jobID)
		} else {
			log.WithError(err).Errorf("failed to deattach nics %v", toDetach)
		}
	}
}

func (a *Allocator) Start(stopCh <-chan struct{}) error {
	// choose vxnet for node
	err := k8s.K8sHelper.ChooseVxnetForNode(a.conf.VxNets, a.conf.MaxNic)
	if err != nil {
		log.WithError(err).Fatalf("failed to choose vxnet for node")
	}

	go a.run(stopCh)
	return nil
}

func (a *Allocator) run(stopCh <-chan struct{}) {
	nodeTimer := time.NewTicker(time.Duration(a.conf.NodeSync) * time.Second).C
	jobTimer := time.NewTicker(time.Duration(a.conf.Sync) * time.Second).C
	for {
		select {
		case <-stopCh:
			log.Info("stoped allocator")
			return
		case <-jobTimer:
			a.SyncHostNic(false)
		case <-nodeTimer:
			log.Infof("period node sync")
			a.SyncHostNic(true)
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

	err := db.Iterator(func(info *rpc.IPAMMessage) error {
		if info.Nic.Status == rpc.Status_USING {
			log.Infof("restore pod %v to nic %s", info.Args, info.Nic.ID)
		} else {
			log.Infof("restore nic %s status %d", info.Nic.ID, info.Nic.Status)
		}

		Alloc.nics[info.Nic.ID] = &nicStatus{
			nic:  info.Nic,
			info: info.Args,
		}

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
	var left []*rpc.HostNic
	for _, nic := range nics {
		if Alloc.nics[nic.ID] == nil {
			link, err := networkutils.NetworkHelper.LinkByMacAddr(nic.HardwareAddr)
			if err != nil && err != constants.ErrNicNotFound {
				log.WithError(err).Fatalf("failed to index link by mac %s", nic.HardwareAddr)
			}
			if link != nil {
				routeTableNum := 0
				name := ""
				if strings.HasPrefix(link.Attrs().Name, constants.NicPrefix) {
					name = link.Attrs().Name
				} else {
					name = link.Attrs().Alias
				}
				routeTableNum, err = strconv.Atoi(strings.TrimPrefix(name, constants.NicPrefix))
				if err != nil {
					left = append(left, nic)
					continue
				}
				nic.RouteTableNum = int32(routeTableNum)
				log.Infof("restore create nic %s routetable num %d", nic.ID, nic.RouteTableNum)
				Alloc.addNicStatus(nic, nil)
			} else {
				left = append(left, nic)
			}
		}
	}
	for _, nic := range left {
		Alloc.addNicStatus(nic, nil)
		log.Infof("restore create nic %s routetable num %d", nic.ID, nic.RouteTableNum)
	}

	Alloc.SyncHostNic(true)
}
